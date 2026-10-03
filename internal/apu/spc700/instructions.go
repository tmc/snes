package spc700

// Instruction Definition
type Instruction struct {
	Op     func(c *SPC700)
	Cycles int
}

var Instructions [256]Instruction

// Init happens in the shared block below or via init() function.
// Using single init() for clarity.

// Helper Methods

func (c *SPC700) fetchByte() uint8 {
	val := c.bus.Read(c.PC)
	c.PC++
	return val
}

func (c *SPC700) fetchWord() uint16 {
	low := c.fetchByte()
	high := c.fetchByte()
	return uint16(low) | (uint16(high) << 8)
}

// ALU Operations

func (c *SPC700) adc(val uint8) {
	result := int(c.A) + int(val) + int(boolToInt(c.C))
	c.V = (c.A&0x80) == (val&0x80) && (val&0x80) != (uint8(result)&0x80)
	c.H = ((c.A & 0x0F) + (val & 0x0F) + uint8(boolToInt(c.C))) > 0x0F
	c.C = result > 0xFF
	c.A = uint8(result)
	c.SetZN(c.A)
}

func (c *SPC700) sbc(val uint8) {
	val = val ^ 0xFF
	result := int(c.A) + int(val) + int(boolToInt(c.C))
	c.V = (c.A&0x80) == (val&0x80) && (val&0x80) != (uint8(result)&0x80)
	c.H = ((c.A & 0x0F) + (val & 0x0F) + uint8(boolToInt(c.C))) > 0x0F
	c.C = result > 0xFF
	c.A = uint8(result)
	c.SetZN(c.A)
}

func (c *SPC700) cmp(val uint8) {
	val = val ^ 0xFF
	result := int(c.A) + int(val) + 1
	c.C = result > 0xFF
	c.SetZN(uint8(result))
}

func (c *SPC700) cmpX(val uint8) {
	val = val ^ 0xFF
	result := int(c.X) + int(val) + 1
	c.C = result > 0xFF
	c.SetZN(uint8(result))
}

