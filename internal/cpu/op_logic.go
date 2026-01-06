package cpu

func opAND(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.A &= val
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | ((c.A & val) & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opORA(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.A |= val
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | ((c.A | val) & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opEOR(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		c.A ^= val
		c.setNZ16(c.A)
	} else {
		c.A = (c.A & 0xFF00) | ((c.A ^ val) & 0xFF)
		c.setNZ(uint8(c.A))
	}
}

func opBIT(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		res := c.A & val
		if res == 0 {
			c.P |= 0x02 // Set Zero
		} else {
			c.P &= 0xFD
		}
		// N and V flags come from Memory Value (val), not result?
		// "The M flag selects the accumulator size ... mask ... Z is set/reset ...
		// N is set to bit 7 (8-bit) or 15 (16-bit) of VALID MEMORY DATA.
		// V is set to bit 6 (8-bit) or 14 (16-bit) of VALID MEMORY DATA."
		// Wait, BIT Immediate affects ONLY Z flag.

		if mode != AddrImm {
			if (val & 0x8000) != 0 {
				c.P |= 0x80
			} else {
				c.P &= 0x7F
			}
			if (val & 0x4000) != 0 {
				c.P |= 0x40
			} else {
				c.P &= 0xBF
			}
		}
	} else {
		res := (c.A & 0xFF) & (val & 0xFF)
		if res == 0 {
			c.P |= 0x02
		} else {
			c.P &= 0xFD
		}

		if mode != AddrImm {
			if (val & 0x80) != 0 {
				c.P |= 0x80
			} else {
				c.P &= 0x7F
			}
			if (val & 0x40) != 0 {
				c.P |= 0x40
			} else {
				c.P &= 0xBF
			}
		}
	}
}

func opTRB(c *CPU, mode AddressingMode) {
	// TRB (Test and Reset Bits)
	// Z flag set if (A & mem) == 0
	// mem = mem & ~A
	// Read-Modify-Write

	size16 := !c.E && (c.P&0x20) == 0

	// We need address to write back.
	// getLoadVal (used in other logic ops) consumes cycles for read but returns value.
	// We need the address to write back.
	// Addressing modes for TRB/TSB: Absolute, Direct.

	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := c.readWord(addr)

		if (c.A & val) == 0 {
			c.P |= 0x02 // Set Z
		} else {
			c.P &= 0xFD // Clear Z
		}

		res := val & ^c.A
		c.write(addr, uint8(res))
		c.write((addr+1)&0xFFFFFF, uint8(res>>8))
	} else {
		val := c.read(addr)

		if (uint8(c.A) & val) == 0 {
			c.P |= 0x02
		} else {
			c.P &= 0xFD
		}

		res := val & ^uint8(c.A)
		c.write(addr, res)
	}
}

func opTSB(c *CPU, mode AddressingMode) {
	// TSB (Test and Set Bits)
	// Z flag set if (A & mem) == 0
	// mem = mem | A

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := c.readWord(addr)

		if (c.A & val) == 0 {
			c.P |= 0x02
		} else {
			c.P &= 0xFD
		}

		res := val | c.A
		c.write(addr, uint8(res))
		c.write((addr+1)&0xFFFFFF, uint8(res>>8))
	} else {
		val := c.read(addr)

		if (uint8(c.A) & val) == 0 {
			c.P |= 0x02
		} else {
			c.P &= 0xFD
		}

		res := val | uint8(c.A)
		c.write(addr, res)
	}
}
