package cpu

func (c *CPU) branch(take bool, mode AddressingMode) {
	if mode == AddrRel {
		offset := int8(c.fetchByte()) // Fetch offset
		if take {
			oldPC := c.PC
			c.PC = uint16(int32(c.PC) + int32(offset))
			c.Idle(6) // +1 CPU cycle for taking branch

			// Emulation mode page crossing check
			if c.E && (oldPC&0xFF00) != (c.PC&0xFF00) {
				c.Idle(6)
			}
		}
	} else if mode == AddrRelL {
		// BRL (Always Long Relative)
		offset := int16(c.fetchWord())
		c.PC = uint16(int32(c.PC) + int32(offset))
		c.Idle(6)
		// BRL always taken.
	}
}

func opBCC(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x01) == 0, mode)
}

func opBCS(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x01) != 0, mode)
}

func opBEQ(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x02) != 0, mode)
}

func opBNE(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x02) == 0, mode)
}

func opBMI(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x80) != 0, mode)
}

func opBPL(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x80) == 0, mode)
}

func opBVC(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x40) == 0, mode)
}

func opBVS(c *CPU, mode AddressingMode) {
	c.branch((c.P&0x40) != 0, mode)
}

func opBRA(c *CPU, mode AddressingMode) {
	c.branch(true, mode)
}

func opBRL(c *CPU, mode AddressingMode) {
	offset := int16(c.fetchWord())
	c.PC = uint16(int32(c.PC) + int32(offset))
	c.Idle(6)
}
