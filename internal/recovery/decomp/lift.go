package decomp

import (
	"encoding/hex"
	"fmt"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// Lifter transforms recovered instructions into machine-semantic IR.
type Lifter struct {
	m8  bool // Accumulator / Memory 8-bit
	x8  bool // Index X/Y 8-bit
	e   bool // Emulation mode
	assumptions []string
}

// ResolveEffectiveContext validates and reconciles caller context with block.Instructions[0].Context.
// It fails if caller context contradicts instruction 0, or if M, X, or E widths are unknown or unspecified.
func ResolveEffectiveContext(block *structure.BasicBlock, callerCtx recovery.Context) (recovery.Context, error) {
	if block == nil {
		return recovery.Context{}, fmt.Errorf("resolve context: nil basic block")
	}

	eff := callerCtx

	if len(block.Instructions) > 0 {
		instCtx := block.Instructions[0].Context

		checkConflict := func(name, cVal, iVal string) error {
			if cVal != "" && iVal != "" && cVal != iVal {
				return fmt.Errorf("context conflict on %s: caller specified %q but instruction context is %q", name, cVal, iVal)
			}
			return nil
		}

		if err := checkConflict("M", callerCtx.M, instCtx.M); err != nil {
			return recovery.Context{}, err
		}
		if err := checkConflict("X", callerCtx.X, instCtx.X); err != nil {
			return recovery.Context{}, err
		}
		if err := checkConflict("E", callerCtx.E, instCtx.E); err != nil {
			return recovery.Context{}, err
		}
		if err := checkConflict("C", callerCtx.C, instCtx.C); err != nil {
			return recovery.Context{}, err
		}

		if eff.M == "" {
			eff.M = instCtx.M
		}
		if eff.X == "" {
			eff.X = instCtx.X
		}
		if eff.E == "" {
			eff.E = instCtx.E
		}
		if eff.C == "" {
			eff.C = instCtx.C
		}
	}

	if eff.E == "" || eff.E == "unknown" {
		return recovery.Context{}, fmt.Errorf("unresolved entry context: E (emulation mode) is unknown or unspecified")
	}
	if eff.M == "" || eff.M == "unknown" {
		return recovery.Context{}, fmt.Errorf("unresolved entry context: M (accumulator width) is unknown or unspecified")
	}
	if eff.X == "" || eff.X == "unknown" {
		return recovery.Context{}, fmt.Errorf("unresolved entry context: X (index width) is unknown or unspecified")
	}

	return eff, nil
}

// LiftBlock lowers all instructions in a basic block into machine-semantic IR.
func LiftBlock(block *structure.BasicBlock, ctx recovery.Context) (*BlockIR, error) {
	if block == nil {
		return nil, fmt.Errorf("lift: nil basic block")
	}

	effCtx, err := ResolveEffectiveContext(block, ctx)
	if err != nil {
		return nil, fmt.Errorf("lift block: %w", err)
	}

	m8 := effCtx.M == "set" || effCtx.M == "1"
	x8 := effCtx.X == "set" || effCtx.X == "1"
	e := effCtx.E == "set" || effCtx.E == "1"
	if e {
		m8 = true
		x8 = true
	}

	l := &Lifter{
		m8: m8,
		x8: x8,
		e:  e,
	}

	ir := &BlockIR{
		BlockID:      block.ID,
		StartAddress: block.StartAddress,
		EndAddress:   block.EndAddress,
		EntryContext: effCtx,
		Instructions: block.Instructions,
		Successors:   block.Successors,
		TotalCount:   len(block.Instructions),
	}

	for _, inst := range block.Instructions {
		bytes, _ := hex.DecodeString(inst.Bytes)
		fallthroughAddr := inst.Address + uint32(len(bytes))

		stmts, err := l.liftInstruction(inst, fallthroughAddr)
		if err != nil {
			ir.Statements = append(ir.Statements, Statement{
				InstructionID: inst.ID,
				Address:       inst.Address,
				Mnemonic:      inst.Mnemonic,
				Kind:          "unsupported",
				Reason:        err.Error(),
			})
			ir.UnsupportedCount++
			continue
		}

		ir.Statements = append(ir.Statements, stmts...)
		ir.LoweredCount++
	}

	return ir, nil
}

func (l *Lifter) liftInstruction(inst recovery.Instruction, nextAddr uint32) ([]Statement, error) {
	bytes, err := hex.DecodeString(inst.Bytes)
	if err != nil || len(bytes) == 0 {
		return nil, fmt.Errorf("invalid instruction bytes %q", inst.Bytes)
	}

	op := bytes[0]
	var stmts []Statement

	emit := func(s Statement) {
		s.InstructionID = inst.ID
		s.Address = inst.Address
		s.Mnemonic = inst.Mnemonic
		if s.Expr != nil && s.ExprString == "" {
			s.ExprString = s.Expr.String()
		}
		if s.MemAddress != nil && s.MemAddrStr == "" {
			s.MemAddrStr = s.MemAddress.String()
		}
		if s.Condition != nil && s.CondString == "" {
			s.CondString = s.Condition.String()
		}
		stmts = append(stmts, s)
	}

	aWidth := Width16
	if l.m8 {
		aWidth = Width8
	}
	xWidth := Width16
	if l.x8 {
		xWidth = Width8
	}

	// Helper to emit NZ updates for an expression
	emitNZ := func(val Expr, w Width) {
		emit(Statement{
			Kind:     "update_flags",
			Expr:     val,
			Width:    w,
			AffectsN: true,
			AffectsZ: true,
		})
	}

	// Parse operand based on opcode / mode
	switch op {
	// --- SEI, CLI, CLD, SED, CLC, SEC, CLV ---
	case 0x78: // SEI
		emit(Statement{Kind: "set_flag", TargetFlag: FlagI, FlagVal: true, AffectsI: true})
		return stmts, nil
	case 0x58: // CLI
		emit(Statement{Kind: "set_flag", TargetFlag: FlagI, FlagVal: false, AffectsI: true})
		return stmts, nil
	case 0xD8: // CLD
		emit(Statement{Kind: "set_flag", TargetFlag: FlagD, FlagVal: false})
		return stmts, nil
	case 0xF8: // SED
		emit(Statement{Kind: "set_flag", TargetFlag: FlagD, FlagVal: true})
		return stmts, nil
	case 0x18: // CLC
		emit(Statement{Kind: "set_flag", TargetFlag: FlagC, FlagVal: false, AffectsC: true})
		return stmts, nil
	case 0x38: // SEC
		emit(Statement{Kind: "set_flag", TargetFlag: FlagC, FlagVal: true, AffectsC: true})
		return stmts, nil
	case 0xB8: // CLV
		emit(Statement{Kind: "set_flag", TargetFlag: FlagV, FlagVal: false, AffectsV: true})
		return stmts, nil

	case 0xEA: // NOP
		emit(Statement{Kind: "nop"})
		return stmts, nil

	// --- REP / SEP ---
	case 0xC2: // REP #imm
		if len(bytes) < 2 {
			return nil, fmt.Errorf("truncated REP")
		}
		imm := bytes[1]
		if (imm & 0x20) != 0 {
			l.m8 = false
			emit(Statement{Kind: "set_flag", TargetFlag: FlagM, FlagVal: false})
		}
		if (imm & 0x10) != 0 {
			l.x8 = false
			emit(Statement{Kind: "set_flag", TargetFlag: FlagX, FlagVal: false})
		}
		if (imm & 0x01) != 0 {
			emit(Statement{Kind: "set_flag", TargetFlag: FlagC, FlagVal: false, AffectsC: true})
		}
		return stmts, nil

	case 0xE2: // SEP #imm
		if len(bytes) < 2 {
			return nil, fmt.Errorf("truncated SEP")
		}
		imm := bytes[1]
		if (imm & 0x20) != 0 {
			l.m8 = true
			emit(Statement{Kind: "set_flag", TargetFlag: FlagM, FlagVal: true})
		}
		if (imm & 0x10) != 0 {
			l.x8 = true
			emit(Statement{Kind: "set_flag", TargetFlag: FlagX, FlagVal: true})
		}
		if (imm & 0x01) != 0 {
			emit(Statement{Kind: "set_flag", TargetFlag: FlagC, FlagVal: true, AffectsC: true})
		}
		return stmts, nil

	// --- Accumulator Decrement / Increment ---
	case 0x3A: // DEC A
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpSub,
				Left:  &RegExpr{Reg: RegA, Width: aWidth},
				Right: &ConstExpr{Value: 1, Width: aWidth},
				Width: aWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0x1A: // INC A
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &RegExpr{Reg: RegA, Width: aWidth},
				Right: &ConstExpr{Value: 1, Width: aWidth},
				Width: aWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	// --- Index Increment / Decrement ---
	case 0xE8: // INX
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &RegExpr{Reg: RegX, Width: xWidth},
				Right: &ConstExpr{Value: 1, Width: xWidth},
				Width: xWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegX, Width: xWidth}, xWidth)
		return stmts, nil

	case 0xCA: // DEX
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr: &BinaryExpr{
				Op:    OpSub,
				Left:  &RegExpr{Reg: RegX, Width: xWidth},
				Right: &ConstExpr{Value: 1, Width: xWidth},
				Width: xWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegX, Width: xWidth}, xWidth)
		return stmts, nil

	case 0xC8: // INY
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &RegExpr{Reg: RegY, Width: xWidth},
				Right: &ConstExpr{Value: 1, Width: xWidth},
				Width: xWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegY, Width: xWidth}, xWidth)
		return stmts, nil

	case 0x88: // DEY
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr: &BinaryExpr{
				Op:    OpSub,
				Left:  &RegExpr{Reg: RegY, Width: xWidth},
				Right: &ConstExpr{Value: 1, Width: xWidth},
				Width: xWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegY, Width: xWidth}, xWidth)
		return stmts, nil

	// --- Transfers ---
	case 0xAA: // TAX
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      &RegExpr{Reg: RegA, Width: xWidth},
		})
		emitNZ(&RegExpr{Reg: RegX, Width: xWidth}, xWidth)
		return stmts, nil

	case 0x8A: // TXA
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      &RegExpr{Reg: RegX, Width: aWidth},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0xA8: // TAY
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr:      &RegExpr{Reg: RegA, Width: xWidth},
		})
		emitNZ(&RegExpr{Reg: RegY, Width: xWidth}, xWidth)
		return stmts, nil

	case 0x98: // TYA
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      &RegExpr{Reg: RegY, Width: aWidth},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0xBA: // TSX
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      &RegExpr{Reg: RegS, Width: xWidth},
		})
		emitNZ(&RegExpr{Reg: RegX, Width: xWidth}, xWidth)
		return stmts, nil

	case 0x9A: // TXS
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegS,
			Width:     Width16,
			Expr:      &RegExpr{Reg: RegX, Width: Width16},
		})
		return stmts, nil

	case 0xBB: // TYX
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      &RegExpr{Reg: RegY, Width: xWidth},
		})
		emitNZ(&RegExpr{Reg: RegX, Width: xWidth}, xWidth)
		return stmts, nil

	case 0x9B: // TXY
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr:      &RegExpr{Reg: RegX, Width: xWidth},
		})
		emitNZ(&RegExpr{Reg: RegY, Width: xWidth}, xWidth)
		return stmts, nil

	// --- ASL / LSR Accumulator ---
	case 0x0A: // ASL A
		emit(Statement{
			Kind:      "set_flag",
			TargetFlag: FlagC,
			AffectsC:  true,
			Expr: &BinaryExpr{
				Op:    OpNotEq,
				Left:  &BinaryExpr{Op: OpAnd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: &ConstExpr{Value: signBit(aWidth), Width: aWidth}, Width: aWidth},
				Right: &ConstExpr{Value: 0, Width: aWidth},
				Width: Width8,
			},
		})
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpShl,
				Left:  &RegExpr{Reg: RegA, Width: aWidth},
				Right: &ConstExpr{Value: 1, Width: aWidth},
				Width: aWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0x4A: // LSR A
		emit(Statement{
			Kind:      "set_flag",
			TargetFlag: FlagC,
			AffectsC:  true,
			Expr: &BinaryExpr{
				Op:    OpNotEq,
				Left:  &BinaryExpr{Op: OpAnd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Right: &ConstExpr{Value: 0, Width: aWidth},
				Width: Width8,
			},
		})
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpShr,
				Left:  &RegExpr{Reg: RegA, Width: aWidth},
				Right: &ConstExpr{Value: 1, Width: aWidth},
				Width: aWidth,
			},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	// --- LDA Immediate ---
	case 0xA9: // LDA #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      val,
		})
		emitNZ(val, aWidth)
		return stmts, nil

	// --- LDX Immediate ---
	case 0xA2: // LDX #imm
		val, err := l.readImm(bytes, xWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      val,
		})
		emitNZ(val, xWidth)
		return stmts, nil

	// --- LDY Immediate ---
	case 0xA0: // LDY #imm
		val, err := l.readImm(bytes, xWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr:      val,
		})
		emitNZ(val, xWidth)
		return stmts, nil

	// --- Memory Loads: LDA ---
	case 0xAF: // LDA long
		addr, err := readLongAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "wram"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, aWidth)
		return stmts, nil

	case 0xAD: // LDA abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "ram"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, aWidth)
		return stmts, nil

	case 0xBD: // LDA abs,x
		addr, err := l.readAbsIndexedAddr(bytes, RegX)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "rom"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, aWidth)
		return stmts, nil

	case 0xB9: // LDA abs,y
		addr, err := l.readAbsIndexedAddr(bytes, RegY)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "rom"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, aWidth)
		return stmts, nil

	case 0xA5: // LDA dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "dp"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, aWidth)
		return stmts, nil

	// --- Memory Loads: LDX ---
	case 0xA6: // LDX dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: xWidth, Space: "dp"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, xWidth)
		return stmts, nil

	case 0xAE: // LDX abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: xWidth, Space: "ram"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegX,
			Width:     xWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, xWidth)
		return stmts, nil

	// --- Memory Loads: LDY ---
	case 0xA4: // LDY dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: xWidth, Space: "dp"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, xWidth)
		return stmts, nil

	case 0xAC: // LDY abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: xWidth, Space: "ram"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegY,
			Width:     xWidth,
			Expr:      memExpr,
		})
		emitNZ(memExpr, xWidth)
		return stmts, nil

	// --- Memory Stores: STA ---
	case 0x8F: // STA long
		addr, err := readLongAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegA, Width: aWidth},
			Width:      aWidth,
			Space:      "wram",
		})
		return stmts, nil

	case 0x8D: // STA abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegA, Width: aWidth},
			Width:      aWidth,
			Space:      "ram",
		})
		return stmts, nil

	case 0x85: // STA dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegA, Width: aWidth},
			Width:      aWidth,
			Space:      "dp",
		})
		return stmts, nil

	// --- Memory Stores: STX / STY / STZ ---
	case 0x86: // STX dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegX, Width: xWidth},
			Width:      xWidth,
			Space:      "dp",
		})
		return stmts, nil

	case 0x8E: // STX abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegX, Width: xWidth},
			Width:      xWidth,
			Space:      "ram",
		})
		return stmts, nil

	case 0x84: // STY dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegY, Width: xWidth},
			Width:      xWidth,
			Space:      "dp",
		})
		return stmts, nil

	case 0x8C: // STY abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &RegExpr{Reg: RegY, Width: xWidth},
			Width:      xWidth,
			Space:      "ram",
		})
		return stmts, nil

	case 0x64: // STZ dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &ConstExpr{Value: 0, Width: aWidth},
			Width:      aWidth,
			Space:      "dp",
		})
		return stmts, nil

	case 0x9C: // STZ abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "store_mem",
			MemAddress: addr,
			Expr:       &ConstExpr{Value: 0, Width: aWidth},
			Width:      aWidth,
			Space:      "ram",
		})
		return stmts, nil

	// --- ADC / SBC ---
	case 0x69: // ADC #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &BinaryExpr{Op: OpAdd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
				Right: &BinaryExpr{Op: OpAnd, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Width: aWidth,
			},
			AffectsC: true,
			AffectsV: true,
			AffectsN: true,
			AffectsZ: true,
		})
		return stmts, nil

	case 0x7D: // ADC abs,x
		addr, err := l.readAbsIndexedAddr(bytes, RegX)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "rom"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &BinaryExpr{Op: OpAdd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: memExpr, Width: aWidth},
				Right: &BinaryExpr{Op: OpAnd, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Width: aWidth,
			},
			AffectsC: true,
			AffectsV: true,
			AffectsN: true,
			AffectsZ: true,
		})
		return stmts, nil

	case 0x6D: // ADC abs
		addr, err := l.readAbsAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "ram"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &BinaryExpr{Op: OpAdd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: memExpr, Width: aWidth},
				Right: &BinaryExpr{Op: OpAnd, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Width: aWidth,
			},
			AffectsC: true,
			AffectsV: true,
			AffectsN: true,
			AffectsZ: true,
		})
		return stmts, nil

	case 0x65: // ADC dp
		addr, err := l.readDPAddr(bytes)
		if err != nil {
			return nil, err
		}
		memExpr := &MemReadExpr{Address: addr, Width: aWidth, Space: "dp"}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpAdd,
				Left:  &BinaryExpr{Op: OpAdd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: memExpr, Width: aWidth},
				Right: &BinaryExpr{Op: OpAnd, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Width: aWidth,
			},
			AffectsC: true,
			AffectsV: true,
			AffectsN: true,
			AffectsZ: true,
		})
		return stmts, nil

	case 0xE9: // SBC #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr: &BinaryExpr{
				Op:    OpSub,
				Left:  &BinaryExpr{Op: OpSub, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
				Right: &BinaryExpr{Op: OpXor, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: aWidth}, Width: aWidth},
				Width: aWidth,
			},
			AffectsC: true,
			AffectsV: true,
			AffectsN: true,
			AffectsZ: true,
		})
		return stmts, nil

	// --- AND / ORA / EOR Immediate ---
	case 0x29: // AND #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      &BinaryExpr{Op: OpAnd, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0x09: // ORA #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      &BinaryExpr{Op: OpOr, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	case 0x49: // EOR #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:      "assign_reg",
			TargetReg: RegA,
			Width:     aWidth,
			Expr:      &BinaryExpr{Op: OpXor, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
		})
		emitNZ(&RegExpr{Reg: RegA, Width: aWidth}, aWidth)
		return stmts, nil

	// --- Comparisons ---
	case 0xC9: // CMP #imm
		val, err := l.readImm(bytes, aWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:     "update_flags",
			Expr:     &BinaryExpr{Op: OpSub, Left: &RegExpr{Reg: RegA, Width: aWidth}, Right: val, Width: aWidth},
			Width:    aWidth,
			AffectsC: true,
			AffectsZ: true,
			AffectsN: true,
		})
		return stmts, nil

	case 0xE0: // CPX #imm
		val, err := l.readImm(bytes, xWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:     "update_flags",
			Expr:     &BinaryExpr{Op: OpSub, Left: &RegExpr{Reg: RegX, Width: xWidth}, Right: val, Width: xWidth},
			Width:    xWidth,
			AffectsC: true,
			AffectsZ: true,
			AffectsN: true,
		})
		return stmts, nil

	case 0xC0: // CPY #imm
		val, err := l.readImm(bytes, xWidth)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:     "update_flags",
			Expr:     &BinaryExpr{Op: OpSub, Left: &RegExpr{Reg: RegY, Width: xWidth}, Right: val, Width: xWidth},
			Width:    xWidth,
			AffectsC: true,
			AffectsZ: true,
			AffectsN: true,
		})
		return stmts, nil

	// --- Control Flow ---
	case 0x80: // BRA rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "jump",
			TargetAddr: target,
		})
		return stmts, nil

	case 0x82: // BRL rel16
		target, err := branchTarget16(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:       "jump",
			TargetAddr: target,
		})
		return stmts, nil

	case 0xD0: // BNE rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagZ}, Right: &ConstExpr{Value: 0, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0xF0: // BEQ rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagZ}, Right: &ConstExpr{Value: 1, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0x90: // BCC rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 0, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0xB0: // BCS rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagC}, Right: &ConstExpr{Value: 1, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0x10: // BPL rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagN}, Right: &ConstExpr{Value: 0, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0x30: // BMI rel8
		target, err := branchTarget8(inst.Address, bytes)
		if err != nil {
			return nil, err
		}
		emit(Statement{
			Kind:            "branch",
			Condition:       &BinaryExpr{Op: OpEqual, Left: &FlagExpr{Flag: FlagN}, Right: &ConstExpr{Value: 1, Width: Width8}, Width: Width8},
			TargetAddr:      target,
			FallthroughAddr: nextAddr,
		})
		return stmts, nil

	case 0x60: // RTS
		emit(Statement{Kind: "return", TargetTemp: "rts"})
		return stmts, nil

	case 0x6B: // RTL
		emit(Statement{Kind: "return", TargetTemp: "rtl"})
		return stmts, nil

	default:
		return nil, fmt.Errorf("unsupported opcode 0x%02X (%s)", op, inst.Mnemonic)
	}
}

