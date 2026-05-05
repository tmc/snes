package ppu

// PPUState captures the serializable PPU state.
type PPUState struct {
	VRAM  []byte
	OAM   []byte
	CGRAM []byte

	Registers PPURegisters

	FrontBuffer []uint16
	Width       int
	Height      int

	Cycles      uint64
	FrameCount  int
	HCounter    int
	VCounter    int
	NMIFlag     bool
	RangeOver   bool
	TimeOver    bool
	LatchedH    uint16
	LatchedV    uint16
	M7PairValid [0x22]bool
	HReadHigh   bool
	VReadHigh   bool
	HVLatched   bool
}

// SaveState returns a snapshot of the PPU state.
func (p *PPU) SaveState() PPUState {
	return PPUState{
		VRAM:        append([]byte(nil), p.VRAM[:]...),
		OAM:         append([]byte(nil), p.OAM[:]...),
		CGRAM:       append([]byte(nil), p.CGRAM[:]...),
		Registers:   p.PPURegisters,
		FrontBuffer: append([]uint16(nil), p.FrontBuffer...),
		Width:       p.Width,
		Height:      p.Height,
		Cycles:      p.cycles,
		FrameCount:  p.FrameCount,
		HCounter:    p.hCounter,
		VCounter:    p.vCounter,
		NMIFlag:     p.NMIFlag,
		RangeOver:   p.RangeOver,
		TimeOver:    p.TimeOver,
		LatchedH:    p.latchedH,
		LatchedV:    p.latchedV,
		M7PairValid: p.m7PairValid,
		HReadHigh:   p.hReadHigh,
		VReadHigh:   p.vReadHigh,
		HVLatched:   p.hvLatched,
	}
}

// LoadState restores a previously saved PPU state.
func (p *PPU) LoadState(state PPUState) {
	copy(p.VRAM[:], state.VRAM)
	copy(p.OAM[:], state.OAM)
	copy(p.CGRAM[:], state.CGRAM)
	p.PPURegisters = state.Registers
	p.FrontBuffer = append(p.FrontBuffer[:0], state.FrontBuffer...)
	p.Width = state.Width
	p.Height = state.Height
	p.cycles = state.Cycles
	p.FrameCount = state.FrameCount
	p.hCounter = state.HCounter
	p.vCounter = state.VCounter
	p.NMIFlag = state.NMIFlag
	p.RangeOver = state.RangeOver
	p.TimeOver = state.TimeOver
	p.latchedH = state.LatchedH
	p.latchedV = state.LatchedV
	p.m7PairValid = state.M7PairValid
	p.hReadHigh = state.HReadHigh
	p.vReadHigh = state.VReadHigh
	p.hvLatched = state.HVLatched
}
