package cpu

func opPHK(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.pushByte(c.PB)
}

func opPHP(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.pushByte(c.P)
}

func opPHA(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if c.E || (c.P&0x20) != 0 { // 8-bit A
		c.pushByte(uint8(c.A))
	} else {
		c.pushWord(c.A)
	}
}

func opPHX(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if c.E || (c.P&0x10) != 0 { // 8-bit X
		c.pushByte(uint8(c.X))
	} else {
		c.pushWord(c.X)
	}
}

func opPHY(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if c.E || (c.P&0x10) != 0 { // 8-bit Y
		c.pushByte(uint8(c.Y))
	} else {
		c.pushWord(c.Y)
	}
}

func opPLP(c *CPU, mode AddressingMode) {
	c.AddCycles(12)
	c.P = c.popByte()
	c.updateMXFlags()
}

func opPLA(c *CPU, mode AddressingMode) {
	c.AddCycles(12)
	if c.E || (c.P&0x20) != 0 {
		c.A = (c.A & 0xFF00) | uint16(c.popByte())
		c.setNZ(uint8(c.A))
	} else {
		c.A = c.popWord()
		c.setNZ16(c.A)
	}
}

func opPLX(c *CPU, mode AddressingMode) {
	c.AddCycles(12)
	if c.E || (c.P&0x10) != 0 {
		c.X = (c.X & 0xFF00) | uint16(c.popByte())
		c.setNZ(uint8(c.X))
	} else {
		c.X = c.popWord()
		c.setNZ16(c.X)
	}
}

func opPLY(c *CPU, mode AddressingMode) {
	c.AddCycles(12)
	if c.E || (c.P&0x10) != 0 {
		c.Y = (c.Y & 0xFF00) | uint16(c.popByte())
		c.setNZ(uint8(c.Y))
	} else {
		c.Y = c.popWord()
		c.setNZ16(c.Y)
	}
}

func opPHB(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.pushByte(c.DB)
}

func opPLB(c *CPU, mode AddressingMode) {
	c.AddCycles(12)
	c.DB = c.popByte()
	c.setNZ(c.DB)
}

func opPHD(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.pushWord(c.D)
}

func opPLD(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	c.D = c.popWord()
	c.setNZ16(c.D)
}

func opPER(c *CPU, mode AddressingMode) {
	// 62: PER (Push Effective PC Relative Indirect)
	// Operand is 16-bit signed offset.
	// Target = PC + Offset
	// Note: PC is address of next instruction (after consuming operand).
	// During fetchWord, PC is incremented.
	// So if PC points to opcode, fetchByte() -> PC+1. fetchWord() -> PC+3.
	// The offset is added to this PC.
	offset := int16(c.fetchWord())
	target := uint16(int32(c.PC) + int32(offset))
	c.pushWord(target)
}

func opPEA(c *CPU, mode AddressingMode) {
	// F4: PEA (Push Effective Absolute Address)
	// Pushes the 16-bit operand (Pointer/Address) onto stack.
	// Effectively pushes immediate 16-bit value.
	val := c.fetchWord()
	c.AddCycles(6)
	c.pushWord(val)
}

func opPEI(c *CPU, mode AddressingMode) {
	dp := uint16(c.fetchByte())
	val := c.readWordDirectPage(dp)
	c.AddCycles(6)
	c.pushWord(val)
}

func opWAI(c *CPU, mode AddressingMode) {
	c.Waiting = true
}

func opSTP(c *CPU, mode AddressingMode) {
	c.Stopped = true
}
