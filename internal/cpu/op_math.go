package cpu

// Comparison Helpers

func (c *CPU) compare(val1, val2 uint16, size16 bool) {
	var diff uint16
	if size16 {
		diff = val1 - val2
		c.setNZ16(diff)
		if val1 >= val2 {
			c.P |= 0x01 // Set Carry
		} else {
			c.P &= 0xFE // Clear Carry
		}
	} else {
		// 8-bit comparison
		v1 := uint8(val1)
		v2 := uint8(val2)
		d := v1 - v2
		c.setNZ(d)
		if v1 >= v2 {
			c.P |= 0x01
		} else {
			c.P &= 0xFE
		}
	}
}

func opCMP(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)
	c.compare(c.A, val, size16)
}

func opCPX(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x10) == 0
	val := c.getLoadVal(mode, size16)
	c.compare(c.X, val, size16)
}

func opCPY(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x10) == 0
	val := c.getLoadVal(mode, size16)
	c.compare(c.Y, val, size16)
}

func opINY(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if !c.E && (c.P&0x10) == 0 {
		c.Y++
		c.setNZ16(c.Y)
	} else {
		c.Y = (c.Y + 1) & 0xFF
		c.setNZ(uint8(c.Y))
	}
}

func opDEY(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if !c.E && (c.P&0x10) == 0 {
		c.Y--
		c.setNZ16(c.Y)
	} else {
		c.Y = (c.Y - 1) & 0xFF
		c.setNZ(uint8(c.Y))
	}
}

func opINX(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if !c.E && (c.P&0x10) == 0 {
		c.X++
		c.setNZ16(c.X)
	} else {
		c.X = (c.X + 1) & 0xFF
		c.setNZ(uint8(c.X))
	}
}

func opDEX(c *CPU, mode AddressingMode) {
	c.AddCycles(6)
	if !c.E && (c.P&0x10) == 0 {
		c.X--
		c.setNZ16(c.X)
	} else {
		c.X = (c.X - 1) & 0xFF
		c.setNZ(uint8(c.X))
	}
}

func opINC(c *CPU, mode AddressingMode) {
	c.AddCycles(6) // Internal (Acc) or Spurious (R-M-W)
	if mode == AddrAcc {
		// Increment Accumulator
		if !c.E && (c.P&0x20) == 0 {
			c.A++
			c.setNZ16(c.A)
		} else {
			c.A = (c.A & 0xFF00) | ((c.A + 1) & 0xFF)
			c.setNZ(uint8(c.A))
		}
		return
	}

	// Memory INC
	addr, _ := c.getEffectiveAddress(mode)
	val := c.read(addr)

	if !c.E && (c.P&0x20) == 0 {
		// 16-bit
		high := c.read((addr + 1) & 0xFFFFFF)
		full := uint16(val) | (uint16(high) << 8)
		full++
		c.write(addr, uint8(full&0xFF))
		c.write((addr+1)&0xFFFFFF, uint8(full>>8))
		c.setNZ16(full)
	} else {
		// 8-bit
		val++
		c.write(addr, val)
		c.setNZ(val)
	}
}

func opDEC(c *CPU, mode AddressingMode) {
	if mode == AddrAcc {
		if !c.E && (c.P&0x20) == 0 {
			c.A--
			c.setNZ16(c.A)
		} else {
			c.A = (c.A & 0xFF00) | ((c.A - 1) & 0xFF)
			c.setNZ(uint8(c.A))
		}
		return
	}

	size16 := !c.E && (c.P&0x20) == 0
	addr, _ := c.getEffectiveAddress(mode)

	if size16 {
		val := uint16(c.read(addr)) | (uint16(c.read((addr+1)&0xFFFFFF)) << 8)
		val--
		c.setNZ16(val)
		c.write(addr, uint8(val))
		c.write((addr+1)&0xFFFFFF, uint8(val>>8))
	} else {
		val := c.read(addr)
		val--
		c.setNZ(val)
		c.write(addr, val)
	}
}

