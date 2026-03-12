package cpu

const (
	// Native Mode Vectors (E=0)
	VectorNativeCOP   = 0xFFE4
	VectorNativeBRK   = 0xFFE6
	VectorNativeABORT = 0xFFE8
	VectorNativeNMI   = 0xFFEA
	VectorNativeRESET = 0xFFFC // Unused effectively?
	VectorNativeIRQ   = 0xFFEE

	// Emulation Mode Vectors (E=1)
	VectorEmulationCOP   = 0xFFF4
	VectorEmulationABORT = 0xFFF8
	VectorEmulationNMI   = 0xFFFA
	VectorEmulationRESET = 0xFFFC
	VectorEmulationIRQ   = 0xFFFE // BRK shares IRQ vector in Emulation
)

// Interrupt triggers a hardware interrupt.
func (c *CPU) Interrupt(vector uint16) {
	// 1. Consume Interrupt Cycles (usually 7-8? Internal overhead)
	// We'll add a fixed amount? Or handled by reads/writes.
	// Spec: IRQ/NMI takes ~7-8 bytes worth of cycles?
	// Let's assume reads/writes account for most, plus internal.

	// 2. Push Stream
	if !c.E {
		// Native Mode: Push PBR (Program Bank)
		c.pushByte(c.PB)
	}

	// Push PC
	c.pushWord(c.PC)

	// Push Status Register
	c.pushByte(c.P)

	// 3. Set Flags
	c.P |= 0x04 // Set I (Interrupt Disable)
	c.P &= 0xF7 // Clear D (Decimal Mode)

	// 4. Updates
	c.PB = 0x00 // Interrupts always jump to Bank 0

	// 5. Fetch Vector
	// Vector is in Bank 0? Always.
	low := c.read(uint32(vector))
	high := c.read(uint32(vector + 1))
	c.PC = uint16(high)<<8 | uint16(low)
	// fmt.Printf("CPU Interrupt Vector %04X -> PC %04X (Mode E=%v)\n", vector, c.PC, c.E)

	// Cycles? Fetch takes cycles.
}

func opRTI(c *CPU, mode AddressingMode) {
	// 40: RTI
	// Pull P, PC, [PB]
	c.P = c.popByte()
	c.updateMXFlags()

	c.PC = c.popWord()

	if !c.E {
		c.PB = c.popByte()
	}
}

func opBRK(c *CPU, mode AddressingMode) {
	// 00: BRK (Software Interrupt)
	// 2 bytes: Opcode, Signature
	c.fetchByte() // Consume signature byte

	if c.E {
		// Emulation Mode BRK
		// Push PC, P (with B flag set) (PB not pushed)
		c.pushWord(c.PC)
		c.pushByte(c.P | 0x10) // Set B flag on stack
		c.P |= 0x04            // Set I
		c.P &= 0xF7            // Clear D
		c.PB = 0
		c.PC = c.readWord(0xFFFE)
	} else {
		// Native Mode BRK
		// Push PB, PC, P
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
		c.P |= 0x04 // Set I
		c.P &= 0xF7 // Clear D
		c.PB = 0
		c.PC = c.readWord(0xFFE6)
	}
}

func opCOP(c *CPU, mode AddressingMode) {
	// 02: COP (Coprocessor Empowerment)
	// 2 bytes: Opcode, Signature
	c.fetchByte() // Signature

	if c.E {
		// Emulation Mode
		c.pushWord(c.PC)
		c.pushByte(c.P)
		// Native/Emulation vector difference.
		c.P |= 0x04 // Set I
		c.P &= 0xF7 // Clear D
		c.PB = 0
		c.PC = c.readWord(0xFFF4)
	} else {
		// Native Mode
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
		c.P |= 0x04
		c.P &= 0xF7
		c.PB = 0
		c.PC = c.readWord(0xFFE4)
	}
}
