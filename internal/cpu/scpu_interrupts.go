package cpu

// S-CPU hardware-line interrupts. NMI ($FFFA/$FFEA), IRQ ($FFFE/
// $FFEE), and RESET ($FFFC/$FFFD) sequences are S-CPU-specific —
// they hardcode the S-CPU's interrupt vectors and the RESET cycle
// accounting (170 cycles via AddCycles). The fields driving this
// state (NMIPending, IRQPending, Waiting) remain on *CPU; only the
// methods live here so the file boundary reflects the future
// Core/SA-1 split. A future SA-1 CPU instance will have its own
// hardware-interrupt file (sa1_interrupts.go) consuming CRV/CNV/CIV
// at $2203-$2208 and a different RESET cycle profile.
//
// Opcode-driven interrupt entries (BRK, COP, RTI) and the generic
// Interrupt(vector) helper plus vector-address constants live in
// interrupts.go because those are 65816-core mechanics shared
// between the S-CPU and a future SA-1 CPU.

func (c *CPU) Power(reset bool) {
	c.executing = true
	defer func() { c.executing = false }()
	c.Stopped = false
	c.Waiting = false
	c.Fault = nil
	c.NMIPending = false
	c.IRQPending = false
	c.Cycles = 0
	c.DRAMRefreshLine = 0
	c.DRAMRefreshScanline = 0
	c.DRAMRefreshLineStart = 0
	c.DRAMRefreshPosition = 0
	c.dramRefreshNext = 0
	c.E = true
	c.D = 0x0000
	c.PB = 0x00
	c.DB = 0

	c.S = 0x01FF // Typical init, though hardware random
	c.P = 0x34   // IRQ disable, Index/Accumulator 8-bit (if hidden bits set)
	// In E mode, X/Y are not necessarily 8-bit but treated as such.
	// Standard status: m=1, x=1, i=1

	c.AddCycles(170)
	low := c.read(0xFFFC)
	high := c.read(0xFFFD)
	c.PC = uint16(high)<<8 | uint16(low)
	// Reset enters through the interrupt sequence, consuming PC/P stack slots.
	c.S = 0x01FC
	c.obsActive = false
	if c.observer != nil {
		c.observeTransition(TransitionReset, Snapshot{}, 0xFFFC, false)
	}
}

func (c *CPU) TriggerNMI() {
	// DEBUG NMI
	// fmt.Println("CPU: TriggerNMI")
	c.NMIPending = true
}

func (c *CPU) TriggerIRQ() {
	c.IRQPending = true
}

func (c *CPU) ClearIRQ() {
	c.IRQPending = false
}

func (c *CPU) doNMI() {
	if c.InterruptHook != nil {
		c.InterruptHook("nmi")
	}
	var before Snapshot
	fromWait := c.Waiting
	if c.observer != nil {
		before = c.Snapshot()
	}
	c.NMIPending = false
	c.Waiting = false // Wake up WAI

	c.Idle(8)

	if c.E {
		// Emulation Mode (6502 style)
		// Push PC (16-bit), P (8-bit)
		c.pushWord(c.PC)
		c.pushByte(c.P) // Break flag? No. B bit is virtual.
	} else {
		// Native Mode
		// Push PB, PC, P
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
	}

	vectorAddr := uint32(0xFFEA)
	if c.E {
		vectorAddr = 0xFFFA
	}
	c.PC = c.readWord(vectorAddr)
	c.PB = 0x00
	c.NMIPending = false

	c.TraceCount = 5000 // Trace next 5000 instructions

	c.P &^= 0x08 // Clear Decimal mode flag
	c.P |= 0x04  // Set IRQ Disable (I)
	if c.observer != nil {
		c.observeTransition(TransitionNMI, before, vectorAddr, fromWait)
	}
}

func (c *CPU) doIRQ() {
	if c.InterruptHook != nil {
		c.InterruptHook("irq")
	}
	var before Snapshot
	fromWait := c.Waiting
	if c.observer != nil {
		before = c.Snapshot()
	}
	c.IRQPending = false // Level triggered? Usually level. But we'll clear for now.
	// fmt.Println("DEBUG: CPU IRQ Triggered!")
	c.Waiting = false

	c.Idle(8) // Approximate

	vectorAddr := uint32(0xFFEE)
	if c.E {
		vectorAddr = 0xFFFE
		c.pushWord(c.PC)
		c.pushByte(c.P)
	} else {
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
	}
	c.PC = c.readWord(vectorAddr)
	c.PB = 0

	c.P &^= 0x08 // Clear Decimal mode flag
	c.P |= 0x04  // Set I
	if c.observer != nil {
		c.observeTransition(TransitionIRQ, before, vectorAddr, fromWait)
	}
}
