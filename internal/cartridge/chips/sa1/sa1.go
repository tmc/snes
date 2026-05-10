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

	// Internal RAM ($00-3F:3000-37FF S-CPU window;
	// $00-3F:0000-07FF + $00-3F:3000-37FF SA-1 window). 2 KiB.
	// Per-256-byte write protection: $2229 SIWP gates S-CPU writes,
	// $222A CIWP gates SA-1 writes. Reads ignore both.
	// bsnes/sfc/coprocessor/sa1/iram.cpp:1-38, io.cpp:233-236, 340-345.
	iram [0x800]uint8
	siwp uint8
	ciwp uint8

	// S-CPU vector override. $2209 SCNT bits 6 (cpu_ivsw) and 4
	// (cpu_nvsw) gate; $220C/D SNV (NMI override) and $220E/F SIV
	// (IRQ override) carry the overriding addresses. Consumed on
	// S-CPU reads of $00:FFEA/EB (native NMI) and $00:FFEE/EF
	// (native IRQ); RESET and emulation-mode vectors are NOT
	// overridden. bsnes/sfc/coprocessor/sa1/io.cpp:252-303 +
	// rom.cpp:22-26.
	//
	// $2209 bit 7 (cpu_irq pulse) and bits 0..3 (cmeg message-port
	// data) are intentionally out of scope for this slice; the
	// $2209 byte mirror in Regs[] preserves all written bits for
	// future slices.
	cpuNVSW bool
	cpuIVSW bool
	snv     uint16
	siv     uint16

	// SA-1 CPU-internal vectors. $2203/$2204 CRV = SA-1 reset vector;
	// $2205/$2206 CNV = SA-1 NMI vector; $2207/$2208 CIV = SA-1 IRQ
	// vector. Bsnes/sfc/coprocessor/sa1/io.cpp:175-185 decodes these
	// as 16-bit byte pairs; sa1.cpp:54 consumes them as r.vector
	// during interrupt dispatch (sa1.cpp:60/67/72/77 select cnv for
	// NMI and civ for timer/dma/sa1_irq). The bytes remain mirrored
	// in Regs[] for backwards compatibility with state round-trips
	// that rely on the byte-mirror; the explicit fields expose the
	// 16-bit accessor a future SA-1 CPU instance needs.
	crv uint16
	cnv uint16
	civ uint16

	// Message ports. $2200 bits 0..3 (smeg, S-CPU → SA-1 message)
	// stored in scpuMessage; $2209 bits 0..3 (cmeg, SA-1 → S-CPU
	// message) reuse cpuMessage. $2209 bit 7 raises cpuIRQFlag (the
	// SA-1 → S-CPU IRQ pulse). bsnes/sfc/coprocessor/sa1/io.cpp:
	// 107-140 ($2200), 252-267 ($2209), 7-15 ($2300), 32-41 ($2301).
	// $2200 bits 7/6/5/4 (sa1_irq pulse, sa1_rdyb, sa1_resb, sa1_nmi)
	// require an SA-1 CPU consumer and are intentionally NOT decoded
	// here; the byte mirror in Regs[] preserves them for a future
	// slice.
	scpuMessage uint8

	// SA-1 DMA control. bsnes/sfc/coprocessor/sa1/io.cpp:347-358
	// ($2230 DCNT) + 491-503 ($2231 CDMA). dmaen/cden/cdsel gate the
	// type-2 character-conversion DMA trigger on $2247/$224F writes;
	// dprio/dd/sd are decoded for completeness so future dmaCC1 +
	// dmaNormal slices need only call additional bodies.
	dmaen   bool   // $2230 bit 7: master DMA enable
	dprio   bool   // $2230 bit 6: DMA priority over SA-1 CPU
	cden    bool   // $2230 bit 5: character DMA enable
	cdsel   bool   // $2230 bit 4: 0=CC2 (BRF→I-RAM), 1=CC1 (BW-RAM→I-RAM read-side)
	dmaDD   uint8  // $2230 bit 2: 0=I-RAM, 1=BW-RAM (DMA::Dest*)
	dmaSD   uint8  // $2230 bits 1..0: 0=ROM, 1=BW-RAM, 2=I-RAM (DMA::Source*)
	chdend  bool   // $2231 bit 7: character DMA end
	dmasize uint8  // $2231 bits 4..2 (clamped to 5): tile-size selector
	dmacb   uint8  // $2231 bits 1..0 (clamped to 2): bits-per-pixel selector (0=8bpp, 1=4bpp, 2=2bpp)
	dsa     uint32 // $2232/$2233/$2234 (24-bit) source start address
	dda     uint32 // $2235/$2236/$2237 (24-bit) destination start address
	dmaLine uint8  // bsnes dma.line: cycles 0..15 across consecutive CC2 triggers; reset to 0 on dmaen=0

	// BRF: bitmap register file. $2240-$224F. CC2 reads BRF[0..7] when
	// dma.line is even, BRF[8..15] when odd. Writes to $2247 and $224F
	// trigger dmaCC2 if dmaen && cden && !cdsel
	// (bsnes/sfc/coprocessor/sa1/io.cpp:371-401).
	brf [16]uint8

	// bwramDMA = bsnes mmio.bwram.dma. Set true when a CC1 trigger
	// arrives at $2236 (dmaen && cden && cdsel). Cleared when $2231
	// chdend bit is written. While true, the cartridge BW-RAM CPU read
	// path routes through DMACC1Read (bsnes bwram.cpp:31).
	bwramDMA bool

	// Normal (direct) DMA. bsnes/sfc/coprocessor/sa1/dma.cpp:1-46.
	// dtc is the 16-bit terminal counter ($2238/$2239); it
	// post-decrements through `while(mmio.dtc--)` so after a run of N
	// bytes the residual is 0xFFFF. dmaIRQFlag matches bsnes
	// mmio.dma_irqfl set on completion (io.cpp:44); it is consumed
	// only by the SA-1-side $2301 read (no S-CPU consumer). bwram is
	// the BW-RAM byte slice the cartridge supplies at attach time so
	// the BW-RAM source/destination dmaNormal sub-cases can run
	// without knowing about cartridge layout.
	dtc         uint16
	dmaIRQFlag  bool
	bwram       []byte
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