func opADC(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)
	if c.PC >= 0x88E0 && c.PC <= 0x88F0 {
		// fmt.Printf("DEBUG: ADC PC:%04X A:%04X Val:%04X P_born:%02X Size16:%v\n", c.PC, c.A, val, c.P, size16)
	}

	if size16 {
		if c.P&0x08 != 0 {
			a := c.A
			result := uint32(a&0x000F) + uint32(val&0x000F)
			if c.P&0x01 != 0 {
				result++
			}
			if result > 0x0009 {
				result += 0x0006
			}

			carry := result > 0x000F
			result = uint32(a&0x00F0) + uint32(val&0x00F0) + (result & 0x000F)
			if carry {
				result += 0x0010
			}
			if result > 0x009F {
				result += 0x0060
			}

			carry = result > 0x00FF
			result = uint32(a&0x0F00) + uint32(val&0x0F00) + (result & 0x00FF)
			if carry {
				result += 0x0100
			}
			if result > 0x09FF {
				result += 0x0600
			}

			carry = result > 0x0FFF
			result = uint32(a&0xF000) + uint32(val&0xF000) + (result & 0x0FFF)
			if carry {
				result += 0x1000
			}

			overflow := (^uint16(a^val) & uint16(a^uint16(result)) & 0x8000) != 0
			if result > 0x9FFF {
				result += 0x6000
			}

			c.P &= 0xBE
			c.setNZ16(uint16(result))
			if result > 0xFFFF {
				c.P |= 0x01
			}
			if overflow {
				c.P |= 0x40
			}
			c.A = uint16(result)
			return
		}

		result := uint32(c.A) + uint32(val)
		if c.P&0x01 != 0 {
			result++
		}

		c.P &= 0xBE
		c.setNZ16(uint16(result))
		if result > 0xFFFF {
			c.P |= 0x01
		}

		if (^uint16(c.A^val) & uint16(c.A^uint16(result)) & 0x8000) != 0 {
			c.P |= 0x40
		}

		c.A = uint16(result)
	} else {
		a := uint8(c.A)
		v := uint8(val)
		if c.P&0x08 != 0 {
			result := uint16(a&0x0F) + uint16(v&0x0F)
			if c.P&0x01 != 0 {
				result++
			}
			if result > 0x09 {
				result += 0x06
			}

			carry := result > 0x0F
			result = uint16(a&0xF0) + uint16(v&0xF0) + (result & 0x0F)
			if carry {
				result += 0x10
			}

			overflow := (^(a ^ v) & (a ^ uint8(result)) & 0x80) != 0
			if result > 0x9F {
				result += 0x60
			}

			c.P &= 0xBE
			c.setNZ(uint8(result))
			if result > 0xFF {
				c.P |= 0x01
			}
			if overflow {
				c.P |= 0x40
			}
			c.A = (c.A & 0xFF00) | (result & 0xFF)
			return
		}

		result := uint16(a) + uint16(v)
		if c.P&0x01 != 0 {
			result++
		}
		c.P &= 0xBE
		c.setNZ(uint8(result))

		if result > 0xFF {
			c.P |= 0x01
		}

		if (^(a ^ v) & (a ^ uint8(result)) & 0x80) != 0 {
			c.P |= 0x40
		}

		c.A = (c.A & 0xFF00) | (result & 0xFF)
	}
}

func opSBC(c *CPU, mode AddressingMode) {
	size16 := !c.E && (c.P&0x20) == 0
	val := c.getLoadVal(mode, size16)

	if size16 {
		operand := ^val
		if c.P&0x08 != 0 {
			a := c.A
			result := int(a&0x000F) + int(operand&0x000F)
			if c.P&0x01 != 0 {
				result++
			}
			if result < 0x0010 {
				result -= 0x0006
			}

			carry := result > 0x000F
			result = int(a&0x00F0) + int(operand&0x00F0) + (result & 0x000F)
			if carry {
				result += 0x0010
			}
			if result < 0x0100 {
				result -= 0x0060
			}

			carry = result > 0x00FF
			result = int(a&0x0F00) + int(operand&0x0F00) + (result & 0x00FF)
			if carry {
				result += 0x0100
			}
			if result < 0x1000 {
				result -= 0x0600
			}

			carry = result > 0x0FFF
			result = int(a&0xF000) + int(operand&0xF000) + (result & 0x0FFF)
			if carry {
				result += 0x1000
			}

			overflow := ((a^operand)&0x8000) == 0 && ((a^uint16(result))&0x8000) != 0
			if result < 0x10000 {
				result -= 0x6000
			}

			c.P &= 0xBE
			c.setNZ16(uint16(result))
			if result > 0xFFFF {
				c.P |= 0x01
			}
			if overflow {
				c.P |= 0x40
			}
			c.A = uint16(result)
			return
		}

		result := uint32(c.A) + uint32(operand)
		if c.P&0x01 != 0 {
			result++
		}

		c.P &= 0xBE
		c.setNZ16(uint16(result))

		if result > 0xFFFF {
			c.P |= 0x01
		}

		if ((c.A ^ val) & (c.A ^ uint16(result)) & 0x8000) != 0 {
			c.P |= 0x40
		}

		c.A = uint16(result)
	} else {
		a := c.A & 0xFF
		v := val & 0xFF
		operand := v ^ 0xFF
		if c.P&0x08 != 0 {
			result := int(a&0x0F) + int(operand&0x0F)
			if c.P&0x01 != 0 {
				result++
			}
			if result < 0x10 {
				result -= 0x06
			}

			carry := result > 0x0F
			result = int(a&0xF0) + int(operand&0xF0) + (result & 0x0F)
			if carry {
				result += 0x10
			}

			overflow := ((uint8(a)^uint8(operand))&0x80) == 0 && ((uint8(a)^uint8(result))&0x80) != 0
			if result < 0x100 {
				result -= 0x60
			}

			c.P &= 0xBE
			c.setNZ(uint8(result))
			if result > 0xFF {
				c.P |= 0x01
			}
			if overflow {
				c.P |= 0x40
			}
			c.A = (c.A & 0xFF00) | uint16(result&0xFF)
			return
		}

		result := uint16(a) + uint16(operand)
		if c.P&0x01 != 0 {
			result++
		}

		c.P &= 0xBE
		c.setNZ(uint8(result))

		if result > 0xFF {
			c.P |= 0x01
		}

		if ((a ^ v) & (a ^ uint16(result)) & 0x80) != 0 {
			c.P |= 0x40
		}

		c.A = (c.A & 0xFF00) | (result & 0xFF)
	}
}
