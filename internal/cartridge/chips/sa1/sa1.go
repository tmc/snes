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

	// Arithmetic unit ($2250-$2254 trigger / $2306-$230B result).
	// bsnes/sfc/coprocessor/sa1/io.cpp:404-466.
	mcntACM  bool   // $2250 bit 1: 0=multiply/divide, 1=accumulative multiply
	mcntMD   bool   // $2250 bit 0: 0=multiply, 1=divide (only when ACM=0)
	ma       uint16 // $2251/$2252 multiplicand or dividend
	mb       uint16 // $2253/$2254 multiplier or divisor
	mr       uint64 // $2306-$230A 40-bit result accumulator
	overflow bool   // $230B bit 7

	// Variable-length bit decoder ($2258-$225B / $230C-$230D).
	// bsnes/sfc/coprocessor/sa1/io.cpp:69-96, 468-486.
	vbdHL   bool   // $2258 bit 7: 0=fixed (advance on VBS write), 1=auto (advance on $230D read)
	vbdVB   uint8  // $2258 bits 0..3: vector bit count (1..16; 0 substitutes 16)
	vbdVA   uint32 // $2259/$225A/$225B: 24-bit address (masked to 0xFFFFFF)
	vbdVBIT uint8  // bit cursor within byte at VA (0..7)

	romReader ROMReader // synthetic ROM-byte reader for VBR; nil → 0xFF.
}

// ROMReader returns the SA-1-side byte at a 24-bit address. Used by
// the variable-length bit decoder ($230C/$230D) to walk a packed bit
// stream out of game-pak ROM, BW-RAM, or I-RAM. Tests inject a
// synthetic reader; production wiring (cartridge) is out of scope for
// this slice.
type ROMReader func(addr uint32) uint8

// SetROMReader installs the byte source consulted by VBR-routed reads
// at $230C/$230D. nil disables: readVBR returns 0xFF for all addresses.
func (d *Device) SetROMReader(r ROMReader) { d.romReader = r }

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
	case 0x2306:
		return uint8(d.mr), true
	case 0x2307:
		return uint8(d.mr >> 8), true
	case 0x2308:
		return uint8(d.mr >> 16), true
	case 0x2309:
		return uint8(d.mr >> 24), true
	case 0x230a:
		return uint8(d.mr >> 32), true
	case 0x230b:
		if d.overflow {
			return 0x80, true
		}
		return 0x00, true
	case 0x230c:
		// VDPL: low byte of (24-bit data >> vbit).
		// bsnes io.cpp:70-77.
		shifted := d.vbdReadStream() >> d.vbdVBIT
		return uint8(shifted), true
	case 0x230d:
		// VDPH: bits 8..15 of (24-bit data >> vbit). In auto mode (HL=1)
		// advance VA/VBIT by VB after the read. bsnes io.cpp:81-95.
		shifted := d.vbdReadStream() >> d.vbdVBIT
		if d.vbdHL {
			d.vbdAdvance()
			d.vbdMirrorVAToRegs()
		}
		return uint8(shifted >> 8), true
	}
	return d.Regs[reg], true
}

// vbdReadStream returns 24 bits read at VA, VA+1, VA+2 via readVBR.
func (d *Device) vbdReadStream() uint32 {
	return uint32(d.readVBR(d.vbdVA)) |
		uint32(d.readVBR((d.vbdVA+1)&0xFFFFFF))<<8 |
		uint32(d.readVBR((d.vbdVA+2)&0xFFFFFF))<<16
}

// readVBR is the SA-1 variable-bit-read bus mux. bsnes
// memory.cpp:113-133 routes to ROM/BW-RAM/I-RAM based on address;
// out-of-range returns 0xFF. This slice only consults the injected
// ROMReader (which tests configure synthetically); cartridge-side
// wiring of the real ROM/BW-RAM/I-RAM mux is a follow-up slice.
func (d *Device) readVBR(addr uint32) uint8 {
	if d.romReader == nil {
		return 0xFF
	}
	return d.romReader(addr & 0xFFFFFF)
}

// vbdAdvance applies the (VBIT += VB; VA += VBIT>>3; VBIT &= 7) update
// per bsnes io.cpp:476-478, with VA wrapping at 24 bits.
func (d *Device) vbdAdvance() {
	d.vbdVBIT += d.vbdVB
	d.vbdVA = (d.vbdVA + uint32(d.vbdVBIT>>3)) & 0xFFFFFF
	d.vbdVBIT &= 7
}

