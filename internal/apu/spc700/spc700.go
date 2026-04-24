package spc700

// Bus defines the memory interface for the SPC700.
// The SPC700 has a 16-bit address space (64KB).
type Bus interface {
	Read(addr uint16) uint8
	Write(addr uint16, val uint8)
	// Idle/Cycles? SPC700 handles its own cycle counting usually?
	// Or Bus returns wait states? SPC700 shouldn't have wait states on RAM, only maybe DSP/Ports.
}

type SPC700 struct {
	// Registers
	PC uint16 // Program Counter
	A  uint8  // Accumulator
	X  uint8  // Index X
	Y  uint8  // Index Y
	SP uint8  // Stack Pointer
	// PSW uint8  // Removed in favor of individual flags

	// Flags (matching standard 6502/SPC700 naming)
	// N V P B H I Z C
	N, V, P, B, H, I, Z, C bool

	// Internal
	Cycles  uint64
	Stopped bool

	bus Bus
}

func New(bus Bus) *SPC700 {
	return &SPC700{
		bus: bus,
	}
}

func (c *SPC700) Reset() {
	c.PC = 0xFFC0 // Reset vector usually points to IPL ROM
	c.SP = 0xEF   // Stack starts below page 1? Or 0xFF?
	c.A = 0
	c.X = 0
	c.Y = 0

	// Flags
	c.N = false
	c.V = false
	c.P = false
	c.B = false
	c.H = false
	c.I = false
	c.Z = false
	c.C = false
}

// GetPSW packs flags into a byte
func (c *SPC700) GetPSW() uint8 {
	var val uint8
	if c.N {
		val |= 0x80
	}
	if c.V {
		val |= 0x40
	}
	if c.P {
		val |= 0x20
	}
	if c.B {
		val |= 0x10
	}
	if c.H {
		val |= 0x08
	}
	if c.I {
		val |= 0x04
	}
	if c.Z {
		val |= 0x02
	}
	if c.C {
		val |= 0x01
	}
	return val
}

// SetPSW unpacks a byte into flags
func (c *SPC700) SetPSW(val uint8) {
	c.N = val&0x80 != 0
	c.V = val&0x40 != 0
	c.P = val&0x20 != 0
	c.B = val&0x10 != 0
	c.H = val&0x08 != 0
	c.I = val&0x04 != 0
	c.Z = val&0x02 != 0
	c.C = val&0x01 != 0
}

func (c *SPC700) SetZN(val uint8) {
	c.Z = val == 0
	c.N = val&0x80 != 0
}

// Flags
const (
	FlagC = 1 << 0 // Carry
	FlagZ = 1 << 1 // Zero
	FlagI = 1 << 2 // Interrupt Disable
	FlagH = 1 << 3 // Half Carry
	FlagB = 1 << 4 // Break
	FlagP = 1 << 5 // Direct Page (0 or 1)
	FlagV = 1 << 6 // Overflow
	FlagN = 1 << 7 // Negative
)
