package cpu

func opASL(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if mode == AddrAcc {
		if !c.E && (c.P&0x20) == 0 {
			// 16-bit
			val := c.A
			c.P &= 0xFE
			if (val & 0x8000) != 0 {
				c.P |= 0x01
			}
			val = val << 1
			c.A = val
			c.setNZ16(c.A)
		} else {
			// 8-bit
			val := uint8(c.A)
			c.P &= 0xFE
			if (val & 0x80) != 0 {
				c.P |= 0x01
			}
			val = val << 1
			c.A = (c.A & 0xFF00) | uint16(val)
			c.setNZ(val)
		}
		return
	}

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := uint16(c.read(addr)) | (uint16(c.read((addr+1)&0xFFFFFF)) << 8)
		c.P &= 0xFE
		if (val & 0x8000) != 0 {
			c.P |= 0x01
		}
		val = val << 1
		c.setNZ16(val)
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
		c.write(addr, uint8(val))
	} else {
		val := c.read(addr)
		c.P &= 0xFE
		if (val & 0x80) != 0 {
			c.P |= 0x01
		}
		val = val << 1
		c.setNZ(val)
		c.write(addr, val)
	}
}

func opLSR(c *CPU, mode AddressingMode) {
	c.Idle(6)
	if mode == AddrAcc {
		if !c.E && (c.P&0x20) == 0 {
			val := c.A
			c.P &= 0xFE
			if (val & 0x0001) != 0 {
				c.P |= 0x01
			}
			val = val >> 1
			c.A = val
			c.setNZ16(c.A)
		} else {
			val := uint8(c.A)
			c.P &= 0xFE
			if (val & 0x01) != 0 {
				c.P |= 0x01
			}
			val = val >> 1
			c.A = (c.A & 0xFF00) | uint16(val)
			c.setNZ(val)
		}
		return
	}

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := uint16(c.read(addr)) | (uint16(c.read((addr+1)&0xFFFFFF)) << 8)
		c.P &= 0xFE
		if (val & 0x0001) != 0 {
			c.P |= 0x01
		}
		val = val >> 1
		c.setNZ16(val)
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
		c.write(addr, uint8(val))
	} else {
		val := c.read(addr)
		c.P &= 0xFE
		if (val & 0x01) != 0 {
			c.P |= 0x01
		}
		val = val >> 1
		c.setNZ(val)
		c.write(addr, val)
	}
}

func opROL(c *CPU, mode AddressingMode) {
	c.Idle(6)
	carry := uint16(0)
	if (c.P & 0x01) != 0 {
		carry = 1
	}

	if mode == AddrAcc {
		if !c.E && (c.P&0x20) == 0 {
			val := c.A
			newCarry := (val & 0x8000) != 0
			val = (val << 1) | carry
			c.A = val
			c.setNZ16(c.A)
			if newCarry {
				c.P |= 0x01
			} else {
				c.P &= 0xFE
			}
		} else {
			val := uint8(c.A)
			newCarry := (val & 0x80) != 0
			val = (val << 1) | uint8(carry)
			c.A = (c.A & 0xFF00) | uint16(val)
			c.setNZ(val)
			if newCarry {
				c.P |= 0x01
			} else {
				c.P &= 0xFE
			}
		}
		return
	}

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := uint16(c.read(addr)) | (uint16(c.read((addr+1)&0xFFFFFF)) << 8)
		newCarry := (val & 0x8000) != 0
		val = (val << 1) | carry
		c.setNZ16(val)
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
		c.write(addr, uint8(val))
		if newCarry {
			c.P |= 0x01
		} else {
			c.P &= 0xFE
		}
	} else {
		val := c.read(addr)
		newCarry := (val & 0x80) != 0
		val = (val << 1) | uint8(carry)
		c.setNZ(val)
		c.write(addr, val)
		if newCarry {
			c.P |= 0x01
		} else {
			c.P &= 0xFE
		}
	}
}

func opROR(c *CPU, mode AddressingMode) {
	c.Idle(6)
	carry := uint16(0)
	if (c.P & 0x01) != 0 {
		carry = 1
	}

	if mode == AddrAcc {
		if !c.E && (c.P&0x20) == 0 {
			val := c.A
			newCarry := (val & 0x0001) != 0
			val = (val >> 1) | (carry << 15)
			c.A = val
			c.setNZ16(c.A)
			if newCarry {
				c.P |= 0x01
			} else {
				c.P &= 0xFE
			}
		} else {
			val := uint8(c.A)
			newCarry := (val & 0x01) != 0
			val = (val >> 1) | (uint8(carry) << 7)
			c.A = (c.A & 0xFF00) | uint16(val)
			c.setNZ(val)
			if newCarry {
				c.P |= 0x01
			} else {
				c.P &= 0xFE
			}
		}
		return
	}

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := uint16(c.read(addr)) | (uint16(c.read((addr+1)&0xFFFFFF)) << 8)
		newCarry := (val & 0x0001) != 0
		val = (val >> 1) | (carry << 15)
		c.setNZ16(val)
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
		c.write(addr, uint8(val))
		if newCarry {
			c.P |= 0x01
		} else {
			c.P &= 0xFE
		}
	} else {
		val := c.read(addr)
		newCarry := (val & 0x01) != 0
		val = (val >> 1) | (uint8(carry) << 7)
		c.setNZ(val)
		c.write(addr, val)
		if newCarry {
			c.P |= 0x01
		} else {
			c.P &= 0xFE
		}
	}
}
