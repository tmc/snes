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

	cpuIRQFlag   bool
	chdmaIRQFlag bool
	cpuIRQEnable bool
	chdmaEnable  bool
	cpuMessage   uint8

	bwrap uint8
	swen  bool
	cwen  bool
	bwp   uint8
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
	switch regBase + reg {
	case 0x2300:
		return d.cpuStatus(), true
	}
	return d.Regs[reg], true
}

// Write implements the cartridge coprocessor register window.
func (d *Device) Write(addr uint32, val uint8) bool {
	reg, ok := mapped(addr)
	if !ok {
		return false
	}
	switch regBase + reg {
	case 0x2201:
		d.cpuIRQEnable = val&0x80 != 0
		d.chdmaEnable = val&0x20 != 0
	case 0x2202:
		if val&0x80 != 0 {
			d.cpuIRQFlag = false
		}
		if val&0x20 != 0 {
			d.chdmaIRQFlag = false
		}
	case 0x2224:
		d.bwrap = val & 0x1f
	case 0x2226:
		d.swen = val&0x80 != 0
	case 0x2227:
		d.cwen = val&0x80 != 0
	case 0x2228:
		d.bwp = val & 0x0f
	}
	d.Regs[reg] = val
	return true
}

// Step advances timed SA-1 hardware. The CPU core is not implemented yet.
func (d *Device) Step(masterCycles uint64) {}

// SignalCPUIRQ records an SA-1-to-S-CPU message and raises the CPU IRQ flag.
func (d *Device) SignalCPUIRQ(message uint8) {
	d.cpuMessage = message & 0x0f
	d.cpuIRQFlag = true
}

// SignalCharacterDMAIRQ raises the character-DMA completion flag.
func (d *Device) SignalCharacterDMAIRQ() { d.chdmaIRQFlag = true }

// CPUBWRAMPage returns the 8 KiB BW-RAM page selected for S-CPU banks
// $00-$3f/$80-$bf:$6000-$7fff.
func (d *Device) CPUBWRAMPage() uint8 { return d.bwrap }

// AllowCPUBWRAMWrite reports whether the translated BW-RAM address is writable.
func (d *Device) AllowCPUBWRAMWrite(addr uint32) bool {
	if d.swen || d.cwen {
		return true
	}
	return addr&0x3ffff >= 0x100<<d.bwp
}

func (d *Device) cpuStatus() uint8 {
	var v uint8
	if d.cpuIRQFlag {
		v |= 0x80
	}
	if d.chdmaIRQFlag {
		v |= 0x20
	}
	v |= d.cpuMessage & 0x0f
	return v
}

type state struct {
	Regs         [regEnd - regBase + 1]uint8
	CPUIRQFlag   bool
	CHDMAIRQFlag bool
	CPUIRQEnable bool
	CHDMAEnable  bool
	CPUMessage   uint8
	BWRAMPage    uint8
	SWEN         bool
	CWEN         bool
	BWP          uint8
}

// Serialize captures SA-1 board state.
func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		Regs:         d.Regs,
		CPUIRQFlag:   d.cpuIRQFlag,
		CHDMAIRQFlag: d.chdmaIRQFlag,
		CPUIRQEnable: d.cpuIRQEnable,
		CHDMAEnable:  d.chdmaEnable,
		CPUMessage:   d.cpuMessage,
		BWRAMPage:    d.bwrap,
		SWEN:         d.swen,
		CWEN:         d.cwen,
		BWP:          d.bwp,
	}); err != nil {
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
	d.cpuIRQFlag = s.CPUIRQFlag
	d.chdmaIRQFlag = s.CHDMAIRQFlag
	d.cpuIRQEnable = s.CPUIRQEnable
	d.chdmaEnable = s.CHDMAEnable
	d.cpuMessage = s.CPUMessage
	d.bwrap = s.BWRAMPage
	d.swen = s.SWEN
	d.cwen = s.CWEN
	d.bwp = s.BWP
	return nil
}
