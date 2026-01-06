package dsp

// DSP represents the Sony SPC700 Digital Signal Processor.
// It manages 8 voices, echo, and main volume.
type DSP struct {
	RAM [128]uint8 // Internal registers (0x00-0x7F)
}

func New() *DSP {
	return &DSP{}
}

// Read returns the value of a DSP register.
func (d *DSP) Read(addr uint8) uint8 {
	return d.RAM[addr&0x7F]
}

// Write sets the value of a DSP register.
func (d *DSP) Write(addr uint8, val uint8) {
	d.RAM[addr&0x7F] = val
	// TODO: Handle register specific logic (Voice parameters, KON, KOFF, etc.)
}
