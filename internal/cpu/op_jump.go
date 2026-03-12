package cpu

func opJMP_Abs(c *CPU, mode AddressingMode) {
	// 4C: JMP Absolute (16-bit target in current bank)
	addr := c.fetchWord()
	c.PC = addr
}

func opJML_Abs(c *CPU, mode AddressingMode) {
	// 5C: JMP/JML Absolute Long (24-bit target)
	addr := c.fetchWord()
	bank := c.fetchByte()
	c.PC = addr
	c.PB = bank
}

func opJMP_Ind(c *CPU, mode AddressingMode) {
	// 6C: JMP (a) Absolute Indirect (16-bit pointer in Bank 0)
	ptr := c.fetchWord()
	// Read new PC from 00:ptr
	low := c.read(uint32(ptr))
	high := c.read(uint32(ptr) + 1)
	c.PC = uint16(high)<<8 | uint16(low)
}

func opJMP_IndX(c *CPU, mode AddressingMode) {
	// 7C: JMP (a,x) Absolute Indexed Indirect (16-bit pointer in Bank PB)
	// Pointer Address = Operand + X
	base := c.fetchWord()
	ptr := base + c.X
	// Read new PC from PB:ptr
	msgAddr := uint32(c.PB)<<16 | uint32(ptr)
	low := c.read(msgAddr)
	// ptr+1 wrapping? 65816 wraps within bank.
	// If ptr is FFFF, does it wrap to 0000 in same bank? Yes.
	// So (ptr+1) & 0xFFFF.
	high := c.read((msgAddr & 0xFF0000) | uint32((ptr+1)&0xFFFF))
	c.PC = uint16(high)<<8 | uint16(low)
}

func opJML_Ind(c *CPU, mode AddressingMode) {
	// DC: JMP/JML [a] (16-bit pointer to 24-bit target)
	ptr := c.fetchWord() // Address in Bank 0
	// Read 3 bytes from 00:ptr
	// Note: Wrapping behavior? Standard 65816 wraps at bank boundary?
	// Indirect Long reads from Bank 0.
	targetLow := c.read(uint32(ptr))
	targetHigh := c.read(uint32(ptr) + 1)
	targetBank := c.read(uint32(ptr) + 2)
	c.PC = uint16(targetHigh)<<8 | uint16(targetLow)
	c.PB = targetBank
}

func opJSL(c *CPU, mode AddressingMode) {
	// 22: JSL Absolute Long
	targetPC := c.fetchWord()
	c.pushByteRaw(c.PB)
	targetPB := c.fetchByte()
	returnPC := c.PC - 1
	c.pushByteRaw(uint8(returnPC >> 8))
	c.pushByteRaw(uint8(returnPC))
	c.normalizeEmulationStack()

	c.PC = targetPC
	c.PB = targetPB
}

func opRTL(c *CPU, mode AddressingMode) {
	// 6B: RTL
	// Pull PCL, PCH, K.
	// PC = PulledPC + 1
	low := c.popByteRaw()
	high := c.popByteRaw()
	pulledPC := uint16(high)<<8 | uint16(low)
	pulledKB := c.popByteRaw()
	c.normalizeEmulationStack()
	c.PC = pulledPC + 1
	c.PB = pulledKB
}

func opJSR(c *CPU, mode AddressingMode) {
	// 20: JSR Absolute
	addr := c.fetchWord()
	// Push return PC (last byte of instruction)
	// PC is at next op.
	returnPC := c.PC - 1
	c.pushWord(returnPC)
	c.PC = addr
}

func opRTS(c *CPU, mode AddressingMode) {
	// 60: RTS
	// Pull PC, PC = PC + 1
	pulledPC := c.popWord()
	c.PC = pulledPC + 1
}

func opJSR_IndX(c *CPU, mode AddressingMode) {
	// FC: JSR (a,x) Absolute Indexed Indirect
	// Push PC (current PC + 2, pointing to last byte of JSR instruction)
	// Targeted Address = (a+x)
	// But it reads address from there.

	base := c.fetchWord()
	ptr := base + c.X

	// Read target address from PB:ptr
	msgAddr := uint32(c.PB)<<16 | uint32(ptr)
	low := c.read(msgAddr)
	// Wrap within bank? Yes?
	high := c.read((msgAddr & 0xFF0000) | uint32((ptr+1)&0xFFFF))
	target := uint16(high)<<8 | uint16(low)

	returnPC := c.PC - 1
	c.pushWord(returnPC)

	c.PC = target
}
