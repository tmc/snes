package spc700

// SPCState captures the serializable SPC700 state.
type SPCState struct {
	PC uint16
	A  uint8
	X  uint8
	Y  uint8
	SP uint8

	N bool
	V bool
	P bool
	B bool
	H bool
	I bool
	Z bool
	C bool

	Cycles  uint64
	Stopped bool
}

// SaveState returns a snapshot of the SPC700 state.
func (c *SPC700) SaveState() SPCState {
	return SPCState{
		PC:      c.PC,
		A:       c.A,
		X:       c.X,
		Y:       c.Y,
		SP:      c.SP,
		N:       c.N,
		V:       c.V,
		P:       c.P,
		B:       c.B,
		H:       c.H,
		I:       c.I,
		Z:       c.Z,
		C:       c.C,
		Cycles:  c.Cycles,
		Stopped: c.Stopped,
	}
}

// LoadState restores a previously saved SPC700 state.
func (c *SPC700) LoadState(state SPCState) {
	c.PC = state.PC
	c.A = state.A
	c.X = state.X
	c.Y = state.Y
	c.SP = state.SP
	c.N = state.N
	c.V = state.V
	c.P = state.P
	c.B = state.B
	c.H = state.H
	c.I = state.I
	c.Z = state.Z
	c.C = state.C
	c.Cycles = state.Cycles
	c.Stopped = state.Stopped
}
