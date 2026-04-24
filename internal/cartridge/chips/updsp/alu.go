package updsp

// execOP implements the OP-class instruction. The 24-bit word layout (after
// masking off the class bits) is:
//
//	bits 21-20: unused
//	bits 19-16: P-select (source operand into the ALU multiplexer)
//	bits 15-14: ALU sub-select (ASL) - the left-hand operand bank
//	bit   13:   destination bank (0 = A, 1 = B)
//	bits 12-9:  ALU operation code
//	bits 8-6:   RP modifier + DP modifier flags
//	bits 5-4:   DPL (data pointer low-nibble update)
//	bits 3-0:   DPH (data pointer high-nibble xor mask)
//	bits 8-6:   MOV destination register select (when OP encodes a MOV side-op)
//	bits 22-20: MOV source register select (RD)
//
// For simplicity the implementation decodes the subset of fields needed by
// DSP-1/DSP-1B: destination bank, ALU op, ASL source, DP/DPL/RP increment, and
// the implicit MOV from P (multiplier low word) or SO (multiplier high word).
func (c *Core) execOP(word uint32, isRT bool) {
	aluOp := uint8((word >> 16) & 0xF)
	alSel := uint8((word >> 20) & 0x3)
	pSel := uint8((word >> 14) & 0x3)
	dstBank := uint8((word >> 13) & 0x1)
	dpl := uint8((word >> 9) & 0x3)
	dph := uint8((word >> 5) & 0xF)
	rpDec := (word>>4)&0x1 != 0
	srcReg := uint8((word >> 20) & 0xF) // alternate MOV source (full 4 bits)
	dstReg := uint8(word & 0xF)

	// Perform an implicit MOV (src -> dst) when the OP opcode encodes one.
	// The MOV field is active when the ALU op is NOP and src != dst.
	if aluOp == aluNOP && srcReg != regNON && dstReg != regNON {
		c.movRegToReg(srcReg, dstReg)
	}

	// Fetch ALU left operand (ASL source).
	var lhs uint16
	switch alSel {
	case aslRAM:
		lhs = c.DRAM[c.DP]
	case aslTR:
		lhs = c.TR
	case aslDR:
		lhs = c.DR
	case aslSR:
		lhs = c.SR
	}
	// Fetch ALU right operand (P-select). On hardware the "P" field picks
	// between the multiplier output N, the data RAM, the RP-indirect data
	// ROM word, or a zero. DSP-1 code mostly uses P=1 (multiplier high).
	var rhs uint16
	switch pSel {
	case 0:
		rhs = c.DRAM[c.DP]
	case 1:
		rhs = c.M
	case 2:
		rhs = c.N
	case 3:
		rhs = c.SI
	}

	// Select which 24-bit accumulator the ALU writes back.
	var acc *uint32
	var fl *Flags
	if dstBank == 0 {
		acc = &c.A
		fl = &c.FA
	} else {
		acc = &c.B
		fl = &c.FB
	}

	cIn := boolToUint(fl.C)
	res, carry, overflow := alu(aluOp, uint16(*acc), rhs, lhs, cIn)
	switch aluOp {
	case aluCMP:
		// CMP updates flags but does not write the destination accumulator.
	default:
		// 16-bit result is written into the low half of the 24-bit acc.
		*acc = (*acc & 0xFF0000) | uint32(res)
	}
	fl.C = carry
	fl.OV0 = overflow
	if overflow {
		fl.OV1 = !fl.OV1 || fl.OV1
	}
	fl.Z = res == 0
	fl.S0 = res&0x8000 != 0
	fl.S1 = fl.S0 != fl.OV0

	// Apply DP low-nibble modifier.
	switch dpl {
	case dplNOP:
	case dplINC:
		c.DP = (c.DP &^ 0x0F) | ((c.DP + 1) & 0x0F)
	case dplDEC:
		c.DP = (c.DP &^ 0x0F) | ((c.DP - 1) & 0x0F)
	case dplCLR:
		c.DP = c.DP &^ 0x0F
	}
	// Apply DP high-nibble xor modifier.
	if dph != 0 {
		c.DP ^= dph << 4
	}

	// Apply RP decrement (OP class can't increment RP explicitly).
	if rpDec {
		c.RP = (c.RP - 1) & 0x03FF
	}

	// Multiplier bank: operands K and L are sampled "sometime during the
	// instruction" on hardware. We sample at the end so that a preceding
	// LD into K or L is visible.
	prod := int32(c.K) * int32(c.L) * 2 // 31-bit product shifted left by 1
	c.M = uint16(uint32(prod) >> 16)
	c.N = uint16(uint32(prod) & 0xFFFF)

	_ = isRT // RT side-effect (return) is applied by the caller
}

// alu performs a single 16-bit ALU op. Returns (result, carry-out, overflow).
func alu(op uint8, acc, rhs, lhs, cIn uint16) (uint16, bool, bool) {
	// Canonical left-hand operand for the ALU is the "lhs" field (ASL source).
	_ = rhs // rhs is consumed by the multiply-accumulate path, not the ALU
	switch op {
	case aluNOP:
		return acc, false, false
	case aluOR:
		return acc | lhs, false, false
	case aluAND:
		return acc & lhs, false, false
	case aluXOR:
		return acc ^ lhs, false, false
	case aluSUB:
		return subWithFlags(acc, lhs, 0)
	case aluADD:
		return addWithFlags(acc, lhs, 0)
	case aluSBB:
		return subWithFlags(acc, lhs, cIn)
	case aluADC:
		return addWithFlags(acc, lhs, cIn)
	case aluDEC:
		return subWithFlags(acc, 1, 0)
	case aluINC:
		return addWithFlags(acc, 1, 0)
	case aluCMP:
		return subWithFlags(acc, lhs, 0)
	case aluSHR1:
		res := (acc >> 1) | (acc & 0x8000)
		return res, acc&1 != 0, false
	case aluSHL1:
		res := (acc << 1) | cIn
		return res, acc&0x8000 != 0, false
	case aluSHL2:
		res := (acc << 2) | (acc >> 14)
		return res, false, false
	case aluSHL4:
		res := (acc << 4) | (acc >> 12)
		return res, false, false
	case aluXCHG:
		return (acc << 8) | (acc >> 8), false, false
	}
	return acc, false, false
}

func addWithFlags(a, b, cIn uint16) (uint16, bool, bool) {
	sum := uint32(a) + uint32(b) + uint32(cIn)
	res := uint16(sum)
	carry := sum > 0xFFFF
	overflow := (a^res)&(b^res)&0x8000 != 0
	return res, carry, overflow
}

func subWithFlags(a, b, cIn uint16) (uint16, bool, bool) {
	// Subtract is A - B - Cin with borrow as carry.
	diff := uint32(a) - uint32(b) - uint32(cIn)
	res := uint16(diff)
	borrow := diff&0x10000 != 0
	overflow := (a^b)&(a^res)&0x8000 != 0
	return res, borrow, overflow
}

func boolToUint(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}