// vbdMirrorVAToRegs syncs the VAL/VAH/VAB byte mirrors so that reads
// at $2259/$225A/$225B observe the live VA after auto/fixed advances.
func (d *Device) vbdMirrorVAToRegs() {
	d.Regs[0x2259-regBase] = uint8(d.vbdVA)
	d.Regs[0x225a-regBase] = uint8(d.vbdVA >> 8)
	d.Regs[0x225b-regBase] = uint8(d.vbdVA >> 16)
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
	case 0x2250: // MCNT
		d.mcntACM = val&0x02 != 0
		d.mcntMD = val&0x01 != 0
		if d.mcntACM {
			d.mr = 0
		}
	case 0x2251: // MAL
		d.ma = d.ma&0xff00 | uint16(val)
	case 0x2252: // MAH
		d.ma = d.ma&0x00ff | uint16(val)<<8
	case 0x2253: // MBL
		d.mb = d.mb&0xff00 | uint16(val)
	case 0x2254: // MBH — write triggers the operation.
		d.mb = d.mb&0x00ff | uint16(val)<<8
		d.Regs[reg] = val
		d.runArith()
		// Mirror the register bytes that the operation cleared so reads
		// at $2251/$2252/$2253 reflect the live ma/mb state.
		d.Regs[0x2253-regBase] = uint8(d.mb)
		d.Regs[0x2254-regBase] = uint8(d.mb >> 8)
		d.Regs[0x2251-regBase] = uint8(d.ma)
		d.Regs[0x2252-regBase] = uint8(d.ma >> 8)
		return true
	case 0x2258: // VBS — set vector-bit count + mode.
		d.vbdHL = val&0x80 != 0
		d.vbdVB = val & 0x0F
		if d.vbdVB == 0 {
			d.vbdVB = 16
		}
		if !d.vbdHL {
			// Fixed mode advances VA/VBIT immediately on the write.
			d.vbdAdvance()
			d.vbdMirrorVAToRegs()
		}
	case 0x2259: // VAL
		d.vbdVA = (d.vbdVA &^ 0x0000FF) | uint32(val)
	case 0x225a: // VAH
		d.vbdVA = (d.vbdVA &^ 0x00FF00) | uint32(val)<<8
	case 0x225b: // VAB — also clears VBIT per io.cpp:486.
		d.vbdVA = (d.vbdVA &^ 0xFF0000) | uint32(val)<<16
		d.vbdVBIT = 0
	}
	d.Regs[reg] = val
	return true
}

// runArith executes one operation per bsnes
// bsnes/sfc/coprocessor/sa1/io.cpp:433-465. Triggered on every $2254 write.
func (d *Device) runArith() {
	switch {
	case !d.mcntACM && !d.mcntMD:
		// Signed multiplication: mr = (uint32)((int16)ma * (int16)mb).
		// Only mb is cleared.
		prod := int32(int16(d.ma)) * int32(int16(d.mb))
		d.mr = uint64(uint32(prod))
		d.mb = 0
	case !d.mcntACM && d.mcntMD:
		// Signed division with floor-toward-negative-infinity rounding.
		// Both ma and mb are cleared.
		if d.mb == 0 {
			d.mr = 0
		} else {
			dividend := int32(int16(d.ma))
			divisor := uint32(d.mb) // unsigned per bsnes
			dividendExt := uint32(dividend) + divisor*65536
			remainder := uint16(dividendExt % divisor)
			quotient := uint16(dividendExt/divisor - 65536)
			d.mr = uint64(remainder)<<16 | uint64(quotient)
		}
		d.ma = 0
		d.mb = 0
	default:
		// Accumulative multiplication (ACM=1): mr += int16*int16, then
		// overflow = (mr >> 40) & 1, then mr truncated to 40 bits.
		// MD bit is ignored. Only mb is cleared.
		const mask40 = (uint64(1) << 40) - 1
		prod := int64(int16(d.ma)) * int64(int16(d.mb))
		d.mr = uint64(int64(d.mr) + prod)
		// bsnes io.cpp:461: bool overflow = mr >> 40 (an assignment,
		// not OR; latches per-step).
		d.overflow = d.mr>>40 != 0
		d.mr &= mask40
		d.mb = 0
	}
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
	MCNTACM      bool
	MCNTMD       bool
	MA           uint16
	MB           uint16
	MR           uint64
	Overflow     bool
	VBDHL        bool
	VBDVB        uint8
	VBDVA        uint32
	VBDVBIT      uint8
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
		MCNTACM:      d.mcntACM,
		MCNTMD:       d.mcntMD,
		MA:           d.ma,
		MB:           d.mb,
		MR:           d.mr,
		Overflow:     d.overflow,
		VBDHL:        d.vbdHL,
		VBDVB:        d.vbdVB,
		VBDVA:        d.vbdVA,
		VBDVBIT:      d.vbdVBIT,
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
	d.mcntACM = s.MCNTACM
	d.mcntMD = s.MCNTMD
	d.ma = s.MA
	d.mb = s.MB
	d.mr = s.MR
	d.overflow = s.Overflow
	d.vbdHL = s.VBDHL
	d.vbdVB = s.VBDVB
	d.vbdVA = s.VBDVA
	d.vbdVBIT = s.VBDVBIT
	return nil
}