// ReadIRAMCPU returns the byte at the given 11-bit-mirrored offset from
// the S-CPU side of the I-RAM bus. Reads are unconditional (no SIWP
// gate). bsnes/sfc/coprocessor/sa1/iram.cpp:20-23.
func (d *Device) ReadIRAMCPU(off uint32) uint8 {
	return d.iram[off&0x7FF]
}

// WriteIRAMCPU writes the byte at the given 11-bit-mirrored offset
// from the S-CPU side, gated by SIWP per-256-byte. Writes to a
// protected block are silently dropped.
// bsnes/sfc/coprocessor/sa1/iram.cpp:25-29.
func (d *Device) WriteIRAMCPU(off uint32, val uint8) {
	if d.siwp&(1<<((off>>8)&7)) == 0 {
		return
	}
	d.iram[off&0x7FF] = val
}

// ReadIRAMSA1 returns the byte at the given 11-bit-mirrored offset
// from the SA-1 side. Reads are unconditional (no CIWP gate).
// bsnes/sfc/coprocessor/sa1/iram.cpp:31-33.
func (d *Device) ReadIRAMSA1(off uint32) uint8 {
	return d.iram[off&0x7FF]
}

// WriteIRAMSA1 writes the byte at the given 11-bit-mirrored offset
// from the SA-1 side, gated by CIWP per-256-byte.
// bsnes/sfc/coprocessor/sa1/iram.cpp:35-38.
func (d *Device) WriteIRAMSA1(off uint32, val uint8) {
	if d.ciwp&(1<<((off>>8)&7)) == 0 {
		return
	}
	d.iram[off&0x7FF] = val
}

