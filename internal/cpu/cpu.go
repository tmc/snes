package cpu

import (
	"fmt"

	"github.com/tmc/snes/internal/bus"
)

// CPU represents the Ricoh 5A22 (WDC 65816 based) processor.
type CPU struct {
	// Registers
	A  uint16 // Accumulator
	X  uint16 // Index X
	Y  uint16 // Index Y
	S  uint16 // Stack Pointer
	PC uint16 // Program Counter
	D  uint16 // Direct Page Register

	DB uint8 // Data Bank
	PB uint8 // Program Bank
	P  uint8 // Status Flags (NVMXDIZC)

	E bool // Emulation Mode

	// Interrupt State
	NMIPending bool
	IRQPending bool

	// Math State
	MultiplicandA        uint8
	Dividend             uint16
	Divisor              uint8
	Quotient             uint16
	MultiplicationResult uint16 // Shared for Product and Remainder

	// Internal State
	Cycles     uint64
	TraceCount int // Debug trace countdown
	Stopped    bool
	Waiting    bool

	Bus *bus.Bus
}

func NewCPU(b *bus.Bus) *CPU {
	return &CPU{
		Bus: b,
		E:   true, // Standard 65c816 reset state is Emulation Mode
		D:   0,
	}
}

func (c *CPU) Run() {
	// DEBUG: Trace P changes

	if c.NMIPending {
		c.doNMI()
		return
	}

	if c.IRQPending {
		if (c.P & 0x04) == 0 {
			// fmt.Println("DEBUG: Executing IRQ")
			c.doIRQ()
			return
		}
	}

	// Check IRQ
	if c.IRQPending && (c.P&0x04) == 0 {
		c.doIRQ()
		return
	}

	if c.Waiting {
		c.Cycles += 6 // Consume cycles while waiting
		return
	}
	if c.Stopped {
		c.Cycles += 2
		return
	}

	// Fetch Opcode
	opcodeByte := c.fetchByte()

	opcode := Opcodes[opcodeByte]

	// Consume Cycles (Opcode fetch + execution)
	// Cycles are now consumed by fetchByte/read/write calls implicitly.
	// We might need minimal internal overhead cycles for specific opcodes later.

	// Execute

	if opcode.Op != nil {
		opcode.Op(c, opcode.Mode)
	} else {
		panic(fmt.Sprintf("CPU Panic: Invalid/Unimplemented Opcode 0x%02X at %02X:%04X", opcodeByte, c.PB, c.PC-1))
	}

}

// Step executes one instruction.
func (c *CPU) Step() {
	c.Run()
}

// fetchByte reads a byte from PC and increments PC. It consumes cycles.
func (c *CPU) fetchByte() uint8 {
	addr := uint32(c.PB)<<16 | uint32(c.PC)
	val := c.read(addr)
	// fmt.Printf("DEBUG: fetchByte PC=%04X Val=%02X\n", c.PC, val)
	// DEBUG: Trace Boot Flow
	// if c.PC&0x8000 != 0 {
	// 	fmt.Printf("CPU %06X: %02X A:%04X X:%04X Y:%04X S:%04X\n", addr, val, c.A, c.X, c.Y, c.S)
	// }

	c.PC++
	return val
}

// read handles cycle counting and bus access
func (c *CPU) read(addr uint32) uint8 {
	addr &= 0xFFFFFF
	// Wait states handled in Bus
	c.Cycles += c.Bus.GetWaitStates(addr)
	val := c.Bus.Read(addr)
	if (c.PC&0xFFFF) >= 0x88A0 && (c.PC&0xFFFF) <= 0x8900 {
		fmt.Printf("CPU Read BootLoop: %02X (PC=%04X) (Addr=%06X)\n", val, c.PC, addr)
	}
	if (addr >> 16) == 0x19 {
		fmt.Printf("CPU Read Bank 19: %02X (PC=%04X) (Addr=%06X)\n", val, c.PC, addr)
	}

	return val
}

