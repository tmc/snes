package decomp

import "fmt"

// SemanticExpression records a recovered value and the instructions defining it.
type SemanticExpression struct {
	Block            uint32   `json:"block"`
	Address          uint32   `json:"address"`
	Name             string   `json:"name"`
	Width            Width    `json:"width"`
	Expression       string   `json:"expression"`
	Instructions     []string `json:"instructions"`
	Operation        string   `json:"operation"`
	EntryAccumulator bool     `json:"entry_accumulator"`
}

type localValue struct {
	name         string
	instructions []string
}

// recoverBlock keeps reads and architectural updates in their original slots.
// Values never cross a basic-block boundary or an unsupported register write.
func recoverBlock(b *BlockIR) (map[int]string, []SemanticExpression) {
	replacements := make(map[int]string)
	var expressions []SemanticExpression
	var accumulator *localValue
	var carryInstructions []string
	version := 0
	for i, s := range b.Statements {
		carryInputs := append([]string{}, carryInstructions...)
		if s.AffectsC {
			carryInstructions = []string{s.InstructionID}
		}
		if s.Kind == "assign_reg" && s.TargetReg == RegA {
			previous := accumulator
			accumulator = nil
			if s.Width != Width8 {
				continue
			}
			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			var expression, code string
			entryAccumulator := false
			ids := []string{s.InstructionID}
			switch e := s.Expr.(type) {
			case *MemReadExpr:
				if e.Width != Width8 || s.AffectsC || s.AffectsV {
					continue
				}
				expression = exprToCompilableC(e, Width8)
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)(%s);\n", name, expression)
				code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n", name)
				code += semanticNZ(name, s.AffectsN, s.AffectsZ)
			case *ConstExpr:
				if s.AffectsC || s.AffectsV {
					continue
				}
				expression = exprToCompilableC(e, Width8)
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)(%s);\n        s.a = (s.a & 0xFF00) | %s;\n", name, expression, name)
				code += semanticNZ(name, s.AffectsN, s.AffectsZ)
			case *BinaryExpr:
				// ADC's typed shape is ((A + immediate) + C). Do not infer
				// operands from mnemonic alone or substitute arbitrary arithmetic.
				inner, ok := e.Left.(*BinaryExpr)
				if !ok || e.Op != OpAdd || inner.Op != OpAdd || e.Width != Width8 || inner.Width != Width8 || s.Mnemonic != "ADC" || !s.AffectsC || !s.AffectsV || !s.AffectsN || !s.AffectsZ {
					continue
				}
				a, ok := inner.Left.(*RegExpr)
				if !ok || a.Reg != RegA || a.Width != Width8 {
					continue
				}
				operand, ok := inner.Right.(*ConstExpr)
				if !ok || operand.Width != Width8 || operand.Value > 255 {
					continue
				}
				carryMask, ok := e.Right.(*BinaryExpr)
				if !ok || carryMask.Op != OpAnd || carryMask.Width != Width8 {
					continue
				}
				carry, ok := carryMask.Left.(*FlagExpr)
				one, oneOK := carryMask.Right.(*ConstExpr)
				if !ok || carry.Flag != FlagC || !oneOK || one.Value != 1 || one.Width != Width8 {
					continue
				}
				if previous == nil {
					entryAccumulator = true
					input := name + "_input"
					code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", input)
					previous = &localValue{name: input}
				}
				ids = append(append([]string{}, previous.instructions...), carryInputs...)
				ids = append(ids, s.InstructionID)
				carryName := name + "_carry"
				code += fmt.Sprintf("        uint8_t %s = s.p & 1;\n", carryName)
				expression = fmt.Sprintf("%s + 0x%02X + %s", previous.name, operand.Value, carryName)
				code += fmt.Sprintf("        uint32_t %s_sum = %s;\n        uint8_t %s = (uint8_t)%s_sum;\n", name, expression, name, name)
				code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n        if (%s_sum > 255) s.p |= 1; else s.p &= ~1;\n", name, name)
				code += semanticNZ(name, true, true)
				code += fmt.Sprintf("        if (~(%s ^ 0x%02X) & (%s ^ %s_sum) & 0x80) s.p |= 0x40; else s.p &= ~0x40;\n", previous.name, operand.Value, previous.name, name)
			default:
				continue
			}
			replacements[i] = code
			if s.AffectsC {
				carryInstructions = append([]string{}, ids...)
			}
			accumulator = &localValue{name, ids}
			expressions = append(expressions, SemanticExpression{Block: b.StartAddress, Address: s.Address, Name: name, Width: Width8, Expression: expression, Instructions: ids, Operation: s.Mnemonic, EntryAccumulator: entryAccumulator})
		} else if s.Kind == "set_flag" && s.TargetFlag == FlagC {
			carryInstructions = []string{s.InstructionID}
		} else if s.Kind == "store_mem" && s.Width == Width8 && accumulator != nil {
			e, ok := s.Expr.(*RegExpr)
			if !ok || e.Reg != RegA || e.Width != Width8 {
				continue
			}
			replacements[i] = fmt.Sprintf("        mem_write8(&res, %s, %s);\n", exprToCompilableC(s.MemAddress, Width24), accumulator.name)
		} else if s.Kind != "set_flag" && s.Kind != "store_mem" && s.Kind != "update_flags" {
			// Calls, pulls, unknown effects and control transfers end local knowledge.
			accumulator = nil
			carryInstructions = nil
		}
	}
	return replacements, expressions
}

func semanticNZ(name string, n, z bool) string {
	var s string
	if z {
		s += fmt.Sprintf("        if (%s == 0) s.p |= 2; else s.p &= ~2;\n", name)
	}
	if n {
		s += fmt.Sprintf("        if (%s & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n", name)
	}
	return s
}
