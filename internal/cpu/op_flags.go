package cpu

func opSEI(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.P |= 0x04 // Set Interrupt Disable
}

func opCLI(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.P &= 0xFB // Clear Interrupt Disable
}

func opCLD(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.P &= 0xF7 // Clear Decimal
}

func opSED(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.P |= 0x08 // Set Decimal
}

func opCLV(c *CPU, mode AddressingMode) {
	c.Idle(6)
	c.P &= 0xBF // Clear Overflow
}