func (c *SPC700) cmpY(val uint8) {
	val = val ^ 0xFF
	result := int(c.Y) + int(val) + 1
	c.C = result > 0xFF
	c.SetZN(uint8(result))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func init() {
	// Initialize Opcode Table
	Instructions[0x00] = Instruction{Op: func(c *SPC700) {}, Cycles: 2} // NOP

	// Data Transfer (Immediate)
	Instructions[0xE8] = Instruction{Op: func(c *SPC700) { c.A = c.fetchByte(); c.SetZN(c.A) }, Cycles: 2} // MOV A, #imm
	Instructions[0xCD] = Instruction{Op: func(c *SPC700) { c.X = c.fetchByte(); c.SetZN(c.X) }, Cycles: 2} // MOV X, #imm
	Instructions[0x8D] = Instruction{Op: func(c *SPC700) { c.Y = c.fetchByte(); c.SetZN(c.Y) }, Cycles: 2} // MOV Y, #imm

	// Arithmetic (Immediate)
	Instructions[0x88] = Instruction{Op: func(c *SPC700) { c.adc(c.fetchByte()) }, Cycles: 2}  // ADC A, #imm
	Instructions[0xA8] = Instruction{Op: func(c *SPC700) { c.sbc(c.fetchByte()) }, Cycles: 2}  // SBC A, #imm
	Instructions[0x68] = Instruction{Op: func(c *SPC700) { c.cmp(c.fetchByte()) }, Cycles: 2}  // CMP A, #imm
	Instructions[0xC8] = Instruction{Op: func(c *SPC700) { c.cmpX(c.fetchByte()) }, Cycles: 2} // CMP X, #imm
	Instructions[0xAD] = Instruction{Op: func(c *SPC700) { c.cmpY(c.fetchByte()) }, Cycles: 2} // CMP Y, #imm

	// Branching
	Instructions[0x2F] = Instruction{Op: func(c *SPC700) { c.branch(true) }, Cycles: 2} // BRA rel
	Instructions[0xD0] = Instruction{Op: func(c *SPC700) { c.branch(!c.Z) }, Cycles: 2} // BNE rel
	Instructions[0xF0] = Instruction{Op: func(c *SPC700) { c.branch(c.Z) }, Cycles: 2}  // BEQ rel
	Instructions[0x10] = Instruction{Op: func(c *SPC700) { c.branch(!c.N) }, Cycles: 2} // BPL rel
	Instructions[0x30] = Instruction{Op: func(c *SPC700) { c.branch(c.N) }, Cycles: 2}  // BMI rel
	Instructions[0x50] = Instruction{Op: func(c *SPC700) { c.branch(!c.V) }, Cycles: 2} // BVC rel
	Instructions[0x70] = Instruction{Op: func(c *SPC700) { c.branch(c.V) }, Cycles: 2}  // BVS rel
	Instructions[0x90] = Instruction{Op: func(c *SPC700) { c.branch(!c.C) }, Cycles: 2} // BCC rel
	Instructions[0xB0] = Instruction{Op: func(c *SPC700) { c.branch(c.C) }, Cycles: 2}  // BCS rel

	// Flag Operations
	Instructions[0x20] = Instruction{Op: func(c *SPC700) { c.P = false }, Cycles: 2}              // CLRP
	Instructions[0x40] = Instruction{Op: func(c *SPC700) { c.P = true }, Cycles: 2}               // SETP
	Instructions[0x60] = Instruction{Op: func(c *SPC700) { c.C = false }, Cycles: 2}              // CLRC
	Instructions[0x80] = Instruction{Op: func(c *SPC700) { c.C = true }, Cycles: 2}               // SETC
	Instructions[0xA0] = Instruction{Op: func(c *SPC700) { c.I = true }, Cycles: 3}               // EI
	Instructions[0xC0] = Instruction{Op: func(c *SPC700) { c.I = false }, Cycles: 3}              // DI
	Instructions[0xE0] = Instruction{Op: func(c *SPC700) { c.V = false; c.H = false }, Cycles: 2} // CLRV

	// Bit Manipulation
	for bit := 0; bit < 8; bit++ {
		b := uint8(bit)
		// SET1 dp.bit
		Instructions[0x02|(b<<5)] = Instruction{Op: func(c *SPC700) {
			addr := c.adrDp()
			val := c.read(addr)
			c.write(addr, val|(1<<b))
		}, Cycles: 4} // Read-Modify-Write usually takes more cycles

		// CLR1 dp.bit
		Instructions[0x12|(b<<5)] = Instruction{Op: func(c *SPC700) {
			addr := c.adrDp()
			val := c.read(addr)
			c.write(addr, val & ^(1<<b))
		}, Cycles: 4}

		// BBS dp.bit, rel
		Instructions[0x03|(b<<5)] = Instruction{Op: func(c *SPC700) {
			addr := c.adrDp()
			val := c.read(addr)
			rel := c.fetchByte()
			c.branchRel(rel, val&(1<<b) != 0)
		}, Cycles: 5}

		// BBC dp.bit, rel
		Instructions[0x13|(b<<5)] = Instruction{Op: func(c *SPC700) {
			addr := c.adrDp()
			val := c.read(addr)
			rel := c.fetchByte()
			c.branchRel(rel, val&(1<<b) == 0)
		}, Cycles: 5}
	}

	// TCALL 0..15
	for n := 0; n < 16; n++ {
		nn := uint8(n)
		Instructions[0x01|(nn<<4)] = Instruction{Op: func(c *SPC700) { c.tcall(nn) }, Cycles: 8}
	}

	// Stack Ops
	Instructions[0x6F] = Instruction{Op: func(c *SPC700) { c.PC = c.popWord() }, Cycles: 5}                    // RET
	Instructions[0x7F] = Instruction{Op: func(c *SPC700) { c.SetPSW(c.pop()); c.PC = c.popWord() }, Cycles: 6} // RET1 (RTI)
	Instructions[0x0D] = Instruction{Op: func(c *SPC700) { c.push(c.GetPSW()) }, Cycles: 4}                    // PUSH PSW

	// Logic (Immediate)
	Instructions[0x28] = Instruction{Op: func(c *SPC700) { c.and(c.fetchByte()) }, Cycles: 2} // AND A, #imm
	Instructions[0x08] = Instruction{Op: func(c *SPC700) { c.or(c.fetchByte()) }, Cycles: 2}  // OR A, #imm
	Instructions[0x48] = Instruction{Op: func(c *SPC700) { c.eor(c.fetchByte()) }, Cycles: 2} // EOR A, #imm
	Instructions[0x0A] = Instruction{Op: func(c *SPC700) {                                    // OR1 C, abs.bit
		addr, bit := c.adrAbsBit()
		c.C = c.C || ((c.read(addr)>>bit)&1) != 0
	}, Cycles: 5}
	Instructions[0x2A] = Instruction{Op: func(c *SPC700) { // OR1 C, /abs.bit
		addr, bit := c.adrAbsBit()
		c.C = c.C || ((^c.read(addr)>>bit)&1) != 0
	}, Cycles: 5}
	Instructions[0x4A] = Instruction{Op: func(c *SPC700) { // AND1 C, abs.bit
		addr, bit := c.adrAbsBit()
		c.C = c.C && ((c.read(addr)>>bit)&1) != 0
	}, Cycles: 4}
	Instructions[0x8A] = Instruction{Op: func(c *SPC700) { // EOR1 C, abs.bit
		addr, bit := c.adrAbsBit()
		c.C = c.C != (((c.read(addr) >> bit) & 1) != 0)
	}, Cycles: 5}
	Instructions[0xAA] = Instruction{Op: func(c *SPC700) { // MOV1 C, abs.bit
		addr, bit := c.adrAbsBit()
		c.C = ((c.read(addr) >> bit) & 1) != 0
	}, Cycles: 4}
	Instructions[0xCA] = Instruction{Op: func(c *SPC700) { // MOV1 abs.bit, C
		addr, bit := c.adrAbsBit()
		mask := uint8(1 << bit)
		val := c.read(addr) &^ mask
		if c.C {
			val |= mask
		}
		c.write(addr, val)
	}, Cycles: 6}

	// Register Increment/Decrement
	Instructions[0xBC] = Instruction{Op: func(c *SPC700) { c.A++; c.SetZN(c.A) }, Cycles: 2} // INC A
	Instructions[0x9C] = Instruction{Op: func(c *SPC700) { c.A--; c.SetZN(c.A) }, Cycles: 2} // DEC A
	Instructions[0x3D] = Instruction{Op: func(c *SPC700) { c.X++; c.SetZN(c.X) }, Cycles: 2} // INC X
	Instructions[0xBC] = Instruction{Op: func(c *SPC700) { c.A++; c.SetZN(c.A) }, Cycles: 2} // INC A
	Instructions[0x3D] = Instruction{Op: func(c *SPC700) { c.X++; c.SetZN(c.X) }, Cycles: 2} // INC X
	Instructions[0xFC] = Instruction{Op: func(c *SPC700) { c.Y++; c.SetZN(c.Y) }, Cycles: 2} // INC Y

	Instructions[0x9C] = Instruction{Op: func(c *SPC700) { c.A--; c.SetZN(c.A) }, Cycles: 2} // DEC A
	Instructions[0x1D] = Instruction{Op: func(c *SPC700) { c.X--; c.SetZN(c.X) }, Cycles: 2} // DEC X
	Instructions[0xDC] = Instruction{Op: func(c *SPC700) { c.Y--; c.SetZN(c.Y) }, Cycles: 2} // DEC Y
	Instructions[0xFC] = Instruction{Op: func(c *SPC700) { c.Y++; c.SetZN(c.Y) }, Cycles: 2} // INC Y
	Instructions[0xDC] = Instruction{Op: func(c *SPC700) { c.Y--; c.SetZN(c.Y) }, Cycles: 2} // DEC Y

	// Addressing Mode Opcodes (Subset for Verification)

	// MOV A, ...
	Instructions[0xE4] = Instruction{Op: func(c *SPC700) { // MOV A, dp
		addr := c.adrDp()
		c.A = c.read(addr)
		c.SetZN(c.A)
		c.armPendingPortLoadA(addr)
	}, Cycles: 3}
	Instructions[0xF4] = Instruction{Op: func(c *SPC700) { // MOV A, dp+X
		addr := c.adrDpx()
		c.A = c.read(addr)
		c.SetZN(c.A)
		c.armPendingPortLoadA(addr)
	}, Cycles: 4}
	Instructions[0xE5] = Instruction{Op: func(c *SPC700) { // MOV A, !abs
		addr := c.adrAbs()
		c.A = c.read(addr)
		c.SetZN(c.A)
		c.armPendingPortLoadA(addr)
	}, Cycles: 4}
	Instructions[0xE6] = Instruction{Op: func(c *SPC700) { c.A = c.read(c.adrInd()); c.SetZN(c.A) }, Cycles: 4} // MOV A, (X)
	Instructions[0xE7] = Instruction{Op: func(c *SPC700) { c.A = c.read(c.adrIdx()); c.SetZN(c.A) }, Cycles: 6} // MOV A, (dp+X)
	Instructions[0xE9] = Instruction{Op: func(c *SPC700) {                                                      // MOV X, abs
		addr := c.adrAbs()
		c.X = c.read(addr)
		c.SetZN(c.X)
		c.armPendingPortLoadX(addr)
	}, Cycles: 4}
	Instructions[0x7D] = Instruction{Op: func(c *SPC700) { // MOV A,X
		c.A = c.X
		c.SetZN(c.A)
	}, Cycles: 2}

	Instructions[0x5D] = Instruction{Op: func(c *SPC700) { // MOV X,A
		c.X = c.A
		c.SetZN(c.X)
	}, Cycles: 2}
	Instructions[0xDD] = Instruction{Op: func(c *SPC700) { // MOV A,Y
		c.A = c.Y
		c.SetZN(c.A)
	}, Cycles: 2}
	Instructions[0xEB] = Instruction{Op: func(c *SPC700) { // MOV Y, dp
		addr := c.adrDp()
		c.Y = c.read(addr)
		c.SetZN(c.Y)
		c.armPendingPortLoadY(addr)
	}, Cycles: 3}
	Instructions[0xF8] = Instruction{Op: func(c *SPC700) { // MOV X, dp
		addr := c.adrDp()
		c.X = c.read(addr)
		c.SetZN(c.X)
		c.armPendingPortLoadX(addr)
	}, Cycles: 3}
	Instructions[0xF9] = Instruction{Op: func(c *SPC700) { // MOV X, dp+Y
		addr := c.adrDpy()
		c.X = c.read(addr)
		c.SetZN(c.X)
		c.armPendingPortLoadX(addr)
	}, Cycles: 4}
	Instructions[0xFB] = Instruction{Op: func(c *SPC700) { // MOV Y, dp+X
		addr := c.adrDpx()
		c.Y = c.read(addr)
		c.SetZN(c.Y)
		c.armPendingPortLoadY(addr)
	}, Cycles: 4}
	Instructions[0xFD] = Instruction{Op: func(c *SPC700) { c.Y = c.A; c.SetZN(c.Y) }, Cycles: 2} // MOV Y, A

	Instructions[0xEC] = Instruction{Op: func(c *SPC700) { // MOV Y, abs
		addr := c.adrAbs()
		c.Y = c.read(addr)
		c.SetZN(c.Y)
		c.armPendingPortLoadY(addr)
	}, Cycles: 4}
	Instructions[0x9D] = Instruction{Op: func(c *SPC700) { // MOV X, SP
		c.X = c.SP
		c.SetZN(c.X)
	}, Cycles: 2}
	Instructions[0x9F] = Instruction{Op: func(c *SPC700) { // XCN A
		c.A = (c.A >> 4) | (c.A << 4)
		c.SetZN(c.A)
	}, Cycles: 5}

	// MOV dp, ...
	Instructions[0xAF] = Instruction{Op: func(c *SPC700) { // MOV (X)+, A
		addr := c.adrInd()
		c.write(addr, c.A)
		c.X++
	}, Cycles: 4}
	Instructions[0xC4] = Instruction{Op: func(c *SPC700) { c.write(c.adrDp(), c.A) }, Cycles: 4}  // MOV dp, A
	Instructions[0xC5] = Instruction{Op: func(c *SPC700) { c.write(c.adrAbs(), c.A) }, Cycles: 5} // MOV abs, A
	Instructions[0xD4] = Instruction{Op: func(c *SPC700) { c.write(c.adrDpx(), c.A) }, Cycles: 5} // MOV dp+X, A
	Instructions[0xD5] = Instruction{Op: func(c *SPC700) { c.write(c.adrAbx(), c.A) }, Cycles: 6} // MOV abs+X, A
	Instructions[0xD6] = Instruction{Op: func(c *SPC700) { c.write(c.adrAby(), c.A) }, Cycles: 6} // MOV abs+Y, A
	Instructions[0xC7] = Instruction{Op: func(c *SPC700) { c.write(c.adrIdx(), c.A) }, Cycles: 7} // MOV [dp+X], A
	Instructions[0xC9] = Instruction{Op: func(c *SPC700) { c.write(c.adrAbs(), c.X) }, Cycles: 5} // MOV abs, X
	Instructions[0xCC] = Instruction{Op: func(c *SPC700) { c.write(c.adrAbs(), c.Y) }, Cycles: 5} // MOV abs, Y

	Instructions[0x8F] = Instruction{Op: func(c *SPC700) { // MOV dp, #imm
		imm := c.fetchByte()
		addr := c.adrDp()
		c.write(addr, imm)
	}, Cycles: 5}
	Instructions[0xFA] = Instruction{Op: func(c *SPC700) { // MOV dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		val := c.read(src)
		c.write(dst, val)
	}, Cycles: 5}

	// Missing Moves (IPL Support)
	Instructions[0xBD] = Instruction{Op: func(c *SPC700) { c.SP = c.X }, Cycles: 2} // MOV SP, X
	Instructions[0xC6] = Instruction{Op: func(c *SPC700) {                          // MOV (X), A
		addr := c.adrInd() // (X)
		c.write(addr, c.A)
	}, Cycles: 4}
	Instructions[0xCB] = Instruction{Op: func(c *SPC700) { // MOV dp, Y
		addr := c.adrDp()
		c.write(addr, c.Y)
	}, Cycles: 4}
	Instructions[0xCE] = Instruction{Op: func(c *SPC700) { c.X = c.pop() }, Cycles: 4}            // POP X
	Instructions[0xD8] = Instruction{Op: func(c *SPC700) { c.write(c.adrDp(), c.X) }, Cycles: 4}  // MOV dp, X
	Instructions[0xD9] = Instruction{Op: func(c *SPC700) { c.write(c.adrDpy(), c.X) }, Cycles: 5} // MOV dp+Y, X
	Instructions[0xD7] = Instruction{Op: func(c *SPC700) {                                        // MOV (dp)+Y, A
		addr := c.adrIdy() // (dp)+Y
		c.read(addr)
		c.write(addr, c.A)
	}, Cycles: 7}
	Instructions[0xDB] = Instruction{Op: func(c *SPC700) { c.write(c.adrDpx(), c.Y) }, Cycles: 5} // MOV dp+X, Y
	Instructions[0xF5] = Instruction{Op: func(c *SPC700) {                                        // MOV A, !abs+X
		addr := c.adrAbx()
		c.A = c.read(addr)
		c.SetZN(c.A)
	}, Cycles: 4}
	Instructions[0xBB] = Instruction{Op: func(c *SPC700) { // INC dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		val++
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x9B] = Instruction{Op: func(c *SPC700) { // DEC dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		val--
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}

	Instructions[0xAB] = Instruction{Op: func(c *SPC700) { // INC dp
		addr := c.adrDp()
		val := c.read(addr)
		val++
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0xAC] = Instruction{Op: func(c *SPC700) { // INC abs
		addr := c.adrAbs()
		val := c.read(addr)
		val++
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}

	Instructions[0x8B] = Instruction{Op: func(c *SPC700) { // DEC dp
		addr := c.adrDp()
		val := c.read(addr)
		val--
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0x8C] = Instruction{Op: func(c *SPC700) { // DEC abs
		addr := c.adrAbs()
		val := c.read(addr)
		val--
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}

	// CMP mixed types
	Instructions[0x7E] = Instruction{Op: func(c *SPC700) { // CMP Y, dp
		addr := c.adrDp()
		val := c.read(addr)
		// Compare Y with val
		// CMP logic: result = Y - val
		val = val ^ 0xFF
		result := int(c.Y) + int(val) + 1
		c.C = result > 0xFF
		c.SetZN(uint8(result))
		c.armPendingPortCompareY(addr)
	}, Cycles: 3}

	Instructions[0x3E] = Instruction{Op: func(c *SPC700) { // CMP X, dp
		addr := c.adrDp()
		val := c.read(addr)
		c.cmpX(val)
		c.armPendingPortCompareX(addr)
	}, Cycles: 3}

	Instructions[0x78] = Instruction{Op: func(c *SPC700) { // CMP dp, #imm
		imm := c.fetchByte()
		addr := c.adrDp()
		val := c.read(addr)

		result := int(val) - int(imm)
		c.C = val >= imm
		c.SetZN(uint8(result))
		c.armPendingPortCompareMemImm(addr, imm)
	}, Cycles: 5}
	Instructions[0x18] = Instruction{Op: func(c *SPC700) { // OR dp, #imm
		imm := c.fetchByte()
		c.orMem(c.adrDp(), imm)
	}, Cycles: 5}
	Instructions[0x58] = Instruction{Op: func(c *SPC700) { // EOR dp, #imm
		imm := c.fetchByte()
		c.eorMem(c.adrDp(), imm)
	}, Cycles: 5}
	Instructions[0x98] = Instruction{Op: func(c *SPC700) { // ADC dp, #imm
		imm := c.fetchByte()
		addr := c.adrDp()
		val := c.read(addr)
		carry := uint8(boolToInt(c.C))
		result := int(val) + int(imm) + int(carry)
		c.V = (val&0x80) == (imm&0x80) && (imm&0x80) != (uint8(result)&0x80)
		c.H = ((val & 0x0F) + (imm & 0x0F) + carry) > 0x0F
		c.C = result > 0xFF
		val = uint8(result)
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0xB8] = Instruction{Op: func(c *SPC700) { // SBC dp, #imm
		imm := c.fetchByte()
		c.sbcMem(c.adrDp(), imm)
	}, Cycles: 5}
	Instructions[0x38] = Instruction{Op: func(c *SPC700) { // AND dp, #imm
		imm := c.fetchByte()
		addr := c.adrDp()
		val := c.read(addr) & imm
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}

	// Arithmetic/Logic with DP
	Instructions[0x06] = Instruction{Op: func(c *SPC700) { // OR A, (X)
		addr := c.adrInd()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 3}
	Instructions[0x07] = Instruction{Op: func(c *SPC700) { // OR A, (dp+X)
		addr := c.adrIdx()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 6}
	Instructions[0x24] = Instruction{Op: func(c *SPC700) { // AND A, dp
		addr := c.adrDp()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 3}
	Instructions[0x26] = Instruction{Op: func(c *SPC700) { // AND A, (X)
		addr := c.adrInd()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 3}
	Instructions[0x27] = Instruction{Op: func(c *SPC700) { // AND A, (dp+X)
		addr := c.adrIdx()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 6}
	Instructions[0x04] = Instruction{Op: func(c *SPC700) { // OR A, dp
		addr := c.adrDp()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 3}
	Instructions[0x09] = Instruction{Op: func(c *SPC700) { // OR dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		c.orMem(dst, c.read(src))
	}, Cycles: 6}
	Instructions[0x29] = Instruction{Op: func(c *SPC700) { // AND dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		c.andMem(dst, c.read(src))
	}, Cycles: 6}
	Instructions[0x49] = Instruction{Op: func(c *SPC700) { // EOR dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		c.eorMem(dst, c.read(src))
	}, Cycles: 6}
	Instructions[0x89] = Instruction{Op: func(c *SPC700) { // ADC dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		c.adcMem(dst, c.read(src))
	}, Cycles: 6}
	Instructions[0xA9] = Instruction{Op: func(c *SPC700) { // SBC dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		c.sbcMem(dst, c.read(src))
	}, Cycles: 6}
	Instructions[0x19] = Instruction{Op: func(c *SPC700) { c.orMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5}  // OR (X), (Y)
	Instructions[0x39] = Instruction{Op: func(c *SPC700) { c.andMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5} // AND (X), (Y)
	Instructions[0x59] = Instruction{Op: func(c *SPC700) { c.eorMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5} // EOR (X), (Y)
	Instructions[0x79] = Instruction{Op: func(c *SPC700) { c.cmpMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5} // CMP (X), (Y)
	Instructions[0x99] = Instruction{Op: func(c *SPC700) { c.adcMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5} // ADC (X), (Y)
	Instructions[0xB9] = Instruction{Op: func(c *SPC700) { c.sbcMem(c.adrInd(), c.read(c.adrIndY())) }, Cycles: 5} // SBC (X), (Y)
	Instructions[0x44] = Instruction{Op: func(c *SPC700) {                                                         // EOR A, dp
		addr := c.adrDp()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 3}
	Instructions[0x84] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrDp()) }, Cycles: 3}  // ADC A, dp
	Instructions[0x85] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrAbs()) }, Cycles: 4} // ADC A, abs
	Instructions[0x86] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrInd()) }, Cycles: 3} // ADC A, (X)
	Instructions[0x87] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrIdx()) }, Cycles: 6} // ADC A, (dp+X)
	Instructions[0x94] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrDpx()) }, Cycles: 4} // ADC A, dp+X
	Instructions[0x95] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrAbx()) }, Cycles: 5} // ADC A, abs+X
	Instructions[0x96] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrAby()) }, Cycles: 5} // ADC A, abs+Y
	Instructions[0x97] = Instruction{Op: func(c *SPC700) { c.adcPort(c.adrIdy()) }, Cycles: 6} // ADC A, (dp)+Y
	Instructions[0xA4] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrDp()) }, Cycles: 3}  // SBC A, dp
	Instructions[0xA5] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrAbs()) }, Cycles: 4} // SBC A, abs
	Instructions[0xA6] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrInd()) }, Cycles: 3} // SBC A, (X)
	Instructions[0xA7] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrIdx()) }, Cycles: 6} // SBC A, (dp+X)
	Instructions[0xB4] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrDpx()) }, Cycles: 4} // SBC A, dp+X
	Instructions[0xB5] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrAbx()) }, Cycles: 5} // SBC A, abs+X
	Instructions[0xB6] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrAby()) }, Cycles: 5} // SBC A, abs+Y
	Instructions[0xB7] = Instruction{Op: func(c *SPC700) { c.sbcPort(c.adrIdy()) }, Cycles: 6} // SBC A, (dp)+Y
	Instructions[0x64] = Instruction{Op: func(c *SPC700) {                                     // CMP A, dp
		addr := c.adrDp()
		val := c.read(addr)
		c.cmp(val)
		c.armPendingPortCompareA(addr)
	}, Cycles: 3}
	Instructions[0x65] = Instruction{Op: func(c *SPC700) { // CMP A, abs
		addr := c.adrAbs()
		val := c.read(addr)
		c.cmp(val)
		c.armPendingPortCompareA(addr)
	}, Cycles: 4}
	Instructions[0x66] = Instruction{Op: func(c *SPC700) { c.cmp(c.read(c.adrInd())) }, Cycles: 3} // CMP A, (X)
	Instructions[0x67] = Instruction{Op: func(c *SPC700) { c.cmp(c.read(c.adrIdx())) }, Cycles: 6} // CMP A, (dp+X)

	// 16-bit Moves (MOVW)
	Instructions[0xBA] = Instruction{Op: func(c *SPC700) { // MOVW YA, dp
		addr := c.adrDp()
		val := c.readWordDirectPage(addr)
		c.A = uint8(val)
		c.Y = uint8(val >> 8)
		c.SetZN16(val)
		// if c.PC >= 0xFFC0 {
		// 	fmt.Printf("DEBUG: MOVW YA dp at %04X. Addr=%04X. Val=%04X. (A=%02X Y=%02X)\n", c.PC-2, addr, val, c.A, c.Y)
		// }
	}, Cycles: 5}
	Instructions[0x9A] = Instruction{Op: func(c *SPC700) { // SUBW YA, dp
		low, high := c.adrDpWord()
		val := c.readWordPair(low, high) ^ 0xFFFF
		ya := uint16(c.A) | (uint16(c.Y) << 8)
		result := uint32(ya) + uint32(val) + 1
		c.V = (ya&0x8000) == (val&0x8000) && (val&0x8000) != (uint16(result)&0x8000)
		c.H = ((ya & 0x0FFF) + (val & 0x0FFF) + 1) > 0x0FFF
		c.C = result > 0xFFFF
		c.Z = uint16(result) == 0
		c.N = uint16(result)&0x8000 != 0
		c.A = uint8(result)
		c.Y = uint8(result >> 8)
	}, Cycles: 5}
	Instructions[0xDA] = Instruction{Op: func(c *SPC700) { // MOVW dp, YA
		addr := c.adrDp()
		val := uint16(c.A) | (uint16(c.Y) << 8)
		c.writeWordDirectPage(addr, val)
	}, Cycles: 5}

	Instructions[0x5F] = Instruction{Op: func(c *SPC700) { // JMP !abs
		c.PC = c.adrAbs()
	}, Cycles: 3}

	Instructions[0x1F] = Instruction{Op: func(c *SPC700) { // JMP (!abs+X)
		ptrAddr := c.adrAbx()
		target := c.readWord(ptrAddr)
		c.PC = target
	}, Cycles: 6}

	Instructions[0x0E] = Instruction{Op: func(c *SPC700) { // TSET1 abs
		addr := c.adrAbs()
		val := c.read(addr)
		result := c.A + (val ^ 0xFF) + 1
		c.SetZN(result)
		c.write(addr, val|c.A)
	}, Cycles: 6}
	Instructions[0x0F] = Instruction{Op: func(c *SPC700) { // BRK
		c.pushWord(c.PC)
		c.push(c.GetPSW())
		c.I = false
		c.B = true
		c.PC = c.readWordPair(0xFFDE, 0xFFDF)
	}, Cycles: 8}

	Instructions[0x14] = Instruction{Op: func(c *SPC700) { // OR A, dp+X
		addr := c.adrDpx()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 4}
	Instructions[0x05] = Instruction{Op: func(c *SPC700) { // OR A, abs
		addr := c.adrAbs()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 4}
	Instructions[0x15] = Instruction{Op: func(c *SPC700) { // OR A, abs+X
		addr := c.adrAbx()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 5}
	Instructions[0x16] = Instruction{Op: func(c *SPC700) { // OR A, abs+Y
		addr := c.adrAby()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 5}
	Instructions[0x17] = Instruction{Op: func(c *SPC700) { // OR A, (dp)+Y
		addr := c.adrIdy()
		lhs := c.A
		c.or(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortOrA, lhs)
	}, Cycles: 6}
	Instructions[0x1A] = Instruction{Op: func(c *SPC700) { // DECW dp
		low, high := c.adrDpWord()
		val := c.readWordPair(low, high) - 1
		c.SetZN16(val)
		c.writeWordPair(low, high, val)
	}, Cycles: 6}
	Instructions[0x1C] = Instruction{Op: func(c *SPC700) { // ASL A
		c.C = c.A&0x80 != 0
		c.A <<= 1
		c.SetZN(c.A)
	}, Cycles: 2}
	Instructions[0x0B] = Instruction{Op: func(c *SPC700) { // ASL dp
		addr := c.adrDp()
		val := c.read(addr)
		c.C = val&0x80 != 0
		val <<= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0x1B] = Instruction{Op: func(c *SPC700) { // ASL dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		c.C = val&0x80 != 0
		val <<= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x2B] = Instruction{Op: func(c *SPC700) { // ROL dp
		addr := c.adrDp()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x80 != 0
		val <<= 1
		if carry {
			val |= 0x01
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0x3B] = Instruction{Op: func(c *SPC700) { // ROL dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x80 != 0
		val <<= 1
		if carry {
			val |= 0x01
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x3C] = Instruction{Op: func(c *SPC700) { // ROL A
		carry := c.C
		c.C = c.A&0x80 != 0
		c.A <<= 1
		if carry {
			c.A |= 0x01
		}
		c.SetZN(c.A)
	}, Cycles: 2}
	Instructions[0x0C] = Instruction{Op: func(c *SPC700) { // ASL abs
		addr := c.adrAbs()
		val := c.read(addr)
		c.C = val&0x80 != 0
		val <<= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x5C] = Instruction{Op: func(c *SPC700) { // LSR A
		c.C = c.A&0x01 != 0
		c.A >>= 1
		c.SetZN(c.A)
	}, Cycles: 2}
	Instructions[0x4C] = Instruction{Op: func(c *SPC700) { // LSR abs
		addr := c.adrAbs()
		val := c.read(addr)
		c.C = val&0x01 != 0
		val >>= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x2C] = Instruction{Op: func(c *SPC700) { // ROL abs
		addr := c.adrAbs()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x80 != 0
		val <<= 1
		if carry {
			val |= 0x01
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x4B] = Instruction{Op: func(c *SPC700) { // LSR dp
		addr := c.adrDp()
		val := c.read(addr)
		c.C = val&0x01 != 0
		val >>= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0x5B] = Instruction{Op: func(c *SPC700) { // LSR dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		c.C = val&0x01 != 0
		val >>= 1
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x1E] = Instruction{Op: func(c *SPC700) { // CMP X, abs
		addr := c.adrAbs()
		val := c.read(addr)
		c.cmpX(val)
		c.armPendingPortCompareX(addr)
	}, Cycles: 4}

	Instructions[0x34] = Instruction{Op: func(c *SPC700) { // AND A, dp+X
		addr := c.adrDpx()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 4}
	Instructions[0x25] = Instruction{Op: func(c *SPC700) { // AND A, abs
		addr := c.adrAbs()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 4}
	Instructions[0x35] = Instruction{Op: func(c *SPC700) { // AND A, abs+X
		addr := c.adrAbx()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 5}
	Instructions[0x36] = Instruction{Op: func(c *SPC700) { // AND A, abs+Y
		addr := c.adrAby()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 5}
	Instructions[0x37] = Instruction{Op: func(c *SPC700) { // AND A, (dp)+Y
		addr := c.adrIdy()
		lhs := c.A
		c.and(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortAndA, lhs)
	}, Cycles: 6}
	Instructions[0x3A] = Instruction{Op: func(c *SPC700) { // INCW dp
		low, high := c.adrDpWord()
		val := c.readWordPair(low, high) + 1
		c.SetZN16(val)
		c.writeWordPair(low, high, val)
	}, Cycles: 6}
	Instructions[0x3F] = Instruction{Op: func(c *SPC700) { // CALL abs
		dst := c.adrAbs()
		c.pushWord(c.PC)
		c.PC = dst
	}, Cycles: 8}
	Instructions[0x6E] = Instruction{Op: func(c *SPC700) { // DBNZ dp, rel
		addr := c.adrDp()
		val := c.read(addr) - 1
		c.write(addr, val)
		rel := c.fetchByte()
		c.branchRel(rel, val != 0)
	}, Cycles: 5}

	Instructions[0x45] = Instruction{Op: func(c *SPC700) { // EOR A, abs
		addr := c.adrAbs()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 4}
	Instructions[0x46] = Instruction{Op: func(c *SPC700) { // EOR A, (X)
		addr := c.adrInd()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 3}
	Instructions[0x47] = Instruction{Op: func(c *SPC700) { // EOR A, (dp+X)
		addr := c.adrIdx()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 6}
	Instructions[0x4E] = Instruction{Op: func(c *SPC700) { // TCLR1 abs
		addr := c.adrAbs()
		val := c.read(addr)
		result := c.A + (val ^ 0xFF) + 1
		c.SetZN(result)
		c.write(addr, val&^c.A)
	}, Cycles: 6}

	Instructions[0x54] = Instruction{Op: func(c *SPC700) { // EOR A, dp+X
		addr := c.adrDpx()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 4}
	Instructions[0x55] = Instruction{Op: func(c *SPC700) { // EOR A, abs+X
		addr := c.adrAbx()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 5}
	Instructions[0x56] = Instruction{Op: func(c *SPC700) { // EOR A, abs+Y
		addr := c.adrAby()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 5}
	Instructions[0x57] = Instruction{Op: func(c *SPC700) { // EOR A, (dp)+Y
		addr := c.adrIdy()
		lhs := c.A
		c.eor(c.read(addr))
		c.armPendingPortLogic(addr, pendingPortEorA, lhs)
	}, Cycles: 6}
	Instructions[0x69] = Instruction{Op: func(c *SPC700) { // CMP dp, dp
		src := c.adrDp()
		dst := c.adrDp()
		srcVal := c.read(src)
		dstVal := c.read(dst)
		result := int(dstVal) - int(srcVal)
		c.C = dstVal >= srcVal
		c.SetZN(uint8(result))
	}, Cycles: 6}
	Instructions[0x5A] = Instruction{Op: func(c *SPC700) { // CMPW dp
		low, high := c.adrDpWord()
		val := c.readWordPair(low, high) ^ 0xFFFF
		ya := uint16(c.A) | (uint16(c.Y) << 8)
		result := uint32(ya) + uint32(val) + 1
		c.C = result > 0xFFFF
		c.Z = uint16(result) == 0
		c.N = uint16(result)&0x8000 != 0
	}, Cycles: 4}
	Instructions[0x5E] = Instruction{Op: func(c *SPC700) { // CMP Y, abs
		addr := c.adrAbs()
		val := c.read(addr)
		c.cmpY(val)
		c.armPendingPortCompareY(addr)
	}, Cycles: 4}

	Instructions[0x6B] = Instruction{Op: func(c *SPC700) { // ROR dp
		addr := c.adrDp()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x01 != 0
		val >>= 1
		if carry {
			val |= 0x80
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 4}
	Instructions[0x6C] = Instruction{Op: func(c *SPC700) { // ROR abs
		addr := c.adrAbs()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x01 != 0
		val >>= 1
		if carry {
			val |= 0x80
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x7B] = Instruction{Op: func(c *SPC700) { // ROR dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		carry := c.C
		c.C = val&0x01 != 0
		val >>= 1
		if carry {
			val |= 0x80
		}
		c.write(addr, val)
		c.SetZN(val)
	}, Cycles: 5}
	Instructions[0x7C] = Instruction{Op: func(c *SPC700) { // ROR A
		carry := c.C
		c.C = c.A&0x01 != 0
		c.A >>= 1
		if carry {
			c.A |= 0x80
		}
		c.SetZN(c.A)
	}, Cycles: 2}

	Instructions[0x6A] = Instruction{Op: func(c *SPC700) { // AND1N abs.bit
		addr, bit := c.adrAbsBit()
		c.C = c.C && ((^c.read(addr)>>bit)&1) != 0
	}, Cycles: 4}

	Instructions[0x74] = Instruction{Op: func(c *SPC700) { // CMP A, dp+X
		addr := c.adrDpx()
		val := c.read(addr)
		c.cmp(val)
		c.armPendingPortCompareA(addr)
	}, Cycles: 4}
	Instructions[0x75] = Instruction{Op: func(c *SPC700) { c.cmp(c.read(c.adrAbx())) }, Cycles: 5} // CMP A, abs+X
	Instructions[0x76] = Instruction{Op: func(c *SPC700) { c.cmp(c.read(c.adrAby())) }, Cycles: 5} // CMP A, abs+Y
	Instructions[0x77] = Instruction{Op: func(c *SPC700) { c.cmp(c.read(c.adrIdy())) }, Cycles: 6} // CMP A, (dp)+Y
	Instructions[0x7A] = Instruction{Op: func(c *SPC700) {                                         // ADDW dp
		low, high := c.adrDpWord()
		val := c.readWordPair(low, high)
		ya := uint16(c.A) | (uint16(c.Y) << 8)
		result := uint32(ya) + uint32(val)
		c.V = (ya&0x8000) == (val&0x8000) && (val&0x8000) != (uint16(result)&0x8000)
		c.H = ((ya & 0x0FFF) + (val & 0x0FFF) + 1) > 0x0FFF
		c.C = result > 0xFFFF
		c.Z = uint16(result) == 0
		c.N = uint16(result)&0x8000 != 0
		c.A = uint8(result)
		c.Y = uint8(result >> 8)
	}, Cycles: 5}

	Instructions[0x9E] = Instruction{Op: func(c *SPC700) { // DIV YA, X
		val := uint16(c.A) | (uint16(c.Y) << 8)
		result := 0xFFFF
		mod := int(c.A)
		if c.X != 0 {
			result = int(val) / int(c.X)
			mod = int(val) % int(c.X)
		}
		c.V = result > 0xFF
		c.H = (c.X & 0x0F) <= (c.Y & 0x0F)
		c.A = uint8(result)
		c.Y = uint8(mod)
		c.SetZN(c.A)
	}, Cycles: 12}
	Instructions[0xDF] = Instruction{Op: func(c *SPC700) { // DAA
		if c.A > 0x99 || c.C {
			c.A += 0x60
			c.C = true
		}
		if (c.A&0x0F) > 9 || c.H {
			c.A += 0x06
		}
		c.SetZN(c.A)
	}, Cycles: 3}
	Instructions[0xBE] = Instruction{Op: func(c *SPC700) { // DAS
		if c.A > 0x99 || !c.C {
			c.A -= 0x60
			c.C = false
		}
		if (c.A&0x0F) > 9 || !c.H {
			c.A -= 6
		}
		c.SetZN(c.A)
	}, Cycles: 3}
	Instructions[0xBF] = Instruction{Op: func(c *SPC700) { // MOV A, (X)+
		addr := c.adrInd()
		c.A = c.read(addr)
		c.X++
		c.SetZN(c.A)
	}, Cycles: 4}
	Instructions[0xCF] = Instruction{Op: func(c *SPC700) { // MUL YA
		result := uint16(c.A) * uint16(c.Y)
		c.A = uint8(result)
		c.Y = uint8(result >> 8)
		c.SetZN(c.Y)
	}, Cycles: 9}
	Instructions[0x2D] = Instruction{Op: func(c *SPC700) { c.push(c.A) }, Cycles: 4}       // PUSH A
	Instructions[0x4D] = Instruction{Op: func(c *SPC700) { c.push(c.X) }, Cycles: 4}       // PUSH X
	Instructions[0x6D] = Instruction{Op: func(c *SPC700) { c.push(c.Y) }, Cycles: 4}       // PUSH Y
	Instructions[0xAE] = Instruction{Op: func(c *SPC700) { c.A = c.pop() }, Cycles: 4}     // POP A
	Instructions[0x8E] = Instruction{Op: func(c *SPC700) { c.SetPSW(c.pop()) }, Cycles: 4} // POP PSW
	Instructions[0x2E] = Instruction{Op: func(c *SPC700) {                                 // CBNE dp, rel
		addr := c.adrDp()
		val := c.read(addr) ^ 0xFF
		result := c.A + val + 1
		rel := c.fetchByte()
		c.branchRel(rel, result != 0)
	}, Cycles: 5}
	Instructions[0xDE] = Instruction{Op: func(c *SPC700) { // CBNE dp+X, rel
		addr := c.adrDpx()
		val := c.read(addr) ^ 0xFF
		result := c.A + val + 1
		rel := c.fetchByte()
		c.branchRel(rel, result != 0)
	}, Cycles: 6}
	Instructions[0xED] = Instruction{Op: func(c *SPC700) { c.C = !c.C }, Cycles: 3}                  // NOTC
	Instructions[0xEE] = Instruction{Op: func(c *SPC700) { c.Y = c.pop(); c.SetZN(c.Y) }, Cycles: 4} // POP Y
	Instructions[0xEF] = Instruction{Op: func(c *SPC700) { c.Stopped = true }, Cycles: 3}            // SLEEP
	Instructions[0xEA] = Instruction{Op: func(c *SPC700) {                                           // NOT1 abs.bit
		addr, bit := c.adrAbsBit()
		c.write(addr, c.read(addr)^(1<<bit))
	}, Cycles: 5}
	Instructions[0x4F] = Instruction{Op: func(c *SPC700) { // PCALL
		dst := 0xFF00 | uint16(c.fetchByte())
		c.pushWord(c.PC)
		c.PC = dst
	}, Cycles: 6}
	Instructions[0xF6] = Instruction{Op: func(c *SPC700) { c.A = c.read(c.adrAby()); c.SetZN(c.A) }, Cycles: 5} // MOV A, abs+Y
	Instructions[0xF7] = Instruction{Op: func(c *SPC700) { c.A = c.read(c.adrIdy()); c.SetZN(c.A) }, Cycles: 6} // MOV A, (dp)+Y
	Instructions[0xFE] = Instruction{Op: func(c *SPC700) {                                                      // DBNZ Y, rel
		c.Y--
		rel := c.fetchByte()
		c.branchRel(rel, c.Y != 0)
	}, Cycles: 4}
	Instructions[0xFF] = Instruction{Op: func(c *SPC700) { c.Stopped = true }, Cycles: 3} // STOP
}

// Helpers for Addressing Modes
// Implemented simple version here, assuming adrDp is needed:

func (c *SPC700) adrDp() uint16 {
	addr := uint16(c.fetchByte())
	if c.P {
		return addr | 0x100
	}
	return addr
}

func (c *SPC700) adrAbs() uint16 {
	return c.fetchWord()
}

func (c *SPC700) adrInd() uint16 {
	// (X)
	addr := uint16(c.X)
	if c.P {
		addr |= 0x100
	}
	return addr
}

func (c *SPC700) adrIndY() uint16 {
	addr := uint16(c.Y)
	if c.P {
		addr |= 0x100
	}
	return addr
}

func (c *SPC700) adrIdx() uint16 {
	// (dp+X) aka Indexed Indirect
	// Pointer is at dp+X, dp+X+1
	base := c.fetchByte()
	ptrLowAddr := (uint16(base) + uint16(c.X)) & 0xFF
	if c.P {
		ptrLowAddr |= 0x100
	}
	// Both pointer bytes wrap within the selected direct page.
	low := c.read(ptrLowAddr)
	high := c.read((ptrLowAddr+1)&0xFF | (ptrLowAddr & 0xFF00))

	return uint16(low) | (uint16(high) << 8)
}

func (c *SPC700) adrIdy() uint16 {
	// (dp)+Y aka Indirect Indexed
	// Pointer is at dp, dp+1. Address is Pointer + Y.
	base := c.fetchByte() // dp offset
	ptrAddr := uint16(base)
	if c.P {
		ptrAddr |= 0x100
	}

	low := c.read(ptrAddr)
	// High byte of pointer address wraps in DP?
	ptrAddrHigh := (ptrAddr+1)&0xFF | (ptrAddr & 0xFF00)
	high := c.read(ptrAddrHigh)

	addr := uint16(low) | (uint16(high) << 8)
	return addr + uint16(c.Y)
}

func (c *SPC700) adrDpx() uint16 {
	// dp+X
	addr := (uint16(c.fetchByte()) + uint16(c.X)) & 0xFF
	if c.P {
		return addr | 0x100
	}
	return addr
}

func (c *SPC700) adrDpy() uint16 {
	// dp+Y
	addr := (uint16(c.fetchByte()) + uint16(c.Y)) & 0xFF
	if c.P {
		return addr | 0x100
	}
	return addr
}

func (c *SPC700) adrAbx() uint16 {
	// !abs+X
	base := c.adrAbs()
	return base + uint16(c.X)
}

func (c *SPC700) adrAby() uint16 {
	// !abs+Y
	base := c.adrAbs()
	return base + uint16(c.Y)
}

func (c *SPC700) adrDpImm() (uint16, uint16) {
	src := c.PC
	c.PC++
	return c.adrDp(), src
}

func (c *SPC700) adrAbsBit() (uint16, uint8) {
	addrBit := c.fetchWord()
	return addrBit & 0x1FFF, uint8(addrBit >> 13)
}

func (c *SPC700) adrDpWord() (uint16, uint16) {
	addr := c.fetchByte()
	low := uint16(addr)
	high := uint16(addr+1) & 0xFF
	if c.P {
		low |= 0x100
		high |= 0x100
	}
	return low, high
}

func (c *SPC700) read(addr uint16) uint8 {
	return c.bus.Read(addr)
}

func (c *SPC700) write(addr uint16, val uint8) {
	c.bus.Write(addr, val)
}

func (c *SPC700) readWord(addr uint16) uint16 {
	low := c.bus.Read(addr)
	high := c.bus.Read((addr + 1) & 0xFFFF)
	return uint16(low) | (uint16(high) << 8)
}

func (c *SPC700) readWordDirectPage(addr uint16) uint16 {
	low := c.bus.Read(addr)
	high := c.bus.Read((addr+1)&0xFF | (addr & 0xFF00))
	return uint16(low) | (uint16(high) << 8)
}

func (c *SPC700) readWordPair(lowAddr, highAddr uint16) uint16 {
	low := c.bus.Read(lowAddr)
	high := c.bus.Read(highAddr)
	return uint16(low) | (uint16(high) << 8)
}

func (c *SPC700) writeWord(addr uint16, val uint16) {
	c.bus.Write(addr, uint8(val))
	c.bus.Write((addr+1)&0xFFFF, uint8(val>>8))
}

func (c *SPC700) writeWordDirectPage(addr uint16, val uint16) {
	c.bus.Write(addr, uint8(val))
	c.bus.Write((addr+1)&0xFF|(addr&0xFF00), uint8(val>>8))
}

func (c *SPC700) writeWordPair(lowAddr, highAddr uint16, val uint16) {
	c.bus.Write(lowAddr, uint8(val))
	c.bus.Write(highAddr, uint8(val>>8))
}

func (c *SPC700) SetZN16(val uint16) {
	c.Z = val == 0
	c.N = (val & 0x8000) != 0
}

func (c *SPC700) branch(cond bool) {
	rel := int8(c.fetchByte())
	if cond {
		c.Cycles += 2
		c.PC = uint16(int(c.PC) + int(rel))
	}
}

func (c *SPC700) branchRel(rel uint8, cond bool) {
	if cond {
		c.Cycles += 2
		c.PC = uint16(int(c.PC) + int(int8(rel)))
	}
}

// ALU Operations (Logic)

func (c *SPC700) and(val uint8) {
	c.A &= val
	c.SetZN(c.A)
}

func (c *SPC700) or(val uint8) {
	c.A |= val
	c.SetZN(c.A)
}

func (c *SPC700) eor(val uint8) {
	c.A ^= val
	c.SetZN(c.A)
}

func (c *SPC700) adcPort(addr uint16) {
	lhs, carry := c.A, c.C
	c.adc(c.read(addr))
	c.armPendingPortArithmetic(addr, pendingPortAdcA, lhs, carry)
}

func (c *SPC700) sbcPort(addr uint16) {
	lhs, carry := c.A, c.C
	c.sbc(c.read(addr))
	c.armPendingPortArithmetic(addr, pendingPortSbcA, lhs, carry)
}

func (c *SPC700) andMem(addr uint16, val uint8) {
	result := c.read(addr) & val
	c.write(addr, result)
	c.SetZN(result)
}

func (c *SPC700) orMem(addr uint16, val uint8) {
	result := c.read(addr) | val
	c.write(addr, result)
	c.SetZN(result)
}

func (c *SPC700) eorMem(addr uint16, val uint8) {
	result := c.read(addr) ^ val
	c.write(addr, result)
	c.SetZN(result)
}

func (c *SPC700) adcMem(addr uint16, val uint8) {
	lhs := c.read(addr)
	result := int(lhs) + int(val) + int(boolToInt(c.C))
	c.V = (lhs&0x80) == (val&0x80) && (val&0x80) != (uint8(result)&0x80)
	c.H = ((lhs & 0x0F) + (val & 0x0F) + uint8(boolToInt(c.C))) > 0x0F
	c.C = result > 0xFF
	c.write(addr, uint8(result))
	c.SetZN(uint8(result))
}

func (c *SPC700) sbcMem(addr uint16, val uint8) {
	lhs := c.read(addr)
	inverted := val ^ 0xFF
	result := int(lhs) + int(inverted) + int(boolToInt(c.C))
	c.V = (lhs&0x80) == (inverted&0x80) && (inverted&0x80) != (uint8(result)&0x80)
	c.H = ((lhs & 0x0F) + (inverted & 0x0F) + uint8(boolToInt(c.C))) > 0x0F
	c.C = result > 0xFF
	c.write(addr, uint8(result))
	c.SetZN(uint8(result))
}

func (c *SPC700) cmpMem(addr uint16, val uint8) {
	lhs := c.read(addr)
	result := int(lhs) - int(val)
	c.C = lhs >= val
	c.SetZN(uint8(result))
}

func (c *SPC700) armPendingPortCompareA(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortCompareA
}

func (c *SPC700) armPendingPortCompareX(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortCompareX
}

func (c *SPC700) armPendingPortCompareY(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortCompareY
}

func (c *SPC700) armPendingPortCompareMemImm(addr uint16, imm uint8) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortCompareMemImm
	c.pendingPortCompareImm = imm
}

func (c *SPC700) armPendingPortLoadA(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortLoadA
}

func (c *SPC700) ArmPendingPortLoadA(addr uint16) {
	c.armPendingPortLoadA(addr)
}

func (c *SPC700) armPendingPortLoadX(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortLoadX
}

func (c *SPC700) armPendingPortLoadY(addr uint16) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = pendingPortLoadY
}

func (c *SPC700) armPendingPortLogic(addr uint16, kind uint8, lhs uint8) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = kind
	c.pendingPortCompareLHS = lhs
}

func (c *SPC700) armPendingPortArithmetic(addr uint16, kind uint8, lhs uint8, carry bool) {
	if addr < 0x00F4 || addr > 0x00F7 {
		return
	}
	c.pendingPortCompareAddr = addr
	c.pendingPortCompareKind = kind
	c.pendingPortCompareLHS = lhs
	c.pendingPortCompareCarry = carry
}

// PatchPortWrite updates a port operation that has retired in the atomic SPC700
// step but whose cycle budget has not yet elapsed in the APU scheduler.
func (c *SPC700) PatchPortWrite(addr uint16, val uint8) {
	if addr != c.pendingPortCompareAddr {
		return
	}

	switch c.pendingPortCompareKind {
	case pendingPortCompareA:
		c.cmp(val)
	case pendingPortCompareX:
		c.cmpX(val)
	case pendingPortCompareY:
		c.cmpY(val)
	case pendingPortCompareMemImm:
		result := int(val) - int(c.pendingPortCompareImm)
		c.C = val >= c.pendingPortCompareImm
		c.SetZN(uint8(result))
	case pendingPortLoadA:
		c.A = val
		c.SetZN(c.A)
	case pendingPortLoadX:
		c.X = val
		c.SetZN(c.X)
	case pendingPortLoadY:
		c.Y = val
		c.SetZN(c.Y)
	case pendingPortOrA:
		c.A = c.pendingPortCompareLHS | val
		c.SetZN(c.A)
	case pendingPortAndA:
		c.A = c.pendingPortCompareLHS & val
		c.SetZN(c.A)
	case pendingPortEorA:
		c.A = c.pendingPortCompareLHS ^ val
		c.SetZN(c.A)
	case pendingPortAdcA:
		c.A = c.pendingPortCompareLHS
		c.C = c.pendingPortCompareCarry
		c.adc(val)
	case pendingPortSbcA:
		c.A = c.pendingPortCompareLHS
		c.C = c.pendingPortCompareCarry
		c.sbc(val)
	}
}

func (c *SPC700) push(val uint8) {
	c.write(0x100|uint16(c.SP), val)
	c.SP--
}

func (c *SPC700) pop() uint8 {
	c.SP++
	return c.read(0x100 | uint16(c.SP))
}

func (c *SPC700) pushWord(val uint16) {
	c.push(uint8(val >> 8))
	c.push(uint8(val))
}

func (c *SPC700) popWord() uint16 {
	low := c.pop()
	high := c.pop()
	return uint16(low) | (uint16(high) << 8)
}

func (c *SPC700) tcall(n uint8) {
	// Table Call: Call address at FFDE - 2*n
	addr := 0xFFDE - uint16(n)*2
	dest := c.readWordPair(addr, (addr+1)&0xFFFF)
	c.pushWord(c.PC)
	c.PC = dest
}

// Run executes the CPU.
func (c *SPC700) Step() {
	if c.Stopped {
		return
	}

	c.ClearPendingPortOperation()

	opcode := c.fetchByte()
	// DEBUG: Trace IPL Execution (and Loaded Driver)
	if c.PC >= 0x0070 {
		// Fetch registers for logging
		// Note: A, X, Y, SP, PSW are on the struct
		// fmt.Printf("APU TRACE: %04X %02X A:%02X X:%02X Y:%02X SP:%02X\n", c.PC-1, opcode, c.A, c.X, c.Y, c.SP)
	}

	inst := Instructions[opcode]
	if inst.Op != nil {
		inst.Op(c)
		c.Cycles += uint64(inst.Cycles)
	} else {
		c.Stopped = true
		c.Cycles += 2
	}
}

// ClearPendingPortOperation ends the previous instruction's input-port
// compatibility operation. An external micro-op executor calls it when
// starting an instruction, just as Step does for atomic execution.
func (c *SPC700) ClearPendingPortOperation() {
	c.pendingPortCompareAddr = 0
	c.pendingPortCompareKind = pendingPortCompareNone
}
