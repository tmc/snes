package dsp1

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

type MapType uint8

const (
	MapLoROMSmall MapType = iota
	MapLoROMLarge
	MapHiROM
)

// Device is a minimal DSP-1 coprocessor state container.
// It is intentionally conservative until full command/response emulation is added.
type Device struct {
	Command uint8
	Status  uint8 // status register (odd-address read)
	Regs    [16]uint16
	Data    uint8 // data register (even-address read/write)
	MapType MapType
}

func New() *Device {
	return &Device{
		Status: 0x80,
	}
}

func (d *Device) SetMapType(mapType MapType) {
	d.MapType = mapType
}

func (d *Device) mapped(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	off := uint16(addr & 0xFFFF)
	switch d.MapType {
	case MapLoROMLarge:
		if !((bank >= 0x60 && bank <= 0x6F) || (bank >= 0xE0 && bank <= 0xEF)) {
			return false
		}
		return off <= 0x7FFF
	case MapHiROM:
		if !((bank >= 0x00 && bank <= 0x1F) || (bank >= 0x80 && bank <= 0x9F)) {
			return false
		}
		return off >= 0x6000 && off <= 0x7FFF
	default:
		if !((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) {
			return false
		}
		return off >= 0x8000 && off <= 0xFFFF
	}
}

func (d *Device) Read(addr uint32) (uint8, bool) {
	if !d.mapped(addr) {
		return 0, false
	}
	if (addr & 1) != 0 {
		return d.Status, true
	}
	return d.Data, true
}

func (d *Device) Write(addr uint32, val uint8) bool {
	if !d.mapped(addr) {
		return false
	}
	// DSP1 accepts writes to the data register at even addresses.
	if (addr & 1) == 0 {
		d.Data = val
		d.Command = val
		d.Status = 0x80
		return true
	}
	return false
}

func (d *Device) Step(masterCycles uint64) {}

type state struct {
	Command uint8
	Status  uint8
	Regs    [16]uint16
	Data    uint8
	MapType MapType
}

func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		Command: d.Command,
		Status:  d.Status,
		Regs:    d.Regs,
		Data:    d.Data,
		MapType: d.MapType,
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
	d.MapType = s.MapType
	return nil
}
