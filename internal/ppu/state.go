package ppu

// PPUState captures the serializable PPU state.
type PPUState struct {
	VRAM  []byte
	OAM   []byte
	CGRAM []byte

	Registers PPURegisters

	FrontBuffer      []uint16
	HiresFrontBuffer []uint16
	Width            int
	Height           int

	Cycles          uint64
	BeamFrameStart  uint64
	BeamVBlankStart uint64
	VBlankActive    bool
	FrameCount      int
	HCounter        int
	VCounter        int
	PALTiming       bool
	PPUField        bool
	PPUInterlace    bool
	VPeriod         int
	HPeriod         int
	NMIFlag         bool
	NMIHold         uint8
	AutoJoypad      bool
	RangeOver       bool
	TimeOver        bool
	LatchedH        uint16
	LatchedV        uint16
	LastM7Pair      Mode7MatrixPairEvent
	M7PairValid     [0x22]bool
	HReadHigh       bool
	VReadHigh       bool
	HVLatched       bool
	LatchCGRAMAddr  uint8
	LatchOAMAddr    uint16
}

// SaveState returns a snapshot of the PPU state.
func (p *PPU) SaveState() PPUState {
	return PPUState{
		VRAM:             append([]byte(nil), p.VRAM[:]...),
		OAM:              append([]byte(nil), p.OAM[:]...),
		CGRAM:            append([]byte(nil), p.CGRAM[:]...),
		Registers:        p.PPURegisters,
		FrontBuffer:      append([]uint16(nil), p.FrontBuffer...),
		HiresFrontBuffer: append([]uint16(nil), p.hiresFrontBuffer...),
		Width:            p.Width,
		Height:           p.Height,
		Cycles:           p.cycles,
		BeamFrameStart:   p.beamFrameStart,
		BeamVBlankStart:  p.beamVBlankStart,
		VBlankActive:     p.vblankActive,
		FrameCount:       p.FrameCount,
		HCounter:         p.hCounter,
		VCounter:         p.vCounter,
		PALTiming:        p.palTiming,
		PPUField:         p.ppuField,
		PPUInterlace:     p.ppuInterlace,
		VPeriod:          p.currentVPeriod(),
		HPeriod:          p.currentHPeriod(),
		NMIFlag:          p.NMIFlag,
		NMIHold:          p.nmiHold,
		AutoJoypad:       p.AutoJoypad,
		RangeOver:        p.RangeOver,
		TimeOver:         p.TimeOver,
		LatchedH:         p.latchedH,
		LatchedV:         p.latchedV,
		LastM7Pair:       p.lastM7Pair,
		M7PairValid:      p.m7PairValid,
		HReadHigh:        p.hReadHigh,
		VReadHigh:        p.vReadHigh,
		HVLatched:        p.hvLatched,
		LatchCGRAMAddr:   p.latchCGRAMAddr,
		LatchOAMAddr:     p.latchOAMAddr,
	}
}

// LoadState restores a previously saved PPU state.
func (p *PPU) LoadState(state PPUState) {
	copy(p.VRAM[:], state.VRAM)
	copy(p.OAM[:], state.OAM)
	copy(p.CGRAM[:], state.CGRAM)
	p.PPURegisters = state.Registers
	p.FrontBuffer = append(p.FrontBuffer[:0], state.FrontBuffer...)
	if state.HiresFrontBuffer != nil {
		p.hiresFrontBuffer = append(p.hiresFrontBuffer[:0], state.HiresFrontBuffer...)
	} else {
		p.hiresFrontBuffer = make([]uint16, 512*240)
	}
	p.Width = state.Width
	p.Height = state.Height
	p.cycles = state.Cycles
	p.beamFrameStart = state.BeamFrameStart
	p.beamVBlankStart = state.BeamVBlankStart
	p.vblankActive = state.VBlankActive
	p.FrameCount = state.FrameCount
	p.hCounter = state.HCounter
	p.vCounter = state.VCounter
	p.palTiming = state.PALTiming
	p.ppuField = state.PPUField
	p.ppuInterlace = state.PPUInterlace
	p.vPeriod = state.VPeriod
	if p.vPeriod == 0 {
		p.vPeriod = p.baseVPeriod()
	}
	p.hPeriod = state.HPeriod
	if p.hPeriod == 0 {
		p.hPeriod = ntscHPeriod
	}
	p.NMIFlag = state.NMIFlag
	p.nmiHold = state.NMIHold
	p.AutoJoypad = state.AutoJoypad
	p.RangeOver = state.RangeOver
	p.TimeOver = state.TimeOver
	p.latchedH = state.LatchedH
	p.latchedV = state.LatchedV
	p.lastM7Pair = state.LastM7Pair
	p.m7PairValid = state.M7PairValid
	p.hReadHigh = state.HReadHigh
	p.vReadHigh = state.VReadHigh
	p.hvLatched = state.HVLatched
	p.latchCGRAMAddr = state.LatchCGRAMAddr
	p.latchOAMAddr = state.LatchOAMAddr
}