// SCPUNMIOverrideEnabled reports whether $2209 bit 4 (cpu_nvsw) is
// set, in which case S-CPU reads of $00:FFEA/$00:FFEB return
// SNV-low/high instead of ROM. bsnes/sfc/coprocessor/sa1/rom.cpp:23-24.
func (d *Device) SCPUNMIOverrideEnabled() bool { return d.cpuNVSW }

// SCPUIRQOverrideEnabled reports whether $2209 bit 6 (cpu_ivsw) is
// set, in which case S-CPU reads of $00:FFEE/$00:FFEF return
// SIV-low/high. bsnes/sfc/coprocessor/sa1/rom.cpp:25-26.
func (d *Device) SCPUIRQOverrideEnabled() bool { return d.cpuIVSW }

// SCPUNMIVector returns the override target for the S-CPU native
// NMI vector ($220C/$220D SNV).
func (d *Device) SCPUNMIVector() uint16 { return d.snv }

// SCPUIRQVector returns the override target for the S-CPU native
// IRQ vector ($220E/$220F SIV).
func (d *Device) SCPUIRQVector() uint16 { return d.siv }

// SA1ResetVector returns the SA-1-internal reset vector ($2203/$2204
// CRV). Consumed by a future SA-1 CPU instance during reset
// dispatch. bsnes/sfc/coprocessor/sa1/io.cpp:176-177.
func (d *Device) SA1ResetVector() uint16 { return d.crv }

// SA1NMIVector returns the SA-1-internal NMI vector ($2205/$2206
// CNV). bsnes sa1.cpp:60 sets r.vector = mmio.cnv on SA-1 NMI.
func (d *Device) SA1NMIVector() uint16 { return d.cnv }

// SA1IRQVector returns the SA-1-internal IRQ vector ($2207/$2208
// CIV). bsnes sa1.cpp:67/73/77 sets r.vector = mmio.civ on
// timer/dma/sa1_irq. io.cpp:184-185.
func (d *Device) SA1IRQVector() uint16 { return d.civ }

// SCPUMessage returns the 4-bit message the S-CPU last sent to the
// SA-1 via $2200 bits 0..3 (smeg). Symmetric to the cmeg path
// exposed via $2300 bits 0..3. bsnes io.cpp:39 + 125; the future
// SA-1 CPU will consume this when it reads $2301.
func (d *Device) SCPUMessage() uint8 { return d.scpuMessage }

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

// inIRAMWindow reports whether addr is in the S-CPU-visible I-RAM
// window at $00-3F:3000-37FF + $80-BF:3000-37FF.
// bsnes/sfc/coprocessor/sa1/iram.cpp:1-6 conflict mask documents this
// range; bsnes manifest at cartridge/load.cpp:327 installs IRAM::readCPU
// here.
func inIRAMWindow(addr uint32) (uint32, bool) {
	bank := (addr >> 16) & 0xff
	off := addr & 0xffff
	if !((bank <= 0x3f) || (bank >= 0x80 && bank <= 0xbf)) {
		return 0, false
	}
	if off < 0x3000 || off > 0x37FF {
		return 0, false
	}
	return off - 0x3000, true
}

