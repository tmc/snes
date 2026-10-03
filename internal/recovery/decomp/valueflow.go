package decomp

import (
	"fmt"
	"reflect"
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
	width        Width
	needsDecl    bool
	expr         Expr
}

type carryState struct {
	known        bool
	value        int
	expr         string
	instructions []string
	isChain      bool
}

type blockFlowState struct {
	accumulator *localValue
	carry       *carryState
	aWidth      Width
}

// recoverBlocks recovers symbolic expressions across all blocks in a region,
// propagating values across sequential basic blocks.
func recoverBlocks(blocks []*BlockIR) (map[uint32]map[int]string, []SemanticExpression) {
	replacements := make(map[uint32]map[int]string)
	var allExpressions []SemanticExpression

	predCount := make(map[uint32]int)
	singlePred := make(map[uint32]*BlockIR)
	for _, b := range blocks {
		for _, succ := range b.Successors {
			predCount[succ]++
			if predCount[succ] == 1 {
				singlePred[succ] = b
			} else {
				delete(singlePred, succ)
			}
		}
	}

	exitStates := make(map[uint32]blockFlowState)

	for _, b := range blocks {
		var entryState blockFlowState
		pred, ok := singlePred[b.StartAddress]
		if ok && predCount[b.StartAddress] == 1 {
			if ps, has := exitStates[pred.StartAddress]; has {
				entryState = ps
				if entryState.accumulator != nil {
					entryState.accumulator = &localValue{
						name:         entryState.accumulator.name,
						instructions: entryState.accumulator.instructions,
						width:        entryState.accumulator.width,
						needsDecl:    true,
						expr:         entryState.accumulator.expr,
					}
				}
				if entryState.carry != nil && entryState.carry.isChain {
					entryState.carry = &carryState{
						known:        true,
						expr:         "(s.p & 1)",
						instructions: entryState.carry.instructions,
						isChain:      true,
					}
				}
			}
		}
		if entryState.aWidth == 0 {
			if b.EntryContext.M == "clear" || b.EntryContext.M == "0" {
				entryState.aWidth = Width16
			} else {
				entryState.aWidth = Width8
			}
		}

		blockReplacements, exprs, exitState := recoverBlockWithState(b, entryState)
		replacements[b.StartAddress] = blockReplacements
		allExpressions = append(allExpressions, exprs...)
		exitStates[b.StartAddress] = exitState
	}

	return replacements, allExpressions
}

// recoverBlock recovers block-local values for a single block.
func recoverBlock(b *BlockIR) (map[int]string, []SemanticExpression) {
	width := Width8
	if b.EntryContext.M == "clear" || b.EntryContext.M == "0" {
		width = Width16
	}
	replacements, exprs, _ := recoverBlockWithState(b, blockFlowState{aWidth: width})
	return replacements, exprs
}

