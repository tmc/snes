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
	cbm   uint8
	sw46  bool
	bbf   bool

	romBank     [4]uint8
	romBankMode [4]bool
}

// New returns a reset SA-1 board shell.
func New() *Device {
	d := &Device{}
	d.romBank = [4]uint8{0, 1, 2, 3}
	return d
}

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
	case 0x2225:
		d.sw46 = val&0x80 != 0
		d.cbm = val & 0x7f
	case 0x2220, 0x2221, 0x2222, 0x2223:
		i := regBase + reg - 0x2220
		d.romBank[i] = val & 0x07
		d.romBankMode[i] = val&0x80 != 0
	case 0x2226:
		d.swen = val&0x80 != 0
	case 0x2227:
		d.cwen = val&0x80 != 0
	case 0x2228:
		d.bwp = val & 0x0f
	case 0x223f:
		d.bbf = val&0x80 != 0
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

// CPUIRQPending reports whether the S-CPU IRQ line should be asserted.
func (d *Device) CPUIRQPending() bool { return d.cpuIRQFlag && d.cpuIRQEnable }

// CPUBWRAMPage returns the 8 KiB BW-RAM page selected for S-CPU banks
// $00-$3f/$80-$bf:$6000-$7fff.
func (d *Device) CPUBWRAMPage() uint8 { return d.bwrap }

// CPUROMAddress maps the S-CPU-visible SA-1 ROM banks to a linear ROM offset.
func (d *Device) CPUROMAddress(addr uint32) (uint32, bool) {
	bank := (addr >> 16) & 0xff
	offset := addr & 0xffff
	if (bank <= 0x3f || bank >= 0x80 && bank <= 0xbf) && offset >= 0x8000 {
		region := bank & 0x3f
		i := region >> 4
		a := uint32(region)<<15 | (offset & 0x7fff)
		if d.romBankMode[i] {
			a = uint32(d.romBank[i])<<20 | (a & 0x0fffff)
		}
		return a, true
	}
	if bank < 0xc0 {
		return 0, false
	}
	i := (bank - 0xc0) >> 4
	if i > 3 {
		return 0, false
	}
	base := uint32(d.romBank[i]) << 20
	return base | uint32(bank&0x0f)<<16 | offset, true
}

// SA1BWRAMAddress maps an SA-1-side BW-RAM access to a linear BW-RAM address.
func (d *Device) SA1BWRAMAddress(addr uint32) (uint32, bool) {
	bank := (addr >> 16) & 0xff
	offset := addr & 0xffff
	switch {
	case bank >= 0x40 && bank <= 0x43:
		return uint32(d.cbm&0x1f)<<13 | (offset & 0x1fff), true
	case bank >= 0x60 && bank <= 0x6f:
		return uint32(d.cbm)<<13 | (offset & 0x1fff), true
	default:
		return 0, false
	}
}

// ReadSA1BWRAM reads through the SA-1-side BW-RAM linear or bitmap view.
func (d *Device) ReadSA1BWRAM(ram []byte, addr uint32) uint8 {
	a, ok := d.SA1BWRAMAddress(addr)
	if !ok || len(ram) == 0 {
		return 0
	}
	if addr>>16 >= 0x60 {
		return d.readBitmap(ram, a)
	}
	return ram[int(a)%len(ram)]
}

// WriteSA1BWRAM writes through the SA-1-side BW-RAM linear or bitmap view.
func (d *Device) WriteSA1BWRAM(ram []byte, addr uint32, val uint8) {
	a, ok := d.SA1BWRAMAddress(addr)
	if !ok || len(ram) == 0 || !d.AllowCPUBWRAMWrite(a) {
		return
	}
	if addr>>16 >= 0x60 {
		d.writeBitmap(ram, a, val)
		return
	}
	ram[int(a)%len(ram)] = val
}

// AllowCPUBWRAMWrite reports whether the translated BW-RAM address is writable.
func (d *Device) AllowCPUBWRAMWrite(addr uint32) bool {
	if d.swen || d.cwen {
		return true
	}
	return addr&0x3ffff >= 0x100<<d.bwp
}

func (d *Device) readBitmap(ram []byte, addr uint32) uint8 {
	if d.bbf {
		shift := (addr & 3) * 2
		return ram[int(addr>>2)%len(ram)] >> shift & 0x03
	}
	shift := (addr & 1) * 4
	return ram[int(addr>>1)%len(ram)] >> shift & 0x0f
}

func (d *Device) writeBitmap(ram []byte, addr uint32, val uint8) {
	if d.bbf {
		i := int(addr>>2) % len(ram)
		shift := (addr & 3) * 2
		mask := uint8(0x03 << shift)
		ram[i] = ram[i]&^mask | (val&0x03)<<shift
		return
	}
	i := int(addr>>1) % len(ram)
	shift := (addr & 1) * 4
	mask := uint8(0x0f << shift)
	ram[i] = ram[i]&^mask | (val&0x0f)<<shift
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
	CBM          uint8
	SW46         bool
	BBF          bool
	ROMBank      [4]uint8
	ROMBankMode  [4]bool
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
		CBM:          d.cbm,
		SW46:         d.sw46,
		BBF:          d.bbf,
		ROMBank:      d.romBank,
		ROMBankMode:  d.romBankMode,
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
	d.cbm = s.CBM
	d.sw46 = s.SW46
	d.bbf = s.BBF
	d.romBank = s.ROMBank
	d.romBankMode = s.ROMBankMode
	return nil
}
