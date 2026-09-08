package cpu

func opTAX(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if !c.E && (c.P&0x10) == 0 {
		c.X = c.A
		c.setNZ16(c.X)
	} else {
		c.X = c.A & 0xFF
		c.setNZ(uint8(c.X))
	}
}

func opTAY(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if !c.E && (c.P&0x10) == 0 { // Check P.X for Size
		c.Y = c.A
		c.setNZ16(c.Y)
	} else {
		// 8-bit Y
		c.Y = c.A & 0xFF
		c.setNZ(uint8(c.Y))
	}
}

func opTSX(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.X = c.S
	if !c.E && (c.P&0x10) == 0 {
		c.setNZ16(c.X)
	} else {
		c.X &= 0xFF
		c.setNZ(uint8(c.X))
	}
}

func opTXA(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if !c.E && (c.P&0x20) == 0 { // Check P.M
		c.A = c.X
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | (c.X & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opTXS(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.S = c.X
	if c.E {
		c.S = (c.S & 0xFF) | 0x0100
	}
}

func opTYA(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if !c.E && (c.P&0x20) == 0 {
		c.A = c.Y
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | (c.Y & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opTXY(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.Y = c.X
	if !c.E && (c.P&0x10) == 0 {
		c.setNZ16(c.Y)
	} else {
		c.Y &= 0xFF
		c.setNZ(uint8(c.Y))
	}
}

func opTYX(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.X = c.Y
	if !c.E && (c.P&0x10) == 0 {
		c.setNZ16(c.X)
	} else {
		c.X &= 0xFF
		c.setNZ(uint8(c.X))
	}
}

func opTCD(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.D = c.A
	c.setNZ16(c.D)
}

func opTDC(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.A = c.D
	c.setNZ16(c.A)
}

func opTCS(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.S = c.A
	if c.E {
		c.S = (c.S & 0xFF) | 0x0100
	}
}

func opTSC(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.A = c.S
	c.setNZ16(c.A)
}
