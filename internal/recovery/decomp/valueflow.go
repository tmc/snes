package decomp

import (
	"fmt"
	"strings"
)

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

	for i := 0; i < len(b.Statements); i++ {
		s := b.Statements[i]

		// 1. Check for Sign Extension idiom: CMP #$80; SBC <op>; EOR #$FF
		if isSignExtend(b.Statements, i) {
			s0 := b.Statements[i]
			s1 := b.Statements[i+1]
			s2 := b.Statements[i+2]

			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			entryAccumulator := false
			var inputName string
			var ids []string
			var code string

			if accumulator == nil {
				entryAccumulator = true
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", inputName)
				ids = append(ids, s0.InstructionID)
			} else {
				inputName = accumulator.name
				ids = append(ids, accumulator.instructions...)
			}
			ids = append(ids, s0.InstructionID, s1.InstructionID, s2.InstructionID)

			code += fmt.Sprintf("        uint8_t %s = (%s & 0x80) ? 0xFF : 0x00;\n", name, inputName)
			code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n", name)
			code += fmt.Sprintf("        if (%s == 0) s.p |= 2; else s.p &= ~2;\n", name)
			code += fmt.Sprintf("        if (%s & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n", name)
			code += fmt.Sprintf("        if (%s & 0x80) s.p |= 1; else s.p &= ~1;\n", inputName)
			code += "        s.p &= ~0x40;\n"

			replacements[i] = code
			replacements[i+1] = "        /* folded into sign extension */\n"
			replacements[i+2] = "        /* folded into sign extension */\n"
			replacements[i+3] = "        /* folded into sign extension */\n"

			accumulator = &localValue{name: name, instructions: ids}
			carryInstructions = append([]string{}, ids...)

			expressions = append(expressions, SemanticExpression{
				Block:            b.StartAddress,
				Address:          s0.Address,
				Name:             name,
				Width:            Width8,
				Expression:       fmt.Sprintf("(%s & 0x80) ? 0xFF : 0x00", inputName),
				Instructions:     ids,
				Operation:        "SIGN_EXTEND",
				EntryAccumulator: entryAccumulator,
			})

			i += 3
			continue
		}

		// 2. Check for ASL Shift Cascade: 1 or more consecutive ASL A
		if isASL(b.Statements, i) {
			k := 0
			for isASL(b.Statements, i+3*k) {
				k++
			}

			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			entryAccumulator := false
			var inputName string
			var ids []string
			var code string

			if accumulator == nil {
				entryAccumulator = true
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", inputName)
				ids = append(ids, b.Statements[i].InstructionID)
			} else {
				inputName = accumulator.name
				ids = append(ids, accumulator.instructions...)
			}

			for step := 0; step < k; step++ {
				aslID := b.Statements[i+3*step].InstructionID
				ids = append(ids, aslID)
			}

			code += fmt.Sprintf("        uint8_t %s = (uint8_t)((%s << %d) & 0xFF);\n", name, inputName, k)
			code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n", name)
			code += semanticNZ(name, true, true)
			if k < 8 {
				code += fmt.Sprintf("        if (%s & (1 << %d)) s.p |= 1; else s.p &= ~1;\n", inputName, 8-k)
			} else {
				code += "        s.p &= ~1;\n"
			}

			for j := i; j < i+3*k; j++ {
				if j == i {
					replacements[j] = code
				} else {
					replacements[j] = "        /* folded into ASL cascade */\n"
				}
			}

			accumulator = &localValue{name: name, instructions: ids}
			carryInstructions = append([]string{}, ids...)

			opName := "ASL"
			if k > 1 {
				opName = "ASL_CASCADE"
			}
			expressions = append(expressions, SemanticExpression{
				Block:            b.StartAddress,
				Address:          b.Statements[i].Address,
				Name:             name,
				Width:            Width8,
				Expression:       fmt.Sprintf("(uint8_t)((%s << %d) & 0xFF)", inputName, k),
				Instructions:     ids,
				Operation:        opName,
				EntryAccumulator: entryAccumulator,
			})

			i += 3*k - 1
			continue
		}

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

func isSignExtend(stmts []Statement, idx int) bool {
	if idx+3 >= len(stmts) {
		return false
	}
	s0 := stmts[idx]
	s1 := stmts[idx+1]
	s2 := stmts[idx+2]
	s3 := stmts[idx+3]

	// 1. CMP #$80
	if !strings.EqualFold(s0.Mnemonic, "CMP") || s0.Kind != "update_flags" ||
		!s0.AffectsC || !s0.AffectsZ || !s0.AffectsN || s0.Width != Width8 {
		return false
	}
	bin0, ok := s0.Expr.(*BinaryExpr)
	if !ok || bin0.Op != OpSub {
		return false
	}
	reg0, ok := bin0.Left.(*RegExpr)
	if !ok || reg0.Reg != RegA || reg0.Width != Width8 {
		return false
	}
	imm0, ok := bin0.Right.(*ConstExpr)
	if !ok || imm0.Value != 0x80 || imm0.Width != Width8 {
		return false
	}

	// 2. SBC operand
	if !strings.EqualFold(s1.Mnemonic, "SBC") || s1.Kind != "assign_reg" ||
		s1.TargetReg != RegA || s1.Width != Width8 ||
		!s1.AffectsC || !s1.AffectsV || !s1.AffectsN || !s1.AffectsZ {
		return false
	}
	bin1, ok := s1.Expr.(*BinaryExpr)
	if !ok || bin1.Op != OpSub || bin1.Width != Width8 {
		return false
	}
	inner1, ok := bin1.Left.(*BinaryExpr)
	if !ok || inner1.Op != OpSub || inner1.Width != Width8 {
		return false
	}
	reg1, ok := inner1.Left.(*RegExpr)
	if !ok || reg1.Reg != RegA || reg1.Width != Width8 {
		return false
	}
	borrow1, ok := bin1.Right.(*BinaryExpr)
	if !ok || borrow1.Op != OpXor || borrow1.Width != Width8 {
		return false
	}
	flag1, ok := borrow1.Left.(*FlagExpr)
	one1, okOne := borrow1.Right.(*ConstExpr)
	if !ok || flag1.Flag != FlagC || !okOne || one1.Value != 1 || one1.Width != Width8 {
		return false
	}

	// 3. EOR #$FF
	if !strings.EqualFold(s2.Mnemonic, "EOR") || s2.Kind != "assign_reg" ||
		s2.TargetReg != RegA || s2.Width != Width8 {
		return false
	}
	bin2, ok := s2.Expr.(*BinaryExpr)
	if !ok || bin2.Op != OpXor || bin2.Width != Width8 {
		return false
	}
	reg2, ok := bin2.Left.(*RegExpr)
	if !ok || reg2.Reg != RegA || reg2.Width != Width8 {
		return false
	}
	imm2, ok := bin2.Right.(*ConstExpr)
	if !ok || imm2.Value != 0xFF || imm2.Width != Width8 {
		return false
	}

	// 4. EOR flags update
	if !strings.EqualFold(s3.Mnemonic, "EOR") || s3.Kind != "update_flags" ||
		!s3.AffectsN || !s3.AffectsZ {
		return false
	}

	return true
}

func isASL(stmts []Statement, idx int) bool {
	if idx+2 >= len(stmts) {
		return false
	}
	s0 := stmts[idx]
	s1 := stmts[idx+1]
	s2 := stmts[idx+2]

	// 1. set_flag FlagC (Carry = bit 7)
	if !strings.EqualFold(s0.Mnemonic, "ASL") || s0.Kind != "set_flag" ||
		s0.TargetFlag != FlagC || !s0.AffectsC {
		return false
	}

	// 2. assign_reg RegA = RegA << 1
	if !strings.EqualFold(s1.Mnemonic, "ASL") || s1.Kind != "assign_reg" ||
		s1.TargetReg != RegA || s1.Width != Width8 {
		return false
	}
	bin1, ok := s1.Expr.(*BinaryExpr)
	if !ok || bin1.Op != OpShl || bin1.Width != Width8 {
		return false
	}
	reg1, ok := bin1.Left.(*RegExpr)
	if !ok || reg1.Reg != RegA || reg1.Width != Width8 {
		return false
	}
	one1, ok := bin1.Right.(*ConstExpr)
	if !ok || one1.Value != 1 || one1.Width != Width8 {
		return false
	}

	// 3. update_flags NZ
	if !strings.EqualFold(s2.Mnemonic, "ASL") || s2.Kind != "update_flags" ||
		!s2.AffectsN || !s2.AffectsZ {
		return false
	}

	return true
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