func (l *Lifter) readImm(bytes []byte, w Width) (Expr, error) {
	if w == Width8 {
		if len(bytes) < 2 {
			return nil, fmt.Errorf("truncated 8-bit immediate")
		}
		return &ConstExpr{Value: uint32(bytes[1]), Width: Width8}, nil
	}
	if len(bytes) < 3 {
		return nil, fmt.Errorf("truncated 16-bit immediate")
	}
	val := uint32(bytes[1]) | (uint32(bytes[2]) << 8)
	return &ConstExpr{Value: val, Width: Width16}, nil
}

func (l *Lifter) readDPAddr(bytes []byte) (Expr, error) {
	if len(bytes) < 2 {
		return nil, fmt.Errorf("truncated direct page address")
	}
	dpOffset := uint32(bytes[1])
	return &BinaryExpr{
		Op:    OpAdd,
		Left:  &RegExpr{Reg: RegD, Width: Width16},
		Right: &ConstExpr{Value: dpOffset, Width: Width16},
		Width: Width16,
	}, nil
}

func (l *Lifter) readAbsAddr(bytes []byte) (Expr, error) {
	if len(bytes) < 3 {
		return nil, fmt.Errorf("truncated absolute address")
	}
	addr := uint32(bytes[1]) | (uint32(bytes[2]) << 8)
	return &BinaryExpr{
		Op: OpOr,
		Left: &BinaryExpr{
			Op:    OpShl,
			Left:  &RegExpr{Reg: RegDB, Width: Width8},
			Right: &ConstExpr{Value: 16, Width: Width8},
			Width: Width24,
		},
		Right: &ConstExpr{Value: addr, Width: Width24},
		Width: Width24,
	}, nil
}

