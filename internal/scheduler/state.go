package scheduler

// SchedulerState captures the serializable scheduler state.
type SchedulerState struct {
	Clock uint64

	NMIEnabled   bool
	NMITriggered bool
	IRQEnabled   bool
	IRQTriggered bool
	IRQH         uint16
	IRQV         uint16
}

// SaveState returns a snapshot of the scheduler state.
func (s *Scheduler) SaveState() SchedulerState {
	return SchedulerState{
		Clock:        s.clock,
		NMIEnabled:   s.nmiEnabled,
		NMITriggered: s.nmiTriggered,
		IRQEnabled:   s.irqEnabled,
		IRQTriggered: s.irqTriggered,
		IRQH:         s.irqH,
		IRQV:         s.irqV,
	}
}

// LoadState restores a previously saved scheduler state.
func (s *Scheduler) LoadState(state SchedulerState) {
	s.clock = state.Clock
	s.nmiEnabled = state.NMIEnabled
	s.nmiTriggered = state.NMITriggered
	s.irqEnabled = state.IRQEnabled
	s.irqTriggered = state.IRQTriggered
	s.irqH = state.IRQH
	s.irqV = state.IRQV
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
