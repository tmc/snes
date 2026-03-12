package dsp1

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// Device is a minimal DSP-1 coprocessor state container.
// It is intentionally conservative until full command/response emulation is added.
type Device struct {
	Command uint8
	Status  uint8
	Regs    [16]uint16
}

func New() *Device {
	return &Device{}
}

func (d *Device) Step(masterCycles uint64) {}

type state struct {
	Command uint8
	Status  uint8
	Regs    [16]uint16
}

func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		Command: d.Command,
		Status:  d.Status,
		Regs:    d.Regs,
	}); err != nil {
		return nil, fmt.Errorf("serialize dsp1: %w", err)
	}
	return buf.Bytes(), nil
}

func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize dsp1: %w", err)
	}
	d.Command = s.Command
	d.Status = s.Status
	d.Regs = s.Regs
	return nil
}
