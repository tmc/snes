package apu

import (
	"github.com/tmc/snes/internal/apu/dsp"
	"github.com/tmc/snes/internal/apu/spc700"
)

// TimerState captures the serializable timer state.
type TimerState struct {
	Enabled bool
	Target  uint8
	Counter uint8
	Divider uint16
	Stage2  uint8
}

// APUState captures the serializable APU state.
type APUState struct {
	InPorts  [4]uint8
	OutPorts [4]uint8

	RAM    []byte
	IPLROM [64]uint8

	Processor spc700.SPCState
	Timers    [3]TimerState
	Control   uint8
	DSP       dsp.DSPState
	DSPAddr   uint8

	Cycles    uint64
	DSPCycles uint64
	Pending   uint32

	PendingOutPortMask uint8
	PendingOutPorts    [4]uint8
	MicroOp            APUMicroOpState

	AudioBuffer []int16
}

// APUMicroOpState captures an in-flight APU-owned SPC700 micro-op.
type APUMicroOpState struct {
	Active bool
	Opcode uint8
	Step   uint8
	Addr   uint16
}

// SaveState returns a snapshot of the APU state.
func (a *APU) SaveState() APUState {
	var timers [3]TimerState
	for i := range a.Timers {
		t := a.Timers[i]
		timers[i] = TimerState{
			Enabled: t.Enabled,
			Target:  t.Target,
			Counter: t.Counter,
			Divider: t.divider,
			Stage2:  t.stage2,
		}
	}

	return APUState{
		InPorts:            a.InPorts,
		OutPorts:           a.OutPorts,
		RAM:                append([]byte(nil), a.RAM[:]...),
		IPLROM:             a.IPLROM,
		Processor:          a.Processor.SaveState(),
		Timers:             timers,
		Control:            a.Control,
		DSP:                a.DSP.SaveState(),
		DSPAddr:            a.dspAddr,
		Cycles:             a.cycles,
		DSPCycles:          a.dspCycles,
		Pending:            a.pending,
		PendingOutPortMask: a.pendingOutPortMask,
		PendingOutPorts:    a.pendingOutPorts,
		MicroOp: APUMicroOpState{
			Active: a.microOp.active,
			Opcode: a.microOp.opcode,
			Step:   a.microOp.step,
			Addr:   a.microOp.addr,
		},
		AudioBuffer: append([]int16(nil), a.audioBuffer[:a.audioCount]...),
	}
}

// LoadState restores a previously saved APU state.
func (a *APU) LoadState(state APUState) {
	a.InPorts = state.InPorts
	a.OutPorts = state.OutPorts
	copy(a.RAM[:], state.RAM)
	a.IPLROM = state.IPLROM
	a.Processor.LoadState(state.Processor)
	for i := range a.Timers {
		t := state.Timers[i]
		a.Timers[i] = Timer{
			Enabled: t.Enabled,
			Target:  t.Target,
			Counter: t.Counter,
			divider: t.Divider,
			stage2:  t.Stage2,
		}
	}
	a.Control = state.Control
	a.DSP.LoadState(state.DSP)
	a.dspAddr = state.DSPAddr
	a.cycles = state.Cycles
	a.dspCycles = state.DSPCycles
	a.pending = state.Pending
	a.pendingOutPortMask = state.PendingOutPortMask
	a.pendingOutPorts = state.PendingOutPorts
	a.microOp = apuMicroOp{
		active: state.MicroOp.Active,
		opcode: state.MicroOp.Opcode,
		step:   state.MicroOp.Step,
		addr:   state.MicroOp.Addr,
	}
	a.audioMu.Lock()
	if cap(a.audioBuffer) < len(state.AudioBuffer) {
		a.audioBuffer = make([]int16, len(state.AudioBuffer))
	}
	copy(a.audioBuffer, state.AudioBuffer)
	a.audioCount = len(state.AudioBuffer)
	a.audioMu.Unlock()
}
