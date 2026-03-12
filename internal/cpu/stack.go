package cpu

// Stack Manipulation Helpers

func (c *CPU) pushByte(val uint8) {
	if c.E {
		addr := 0x0100 | uint32(c.S&0x00FF)
		c.write(addr, val)
		c.S = 0x0100 | ((c.S - 1) & 0x00FF)
	} else {
		c.write(uint32(c.S), val)
		c.S--
	}
}

func (c *CPU) pushByteRaw(val uint8) {
	c.write(uint32(c.S), val)
	c.S--
}

func (c *CPU) pushWord(val uint16) {
	c.pushByte(uint8(val >> 8)) // High
	c.pushByte(uint8(val))      // Low
}

func (c *CPU) popByte() uint8 {
	if c.E {
		c.S = 0x0100 | ((c.S + 1) & 0x00FF)
		return c.read(0x0100 | uint32(c.S&0x00FF))
	}
	c.S++
	return c.read(uint32(c.S))
}

func (c *CPU) popByteRaw() uint8 {
	c.S++
	return c.read(uint32(c.S))
}

func (c *CPU) popWord() uint16 {
	low := c.popByte()
	high := c.popByte()
	return uint16(high)<<8 | uint16(low)
}

func (c *CPU) normalizeEmulationStack() {
	if c.E {
		c.S = 0x0100 | (c.S & 0x00FF)
	}
}
