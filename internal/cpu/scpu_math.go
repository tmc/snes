package cpu

// S-CPU $4202-$4206 multiply/divide ALU. The 5A22 multiplier is driven
// by writes to MultiplicandA ($4202) and the multiplier byte ($4203);
// it produces one bit per CPU cycle, so mathALUEdge is called from the
// bus read/write path. A future SA-1 CPU sharing the WDC65816 core
// leaves MultiplyCounter at zero and these methods become no-ops.

func (c *CPU) StartMultiply(multiplier uint8) {
	c.MultiplicationResult = 0
	if c.MultiplyCounter != 0 {
		return
	}
	c.Quotient = uint16(multiplier)<<8 | uint16(c.MultiplicandA)
	c.MultiplyDividend = c.Quotient
	c.MultiplyShift = uint16(multiplier)
	c.MultiplyCounter = 8
	c.PendingProduct = 0
	c.ProductReadyCycle = 0
}

func (c *CPU) mathALUEdge() {
	if c.MultiplyCounter == 0 {
		return
	}
	c.MultiplyCounter--
	if c.Quotient&1 != 0 {
		c.MultiplicationResult += c.MultiplyShift
	}
	c.Quotient >>= 1
	c.MultiplyDividend = c.Quotient
	c.MultiplyShift <<= 1
}
