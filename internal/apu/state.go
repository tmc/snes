package apu

import (
	"fmt"

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
	// ClockVersion 1 uses SMP clocks for Cycles/DSPCycles and opcode
	// cycles for Pending and Processor.Cycles. Older states are rejected.
	ClockVersion                    uint8
	DummyReadWaiting, DummyReadDone bool
	InputRead                       APUInputReadState

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
	Val    uint8
}

// APUInputReadState preserves an IPL input instruction at an SMP half-cycle.
type APUInputReadState struct {
	Active                bool
	Opcode, Phase         uint8
	Addr                  uint16
	Immediate, Low, Value uint8
	Waiting               bool
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
		ClockVersion:     1,
		DummyReadWaiting: a.dummyReadWaiting, DummyReadDone: a.dummyReadDone,
		InputRead:          APUInputReadState{a.inputOp.active, a.inputOp.opcode, a.inputOp.phase, a.inputOp.addr, a.inputOp.immediate, a.inputOp.low, a.inputOp.value, a.inputOp.waiting},
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
			Val:    a.microOp.val,
		},
		AudioBuffer: append([]int16(nil), a.audioBuffer[:a.audioCount]...),
	}
}

// LoadState restores a previously saved APU state.
func (a *APU) LoadState(state APUState) error {
	if err := ValidateState(state); err != nil {
		return err
	}
	a.dummyReadWaiting = state.DummyReadWaiting
	a.dummyReadDone = state.DummyReadDone
	a.inputOp = inputReadOp{state.InputRead.Active, state.InputRead.Opcode, state.InputRead.Phase, state.InputRead.Addr, state.InputRead.Immediate, state.InputRead.Low, state.InputRead.Value, state.InputRead.Waiting}
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
	if err := a.DSP.LoadState(state.DSP); err != nil {
		return err
	}
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
		val:    state.MicroOp.Val,
	}
	a.audioMu.Lock()
	if cap(a.audioBuffer) < len(state.AudioBuffer) {
		a.audioBuffer = make([]int16, len(state.AudioBuffer))
	}
	copy(a.audioBuffer, state.AudioBuffer)
	a.audioCount = len(state.AudioBuffer)
	a.audioMu.Unlock()
	return nil
}

// ValidateState checks that an APU snapshot can resume its in-flight instruction.
// It does not modify the snapshot or an APU.
func ValidateState(state APUState) error {
	if state.ClockVersion != 1 {
		return fmt.Errorf("apu state: unsupported clock version %d", state.ClockVersion)
	}
	if state.DummyReadWaiting || state.DummyReadDone {
		m := state.MicroOp
		phase := m.Opcode == 0x8f && m.Step == 3 || (m.Opcode == 0xc4 || m.Opcode == 0xcb || m.Opcode == 0xd8) && m.Step == 2
		if !m.Active || !phase || !isOutputPort(m.Addr) || state.Cycles%2 != 1 || state.InputRead.Active || state.DummyReadWaiting && state.DummyReadDone {
			return fmt.Errorf("apu state: invalid dummy read phase")
		}
	}
	if err := validateInputRead(state); err != nil {
		return err
	}
	if state.DSPCycles >= dspSampleDivider {
		return fmt.Errorf("apu state: invalid dsp sample phase")
	}
	if err := dsp.ValidateState(state.DSP); err != nil {
		return err
	}
	m := state.MicroOp
	if !m.Active {
		return nil
	}
	if state.Pending != 0 || state.PendingOutPortMask != 0 || state.Processor.Stopped {
		return fmt.Errorf("apu state: micro-op conflicts with pending or stopped instruction")
	}
	switch m.Opcode {
	case 0xe4:
		if m.Step >= 1 && m.Step <= 2 {
			return nil
		}
	case 0x8f:
		if m.Step >= 1 && m.Step <= 4 {
			return nil
		}
		if m.Step == 5 && isOutputPort(m.Addr) {
			return nil
		}
	case 0xc4, 0xcb, 0xd8:
		if m.Step == 1 || m.Step >= 2 && m.Step <= 4 && isOutputPort(m.Addr) {
			return nil
		}
	}
	return fmt.Errorf("apu state: invalid micro-op opcode %02x step %d address %04x", m.Opcode, m.Step, m.Addr)
}

func validateInputRead(state APUState) error {
	m := state.InputRead
	if !m.Active {
		if m.Waiting {
			return fmt.Errorf("apu state: inactive input read is waiting")
		}
		return nil
	}
	if state.Pending != 0 || state.PendingOutPortMask != 0 || state.MicroOp.Active || state.Processor.Stopped {
		return fmt.Errorf("apu state: input read conflicts with pending instruction")
	}
	max := uint8(5)
	switch m.Opcode {
	case 0xe4, 0xeb, 0x7e:
	case 0x78, 0xba:
		max = 9
	default:
		return fmt.Errorf("apu state: invalid input opcode %02x", m.Opcode)
	}
	if m.Phase < 2 || m.Phase > max || uint64(m.Phase%2) != state.Cycles%2 {
		return fmt.Errorf("apu state: invalid input phase %d", m.Phase)
	}
	addressReady := m.Phase >= 4
	if m.Opcode == 0x78 {
		addressReady = m.Phase >= 6
	}
	if addressReady && !isOutputPort(m.Addr) {
		return fmt.Errorf("apu state: invalid input address %04x", m.Addr)
	}
	canWait := m.Phase == 5
	if m.Opcode == 0x78 {
		canWait = m.Phase == 7
	}
	if m.Opcode == 0xba {
		canWait = m.Phase == 5 || m.Phase == 9
	}
	if m.Waiting && !canWait {
		return fmt.Errorf("apu state: invalid input suspension")
	}
	return nil
}
