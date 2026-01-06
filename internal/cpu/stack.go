package cpu

// Stack Manipulation Helpers

func (c *CPU) pushByte(val uint8) {
	c.write(uint32(c.S), val)

	if c.E {
		// Emulation Mode: Stack confined to Page 1 ($0100-$01FF)
		// SP is 8-bit effectively in low byte? Or standard 6502 wrapping?
		// 6502: SP is $01xx. Decrement wraps xx within FF->00.
		// So S = (S & 0xFF00) | ((S - 1) & 0xFF)
		// But in E mode, SH is forced to 01.
		c.S = (c.S & 0xFF00) | ((c.S - 1) & 0xFF)
	} else {
		c.S--
	}
}

func (c *CPU) pushWord(val uint16) {
	c.pushByte(uint8(val >> 8)) // High
	c.pushByte(uint8(val))      // Low
}

func (c *CPU) popByte() uint8 {
	if c.E {
		c.S = (c.S & 0xFF00) | ((c.S + 1) & 0xFF)
	} else {
		c.S++
	}
	return c.read(uint32(c.S))
}

func (c *CPU) popWord() uint16 {
	low := c.popByte()
	high := c.popByte()
	return uint16(high)<<8 | uint16(low)
}
