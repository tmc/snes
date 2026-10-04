package callsummary

import (
	"encoding/hex"
	"fmt"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// Options configures the callee contract analyzer.
type Options struct {
	CallOp      byte                    // 0x20 for JSR (default), 0x22 for JSL
	InitialM    bool                    // true = 8-bit (default true), false = 16-bit
	InitialX    bool                    // true = 8-bit (default true), false = 16-bit
	userSetM    bool
	userSetX    bool
	Subroutines map[uint32]CallContract // pre-computed summaries for nested calls
}

// Option configures an analysis Option.
type Option func(*Options)

func defaultOptions() Options {
	return Options{
		CallOp:      CallJSR,
		InitialM:    true,
		InitialX:    true,
		Subroutines: make(map[uint32]CallContract),
	}
}

// WithCallOp sets the caller call opcode (CallJSR or CallJSL).
func WithCallOp(op byte) Option {
	return func(o *Options) {
		o.CallOp = op
	}
}

// WithInitialM sets the initial M flag width (true for 8-bit, false for 16-bit).
func WithInitialM(m8 bool) Option {
	return func(o *Options) {
		o.InitialM = m8
		o.userSetM = true
	}
}

// WithInitialX sets the initial X flag width (true for 8-bit, false for 16-bit).
func WithInitialX(x8 bool) Option {
	return func(o *Options) {
		o.InitialX = x8
		o.userSetX = true
	}
}

// WithSubroutineSummaries registers pre-computed contracts for nested subroutines.
func WithSubroutineSummaries(summaries map[uint32]CallContract) Option {
	return func(o *Options) {
		if summaries != nil {
			o.Subroutines = summaries
		}
	}
}

type valType int

const (
	valInitial valType = iota // preserved entry value
	valConst                  // known constant
	valClobbered              // modified / indeterminate
)

type regVal struct {
	kind valType
	val  uint16
}

type flagVal struct {
	kind valType
	val  bool
}

type stackItemKind int

const (
	itemReturnAddr stackItemKind = iota
	itemDB
	itemDP
	itemA
	itemX
	itemY
	itemP
	itemConst
	itemOther
)

type stackSlot struct {
	kind       stackItemKind
	bytes      int
	val        uint16
	regVal     regVal
	savedFlags map[Flag]flagVal
}

type symbolicState struct {
	regs       map[Register]regVal
	flags      map[Flag]flagVal
	stack      []stackSlot
	currentM   bool // true = 8-bit, false = 16-bit
	currentX   bool // true = 8-bit, false = 16-bit
	stackTainted bool
	callOp     byte
}

func newSymbolicState(opts Options) symbolicState {
	s := symbolicState{
		regs:     make(map[Register]regVal),
		flags:    make(map[Flag]flagVal),
		currentM: opts.InitialM,
		currentX: opts.InitialX,
		callOp:   opts.CallOp,
	}

	for r := RegA; r <= RegPB; r++ {
		s.regs[r] = regVal{kind: valInitial}
	}
	for f := FlagM; f <= FlagD; f++ {
		s.flags[f] = flagVal{kind: valInitial}
	}

	retBytes := 2
	if opts.CallOp == CallJSL {
		retBytes = 3
	}
	s.stack = []stackSlot{{kind: itemReturnAddr, bytes: retBytes}}
	return s
}

func (s *symbolicState) clone() symbolicState {
	c := symbolicState{
		regs:         make(map[Register]regVal, len(s.regs)),
		flags:        make(map[Flag]flagVal, len(s.flags)),
		stack:        make([]stackSlot, len(s.stack)),
		currentM:     s.currentM,
		currentX:     s.currentX,
		stackTainted: s.stackTainted,
		callOp:       s.callOp,
	}
	for k, v := range s.regs {
		c.regs[k] = v
	}
	for k, v := range s.flags {
		c.flags[k] = v
	}
	for i, slot := range s.stack {
		c.stack[i] = slot
		if slot.savedFlags != nil {
			c.stack[i].savedFlags = make(map[Flag]flagVal, len(slot.savedFlags))
			for fk, fv := range slot.savedFlags {
				c.stack[i].savedFlags[fk] = fv
			}
		}
	}
	return c
}

func (s *symbolicState) push(slot stackSlot) {
	s.stack = append(s.stack, slot)
}

func (s *symbolicState) pop(nBytes int) (stackSlot, error) {
	if len(s.stack) == 0 {
		s.stackTainted = true
		return stackSlot{}, ErrStackUnderflow
	}
	top := s.stack[len(s.stack)-1]
	if top.kind == itemReturnAddr {
		// Attempting to pop return address before RTS/RTL
		s.stackTainted = true
		return stackSlot{}, ErrStackUnderflow
	}

	if top.bytes == nBytes {
		s.stack = s.stack[:len(s.stack)-1]
		return top, nil
	}

	if top.bytes > nBytes {
		top.bytes -= nBytes
		s.stack[len(s.stack)-1] = top
		return stackSlot{kind: itemOther, bytes: nBytes}, nil
	}

	// top.bytes < nBytes
	needed := nBytes - top.bytes
	s.stack = s.stack[:len(s.stack)-1]
	if len(s.stack) == 0 {
		s.stackTainted = true
		return stackSlot{}, ErrStackUnderflow
	}
	if s.stack[len(s.stack)-1].kind == itemReturnAddr {
		s.stackTainted = true
		return stackSlot{}, ErrStackUnderflow
	}
	if s.stack[len(s.stack)-1].bytes >= needed {
		s.stack[len(s.stack)-1].bytes -= needed
		if s.stack[len(s.stack)-1].bytes == 0 {
			s.stack = s.stack[:len(s.stack)-1]
		}
		return stackSlot{kind: itemOther, bytes: nBytes}, nil
	}

	s.stackTainted = true
	return stackSlot{}, ErrStackUnderflow
}

func (s *symbolicState) clobberFlags(flags ...Flag) {
	for _, f := range flags {
		s.flags[f] = flagVal{kind: valClobbered}
	}
}

func (s *symbolicState) toContract(returnsWith byte) (CallContract, error) {
	c := CallContract{
		ReturnsWith: returnsWith,
	}

	// 1. Registers
	for r := RegA; r <= RegPB; r++ {
		rv := s.regs[r]
		var rs RegisterState
		switch rv.kind {
		case valInitial:
			rs = RegisterState{Status: Preserved}
		case valConst:
			rs = RegisterState{Status: Guaranteed, Value: rv.val}
		default:
			rs = RegisterState{Status: Clobbered}
		}
		c.SetRegister(r, rs)
	}

	// 2. Flags
	for f := FlagM; f <= FlagD; f++ {
		fv := s.flags[f]
		var fs FlagState
		switch fv.kind {
		case valInitial:
			fs = FlagState{Status: Preserved}
		case valConst:
			fs = FlagState{Status: Guaranteed, Value: fv.val}
		default:
			fs = FlagState{Status: Clobbered}
		}
		c.SetFlag(f, fs)
	}

	// 3. Stack discipline & StackDelta
	expectedReturnBytes := 2
	expectedReturnOp := ReturnRTS
	if s.callOp == CallJSL {
		expectedReturnBytes = 3
		expectedReturnOp = ReturnRTL
	}

	if returnsWith != expectedReturnOp {
		c.Registers.S = RegisterState{Status: Clobbered}
		c.StackDelta = -1
		return c, fmt.Errorf("call 0x%02X returned with 0x%02X, want 0x%02X: %w",
			s.callOp, returnsWith, expectedReturnOp, ErrMismatchedReturn)
	}

	if s.stackTainted {
		c.Registers.S = RegisterState{Status: Clobbered}
		c.StackDelta = -1
		return c, ErrUnbalancedStack
	}

	// Verify stack contains exactly the return address slot
	if len(s.stack) != 1 {
		// Calculate net delta
		unpopped := 0
		for _, slot := range s.stack {
			if slot.kind != itemReturnAddr {
				unpopped += slot.bytes
			}
		}
		c.StackDelta = -unpopped
		c.Registers.S = RegisterState{Status: Clobbered}
		return c, fmt.Errorf("unbalanced stack with %d unpopped bytes: %w", unpopped, ErrUnbalancedStack)
	}

	if s.stack[0].kind != itemReturnAddr || s.stack[0].bytes != expectedReturnBytes {
		c.StackDelta = -1
		c.Registers.S = RegisterState{Status: Clobbered}
		return c, ErrUnbalancedStack
	}

	// Stack balanced: return address cleanly consumed
	c.StackDelta = 0
	c.Registers.S = RegisterState{Status: Preserved}
	return c, nil
}

func parseInst(inst recovery.Instruction) (byte, []byte, string) {
	var raw []byte
	if inst.Bytes != "" {
		if b, err := hex.DecodeString(inst.Bytes); err == nil && len(b) > 0 {
			raw = b
		}
	}
	opByte := inst.Opcode
	if opByte == 0 && len(raw) > 0 {
		opByte = raw[0]
	}
	if len(raw) == 0 && opByte != 0 {
		raw = []byte{opByte}
	}
	mnemonic := inst.Mnemonic
	if mnemonic == "" {
		mnemonic = cpu.Opcodes[opByte].Name
	}
	return opByte, raw, mnemonic
}

func readImm8(raw []byte) byte {
	if len(raw) >= 2 {
		return raw[1]
	}
	return 0
}

func readImm16(raw []byte) uint16 {
	if len(raw) >= 3 {
		return uint16(raw[1]) | (uint16(raw[2]) << 8)
	}
	if len(raw) >= 2 {
		return uint16(raw[1])
	}
	return 0
}

// step executes a single instruction on symbolicState.
// Returns (isReturn, returnOp, error).
func (s *symbolicState) step(inst recovery.Instruction, opts Options) (bool, byte, error) {
	opByte, raw, mnemonic := parseInst(inst)

	switch opByte {
	case ReturnRTS: // 0x60
		return true, ReturnRTS, nil

	case ReturnRTL: // 0x6B
		return true, ReturnRTL, nil

	// Stack Pushes
	case 0x48: // PHA
		bytes := 1
		if !s.currentM {
			bytes = 2
		}
		s.push(stackSlot{kind: itemA, bytes: bytes, regVal: s.regs[RegA]})

	case 0xDA: // PHX
		bytes := 1
		if !s.currentX {
			bytes = 2
		}
		s.push(stackSlot{kind: itemX, bytes: bytes, regVal: s.regs[RegX]})

	case 0x5A: // PHY
		bytes := 1
		if !s.currentX {
			bytes = 2
		}
		s.push(stackSlot{kind: itemY, bytes: bytes, regVal: s.regs[RegY]})

	case 0x08: // PHP
		saved := make(map[Flag]flagVal, len(s.flags))
		for k, v := range s.flags {
			saved[k] = v
		}
		s.push(stackSlot{kind: itemP, bytes: 1, savedFlags: saved})

	case 0x8B: // PHB
		s.push(stackSlot{kind: itemDB, bytes: 1, regVal: s.regs[RegDB]})

	case 0x0B: // PHD
		s.push(stackSlot{kind: itemDP, bytes: 2, regVal: s.regs[RegDP]})

	case 0x4B: // PHK
		s.push(stackSlot{kind: itemOther, bytes: 1, regVal: s.regs[RegPB]})

	case 0xF4: // PEA $xxxx
		val := readImm16(raw)
		s.push(stackSlot{kind: itemConst, bytes: 2, val: val, regVal: regVal{kind: valConst, val: val}})

	case 0xD4, 0x62: // PEI ($xx), PER $xxxx
		s.push(stackSlot{kind: itemOther, bytes: 2})

	// Stack Pops
	case 0x68: // PLA
		bytes := 1
		if !s.currentM {
			bytes = 2
		}
		slot, err := s.pop(bytes)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemA && slot.bytes == bytes {
			s.regs[RegA] = slot.regVal
		} else if slot.kind == itemConst {
			s.regs[RegA] = regVal{kind: valConst, val: slot.val}
		} else {
			s.regs[RegA] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0xFA: // PLX
		bytes := 1
		if !s.currentX {
			bytes = 2
		}
		slot, err := s.pop(bytes)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemX && slot.bytes == bytes {
			s.regs[RegX] = slot.regVal
		} else if slot.kind == itemConst {
			s.regs[RegX] = regVal{kind: valConst, val: slot.val}
		} else {
			s.regs[RegX] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0x7A: // PLY
		bytes := 1
		if !s.currentX {
			bytes = 2
		}
		slot, err := s.pop(bytes)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemY && slot.bytes == bytes {
			s.regs[RegY] = slot.regVal
		} else if slot.kind == itemConst {
			s.regs[RegY] = regVal{kind: valConst, val: slot.val}
		} else {
			s.regs[RegY] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0xAB: // PLB
		slot, err := s.pop(1)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemDB {
			s.regs[RegDB] = slot.regVal
		} else if slot.kind == itemConst {
			s.regs[RegDB] = regVal{kind: valConst, val: slot.val & 0xFF}
		} else if slot.kind == itemA && slot.regVal.kind == valConst {
			s.regs[RegDB] = regVal{kind: valConst, val: slot.regVal.val & 0xFF}
		} else {
			s.regs[RegDB] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0x2B: // PLD
		slot, err := s.pop(2)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemDP {
			s.regs[RegDP] = slot.regVal
		} else if slot.kind == itemConst {
			s.regs[RegDP] = regVal{kind: valConst, val: slot.val}
		} else if slot.kind == itemA && slot.regVal.kind == valConst {
			s.regs[RegDP] = regVal{kind: valConst, val: slot.regVal.val}
		} else {
			s.regs[RegDP] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0x28: // PLP
		slot, err := s.pop(1)
		if err != nil {
			return false, 0, err
		}
		if slot.kind == itemP && slot.savedFlags != nil {
			for f, v := range slot.savedFlags {
				s.flags[f] = v
			}
			if s.flags[FlagM].kind == valConst {
				s.currentM = s.flags[FlagM].val
			}
			if s.flags[FlagX].kind == valConst {
				s.currentX = s.flags[FlagX].val
			}
		} else {
			s.clobberFlags(FlagM, FlagX, FlagC, FlagZ, FlagN, FlagI, FlagD)
		}

	// Flag instructions
	case 0xC2: // REP #imm
		imm := readImm8(raw)
		if imm&0x20 != 0 {
			s.flags[FlagM] = flagVal{kind: valConst, val: false}
			s.currentM = false
		}
		if imm&0x10 != 0 {
			s.flags[FlagX] = flagVal{kind: valConst, val: false}
			s.currentX = false
		}
		if imm&0x01 != 0 {
			s.flags[FlagC] = flagVal{kind: valConst, val: false}
		}
		if imm&0x08 != 0 {
			s.flags[FlagD] = flagVal{kind: valConst, val: false}
		}
		if imm&0x04 != 0 {
			s.flags[FlagI] = flagVal{kind: valConst, val: false}
		}
		if imm&0x02 != 0 {
			s.flags[FlagZ] = flagVal{kind: valConst, val: false}
		}
		if imm&0x80 != 0 {
			s.flags[FlagN] = flagVal{kind: valConst, val: false}
		}

	case 0xE2: // SEP #imm
		imm := readImm8(raw)
		if imm&0x20 != 0 {
			s.flags[FlagM] = flagVal{kind: valConst, val: true}
			s.currentM = true
		}
		if imm&0x10 != 0 {
			s.flags[FlagX] = flagVal{kind: valConst, val: true}
			s.currentX = true
		}
		if imm&0x01 != 0 {
			s.flags[FlagC] = flagVal{kind: valConst, val: true}
		}
		if imm&0x08 != 0 {
			s.flags[FlagD] = flagVal{kind: valConst, val: true}
		}
		if imm&0x04 != 0 {
			s.flags[FlagI] = flagVal{kind: valConst, val: true}
		}
		if imm&0x02 != 0 {
			s.flags[FlagZ] = flagVal{kind: valConst, val: true}
		}
		if imm&0x80 != 0 {
			s.flags[FlagN] = flagVal{kind: valConst, val: true}
		}

	case 0x18: // CLC
		s.flags[FlagC] = flagVal{kind: valConst, val: false}
	case 0x38: // SEC
		s.flags[FlagC] = flagVal{kind: valConst, val: true}
	case 0xD8: // CLD
		s.flags[FlagD] = flagVal{kind: valConst, val: false}
	case 0xF8: // SED
		s.flags[FlagD] = flagVal{kind: valConst, val: true}
	case 0x58: // CLI
		s.flags[FlagI] = flagVal{kind: valConst, val: false}
	case 0x78: // SEI
		s.flags[FlagI] = flagVal{kind: valConst, val: true}
	case 0xB8: // CLV
		// overflow flag V cleared

	// Accumulator instructions
	case 0xA9: // LDA #imm
		if s.currentM {
			val := uint16(readImm8(raw))
			s.regs[RegA] = regVal{kind: valConst, val: val}
		} else {
			val := readImm16(raw)
			s.regs[RegA] = regVal{kind: valConst, val: val}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0xA5, 0xB5, 0xAD, 0xBD, 0xB9, 0xAF, 0xBF, 0xA7, 0xB7, 0xA1, 0xB1, 0xB2, 0xA3, 0xB3: // LDA memory
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0xA2: // LDX #imm
		if s.currentX {
			val := uint16(readImm8(raw))
			s.regs[RegX] = regVal{kind: valConst, val: val}
		} else {
			val := readImm16(raw)
			s.regs[RegX] = regVal{kind: valConst, val: val}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0xA6, 0xB6, 0xAE, 0xBE: // LDX memory
		s.regs[RegX] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0xA0: // LDY #imm
		if s.currentX {
			val := uint16(readImm8(raw))
			s.regs[RegY] = regVal{kind: valConst, val: val}
		} else {
			val := readImm16(raw)
			s.regs[RegY] = regVal{kind: valConst, val: val}
		}
		s.clobberFlags(FlagZ, FlagN)

	case 0xA4, 0xB4, 0xAC, 0xBC: // LDY memory
		s.regs[RegY] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0xE8, 0xCA: // INX, DEX
		s.regs[RegX] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0xC8, 0x88: // INY, DEY
		s.regs[RegY] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0x1A, 0x3A: // INC A, DEC A
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0xAA: // TAX
		s.regs[RegX] = s.regs[RegA]
		s.clobberFlags(FlagZ, FlagN)

	case 0xA8: // TAY
		s.regs[RegY] = s.regs[RegA]
		s.clobberFlags(FlagZ, FlagN)

	case 0x8A: // TXA
		s.regs[RegA] = s.regs[RegX]
		s.clobberFlags(FlagZ, FlagN)

	case 0x98: // TYA
		s.regs[RegA] = s.regs[RegY]
		s.clobberFlags(FlagZ, FlagN)

	case 0xBA: // TSX
		s.regs[RegX] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0x9A: // TXS
		s.regs[RegS] = regVal{kind: valClobbered}
		s.stackTainted = true

	case 0x1B: // TCS
		s.regs[RegS] = regVal{kind: valClobbered}
		s.stackTainted = true

	case 0x3B: // TSC
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)

	case 0x5B: // TCD
		s.regs[RegDP] = s.regs[RegA]
		s.clobberFlags(FlagZ, FlagN)

	case 0x7B: // TDC
		s.regs[RegA] = s.regs[RegDP]
		s.clobberFlags(FlagZ, FlagN)

	case 0x9B: // TXY
		s.regs[RegY] = s.regs[RegX]
		s.clobberFlags(FlagZ, FlagN)

	case 0xBB: // TYX
		s.regs[RegX] = s.regs[RegY]
		s.clobberFlags(FlagZ, FlagN)

	case 0xEB: // XBA
		if s.regs[RegA].kind == valConst {
			v := s.regs[RegA].val
			s.regs[RegA] = regVal{kind: valConst, val: (v>>8)&0xFF | (v&0xFF)<<8}
		} else {
			s.regs[RegA] = regVal{kind: valClobbered}
		}
		s.clobberFlags(FlagZ, FlagN)

	// Memory Stores (does NOT modify registers or flags)
	case 0x85, 0x95, 0x8D, 0x9D, 0x99, 0x8F, 0x9F, 0x87, 0x97, 0x81, 0x91, 0x92, 0x02: // STA
	case 0x86, 0x96, 0x8E: // STX
	case 0x84, 0x94, 0x8C: // STY
	case 0x64, 0x74, 0x9C, 0x9E: // STZ

	// Compares & Tests (flags modified, registers preserved)
	case 0xC9, 0xC5, 0xD5, 0xCD, 0xDD, 0xD9, 0xCF, 0xDF, 0xC7, 0xD7, 0xC1, 0xD1, 0xD2: // CMP
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0xE0, 0xE4, 0xEC: // CPX
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0xC0, 0xC4, 0xCC: // CPY
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0x89, 0x24, 0x2C, 0x34, 0x3C: // BIT
		s.clobberFlags(FlagZ, FlagN)
	case 0x04, 0x0C, 0x14, 0x1C: // TSB, TRB
		s.clobberFlags(FlagZ)

	// ALU & Shifts
	case 0x69, 0x65, 0x6D, 0x75, 0x7D, 0x79, 0x61, 0x71, 0x72, 0x67, 0x77, 0x6F, 0x7F, 0x63, 0x73: // ADC
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0xE9, 0xE5, 0xED, 0xF5, 0xFD, 0xF9, 0xE1, 0xF1, 0xF2, 0xE7, 0xF7, 0xEF, 0xFF, 0xE3, 0xF3: // SBC
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0x29, 0x25, 0x2D, 0x35, 0x3D, 0x39, 0x2F, 0x3F, 0x27, 0x37, 0x21, 0x31, 0x32: // AND
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)
	case 0x09, 0x05, 0x0D, 0x15, 0x1D, 0x19, 0x0F, 0x1F, 0x07, 0x17, 0x01, 0x11, 0x12, 0x03, 0x13: // ORA
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)
	case 0x49, 0x45, 0x4D, 0x55, 0x5D, 0x59, 0x4F, 0x5F, 0x47, 0x57, 0x41, 0x51, 0x52: // EOR
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN)
	case 0x0A, 0x4A, 0x2A, 0x6A: // ASL A, LSR A, ROL A, ROR A
		s.regs[RegA] = regVal{kind: valClobbered}
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0x06, 0x0E, 0x16, 0x1E, 0x46, 0x4E, 0x56, 0x5E, 0x26, 0x2E, 0x36, 0x3E, 0x66, 0x6E, 0x76, 0x7E: // Memory shifts/rotates
		s.clobberFlags(FlagZ, FlagN, FlagC)
	case 0xE6, 0xF6, 0xEE, 0xFE, 0xC6, 0xD6, 0xCE, 0xDE: // Memory INC, DEC
		s.clobberFlags(FlagZ, FlagN)

	// Block Move
	case 0x44, 0x54: // MVP, MVN
		s.regs[RegA] = regVal{kind: valConst, val: 0xFFFF}
		s.regs[RegX] = regVal{kind: valClobbered}
		s.regs[RegY] = regVal{kind: valClobbered}
		if len(raw) >= 2 {
			s.regs[RegDB] = regVal{kind: valConst, val: uint16(raw[1])}
		} else {
			s.regs[RegDB] = regVal{kind: valClobbered}
		}

	case 0xEA: // NOP
		// No effect

	// Nested subroutine calls
	case CallJSR, CallJSL:
		var target uint32
		if opByte == CallJSR && len(raw) >= 3 {
			target = (inst.Address & 0xFF0000) | uint32(raw[1]) | (uint32(raw[2]) << 8)
		} else if opByte == CallJSL && len(raw) >= 4 {
			target = uint32(raw[1]) | (uint32(raw[2]) << 8) | (uint32(raw[3]) << 16)
		}
		if sub, ok := opts.Subroutines[target]; ok {
			s.applySubroutineContract(sub)
		} else {
			// Conservative fallback for unknown nested call:
			// Clobber A, X, Y and arithmetic flags
			s.regs[RegA] = regVal{kind: valClobbered}
			s.regs[RegX] = regVal{kind: valClobbered}
			s.regs[RegY] = regVal{kind: valClobbered}
			s.clobberFlags(FlagZ, FlagN, FlagC)
		}

	// Branches & Jumps (handled by CFG traversal or ignored in linear block step)
	case 0x80, 0x82, 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0, 0x4C, 0x5C:
		// Control flow transition

	default:
		// Conservatively clobber outputs for unknown/unmodeled instruction
		s.regs[RegA] = regVal{kind: valClobbered}
		s.regs[RegX] = regVal{kind: valClobbered}
		s.regs[RegY] = regVal{kind: valClobbered}
		s.clobberFlags(FlagM, FlagX, FlagC, FlagZ, FlagN, FlagI, FlagD)
		if mnemonic == "" {
			mnemonic = fmt.Sprintf("0x%02X", opByte)
		}
	}

	return false, 0, nil
}

func (s *symbolicState) applySubroutineContract(sub CallContract) {
	// Apply register preservations/guarantees
	for r := RegA; r <= RegPB; r++ {
		rs := sub.Register(r)
		switch rs.Status {
		case Preserved:
			// remains whatever s.regs[r] currently is
		case Guaranteed:
			s.regs[r] = regVal{kind: valConst, val: rs.Value}
		default:
			s.regs[r] = regVal{kind: valClobbered}
		}
	}
	// Apply flag preservations/guarantees
	for f := FlagM; f <= FlagD; f++ {
		fs := sub.Flag(f)
		switch fs.Status {
		case Preserved:
			// remains
		case Guaranteed:
			s.flags[f] = flagVal{kind: valConst, val: fs.Value}
		default:
			s.flags[f] = flagVal{kind: valClobbered}
		}
	}
	if s.flags[FlagM].kind == valConst {
		s.currentM = s.flags[FlagM].val
	}
	if s.flags[FlagX].kind == valConst {
		s.currentX = s.flags[FlagX].val
	}
	if sub.StackDelta != 0 {
		s.stackTainted = true
	}
}

// mergeContracts merges two path contracts into a conservative joint contract.
func mergeContracts(c1, c2 CallContract) CallContract {
	merged := CallContract{}

	// Return opcode must match
	if c1.ReturnsWith == c2.ReturnsWith {
		merged.ReturnsWith = c1.ReturnsWith
	} else {
		merged.ReturnsWith = 0
	}

	// Stack delta
	if c1.StackDelta == c2.StackDelta {
		merged.StackDelta = c1.StackDelta
	} else {
		merged.StackDelta = -1
	}

	// Registers
	for r := RegA; r <= RegPB; r++ {
		r1 := c1.Register(r)
		r2 := c2.Register(r)
		if r1.Status == Preserved && r2.Status == Preserved {
			merged.SetRegister(r, RegisterState{Status: Preserved})
		} else if r1.Status == Guaranteed && r2.Status == Guaranteed && r1.Value == r2.Value {
			merged.SetRegister(r, RegisterState{Status: Guaranteed, Value: r1.Value})
		} else {
			merged.SetRegister(r, RegisterState{Status: Clobbered})
		}
	}

	// Flags
	for f := FlagM; f <= FlagD; f++ {
		f1 := c1.Flag(f)
		f2 := c2.Flag(f)
		if f1.Status == Preserved && f2.Status == Preserved {
			merged.SetFlag(f, FlagState{Status: Preserved})
		} else if f1.Status == Guaranteed && f2.Status == Guaranteed && f1.Value == f2.Value {
			merged.SetFlag(f, FlagState{Status: Guaranteed, Value: f1.Value})
		} else {
			merged.SetFlag(f, FlagState{Status: Clobbered})
		}
	}

	return merged
}

// AnalyzeInstructions analyzes a sequence of instructions representing a callee subroutine.
func AnalyzeInstructions(insns []recovery.Instruction, opts ...Option) (CallContract, error) {
	if len(insns) == 0 {
		return CallContract{}, ErrNoReturn
	}

	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	if !cfg.userSetM && len(insns) > 0 {
		if insns[0].Context.M == "clear" || insns[0].Context.M == "0" {
			cfg.InitialM = false
		}
	}
	if !cfg.userSetX && len(insns) > 0 {
		if insns[0].Context.X == "clear" || insns[0].Context.X == "0" {
			cfg.InitialX = false
		}
	}

	state := newSymbolicState(cfg)

	for _, inst := range insns {
		isRet, retOp, err := state.step(inst, cfg)
		if err != nil {
			contract, _ := state.toContract(ReturnRTS)
			return contract, err
		}
		if isRet {
			return state.toContract(retOp)
		}
	}

	contract, _ := state.toContract(0)
	return contract, ErrNoReturn
}

// AnalyzeBlock analyzes a single basic block callee subroutine.
func AnalyzeBlock(block *structure.BasicBlock, opts ...Option) (CallContract, error) {
	if block == nil || len(block.Instructions) == 0 {
		return CallContract{}, ErrNoReturn
	}
	return AnalyzeInstructions(block.Instructions, opts...)
}

// AnalyzeRoutine analyzes an acyclic routine structure (CFG of basic blocks or flat instructions).
func AnalyzeRoutine(ast *RoutineAST, opts ...Option) (CallContract, error) {
	if ast == nil {
		return CallContract{}, ErrNoReturn
	}
	if len(ast.Blocks) == 0 && len(ast.Instructions) > 0 {
		return AnalyzeInstructions(ast.Instructions, opts...)
	}
	return AnalyzeBlocks(ast.Blocks, opts...)
}

// AnalyzeBlocks analyzes an acyclic control flow graph composed of basic blocks.
func AnalyzeBlocks(blocks []*structure.BasicBlock, opts ...Option) (CallContract, error) {
	if len(blocks) == 0 {
		return CallContract{}, ErrNoReturn
	}
	if len(blocks) == 1 {
		return AnalyzeBlock(blocks[0], opts...)
	}

	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	if !cfg.userSetM && len(blocks[0].Instructions) > 0 {
		if blocks[0].Instructions[0].Context.M == "clear" || blocks[0].Instructions[0].Context.M == "0" {
			cfg.InitialM = false
		}
	}
	if !cfg.userSetX && len(blocks[0].Instructions) > 0 {
		if blocks[0].Instructions[0].Context.X == "clear" || blocks[0].Instructions[0].Context.X == "0" {
			cfg.InitialX = false
		}
	}

	blockMap := make(map[uint32]*structure.BasicBlock, len(blocks))
	for _, b := range blocks {
		blockMap[b.StartAddress] = b
	}

	entryAddr := blocks[0].StartAddress
	initState := newSymbolicState(cfg)

	var pathContracts []CallContract
	active := make(map[uint32]bool)

	var traverse func(addr uint32, state symbolicState) error
	traverse = func(addr uint32, state symbolicState) error {
		if active[addr] {
			return fmt.Errorf("cycle detected at block $%06X: %w", addr, ErrCyclicRoutine)
		}
		active[addr] = true
		defer delete(active, addr)

		b, ok := blockMap[addr]
		if !ok {
			// Unmodeled external branch: conservative fallback
			c := ClobberedContract(ReturnRTS)
			pathContracts = append(pathContracts, c)
			return nil
		}

		for _, inst := range b.Instructions {
			isRet, retOp, err := state.step(inst, cfg)
			if err != nil {
				contract, _ := state.toContract(ReturnRTS)
				pathContracts = append(pathContracts, contract)
				return err
			}
			if isRet {
				contract, err := state.toContract(retOp)
				pathContracts = append(pathContracts, contract)
				return err
			}
		}

		// Find successors
		succs := b.Successors
		if len(succs) == 0 && len(b.Instructions) > 0 {
			lastInst := b.Instructions[len(b.Instructions)-1]
			opByte, raw, _ := parseInst(lastInst)
			bank := lastInst.Address & 0xFF0000

			switch opByte {
			case 0x80: // BRA rel8
				if len(raw) >= 2 {
					rel := int8(raw[1])
					t16 := uint16(int32(uint16(lastInst.Address)+2) + int32(rel))
					succs = []uint32{bank | uint32(t16)}
				}
			case 0x82: // BRL rel16
				if len(raw) >= 3 {
					rel16 := int16(uint16(raw[1]) | (uint16(raw[2]) << 8))
					t16 := uint16(int32(uint16(lastInst.Address)+3) + int32(rel16))
					succs = []uint32{bank | uint32(t16)}
				}
			case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0: // conditional branch
				fallthroughAddr := bank | uint32(uint16(lastInst.Address)+uint16(len(raw)))
				succs = append(succs, fallthroughAddr)
				if len(raw) >= 2 {
					rel := int8(raw[1])
					t16 := uint16(int32(uint16(lastInst.Address)+2) + int32(rel))
					succs = append(succs, bank|uint32(t16))
				}
			default:
				fallthroughAddr := bank | uint32(uint16(lastInst.Address)+uint16(len(raw)))
				succs = []uint32{fallthroughAddr}
			}
		}

		for _, succAddr := range succs {
			branchState := state.clone()
			if err := traverse(succAddr, branchState); err != nil {
				return err
			}
		}
		return nil
	}

	if err := traverse(entryAddr, initState); err != nil {
		if len(pathContracts) > 0 {
			finalContract := pathContracts[0]
			for _, pc := range pathContracts[1:] {
				finalContract = mergeContracts(finalContract, pc)
			}
			return finalContract, err
		}
		return CallContract{}, err
	}

	if len(pathContracts) == 0 {
		return CallContract{}, ErrNoReturn
	}

	finalContract := pathContracts[0]
	for _, pc := range pathContracts[1:] {
		finalContract = mergeContracts(finalContract, pc)
	}

	if finalContract.ReturnsWith == 0 {
		return finalContract, ErrMismatchedReturn
	}
	if finalContract.StackDelta != 0 {
		return finalContract, ErrUnbalancedStack
	}

	return finalContract, nil
}
