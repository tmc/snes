package cpu

// Helper to get value based on mode/size
func (c *CPU) getLoadVal(mode AddressingMode, size16 bool) uint16 {
	if mode == AddrImm {
		if size16 {
			return c.fetchWord()
		}
		return uint16(c.fetchByte())
	}

	// Address resolution
	addr, _ := c.getEffectiveAddress(mode)

	// Memory read
	if size16 {
		low := c.read(addr)
		high := c.read((addr + 1) & 0xFFFFFF) // Check wrapping? Usually linear.
		return uint16(high)<<8 | uint16(low)
	}
	return uint16(c.read(addr))
}

func opLDA(c *CPU, mode AddressingMode) {
	// Check M flag for size
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.A = val
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | (val & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opLDX(c *CPU, mode AddressingMode) {
	// Check X flag for size
	size16 := !c.E && (c.P&0x10) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.X = val
		c.setNZ16(c.X)
	} else {
		c.X = (c.X & 0xFF00) | (val & 0xFF)
		c.setNZ(uint8(c.X))
	}
}

func opLDY(c *CPU, mode AddressingMode) {
	// Check X flag (Y uses X flag)
	size16 := !c.E && (c.P&0x10) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.Y = val
		c.setNZ16(c.Y)
	} else {
		c.Y = (c.Y & 0xFF00) | (val & 0xFF)
		c.setNZ(uint8(c.Y))
	}
}