// write handles cycle counting and bus access
func (c *CPU) write(addr uint32, val uint8) {
	addr &= 0xFFFFFF
	// Wait states handled in Bus
	if (addr&0xFFFF) == 0x2140 || (addr&0xFFFF) == 0x2141 {
		fmt.Printf("CPU Write $%04X: %02X (Addr=%06X)\n", addr&0xFFFF, val, addr)
	}
	if addr < 0x10 {
		fmt.Printf("CPU Write ZP $%02X: %02X\n", addr, val)
	}
	if addr == 0x420B {
		fmt.Printf("HDMA Enable Write: %02X\n", val)
	}
	if addr == 0x4200 {
		fmt.Printf("NMI Enable Write: %02X\n", val)
	}
	c.Bus.Write(addr, val)
}

func (c *CPU) readWord(addr uint32) uint16 {
	low := c.read(addr)
	high := c.read((addr + 1) & 0xFFFFFF)
	return uint16(low) | (uint16(high) << 8)
}

func (c *CPU) ResetCycles() {
	c.Cycles = 0
}

func (c *CPU) GetCycles() uint64 {
	return c.Cycles
}

func (c *CPU) Frequency() uint64 {
	return 21477272 // 21.477 MHz
}

func (c *CPU) Power(reset bool) {
	c.E = true
	c.D = 0x0000
	c.PB = 0x00
	c.DB = 0

	c.S = 0x01FF // Typical init, though hardware random
	c.P = 0x34   // IRQ disable, Index/Accumulator 8-bit (if hidden bits set)
	// In E mode, X/Y are not necessarily 8-bit but treated as such.
	// Standard status: m=1, x=1, i=1

	low := c.read(0xFFFC)
	high := c.read(0xFFFD)
	c.PC = uint16(high)<<8 | uint16(low)
}

func (c *CPU) TriggerNMI() {
	// DEBUG NMI
	// fmt.Println("CPU: TriggerNMI")
	c.NMIPending = true
}

func (c *CPU) TriggerIRQ() {
	c.IRQPending = true
}

func (c *CPU) doNMI() {
	fmt.Println("CPU: doNMI Execution")
	c.NMIPending = false
	fmt.Println("DEBUG: CPU NMI Triggered!")
	c.Waiting = false // Wake up WAI

	// Cycles: 7 (Native) / 8?
	// NMI Logic:
	// Push PB (if Native), PC, P.

	if c.E {
		// Emulation Mode (6502 style)
		// Push PC (16-bit), P (8-bit)
		c.pushWord(c.PC)
		c.pushByte(c.P) // Break flag? No. B bit is virtual.

		// Vector FFFA
		low := c.read(0xFFFA)
		high := c.read(0xFFFB)
		c.PC = uint16(high)<<8 | uint16(low)
		c.PB = 0 // Reset PB to 0 in Emulation? Usually.
	} else {
		// Native Mode
		// Push PB, PC, P
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
	}

	var vector uint16
	if c.E {
		vector = c.readWord(0xFFFA)
	} else {
		vector = c.readWord(0xFFEA)
	}
	fmt.Printf("DEBUG: NMI Jump -> Vector=%04X (E=%v) from PC=%02X:%04X\n", vector, c.E, c.PB, c.PC)
	c.PC = vector
	c.PB = 0x00
	c.NMIPending = false

	c.TraceCount = 5000 // Trace next 5000 instructions

	c.D = 0     // Clear Decimal
	c.P |= 0x04 // Set IRQ Disable (I)
	// Cycles consumed during pushes/reads.
}

func (c *CPU) doIRQ() {
	c.IRQPending = false // Level triggered? Usually level. But we'll clear for now.
	// fmt.Println("DEBUG: CPU IRQ Triggered!")
	c.Waiting = false

	c.AddCycles(8) // Approximate

	if c.E {
		c.pushWord(c.PC)
		c.pushByte(c.P)
		c.PC = c.readWord(0xFFFE)
		c.PB = 0
	} else {
		c.pushByte(c.PB)
		c.pushWord(c.PC)
		c.pushByte(c.P)
		c.PC = c.readWord(0xFFEE)
		c.PB = 0x00
	}

	c.D = 0
	c.P |= 0x04 // Set I
}

// AddCycles increments the cycle counter (e.g. from DMA).
func (c *CPU) AddCycles(cycles uint64) {
	c.Cycles += cycles
}
