package cpu

// Block Move Instructions

func blockMoveAdjustIndex(c *CPU, value uint16, adjust int) uint16 {
	if c.E || (c.P&0x10) != 0 {
		low := uint8(value)
		if adjust < 0 {
			low--
		} else {
			low++
		}
		return uint16(low)
	}

	if adjust < 0 {
		return value - 1
	}
	return value + 1
}

func opMVN(c *CPU, mode AddressingMode) {
	// MVN DestBank, SrcBank
	// Moves a block of memory from SrcBank:X to DestBank:Y.
	// As bytes are moved, X and Y are incremented. C (Accumulator) is decremented.
	// The process repeats until C is FFFF (underflow from 0).
	// DB is set to DestBank.

	// Operands are fetched by the loop/logic or prepared?
	// The format is `54 dd ss`.
	// fetchByte() called by run got 0x54. PC advanced 1.
	// We need 2 operands.
	destBank := c.fetchByte()
	srcBank := c.fetchByte()

	c.DB = destBank // DB updated to destination bank

	// Perform Move
	// Note: In a real cycle-accurate emulator, we wouldn't loop all at once.
	// We would execute one byte move, decrement C, and if C != FFFF, set PC back to opcode.
	// However, we are "instruction steps" based for now.
	// WARNING: Interrupts can happen during block moves.
	// If we loop here, we freeze the CPU for potentially 65536 * 7 cycles.
	// This is bad for audio/video sync.
	// BUT, our `Run()` is a step.
	// The canonical way is: Perform ONE byte move. Update registers.
	// Check if C != 0xFFFF. If so, set PC = PC - 3. (Re-execute instruction).
	// This allows interrupts to be serviced between bytes.

	// Address Logic
	srcAddr := uint32(srcBank)<<16 | uint32(c.X)
	destAddr := uint32(destBank)<<16 | uint32(c.Y)

	val := c.read(srcAddr)
	c.write(destAddr, val)

	// Increment specific to MVN
	c.X = blockMoveAdjustIndex(c, c.X, +1)
	c.Y = blockMoveAdjustIndex(c, c.Y, +1)
	c.A-- // A is C (full 16-bit accumulator)

	if c.A != 0xFFFF {
		c.PC -= 3
	}
}

func opMVP(c *CPU, mode AddressingMode) {
	// MVP DestBank, SrcBank
	// Moves from SrcBank:X to DestBank:Y. Decrement X/Y. Decrement C.
	destBank := c.fetchByte()
	srcBank := c.fetchByte()

	c.DB = destBank

	srcAddr := uint32(srcBank)<<16 | uint32(c.X)
	destAddr := uint32(destBank)<<16 | uint32(c.Y)

	val := c.read(srcAddr)
	c.write(destAddr, val)

	c.X = blockMoveAdjustIndex(c, c.X, -1)
	c.Y = blockMoveAdjustIndex(c, c.Y, -1)
	c.A--

	if c.A != 0xFFFF {
		c.PC -= 3
	}
}
