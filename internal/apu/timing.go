package apu

// TimingEvent records a production APU operation. Cycle counts completed APU
// ticks, in the same units as GetCycles. Clocked bus operations are observed
// after their wait clocks; sample events precede bus operations completing
// the same tick. Unconverted opcodes still apply atomic effects at dispatch.
// PC is the live SPC program counter, which can already point past operands.
//
// Kind is "input-port", "input-read", "output-port", "dsp-write", or "sample".
// "input-read" includes dummy reads and records the value actually returned.
// Address is
// a port index or DSP register address. Left and Right are meaningful only for
// samples. These events describe this implementation, not hardware bus phases.
type TimingEvent struct {
	Kind        string
	Cycle       uint64
	PC          uint16
	Address     uint16
	Value       uint8
	Left, Right int16
}

func (a *APU) trace(kind string, address uint16, value uint8, left, right int16) {
	if a.Trace != nil {
		a.Trace(TimingEvent{kind, a.cycles, a.Processor.PC, address, value, left, right})
	}
}
