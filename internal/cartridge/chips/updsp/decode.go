package updsp

// Instruction classes — the top two bits of the 24-bit opcode.
//
// Reference: NEC uPD77C25 datasheet "instruction format" and bsnes
// sfc/coprocessor/necdsp/instruction.cpp.
const (
	classOP = 0
	classRT = 1
	classJP = 2
	classLD = 3
)

// ALU operation codes (4 bits).
const (
	aluNOP  = 0x0
	aluOR   = 0x1
	aluAND  = 0x2
	aluXOR  = 0x3
	aluSUB  = 0x4
	aluADD  = 0x5
	aluSBB  = 0x6 // subtract with borrow
	aluADC  = 0x7 // add with carry
	aluDEC  = 0x8
	aluINC  = 0x9
	aluCMP  = 0xA
	aluSHR1 = 0xB // arithmetic right shift by 1
	aluSHL1 = 0xC // rotate left by 1
	aluSHL2 = 0xD // rotate left by 2
	aluSHL4 = 0xE // rotate left by 4
	aluXCHG = 0xF // swap high and low bytes of the accumulator
)

// ASL source encoding (2 bits): selects the left-hand operand for the ALU.
const (
	aslRAM = 0 // DRAM[DP]
	aslTR  = 1 // TR register
	aslDR  = 2 // DR register
	aslSR  = 3 // SR register
)

// DPL — data pointer low-nibble modifier (2 bits).
const (
	dplNOP = 0
	dplINC = 1
	dplDEC = 2
	dplCLR = 3
)

// SRC/DST register codes (4 bits). The DSP has a single mux used both for
// "source" (when moving to an accumulator) and "destination" (when a register
// is the target of a LD or of a move).
const (
	regNON  = 0x0
	regA    = 0x1
	regB    = 0x2
	regTR   = 0x3
	regDP   = 0x4
	regRP   = 0x5
	regRO   = 0x6 // data ROM at DROM[RP]
	regSGN  = 0x7 // sign of bit15 of A -> +-0x7FFF, hardware quirk
	regDR   = 0x8
	regDRNF = 0x9 // DR, no flag update (DR -> R, don't update SR)
	regSR   = 0xA
	regSIM  = 0xB
	regSIL  = 0xC
	regK    = 0xD
	regL    = 0xE
	regMEM  = 0xF // data RAM[DP]
)

// jumpCond selects a branch predicate (5 bits in the JP class).
// See bsnes necdsp/instruction-jp.cpp for the condition table.

// decoded holds the pre-split fields of a 24-bit opcode, so exec helpers can
// index them without repeating the bit masking.
type decoded struct {
	Class uint8 // top two bits
	Word  uint32
}

func decode(w uint32) decoded {
	return decoded{Class: uint8((w >> 22) & 0x3), Word: w & 0xFFFFFF}
}

// execOne fetches, decodes, and executes one instruction from PRG[PC].
func (c *Core) execOne() {
	// Program counter masked to the ROM size.
	if int(c.PC) >= len(c.PRG) {
		c.PC = 0
	}
	word := c.PRG[c.PC] & 0xFFFFFF
	c.PC++
	c.CycleCount++

	d := decode(word)
	switch d.Class {
	case classOP:
		c.execOP(d.Word, false)
	case classRT:
		c.execOP(d.Word, true)
		c.retStack()
	case classJP:
		c.execJP(d.Word)
	case classLD:
		c.execLD(d.Word)
	}
}

// retStack pops PC from the return stack (used by RT-class and RET).
func (c *Core) retStack() {
	if c.SP == 0 {
		// Underflow wraps on the real hardware; emulate the same.
		c.SP = uint8(len(c.STK) - 1)
	} else {
		c.SP--
	}
	c.PC = c.STK[c.SP]
}

// pushStack pushes PC for CALL (used by JP class with the call flag).
func (c *Core) pushStack(addr uint16) {
	c.STK[c.SP] = c.PC
	c.SP = (c.SP + 1) & uint8(len(c.STK)-1)
	c.PC = addr
}
