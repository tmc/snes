package scheduler

// SchedulerState captures the serializable scheduler state.
type SchedulerState struct {
	Clock uint64

	NMIEnabled   bool
	NMITriggered bool
	IRQMode      uint8
	IRQTriggered bool
	IRQLine      uint64
	IRQFlag      bool
	IRQH         uint16
	IRQV         uint16
	PAL          bool
}

// SaveState returns a snapshot of the scheduler state.
func (s *Scheduler) SaveState() SchedulerState {
	return SchedulerState{
		Clock:        s.clock,
		NMIEnabled:   s.nmiEnabled,
		NMITriggered: s.nmiTriggered,
		IRQMode:      s.irqMode,
		IRQTriggered: s.irqTriggered,
		IRQLine:      s.irqLine,
		IRQFlag:      s.irqFlag,
		IRQH:         s.irqH,
		IRQV:         s.irqV,
		PAL:          s.pal,
	}
}

// LoadState restores a previously saved scheduler state.
func (s *Scheduler) LoadState(state SchedulerState) {
	s.clock = state.Clock
	s.nmiEnabled = state.NMIEnabled
	s.nmiTriggered = state.NMITriggered
	s.irqMode = state.IRQMode
	s.irqTriggered = state.IRQTriggered
	s.irqLine = state.IRQLine
	s.irqFlag = state.IRQFlag
	s.irqH = state.IRQH
	s.irqV = state.IRQV
	s.SetPAL(state.PAL)
}

// Reset clears the scheduler state while preserving thread registrations.
func (s *Scheduler) Reset() {
	s.LoadState(SchedulerState{})
	if s.cpu != nil {
		s.cpu.ResetCycles()
	}
	if s.apu != nil {
		s.apu.ResetCycles()
	}
	if s.ppu != nil {
		s.ppu.ResetCycles()
	}
}
