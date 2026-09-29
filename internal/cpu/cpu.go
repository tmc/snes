package cpu

import (
	"fmt"
	"log"
	"os"

	"github.com/tmc/snes/internal/bus"
)

var traceBoot = os.Getenv("SNES_TRACE_BOOT") != ""

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
	PendingProduct       uint16
	ProductReadyCycle    uint64
	MultiplyCounter      uint8
	MultiplyDividend     uint16
	MultiplyShift        uint16
	DRAMRefreshLine      uint64
	DRAMRefreshScanline  uint64
	DRAMRefreshLineStart uint64
	DRAMRefreshPosition  uint64

	// dramRefreshNext is the earliest clock at which maybeDRAMRefresh
	// can act, or 0 if unknown.
	dramRefreshNext uint64

	// Internal State
	Cycles     uint64
	TraceCount int // Debug trace countdown
	Stopped    bool
	Waiting    bool
	Fault      error

	LastOpcode   uint8
	LastOpcodePB uint8
	LastOpcodePC uint16

	Bus BusIO

	// BusEdge runs before each CPU bus or internal cycle. The host may suspend
	// this call while DMA owns the bus. ClockAdvanced observes elapsed clocks.
	BusEdge       func(clocks uint64)
	ClockAdvanced func()
	executing     bool

	// BeforeExecute is a diagnostic hook after opcode fetch.
	BeforeExecute func()

	// AfterExecute, if non-nil, is called after a decoded instruction finishes.
	// It is intended for diagnostics that need both start and successor PCs.
	AfterExecute func()

	// InterruptHook, if non-nil, is called immediately before NMI or IRQ
	// vector entry mutates PC/PB/P/stack state.
	InterruptHook func(kind string)

	// config parameterizes host-system specifics (clock frequency,
	// MDR-restore window, DRAM-refresh enable). NewCPU supplies
	// DefaultSCPUConfig; NewCPUWithConfig overrides for non-S-CPU
	// instances. See cpu_config.go.
	config CPUConfig
}

// NewCPU constructs the S-CPU instance using DefaultSCPUConfig.
// This wrapper is the permanent default-S-CPU entry point; new
// non-S-CPU instances should call NewCPUWithConfig directly.
func NewCPU(b *bus.Bus) *CPU {
	return NewCPUWithConfig(b, DefaultSCPUConfig)
}

func (c *CPU) Run() {
	c.executing = true
	defer func() { c.executing = false }()
	if c.Fault != nil {
		c.Cycles += 2
		return
	}

	if c.E {
		c.P |= 0x30
		c.S = 0x0100 | (c.S & 0x00FF)
	}

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

	if c.Waiting && c.IRQPending {
		c.Waiting = false
	}

	if c.Waiting {
		c.Idle(6) // Consume cycles while waiting
		return
	}
	if c.Stopped {
		c.Cycles += 2
		return
	}

	// Fetch Opcode
	opcodeByte := c.fetchByte()
	c.LastOpcode = opcodeByte
	c.LastOpcodePB = c.PB
	c.LastOpcodePC = c.PC - 1
	if traceBoot && c.PB == 0x00 && c.PC >= 0x8888 && c.PC <= 0x8905 {
		log.Printf("cpu pc=%04X op=%02X a=%04X x=%04X y=%04X d=%04X s=%04X p=%02X db=%02X",
			c.PC-1, opcodeByte, c.A, c.X, c.Y, c.D, c.S, c.P, c.DB)
	}

	opcode := Opcodes[opcodeByte]
	if c.BeforeExecute != nil {
		c.BeforeExecute()
	}

	// Consume Cycles (Opcode fetch + execution)
	// Cycles are now consumed by fetchByte/read/write calls implicitly.
	// We might need minimal internal overhead cycles for specific opcodes later.

	// Execute

	if opcode.Op != nil {
		opcode.Op(c, opcode.Mode)
	} else {
		c.setFaultf("invalid or unimplemented opcode %02X at %02X:%04X", opcodeByte, c.PB, c.PC-1)
	}
	if c.AfterExecute != nil {
		c.AfterExecute()
	}

}

func (c *CPU) setFaultf(format string, args ...any) {
	if c.Fault != nil {
		return
	}
	c.Fault = fmt.Errorf(format, args...)
	c.Stopped = true
	c.Waiting = false
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
	wait := c.Bus.GetWaitStates(addr)
	c.busEdge(wait)
	if wait > 4 {
		c.addBusCycles(wait - 4)
	}
	mdr := c.Bus.MDR()
	val := c.Bus.Read(addr)
	if c.config.MDRRestoreMask != 0 && addr&c.config.MDRRestoreMask == c.config.MDRRestoreVal {
		c.Bus.SetMDR(mdr)
	}
	if wait >= 4 {
		c.addBusCycles(4)
	} else {
		c.addBusCycles(wait)
	}
	c.mathALUEdge()
	return val
}

// write handles cycle counting and bus access
func (c *CPU) write(addr uint32, val uint8) {
	addr &= 0xFFFFFF
	c.mathALUEdge()
	wait := c.Bus.GetWaitStates(addr)
	c.busEdge(wait)
	c.addBusCycles(wait)
	c.Bus.Write(addr, val)
}

func (c *CPU) addBusCycles(cycles uint64) {
	c.Cycles += cycles
	if c.config.DRAMRefreshEnabled {
		c.maybeDRAMRefresh()
	}
	if c.ClockAdvanced != nil {
		c.ClockAdvanced()
	}
}

func (c *CPU) busEdge(clocks uint64) {
	if c.BusEdge != nil {
		c.BusEdge(clocks)
	}
}

// Executing reports whether an instruction's Go continuation is active.
func (c *CPU) Executing() bool { return c.executing }

// AdvanceDMA advances clocks without a CPU bus edge or ordinary ALU edge.
func (c *CPU) AdvanceDMA(clocks uint64) { c.addBusCycles(clocks) }

// Idle executes internal CPU cycles, each of which can yield the bus to DMA.
func (c *CPU) Idle(clocks uint64) {
	for clocks != 0 {
		n := clocks
		if n > 8 {
			n = 6
		}
		c.busEdge(n)
		c.addBusCycles(n)
		c.mathALUEdge()
		clocks -= n
	}
}

func (c *CPU) readWord(addr uint32) uint16 {
	low := c.read(addr)
	high := c.read((addr + 1) & 0xFFFFFF)
	return uint16(low) | (uint16(high) << 8)
}

func (c *CPU) ResetCycles() {
	c.Cycles = 0
	c.DRAMRefreshLine = 0
	c.DRAMRefreshScanline = 0
	c.DRAMRefreshLineStart = 0
	c.DRAMRefreshPosition = 0
	c.dramRefreshNext = 0
}

func (c *CPU) GetCycles() uint64 {
	return c.Cycles
}

func (c *CPU) Frequency() uint64 {
	return c.config.FrequencyHz
}

// AddCycles increments the cycle counter (e.g. from DMA).
func (c *CPU) AddCycles(cycles uint64) {
	c.Cycles += cycles
	for ; cycles >= 6; cycles -= 6 {
		c.mathALUEdge()
	}
	if c.config.DRAMRefreshEnabled {
		c.maybeDRAMRefresh()
	}
}
