package sa1

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

const (
	regBase = 0x2200
	regEnd  = 0x23ff
)

// Device is the cartridge-facing SA-1 board state.
type Device struct {
	Regs [regEnd - regBase + 1]uint8
}

// New returns a reset SA-1 board shell.
func New() *Device { return &Device{} }

func mapped(addr uint32) (uint16, bool) {
	bank := (addr >> 16) & 0xff
	if !((bank <= 0x3f) || (bank >= 0x80 && bank <= 0xbf)) {
		return 0, false
	}
	off := uint16(addr)
	if off < regBase || off > regEnd {
		return 0, false
	}
	return off - regBase, true
}

// Read implements the cartridge coprocessor register window.
func (d *Device) Read(addr uint32) (uint8, bool) {
	reg, ok := mapped(addr)
	if !ok {
		return 0, false
	}
	return d.Regs[reg], true
}

// Write implements the cartridge coprocessor register window.
func (d *Device) Write(addr uint32, val uint8) bool {
	reg, ok := mapped(addr)
	if !ok {
		return false
	}
	d.Regs[reg] = val
	return true
}

// Step advances timed SA-1 hardware. The CPU core is not implemented yet.
func (d *Device) Step(masterCycles uint64) {}

type state struct {
	Regs [regEnd - regBase + 1]uint8
}

// Serialize captures SA-1 board state.
func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{Regs: d.Regs}); err != nil {
		return nil, fmt.Errorf("serialize sa1: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores SA-1 board state produced by Serialize.
func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize sa1: %w", err)
	}
	d.Regs = s.Regs
	return nil
}