func recoverBlockWithState(b *BlockIR, entryState blockFlowState) (map[int]string, []SemanticExpression, blockFlowState) {
	replacements := make(map[int]string)
	var expressions []SemanticExpression
	accumulator := entryState.accumulator
	lastCarry := entryState.carry
	aWidth := entryState.aWidth
	if aWidth == 0 {
		aWidth = Width8
		if b.EntryContext.M == "clear" || b.EntryContext.M == "0" {
			aWidth = Width16
		}
	}
	var carryInstructions []string
	if lastCarry != nil {
		carryInstructions = append([]string{}, lastCarry.instructions...)
	}
	version := 0

	for i := 0; i < len(b.Statements); i++ {
		s := b.Statements[i]

		// 1. Check for Sign Extension idiom: CMP #$80; SBC <op>; EOR #$FF
		if aWidth == Width8 && isSignExtend(b.Statements, i, accumulator) {
			s0 := b.Statements[i]
			s1 := b.Statements[i+1]
			s2 := b.Statements[i+2]

			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			entryAccumulator := false
			var inputName string
			var ids []string
			var code string

			if accumulator == nil || accumulator.width != Width8 {
				entryAccumulator = true
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", inputName)
				ids = append(ids, s0.InstructionID)
			} else if accumulator.needsDecl {
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)(s.a & 0xFF);\n", inputName)
				ids = append(ids, accumulator.instructions...)
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

			// Preserve the SBC memory read to uphold the contract:
			// "Memory reads, stores and architectural updates are neither removed nor reordered."
			bin1 := s1.Expr.(*BinaryExpr)
			inner1 := bin1.Left.(*BinaryExpr)
			sbcOperand := inner1.Right
			if sbcMem, ok := sbcOperand.(*MemReadExpr); ok {
				replacements[i+1] = fmt.Sprintf("        (void)%s; /* preserved read for sign extension */\n", exprToCompilableC(sbcMem, Width8))
			} else {
				replacements[i+1] = "        /* folded into sign extension */\n"
			}
			replacements[i+2] = "        /* folded into sign extension */\n"
			replacements[i+3] = "        /* folded into sign extension */\n"

			accumulator = &localValue{name: name, instructions: ids, width: Width8, expr: nil}
			carryInstructions = append([]string{}, ids...)
			lastCarry = &carryState{known: false, instructions: ids, isChain: false}

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
		if aWidth == Width8 && isASL(b.Statements, i) {
			k := 0
			for isASL(b.Statements, i+3*k) {
				k++
			}
			if k > 8 {
				// Constrain cascade to supported domain 1..8; leave longer cascades unfolded.
				continue
			}

			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			entryAccumulator := false
			var inputName string
			var ids []string
			var code string

			if accumulator == nil || accumulator.width != Width8 {
				entryAccumulator = true
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", inputName)
				ids = append(ids, b.Statements[i].InstructionID)
			} else if accumulator.needsDecl {
				inputName = name + "_input"
				code = fmt.Sprintf("        uint8_t %s = (uint8_t)(s.a & 0xFF);\n", inputName)
				ids = append(ids, accumulator.instructions...)
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
			code += semanticNZ(name, true, true, Width8)
			if k < 8 {
				code += fmt.Sprintf("        if (%s & (1 << %d)) s.p |= 1; else s.p &= ~1;\n", inputName, 8-k)
			} else { // k == 8: carry is the lowest bit shifted out (original bit 0)
				code += fmt.Sprintf("        if (%s & 1) s.p |= 1; else s.p &= ~1;\n", inputName)
			}

			for j := i; j < i+3*k; j++ {
				if j == i {
					replacements[j] = code
				} else {
					replacements[j] = "        /* folded into ASL cascade */\n"
				}
			}

			accumulator = &localValue{name: name, instructions: ids, width: Width8, expr: nil}
			carryInstructions = append([]string{}, ids...)
			lastCarry = &carryState{known: false, instructions: ids, isChain: false}

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
		if s.AffectsC && s.Kind != "assign_reg" && s.Kind != "set_flag" && s.Kind != "clear_flag_mask" && s.Kind != "set_flag_mask" {
			carryInstructions = []string{s.InstructionID}
			lastCarry = &carryState{known: false, instructions: []string{s.InstructionID}, isChain: false}
		}

		if s.Kind == "assign_reg" && s.TargetReg == RegA {
			previous := accumulator
			accumulator = nil
			if s.Width != aWidth {
				continue
			}
			name := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
			version++
			var expression, code string
			entryAccumulator := false
			ids := []string{s.InstructionID}

			switch e := s.Expr.(type) {
			case *MemReadExpr:
				if e.Width != aWidth || s.AffectsC || s.AffectsV {
					continue
				}
				expression = exprToCompilableC(e, aWidth)
				if aWidth == Width8 {
					code = fmt.Sprintf("        uint8_t %s = (uint8_t)(%s);\n", name, expression)
					code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n", name)
				} else {
					code = fmt.Sprintf("        uint16_t %s = (uint16_t)(%s);\n", name, expression)
					code += fmt.Sprintf("        s.a = %s;\n", name)
				}
				code += semanticNZ(name, s.AffectsN, s.AffectsZ, aWidth)
				accumulator = &localValue{name: name, instructions: ids, width: aWidth, expr: s.Expr}

			case *ConstExpr:
				if s.AffectsC || s.AffectsV {
					continue
				}
				expression = exprToCompilableC(e, aWidth)
				if aWidth == Width8 {
					code = fmt.Sprintf("        uint8_t %s = (uint8_t)(%s);\n        s.a = (s.a & 0xFF00) | %s;\n", name, expression, name)
				} else {
					code = fmt.Sprintf("        uint16_t %s = (uint16_t)(%s);\n        s.a = %s;\n", name, expression, name)
				}
				code += semanticNZ(name, s.AffectsN, s.AffectsZ, aWidth)
				accumulator = &localValue{name: name, instructions: ids, width: aWidth, expr: s.Expr}

			case *BinaryExpr:
				isADC := s.Mnemonic == "ADC"
				isSBC := s.Mnemonic == "SBC"
				if (!isADC && !isSBC) || !s.AffectsC || !s.AffectsV || !s.AffectsN || !s.AffectsZ || s.Width != aWidth || e.Width != aWidth {
					continue
				}
				inner, ok := e.Left.(*BinaryExpr)
				if !ok || inner.Width != aWidth {
					continue
				}
				if isADC && (e.Op != OpAdd || inner.Op != OpAdd) {
					continue
				}
				if isSBC && (e.Op != OpSub || inner.Op != OpSub) {
					continue
				}
				a, ok := inner.Left.(*RegExpr)
				if !ok || a.Reg != RegA || a.Width != aWidth {
					continue
				}

				carryMask, ok := e.Right.(*BinaryExpr)
				if !ok || carryMask.Width != aWidth {
					continue
				}
				if isADC && carryMask.Op != OpAnd {
					continue
				}
				if isSBC && carryMask.Op != OpXor {
					continue
				}
				carryFlag, ok := carryMask.Left.(*FlagExpr)
				one, oneOK := carryMask.Right.(*ConstExpr)
				if !ok || carryFlag.Flag != FlagC || !oneOK || one.Value != 1 || one.Width != aWidth {
					continue
				}

				var operandStr string
				if c, ok := inner.Right.(*ConstExpr); ok {
					if aWidth == Width8 {
						operandStr = fmt.Sprintf("0x%02X", c.Value&0xFF)
					} else {
						operandStr = fmt.Sprintf("0x%04X", c.Value&0xFFFF)
					}
				} else if mem, ok := inner.Right.(*MemReadExpr); ok {
					operandStr = exprToCompilableC(mem, aWidth)
				} else {
					continue
				}

				isChain := false
				var carryExpr string
				var carryIns []string
				if lastCarry != nil && lastCarry.known {
					carryIns = append([]string{}, lastCarry.instructions...)
					if lastCarry.isChain {
						isChain = true
						carryExpr = lastCarry.expr
					} else if lastCarry.value == 0 {
						carryExpr = "0"
					} else if lastCarry.value == 1 {
						carryExpr = "1"
					}
				} else {
					carryIns = append([]string{}, carryInputs...)
					carryExpr = "s.p & 1"
				}

				carryName := name + "_carry"
				code += fmt.Sprintf("        uint8_t %s = %s;\n", carryName, carryExpr)

				if previous == nil || previous.width != aWidth {
					entryAccumulator = true
					input := name + "_input"
					if aWidth == Width8 {
						code = fmt.Sprintf("        uint8_t %s = (uint8_t)s.a;\n", input) + code
					} else {
						code = fmt.Sprintf("        uint16_t %s = s.a;\n", input) + code
					}
					previous = &localValue{name: input, width: aWidth}
				} else if previous.needsDecl {
					input := name + "_input"
					if aWidth == Width8 {
						code = fmt.Sprintf("        uint8_t %s = (uint8_t)(s.a & 0xFF);\n", input) + code
					} else {
						code = fmt.Sprintf("        uint16_t %s = s.a;\n", input) + code
					}
					previous = &localValue{name: input, instructions: previous.instructions, width: aWidth}
				}

				ids = append(append([]string{}, previous.instructions...), carryIns...)
				ids = append(ids, s.InstructionID)

				carryOutName := name + "_carry_out"
				if isADC {
					expression = fmt.Sprintf("%s + %s + %s", previous.name, operandStr, carryName)
					if aWidth == Width8 {
						code += fmt.Sprintf("        uint32_t %s_sum = %s;\n        uint8_t %s = (uint8_t)%s_sum;\n", name, expression, name, name)
						code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n        if (%s_sum > 255) s.p |= 1; else s.p &= ~1;\n", name, name)
						code += semanticNZ(name, true, true, Width8)
						code += fmt.Sprintf("        if (~(%s ^ %s) & (%s ^ %s_sum) & 0x80) s.p |= 0x40; else s.p &= ~0x40;\n", previous.name, operandStr, previous.name, name)
						code += fmt.Sprintf("        uint8_t __attribute__((unused)) %s = (%s_sum > 255) ? 1 : 0;\n", carryOutName, name)
					} else {
						code += fmt.Sprintf("        uint32_t %s_sum = (uint32_t)%s + (uint32_t)%s + (uint32_t)%s;\n        uint16_t %s = (uint16_t)%s_sum;\n", name, previous.name, operandStr, carryName, name, name)
						code += fmt.Sprintf("        s.a = %s;\n        if (%s_sum > 0xFFFF) s.p |= 1; else s.p &= ~1;\n", name, name)
						code += semanticNZ(name, true, true, Width16)
						code += fmt.Sprintf("        if (~(%s ^ %s) & (%s ^ %s_sum) & 0x8000) s.p |= 0x40; else s.p &= ~0x40;\n", previous.name, operandStr, previous.name, name)
						code += fmt.Sprintf("        uint8_t __attribute__((unused)) %s = (%s_sum > 0xFFFF) ? 1 : 0;\n", carryOutName, name)
					}
					lastCarry = &carryState{known: true, expr: carryOutName, instructions: ids, isChain: true}
				} else {
					expression = fmt.Sprintf("%s - %s - (1 - %s)", previous.name, operandStr, carryName)
					if aWidth == Width8 {
						code += fmt.Sprintf("        uint32_t %s_diff = (uint32_t)%s + (~(uint32_t)(%s) & 0xFF) + (uint32_t)%s;\n        uint8_t %s = (uint8_t)%s_diff;\n", name, previous.name, operandStr, carryName, name, name)
						code += fmt.Sprintf("        s.a = (s.a & 0xFF00) | %s;\n        if (%s_diff > 255) s.p |= 1; else s.p &= ~1;\n", name, name)
						code += semanticNZ(name, true, true, Width8)
						code += fmt.Sprintf("        if ((%s ^ %s) & (%s ^ %s_diff) & 0x80) s.p |= 0x40; else s.p &= ~0x40;\n", previous.name, operandStr, previous.name, name)
						code += fmt.Sprintf("        uint8_t __attribute__((unused)) %s = (%s_diff > 255) ? 1 : 0;\n", carryOutName, name)
					} else {
						code += fmt.Sprintf("        uint32_t %s_diff = (uint32_t)%s + (~(uint32_t)(%s) & 0xFFFF) + (uint32_t)%s;\n        uint16_t %s = (uint16_t)%s_diff;\n", name, previous.name, operandStr, carryName, name, name)
						code += fmt.Sprintf("        s.a = %s;\n        if (%s_diff > 0xFFFF) s.p |= 1; else s.p &= ~1;\n", name, name)
						code += semanticNZ(name, true, true, Width16)
						code += fmt.Sprintf("        if ((%s ^ %s) & (%s ^ %s_diff) & 0x8000) s.p |= 0x40; else s.p &= ~0x40;\n", previous.name, operandStr, previous.name, name)
						code += fmt.Sprintf("        uint8_t __attribute__((unused)) %s = (%s_diff > 0xFFFF) ? 1 : 0;\n", carryOutName, name)
					}
					lastCarry = &carryState{known: true, expr: carryOutName, instructions: ids, isChain: true}
				}

				carryInstructions = append([]string{}, ids...)
				accumulator = &localValue{name: name, instructions: ids, width: aWidth, expr: s.Expr}

				opName := s.Mnemonic
				if isChain {
					opName = s.Mnemonic + "_CHAIN"
				}
				expressions = append(expressions, SemanticExpression{
					Block:            b.StartAddress,
					Address:          s.Address,
					Name:             name,
					Width:            aWidth,
					Expression:       expression,
					Instructions:     ids,
					Operation:        opName,
					EntryAccumulator: entryAccumulator,
				})

			default:
				continue
			}

			replacements[i] = code
			if s.AffectsC && !strings.EqualFold(s.Mnemonic, "ADC") && !strings.EqualFold(s.Mnemonic, "SBC") {
				carryInstructions = append([]string{}, ids...)
			}
			if s.Mnemonic == "LDA" {
				expressions = append(expressions, SemanticExpression{
					Block:            b.StartAddress,
					Address:          s.Address,
					Name:             name,
					Width:            aWidth,
					Expression:       expression,
					Instructions:     ids,
					Operation:        s.Mnemonic,
					EntryAccumulator: entryAccumulator,
				})
			}

		} else if s.Kind == "clear_flag_mask" {
			if c, ok := s.Expr.(*ConstExpr); ok {
				if (c.Value & 0x20) != 0 {
					aWidth = Width16
					accumulator = nil
				}
				if (c.Value & 0x01) != 0 {
					lastCarry = &carryState{
						known:        true,
						value:        0,
						expr:         "0",
						instructions: []string{s.InstructionID},
						isChain:      false,
					}
					carryInstructions = []string{s.InstructionID}
				}
			}
		} else if s.Kind == "set_flag_mask" {
			if c, ok := s.Expr.(*ConstExpr); ok {
				if (c.Value & 0x20) != 0 {
					aWidth = Width8
					accumulator = nil
				}
				if (c.Value & 0x01) != 0 {
					lastCarry = &carryState{
						known:        true,
						value:        1,
						expr:         "1",
						instructions: []string{s.InstructionID},
						isChain:      false,
					}
					carryInstructions = []string{s.InstructionID}
				}
			}
		} else if s.Kind == "set_flag" && s.TargetFlag == FlagC {
			val := 0
			if s.FlagVal {
				val = 1
			}
			lastCarry = &carryState{
				known:        true,
				value:        val,
				expr:         fmt.Sprintf("%d", val),
				instructions: []string{s.InstructionID},
				isChain:      false,
			}
			carryInstructions = []string{s.InstructionID}
		} else if s.Kind == "store_mem" && accumulator != nil {
			// Memory version/alias invalidation: if accumulator holds a memory read expression,
			// any store_mem that may alias the read address invalidates the operand value equality.
			if memRead, isMem := accumulator.expr.(*MemReadExpr); isMem {
				if mayAlias(s.MemAddress, memRead.Address) {
					accumulator.expr = nil
				}
			}

			e, ok := s.Expr.(*RegExpr)
			if !ok || e.Reg != RegA || s.Width != accumulator.width {
				continue
			}
			var code string
			accName := accumulator.name
			if accumulator.needsDecl {
				declName := fmt.Sprintf("value_%06x_%d", b.StartAddress, version)
				version++
				if accumulator.width == Width8 {
					code = fmt.Sprintf("        uint8_t %s = (uint8_t)(s.a & 0xFF);\n", declName)
				} else {
					code = fmt.Sprintf("        uint16_t %s = s.a;\n", declName)
				}
				accName = declName
				accumulator.name = declName
				accumulator.needsDecl = false
			}
			if s.Width == Width8 {
				code += fmt.Sprintf("        mem_write8(&res, %s, %s);\n", exprToCompilableC(s.MemAddress, Width24), accName)
			} else {
				helper := "mem_write16"
				if s.WordAddressing == WordBankZero16 {
					helper = "mem_write16_bank0"
				}
				code += fmt.Sprintf("        %s(&res, %s, %s);\n", helper, exprToCompilableC(s.MemAddress, Width24), accName)
			}
			replacements[i] = code
		} else if s.Kind != "set_flag" && s.Kind != "clear_flag_mask" && s.Kind != "set_flag_mask" && s.Kind != "store_mem" && s.Kind != "update_flags" && s.Kind != "nop" && s.Kind != "branch" && s.Kind != "jump" {
			// Calls, pulls, unknown effects and returns end local knowledge.
			accumulator = nil
			lastCarry = nil
			carryInstructions = nil
		}
	}

	exitState := blockFlowState{
		accumulator: accumulator,
		carry:       lastCarry,
		aWidth:      aWidth,
	}
	return replacements, expressions, exitState
}

func isSignExtend(stmts []Statement, idx int, acc *localValue) bool {
	if acc == nil || acc.width != Width8 || acc.expr == nil {
		return false
	}
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

	// Verify SBC operand equality with incoming accumulator expression.
	sbcOperand := inner1.Right
	if !sameExpr(acc.expr, sbcOperand) {
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

func isProvenDisjoint(a, b Expr) bool {
	if a == nil || b == nil {
		return false
	}
	if ca, okA := a.(*ConstExpr); okA {
		if cb, okB := b.(*ConstExpr); okB {
			return ca.Value != cb.Value
		}
	}
	ba, okA := a.(*BinaryExpr)
	bb, okB := b.(*BinaryExpr)
	if okA && okB && ba.Op == OpAdd && bb.Op == OpAdd {
		if ra, okRA := ba.Left.(*RegExpr); okRA {
			if rb, okRB := bb.Left.(*RegExpr); okRB && ra.Reg == rb.Reg {
				ca, okCA := ba.Right.(*ConstExpr)
				cb, okCB := bb.Right.(*ConstExpr)
				if okCA && okCB {
					return ca.Value != cb.Value
				}
			}
		}
	}
	return false
}

func mayAlias(writeAddr, readAddr Expr) bool {
	return !isProvenDisjoint(writeAddr, readAddr)
}

func isMMIOAddress(addr Expr) bool {
	if addr == nil {
		return false
	}
	if c, ok := addr.(*ConstExpr); ok {
		val16 := c.Value & 0xFFFF
		bank := (c.Value >> 16) & 0xFF
		if bank == 0x00 || (bank >= 0x80 && bank <= 0xBF) {
			if (val16 >= 0x2100 && val16 <= 0x21FF) || (val16 >= 0x4200 && val16 <= 0x437F) {
				return true
			}
		}
	}
	return false
}

func sameExpr(a, b Expr) bool {
	if a == nil || b == nil {
		return false
	}
	switch ea := a.(type) {
	case *MemReadExpr:
		eb, ok := b.(*MemReadExpr)
		if !ok || ea.Width != eb.Width {
			return false
		}
		if ea.Space == "mmio" || eb.Space == "mmio" || isMMIOAddress(ea.Address) || isMMIOAddress(eb.Address) {
			return false
		}
		return reflect.DeepEqual(ea.Address, eb.Address) ||
			exprToCompilableC(ea.Address, Width24) == exprToCompilableC(eb.Address, Width24)
	case *ConstExpr:
		eb, ok := b.(*ConstExpr)
		if !ok || ea.Width != eb.Width {
			return false
		}
		return ea.Value == eb.Value
	default:
		return false
	}
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

func semanticNZ(name string, n, z bool, width Width) string {
	var s string
	if z {
		s += fmt.Sprintf("        if (%s == 0) s.p |= 2; else s.p &= ~2;\n", name)
	}
	if n {
		if width == Width16 {
			s += fmt.Sprintf("        if (%s & 0x8000) s.p |= 0x80; else s.p &= ~0x80;\n", name)
		} else {
			s += fmt.Sprintf("        if (%s & 0x80) s.p |= 0x80; else s.p &= ~0x80;\n", name)
		}
	}
	return s
}