// Read implements the cartridge coprocessor register window.
func (d *Device) Read(addr uint32) (uint8, bool) {
	if off, ok := inIRAMWindow(addr); ok {
		return d.ReadIRAMCPU(off), true
	}
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
	if off, ok := inIRAMWindow(addr); ok {
		d.WriteIRAMCPU(off, val)
		return true
	}
	reg, ok := mapped(addr)
	if !ok {
		return false
	}
	switch regBase + reg {
	case 0x2200:
		// CCNT — only the smeg low nibble is decoded here (the
		// S-CPU → SA-1 message). Bits 7/6/5/4 (sa1_irq pulse,
		// sa1_rdyb, sa1_resb, sa1_nmi) require an SA-1 CPU
		// consumer and are intentionally not modelled. Byte
		// mirror in Regs[] preserves them.
		d.scpuMessage = val & 0x0F
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
	case 0x2209:
		// SCNT — bit 7 cpu_irq pulse: SA-1 → S-CPU IRQ trigger
		// (bsnes io.cpp:258-264). Bit 6 cpu_ivsw + bit 4 cpu_nvsw:
		// S-CPU vector override switches (e12ca3b). Bits 0..3 cmeg:
		// SA-1 → S-CPU message; observable via $2300 low nibble.
		// IRQ assertion through irqTarget is gated by the existing
		// CPUIRQPending = cpuIRQFlag && cpuIRQEnable predicate;
		// pollSA1IRQ raises the line when SIE bit 7 is set.
		if val&0x80 != 0 {
			d.cpuIRQFlag = true
		}
		d.cpuIVSW = val&0x40 != 0
		d.cpuNVSW = val&0x10 != 0
		d.cpuMessage = val & 0x0F
	case 0x2203:
		d.crv = d.crv&0xFF00 | uint16(val)
	case 0x2204:
		d.crv = d.crv&0x00FF | uint16(val)<<8
	case 0x2205:
		d.cnv = d.cnv&0xFF00 | uint16(val)
	case 0x2206:
		d.cnv = d.cnv&0x00FF | uint16(val)<<8
	case 0x2207:
		d.civ = d.civ&0xFF00 | uint16(val)
	case 0x2208:
		d.civ = d.civ&0x00FF | uint16(val)<<8
	case 0x220c:
		d.snv = d.snv&0xFF00 | uint16(val)
	case 0x220d:
		d.snv = d.snv&0x00FF | uint16(val)<<8
	case 0x220e:
		d.siv = d.siv&0xFF00 | uint16(val)
	case 0x220f:
		d.siv = d.siv&0x00FF | uint16(val)<<8
	case 0x2229:
		// SIWP — S-CPU I-RAM write protection (1 bit per 256 bytes).
		d.siwp = val
	case 0x222a:
		// CIWP — SA-1 I-RAM write protection (1 bit per 256 bytes).
		d.ciwp = val
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
	case 0x2230: // DCNT — DMA control. bsnes io.cpp:347-358.
		d.dmaen = val&0x80 != 0
		d.dprio = val&0x40 != 0
		d.cden = val&0x20 != 0
		d.cdsel = val&0x10 != 0
		d.dmaDD = (val >> 2) & 0x01
		d.dmaSD = val & 0x03
		if !d.dmaen {
			d.dmaLine = 0
		}
	case 0x2231: // CDMA — character DMA parameters. bsnes io.cpp:495-503.
		d.chdend = val&0x80 != 0
		d.dmasize = (val >> 2) & 0x07
		if d.dmasize > 5 {
			d.dmasize = 5
		}
		d.dmacb = val & 0x03
		if d.dmacb > 2 {
			d.dmacb = 2
		}
		// chdend resets the BW-RAM CC1 latch per bsnes io.cpp:500.
		if d.chdend {
			d.bwramDMA = false
		}
	case 0x2232: // SDA low. bsnes io.cpp:507.
		d.dsa = (d.dsa & 0xffff00) | uint32(val)
	case 0x2233: // SDA mid. bsnes io.cpp:508.
		d.dsa = (d.dsa & 0xff00ff) | uint32(val)<<8
	case 0x2234: // SDA high. bsnes io.cpp:509.
		d.dsa = (d.dsa & 0x00ffff) | uint32(val)<<16
	case 0x2235: // DDA low. bsnes io.cpp:512.
		d.dda = (d.dda & 0xffff00) | uint32(val)
	case 0x2236: // DDA mid. bsnes io.cpp:513-521.
		d.dda = (d.dda & 0xff00ff) | uint32(val)<<8
		if d.dmaen {
			switch {
			case d.cden && d.cdsel:
				d.dmaCC1()
			case !d.cden && d.dmaDD == 0: // dd == DestIRAM
				d.dmaNormal()
			}
		}
	case 0x2237: // DDA high. bsnes io.cpp:523-528.
		d.dda = (d.dda & 0x00ffff) | uint32(val)<<16
		if d.dmaen && !d.cden && d.dmaDD == 1 { // dd == DestBWRAM
			d.dmaNormal()
		}
	case 0x2238: // DTC low. bsnes io.cpp:365.
		d.dtc = (d.dtc & 0xff00) | uint16(val)
	case 0x2239: // DTC high. bsnes io.cpp:366.
		d.dtc = (d.dtc & 0x00ff) | uint16(val)<<8
	case 0x2240, 0x2241, 0x2242, 0x2243, 0x2244, 0x2245, 0x2246:
		d.brf[(regBase+reg)-0x2240] = val
	case 0x2247: // BRF[7] — write triggers dmaCC2 if armed. bsnes io.cpp:379-385.
		d.brf[7] = val
		if d.dmaen && d.cden && !d.cdsel {
			d.dmaCC2()
		}
	case 0x2248, 0x2249, 0x224a, 0x224b, 0x224c, 0x224d, 0x224e:
		d.brf[(regBase+reg)-0x2240] = val
	case 0x224f: // BRF[15] — write triggers dmaCC2 if armed. bsnes io.cpp:394-400.
		d.brf[15] = val
		if d.dmaen && d.cden && !d.cdsel {
			d.dmaCC2()
		}
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

// dmaCC2 performs one type-2 character-conversion DMA line per
// bsnes/sfc/coprocessor/sa1/dma.cpp:110-128. Triggered synchronously
// from $2247 / $224F writes when dmaen && cden && !cdsel. Synthesizes
// one tile-line of planar bitmap data from BRF[0..7] (even line) or
// BRF[8..15] (odd line) and writes it into I-RAM at an address
// derived from DDA + dma.line + dmacb. dmacb selects bpp (0=8bpp,
// 1=4bpp, 2=2bpp). Writes go through WriteIRAMSA1 so CIWP gates them.
func (d *Device) dmaCC2() {
	base := (uint(d.dmaLine) & 1) << 3
	bpp := uint(2) << (2 - uint(d.dmacb))
	addr := uint(d.dda) & 0x07ff
	addr &^= (1 << (7 - uint(d.dmacb))) - 1
	addr += (uint(d.dmaLine) & 8) * bpp
	addr += (uint(d.dmaLine) & 7) * 2
	for byteIdx := uint(0); byteIdx < bpp; byteIdx++ {
		var output uint8
		for bit := uint(0); bit < 8; bit++ {
			output |= ((d.brf[base+bit] >> byteIdx) & 1) << (7 - bit)
		}
		dest := addr + ((byteIdx & 6) << 3) + (byteIdx & 1)
		d.WriteIRAMSA1(uint32(dest), output)
	}
	d.dmaLine = (d.dmaLine + 1) & 0x0f
}

// dmaCC1 arms the type-1 character-conversion DMA per bsnes
// dma.cpp:48-56. Sets bwramDMA so subsequent S-CPU BW-RAM reads route
// through DMACC1Read; raises chdma_irqfl. The S-CPU IRQ line will pick
// up the chdma channel via CPUIRQPending once chdma_irqen is set.
func (d *Device) dmaCC1() {
	d.bwramDMA = true
	d.chdmaIRQFlag = true
}

// BWRAMDMAActive reports whether a CC1 conversion is currently armed.
// Cartridge-side BW-RAM CPU read paths use this to decide whether to
// route through DMACC1Read.
func (d *Device) BWRAMDMAActive() bool { return d.bwramDMA }

// SetBWRAMSlice installs the cartridge BW-RAM slice consulted by
// dmaNormal sub-cases that read or write BW-RAM (ROM→BWRAM,
// BWRAM→IRAM, IRAM→BWRAM). The cartridge sets this at attach time;
// it is independent of the per-S-CPU-access translation done by
// sa1BWRAMAddress. dmaNormal applies a power-of-2 wrap mask
// (len(bwram)-1) per bsnes bwram.cpp:11/17 (`bus.mirror(addr, size())`).
func (d *Device) SetBWRAMSlice(ram []byte) { d.bwram = ram }

// DMAIRQPending reports whether mmio.dma_irqfl is set. Bsnes exposes
// it via $2301 bit 5 (SA-1-side read only); there is no S-CPU consumer
// today so this accessor exists for state inspection and round-trip
// tests.
func (d *Device) DMAIRQPending() bool { return d.dmaIRQFlag }

// DTCRaw / DSARaw / DDARaw expose the post-state register values for
// tests. After a normal DMA run of N bytes, DTC is 0xFFFF (one past
// zero per `while(dtc--)`) and DSA/DDA are advanced by N.
func (d *Device) DTCRaw() uint16 { return d.dtc }
func (d *Device) DSARaw() uint32 { return d.dsa }
func (d *Device) DDARaw() uint32 { return d.dda }

// DMACC1Read implements the lazy type-1 character-conversion read per
// bsnes dma.cpp:64-107. addr is the bsnes-translated BW-RAM address
// (cartridge.go:431-440 sa1BWRAMAddress already produces the same
// translation that bsnes bwram.cpp:24-29 applies before invoking
// dmaCC1Read). bwram is the cartridge's BW-RAM slice.
//
// On a character-aligned address ((addr & charmask)==0) the next
// character (8 lines × bpp bytes) is synthesized from BW-RAM into
// internal I-RAM at dda + (y<<1) + ((byte&6)<<3) + (byte&1). All
// I-RAM reads/writes inside this path go through the raw iram slice
// (bsnes uses iram.write at dma.cpp:101 which bypasses CIWP per
// iram.cpp:14-18). Returns iram[(dda + (addr & charmask)) & 0x07ff].
//
// Returns 0 if BWRAMDMAActive() is false (defensive — callers should
// gate first).
func (d *Device) DMACC1Read(addr uint32, bwram []byte) uint8 {
	if !d.bwramDMA {
		return 0
	}
	if len(bwram) == 0 {
		return 0
	}
	bwmask := uint32(len(bwram) - 1)
	charmask := (uint32(1) << (6 - uint32(d.dmacb))) - 1
	if addr&charmask == 0 {
		bpp := uint32(2) << (2 - uint32(d.dmacb))
		bpl := (uint32(8) << uint32(d.dmasize)) >> uint32(d.dmacb)
		tile := ((addr - d.dsa) & bwmask) >> (6 - uint32(d.dmacb))
		ty := tile >> uint32(d.dmasize)
		tx := tile & ((uint32(1) << uint32(d.dmasize)) - 1)
		bwaddr := d.dsa + ty*8*bpl + tx*bpp
		for y := uint32(0); y < 8; y++ {
			var data uint64
			for byteIdx := uint32(0); byteIdx < bpp; byteIdx++ {
				data |= uint64(bwram[(bwaddr+byteIdx)&bwmask]) << (byteIdx << 3)
			}
			bwaddr += bpl
			var out [8]uint8
			for x := uint32(0); x < 8; x++ {
				out[0] |= uint8(data&1) << (7 - x)
				data >>= 1
				out[1] |= uint8(data&1) << (7 - x)
				data >>= 1
				if d.dmacb == 2 {
					continue
				}
				out[2] |= uint8(data&1) << (7 - x)
				data >>= 1
				out[3] |= uint8(data&1) << (7 - x)
				data >>= 1
				if d.dmacb == 1 {
					continue
				}
				out[4] |= uint8(data&1) << (7 - x)
				data >>= 1
				out[5] |= uint8(data&1) << (7 - x)
				data >>= 1
				out[6] |= uint8(data&1) << (7 - x)
				data >>= 1
				out[7] |= uint8(data&1) << (7 - x)
				data >>= 1
			}
			for byteIdx := uint32(0); byteIdx < bpp; byteIdx++ {
				p := d.dda + (y << 1) + ((byteIdx & 6) << 3) + (byteIdx & 1)
				d.iram[p&0x07ff] = out[byteIdx]
			}
		}
	}
	return d.iram[(d.dda+(addr&charmask))&0x07ff]
}

// dmaNormal performs the type-0 (direct) byte-copy DMA per
// bsnes/sfc/coprocessor/sa1/dma.cpp:1-46. Triggered synchronously
// from $2236 (cden==0 && dd==DestIRAM) or $2237 (cden==0 &&
// dd==DestBWRAM). Loops dtc times, post-incrementing dsa/dda each
// iteration. The four (sd, dd) sub-cases dispatch read/write
// independently; mismatched pairs (e.g. sd=3 reserved) fall through
// without reading or writing — bsnes models this as `data = r.mdr`
// initial value with no `if` block matching, so the byte never
// reaches a destination. After the loop dtc is 0xFFFF (one past 0)
// and dma_irqfl is set. SA-1-thread step()/conflict() penalties are
// skipped — there is no SA-1 thread to charge.
//
// ROM source uses the existing romReader callback (matches the path
// used by sa1VBRReader for VBR reads). BW-RAM source/destination
// uses the bwram slice installed by SetBWRAMSlice. I-RAM source/
// destination uses the internal iram[] raw access (bsnes iram.read/
// iram.write at dma.cpp:22, 31, 39 are raw, bypassing CIWP).
func (d *Device) dmaNormal() {
	// Mirror bsnes `while(mmio.dtc--)`: evaluate dtc, post-decrement;
	// loop body runs while dtc was nonzero before decrement. Initial
	// dtc=N → N iterations, residual dtc=0xFFFF (one past 0).
	for {
		if d.dtc == 0 {
			d.dtc = 0xFFFF
			break
		}
		d.dtc--
		source := d.dsa
		target := d.dda
		d.dsa = (d.dsa + 1) & 0x00FFFFFF
		d.dda = (d.dda + 1) & 0x00FFFFFF
		switch {
		case d.dmaSD == 0 && d.dmaDD == 1: // ROM → BWRAM
			data := d.readROMSource(source)
			d.writeBWRAMRaw(target, data)
		case d.dmaSD == 0 && d.dmaDD == 0: // ROM → IRAM
			data := d.readROMSource(source)
			d.iram[target&0x07FF] = data
		case d.dmaSD == 1 && d.dmaDD == 0: // BWRAM → IRAM
			data := d.readBWRAMRaw(source)
			d.iram[target&0x07FF] = data
		case d.dmaSD == 2 && d.dmaDD == 1: // IRAM → BWRAM
			data := d.iram[source&0x07FF]
			d.writeBWRAMRaw(target, data)
		}
	}
	d.dmaIRQFlag = true
}

// readROMSource resolves an SA-1-side ROM byte using the installed
// romReader (which routes through CPUROMAddress per
// internal/cartridge/cartridge.go:271-292). Returns 0xFF if no
// reader is installed.
func (d *Device) readROMSource(addr uint32) uint8 {
	if d.romReader == nil {
		return 0xFF
	}
	return d.romReader(addr & 0x00FFFFFF)
}

// readBWRAMRaw / writeBWRAMRaw access the cartridge BW-RAM slice
// directly. bsnes bwram.cpp:9-19 wraps the address with
// `bus.mirror(address, size())`, equivalent to `addr & (size-1)`
// since BW-RAM sizes are power-of-2.
func (d *Device) readBWRAMRaw(addr uint32) uint8 {
	if len(d.bwram) == 0 {
		return 0xFF
	}
	return d.bwram[addr&uint32(len(d.bwram)-1)]
}

func (d *Device) writeBWRAMRaw(addr uint32, val uint8) {
	if len(d.bwram) == 0 {
		return
	}
	d.bwram[addr&uint32(len(d.bwram)-1)] = val
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
// bsnes drives the S-CPU IRQ from either the cpu_irq pulse channel
// ($2209 bit 7 → cpu_irqfl, gated by SIE bit 7 cpu_irqen) or the
// CHDMA-completion channel (chdma_irqfl, gated by SIE bit 5
// chdma_irqen); per io.cpp:144-156 + 167-171 the line asserts when
// either pending+enabled pair is true and deasserts only when both
// flags are clear.
func (d *Device) CPUIRQPending() bool {
	return (d.cpuIRQFlag && d.cpuIRQEnable) || (d.chdmaIRQFlag && d.chdmaEnable)
}

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

// cpuStatus composes the $2300 SFR readback per
// bsnes/sfc/coprocessor/sa1/io.cpp:7-15 and snes9x/sa1.cpp:184: bit
// 7 cpu_irqfl, bit 6 cpu_ivsw, bit 5 chdma_irqfl, bit 4 cpu_nvsw,
// bits 0..3 cmeg.
func (d *Device) cpuStatus() uint8 {
	var v uint8
	if d.cpuIRQFlag {
		v |= 0x80
	}
	if d.cpuIVSW {
		v |= 0x40
	}
	if d.chdmaIRQFlag {
		v |= 0x20
	}
	if d.cpuNVSW {
		v |= 0x10
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
	IRAM         [0x800]uint8
	SIWP         uint8
	CIWP         uint8
	CPUNVSW      bool
	CPUIVSW      bool
	SNV          uint16
	SIV          uint16
	CRV          uint16
	CNV          uint16
	CIV          uint16
	SCPUMessage  uint8
	DMAEN        bool
	DPRIO        bool
	CDEN         bool
	CDSEL        bool
	DMADD        uint8
	DMASD        uint8
	CHDEND       bool
	DMASIZE      uint8
	DMACB        uint8
	DSA          uint32
	DDA          uint32
	DMALine      uint8
	BRF          [16]uint8
	BWRAMDMA     bool
	DTC          uint16
	DMAIRQFlag   bool
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
		IRAM:         d.iram,
		SIWP:         d.siwp,
		CIWP:         d.ciwp,
		CPUNVSW:      d.cpuNVSW,
		CPUIVSW:      d.cpuIVSW,
		SNV:          d.snv,
		CRV:          d.crv,
		CNV:          d.cnv,
		CIV:          d.civ,
		SIV:          d.siv,
		SCPUMessage:  d.scpuMessage,
		DMAEN:        d.dmaen,
		DPRIO:        d.dprio,
		CDEN:         d.cden,
		CDSEL:        d.cdsel,
		DMADD:        d.dmaDD,
		DMASD:        d.dmaSD,
		CHDEND:       d.chdend,
		DMASIZE:      d.dmasize,
		DMACB:        d.dmacb,
		DSA:          d.dsa,
		DDA:          d.dda,
		DMALine:      d.dmaLine,
		BRF:          d.brf,
		BWRAMDMA:     d.bwramDMA,
		DTC:          d.dtc,
		DMAIRQFlag:   d.dmaIRQFlag,
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
	d.iram = s.IRAM
	d.siwp = s.SIWP
	d.ciwp = s.CIWP
	d.cpuNVSW = s.CPUNVSW
	d.cpuIVSW = s.CPUIVSW
	d.snv = s.SNV
	d.siv = s.SIV
	d.crv = s.CRV
	d.cnv = s.CNV
	d.civ = s.CIV
	d.scpuMessage = s.SCPUMessage
	d.dmaen = s.DMAEN
	d.dprio = s.DPRIO
	d.cden = s.CDEN
	d.cdsel = s.CDSEL
	d.dmaDD = s.DMADD
	d.dmaSD = s.DMASD
	d.chdend = s.CHDEND
	d.dmasize = s.DMASIZE
	d.dmacb = s.DMACB
	d.dsa = s.DSA
	d.dda = s.DDA
	d.dmaLine = s.DMALine
	d.brf = s.BRF
	d.bwramDMA = s.BWRAMDMA
	d.dtc = s.DTC
	d.dmaIRQFlag = s.DMAIRQFlag
	return nil
}
