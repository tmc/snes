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
	Data    uint16
}

func New() *Device {
	return &Device{}
}

func (d *Device) Read(addr uint32) (uint8, bool) {
	off := uint16(addr & 0xFFFF)
	switch off {
	case 0x8000:
		return uint8(d.Data), true
	case 0x8001:
		return uint8(d.Data >> 8), true
	case 0x8002:
		return d.Status, true
	default:
		return 0, false
	}
}

func (d *Device) Write(addr uint32, val uint8) bool {
	off := uint16(addr & 0xFFFF)
	switch off {
	case 0x8000:
		d.Data = (d.Data & 0xFF00) | uint16(val)
		return true
	case 0x8001:
		d.Data = (d.Data & 0x00FF) | (uint16(val) << 8)
		return true
	case 0x8002:
		d.Command = val
		d.Status = 0x80
		return true
	default:
		return false
	}
}

func (d *Device) Step(masterCycles uint64) {}

type state struct {
	Command uint8
	Status  uint8
	Regs    [16]uint16
	Data    uint16
}

func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		Command: d.Command,
		Status:  d.Status,
		Regs:    d.Regs,
		Data:    d.Data,
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
	d.Data = s.Data
	return nil
}