func (l *Lifter) readAbsIndexedAddr(bytes []byte, idxReg Register) (Expr, error) {
	if len(bytes) < 3 {
		return nil, fmt.Errorf("truncated absolute indexed address")
	}
	addr := uint32(bytes[1]) | (uint32(bytes[2]) << 8)
	base := &BinaryExpr{
		Op: OpOr,
		Left: &BinaryExpr{
			Op:    OpShl,
			Left:  &RegExpr{Reg: RegDB, Width: Width8},
			Right: &ConstExpr{Value: 16, Width: Width8},
			Width: Width24,
		},
		Right: &ConstExpr{Value: addr, Width: Width24},
		Width: Width24,
	}
	return &BinaryExpr{
		Op:    OpAdd,
		Left:  base,
		Right: &RegExpr{Reg: idxReg, Width: Width16},
		Width: Width24,
	}, nil
}

func readLongAddr(bytes []byte) (Expr, error) {
	if len(bytes) < 4 {
		return nil, fmt.Errorf("truncated long address")
	}
	addr := uint32(bytes[1]) | (uint32(bytes[2]) << 8) | (uint32(bytes[3]) << 16)
	return &ConstExpr{Value: addr, Width: Width24}, nil
}

func branchTarget8(pc uint32, bytes []byte) (uint32, error) {
	if len(bytes) < 2 {
		return 0, fmt.Errorf("truncated relative branch")
	}
	rel := int8(bytes[1])
	// Next instruction is at pc + 2 in the same bank
	bank := pc & 0xFF0000
	next16 := uint16(pc) + 2
	target16 := uint16(int32(next16) + int32(rel))
	return bank | uint32(target16), nil
}

func branchTarget16(pc uint32, bytes []byte) (uint32, error) {
	if len(bytes) < 3 {
		return 0, fmt.Errorf("truncated relative long branch")
	}
	rel := int16(uint16(bytes[1]) | (uint16(bytes[2]) << 8))
	bank := pc & 0xFF0000
	next16 := uint16(pc) + 3
	target16 := uint16(int32(next16) + int32(rel))
	return bank | uint32(target16), nil
}

func signBit(w Width) uint32 {
	if w == Width8 {
		return 0x80
	}
	return 0x8000
}
