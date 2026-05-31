package cpu

// Helper to write value based on mode/size
func (c *CPU) putStoreVal(mode AddressingMode, val uint16, size16 bool) {
	if mode == AddrImm {
		c.setFaultf("invalid store addressing mode: immediate")
		return
	}

	if mode == AddrAbsX || mode == AddrAbsY {
		addr := c.fetchWord()
		index := c.X
		if mode == AddrAbsY {
			index = c.Y
		}
		c.AddCycles(6)
		full := ((uint32(c.DB) << 16) + uint32(addr) + uint32(index)) & 0xFFFFFF
		if size16 {
			c.write(full, uint8(val))
			c.write((full+1)&0xFFFFFF, uint8(val>>8))
		} else {
			c.write(full, uint8(val))
		}
		return
	}

	addr, _ := c.getEffectiveAddress(mode)
	if mode == AddrIndY {
		c.AddCycles(6)
	}

	if size16 {
		c.write(addr, uint8(val))
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
	} else {
		c.write(addr, uint8(val)) // Low byte only
	}
}

func opSTA(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	c.putStoreVal(mode, c.A, size16)
}

func opSTX(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x10) == 0
	c.putStoreVal(mode, c.X, size16)
}

func opSTY(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x10) == 0
	c.putStoreVal(mode, c.Y, size16)
}

func opSTZ(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0 // STZ follows M flag? Yes.
	c.putStoreVal(mode, 0, size16)
}
