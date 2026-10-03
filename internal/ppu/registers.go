package ppu

// Register Logic

type PPURegisters struct {
	// PPU1OpenBus and PPU2OpenBus are the S-PPU open-bus latches. They are
	// separate from the CPU bus MDR and from each other.
	PPU1OpenBus uint8
	PPU2OpenBus uint8

	// VRAM Access
	VRAMIncMode uint8  // $2115 VMAIN
	VRAMAddr    uint16 // $2116 VMADDL/H
	VRAMReadBuf uint16 // Internal Read Buffer

	// OAM Access
	OBSEL       uint8  // $2101 OBSEL (Size, Base)
	OAMBaseAddr uint16 // Base address from $2102/$2103
	OAMPriority bool   // $2103 bit 7 priority rotation enable
	OAMAddr     uint16 // $2102/03 OAMADDL/H (10-bit byte address)
	OAMWriteBuf uint8  // Use for ensuring double-write? No, OAM writes are byte-wise usually.
	// Actually $2104 OAMDATA writes to OAMAddr.
	// If OAMAddr is even (low byte), it buffers? Or just writes.
	// "Writes to $2104... if address even... data is latched... if address odd... data+latch written"
	// S-PPU logic is specific.

	// CGRAM Access
	CGRAMAddr      uint8 // $2121 CGADD
	CGRAMLatch     uint8 // Internal Latch for 2nd write
	CGRAMWritePair bool  // Toggle for High/Low byte

	// Background Registers
	INIDISP uint8 // $2100
	BGMode  uint8 // $2105
	MOSAIC  uint8 // $2106 (Placeholder)

	// Color Math
	CGWSEL  uint8 // $2130
	CGADSUB uint8 // $2131
	COLDATA uint8 // $2132
	SETINI  uint8 // $2133
	FixedR  uint8 // 5-bit fixed color red
	FixedG  uint8 // 5-bit fixed color green
	FixedB  uint8 // 5-bit fixed color blue
	// Background Registers
	BG1SC uint8 // $2107
	BG2SC uint8 // $2108
	BG3SC uint8 // $2109
	BG4SC uint8 // $210A

	BG12NBA uint8 // $210B (BG1/2)
	BG34NBA uint8 // $210C (BG3/4)

	BG1HOFS uint16 // $210D
	BG1VOFS uint16 // $210E
	BG2HOFS uint16 // $210F
	BG2VOFS uint16 // $2110
	BG3HOFS uint16 // $2111
	BG3VOFS uint16 // $2112
	BG4HOFS uint16 // $2113
	BG4VOFS uint16 // $2114

	// Scroll latches. Horizontal scroll uses the previous fine-scroll bits
	// from latch 2 and the coarse bits from latch 1; vertical scroll uses
	// latch 1 directly.
	BGScrollPrev  uint8 // Previous write byte; kept for save-state compatibility.
	BGScrollPrev2 uint8

	// Layer Control
	W12SEL  uint8 // $2123 Window mask settings for BG1/BG2
	W34SEL  uint8 // $2124 Window mask settings for BG3/BG4
	WOBJSEL uint8 // $2125 Window mask settings for OBJ/Color
	WH0     uint8 // $2126 Window 1 left
	WH1     uint8 // $2127 Window 1 right
	WH2     uint8 // $2128 Window 2 left
	WH3     uint8 // $2129 Window 2 right
	WBGLOG  uint8 // $212A BG window mask logic
	WOBJLOG uint8 // $212B OBJ/color window mask logic
	TM      uint8 // $212C Main Screen Designation
	TS      uint8 // $212D Sub Screen Designation
	TMW     uint8 // $212E Main screen window enable
	TSW     uint8 // $212F Sub screen window enable

	// Mode 7 matrix and signed multiply registers.
	M7SEL   uint8  // $211A
	M7A     uint16 // $211B
	M7B     uint16 // $211C
	M7C     uint16 // $211D
	M7D     uint16 // $211E
	M7X     uint16 // $211F, 13-bit
	M7Y     uint16 // $2120, 13-bit
	M7HOFS  uint16 // $210D mirror used by Mode 7, 13-bit
	M7VOFS  uint16 // $210E mirror used by Mode 7, 13-bit
	M7Latch uint8  // shared Mode 7 write-twice latch
	M7Large bool
	M7Fill  bool
	M7XFlip bool
	M7YFlip bool
}

func (p *PPU) vramWordAddr(addr uint16) uint16 {
	switch (p.VRAMIncMode >> 2) & 0x03 {
	case 1:
		return (addr & 0xFF00) | ((addr << 3) & 0x00F8) | ((addr >> 5) & 0x0007)
	case 2:
		return (addr & 0xFE00) | ((addr << 3) & 0x01F8) | ((addr >> 6) & 0x0007)
	case 3:
		return (addr & 0xFC00) | ((addr << 3) & 0x03F8) | ((addr >> 7) & 0x0007)
	default:
		return addr
	}
}

func (p *PPU) readVRAMWord(addr uint16) uint16 {
	if p.vramReadsBlocked() {
		return 0
	}
	wordAddr := p.vramWordAddr(addr) & 0x7FFF
	idx := int(wordAddr) * 2
	return uint16(p.VRAM[idx]) | (uint16(p.VRAM[idx+1]) << 8)
}

func (p *PPU) preloadVRAMReadBuf() {
	p.VRAMReadBuf = p.readVRAMWord(p.VRAMAddr)
}

func oamDataIndex(addr uint16) uint16 {
	if addr&0x0200 != 0 {
		return 0x0200 | (addr & 0x001F)
	}
	return addr & 0x01FF
}

func (p *PPU) m7MultiplyResult() int32 {
	return int32(int16(p.M7A)) * int32(int8(p.M7B>>8))
}

func (p *PPU) noteMode7Latch(addr, value uint16) {
	if p.Mode7LatchHook != nil {
		p.Mode7LatchHook(addr, value)
	}
	if p.Mode7LatchEventHook != nil {
		p.Mode7LatchEventHook(Mode7LatchEvent{
			Addr:       addr,
			Value:      value,
			FrameCount: p.FrameCount,
			HCounter:   p.hCounter,
			VCounter:   p.vCounter,
		})
	}
}

func (p *PPU) noteMode7MatrixPair(firstAddr, firstValue, nextAddr, nextValue uint16) {
	e := Mode7MatrixPairEvent{
		FirstAddr:  firstAddr,
		FirstValue: firstValue,
		NextAddr:   nextAddr,
		NextValue:  nextValue,
		FrameCount: p.FrameCount,
		HCounter:   p.hCounter,
		VCounter:   p.vCounter,
	}
	p.lastM7Pair = e
	if p.Mode7MatrixPairHook != nil {
		p.Mode7MatrixPairHook(e)
	}
}

func (p *PPU) noteMode7MatrixWrite(addr, value uint16) {
	if addr <= 0x2121 {
		p.m7PairValid[addr&0xFF] = !p.m7PairValid[addr&0xFF]
	}
	p.noteMode7Latch(addr, value)
}

func (p *PPU) noteMode7MatrixPairWrite(firstAddr, firstValue, nextAddr, nextValue uint16) {
	idx := nextAddr & 0xFF
	complete := p.m7PairValid[idx]
	p.noteMode7MatrixWrite(nextAddr, nextValue)
	if complete {
		p.noteMode7MatrixPair(firstAddr, firstValue, nextAddr, nextValue)
	}
}

func (p *PPU) readPPU1OpenBus() uint8 {
	return p.PPU1OpenBus
}

func (p *PPU) readPPU2OpenBus() uint8 {
	return p.PPU2OpenBus
}

func (p *PPU) readPPU1(val uint8) uint8 {
	p.PPU1OpenBus = val
	return val
}

func (p *PPU) readPPU2(val uint8) uint8 {
	p.PPU2OpenBus = val
	return val
}

func (p *PPU) ReadRegister(addr uint16) uint8 {
	return p.ReadRegisterWithCPUOpenBus(addr, 0)
}

func (p *PPU) ReadRegisterWithCPUOpenBus(addr uint16, cpuMDR uint8) uint8 {
	// Addr is 0x21xx usually. We only get lower byte or full?
	// snes.go IODevice passes full addr. Masked 0xFF.
	reg := addr & 0xFF

	switch reg {
	case 0x04, 0x05, 0x06, 0x08, 0x09, 0x0A, 0x14, 0x15,
		0x16, 0x18, 0x19, 0x1A, 0x24, 0x25, 0x26, 0x28,
		0x29, 0x2A:
		return p.readPPU1OpenBus()
	case 0x37: // $2137 SLHV
		p.LatchBeam()
		return p.readPPU2OpenBus()
	case 0x38: // $2138 OAMDATAREAD
		addr := p.OAMAddr & 0x03FF
		p.OAMAddr = (p.OAMAddr + 1) & 0x03FF
		return p.readPPU1(p.OAM[oamDataIndex(addr)])
	case 0x3C: // $213C OPHCT
		if !p.hReadHigh {
			p.hReadHigh = true
			return p.readPPU2(uint8(p.latchedH))
		}
		p.hReadHigh = false
		return p.readPPU2((p.PPU2OpenBus & 0xFE) | uint8(p.latchedH>>8)&0x01)
	case 0x3D: // $213D OPVCT
		if !p.vReadHigh {
			p.vReadHigh = true
			return p.readPPU2(uint8(p.latchedV))
		}
		p.vReadHigh = false
		return p.readPPU2((p.PPU2OpenBus & 0xFE) | uint8(p.latchedV>>8)&0x01)
	// VRAM Read
	case 0x39: // $2139 VMDATAL
		val := uint8(p.VRAMReadBuf)
		if (p.VRAMIncMode & 0x80) == 0 {
			p.preloadVRAMReadBuf()
			p.incrementVRAMAddr()
		}
		return p.readPPU1(val)

	case 0x3A: // $213A VMDATAH
		val := uint8(p.VRAMReadBuf >> 8)
		if (p.VRAMIncMode & 0x80) != 0 {
			p.preloadVRAMReadBuf()
			p.incrementVRAMAddr()
		}
		return p.readPPU1(val)
	case 0x3B: // $213B CGDATAREAD
		idx := int(p.CGRAMAddr) * 2
		if !p.CGRAMWritePair {
			p.CGRAMWritePair = true
			return p.readPPU2(p.CGRAM[idx])
		}
		p.CGRAMWritePair = false
		p.CGRAMAddr++
		return p.readPPU2((p.PPU2OpenBus & 0x80) | (p.CGRAM[idx+1] & 0x7F))
	case 0x34, 0x35, 0x36: // $2134-$2136 MPYL/MPYM/MPYH
		shift := (reg - 0x34) * 8
		return p.readPPU2(uint8(uint32(p.m7MultiplyResult()) >> shift))
	case 0x3E: // $213E STAT77
		val := (p.PPU1OpenBus & 0x10) | 0x01
		if p.RangeOver {
			val |= 0x40
		}
		if p.TimeOver {
			val |= 0x80
		}
		return p.readPPU1(val)
	case 0x3F: // $213F STAT78
		return p.ReadSTAT78(true)

		// ... OAM/CGRAM Reads ...
	}

	return cpuMDR
}

// ReadSTAT78 reads $213F using the CPU WRIO latch-enable state.
func (p *PPU) ReadSTAT78(counterLatchEnabled bool) uint8 {
	val := (p.PPU2OpenBus & 0x20) | 0x03
	if p.palTiming {
		val |= 0x10
	}
	if p.stat78Field() {
		val |= 0x80
	}
	if !counterLatchEnabled || p.hvLatched {
		val |= 0x40
	}
	if counterLatchEnabled {
		p.hvLatched = false
	}
	p.hReadHigh = false
	p.vReadHigh = false
	return p.readPPU2(val)
}

func (p *PPU) WriteRegister(addr uint16, val uint8) {
	reg := addr & 0xFF

	switch reg {
	// VRAM
	case 0x15: // $2115 VMAIN
		p.VRAMIncMode = val
		if (val & 0x0C) != 0 {
			// fmt.Printf("PPU: VMAIN Remap detected! Val=%02X\n", val)
		}
	case 0x16: // $2116 VMADDL
		p.VRAMAddr = (p.VRAMAddr & 0xFF00) | uint16(val)
		p.preloadVRAMReadBuf()
	case 0x17: // $2117 VMADDH
		p.VRAMAddr = (p.VRAMAddr & 0x00FF) | (uint16(val) << 8)
		p.preloadVRAMReadBuf()

	case 0x18: // $2118 VMDATAL
		if !p.writesBlocked() {
			addr := p.vramWordAddr(p.VRAMAddr) & 0x7FFF
			p.writeVRAM(addr*2, val, 0x2118)
		}
		// VMAIN bit 7 = 0: increment after $2118 (low-byte) access. Matches
		// bsnes addressVRAM / io.cpp writes. The increment fires regardless
		// of whether the byte landed — bsnes io.cpp $2118 bumps vramAddress
		// unconditionally and only writeVRAM() short-circuits.
		if (p.VRAMIncMode & 0x80) == 0 {
			p.incrementVRAMAddr()
		}

	case 0x19: // $2119 VMDATAH
		if !p.writesBlocked() {
			addr := p.vramWordAddr(p.VRAMAddr) & 0x7FFF
			p.writeVRAM(addr*2+1, val, 0x2119)
		}
		// VMAIN bit 7 = 1: increment after $2119 (high-byte) access.
		if (p.VRAMIncMode & 0x80) != 0 {
			p.incrementVRAMAddr()
		}

	// OAM
	case 0x01: // OBSEL
		// fmt.Printf("PPU Write OBSEL: %02X\n", val)
		p.OBSEL = val
	case 0x02: // OAMADDL
		p.OAMBaseAddr = (p.OAMBaseAddr & 0x0200) | (uint16(val) << 1)
		p.OAMAddr = p.OAMBaseAddr
	case 0x03: // OAMADDH
		// Bit 0 is the 10th byte-address bit. Bit 7 controls priority rotation.
		p.OAMBaseAddr = (uint16(val&1) << 9) | (p.OAMBaseAddr & 0x01FE)
		p.OAMPriority = (val & 0x80) != 0
		p.OAMAddr = p.OAMBaseAddr

	case 0x04: // OAMDATA
		latchBit := p.OAMAddr & 1
		addr := p.OAMAddr & 0x03FF
		p.OAMAddr = (p.OAMAddr + 1) & 0x03FF

		if latchBit == 0 {
			p.OAMWriteBuf = val
		}
		// Active-display OAM writes are redirected to latch.oamAddress
		// rather than dropped (bsnes sfc/ppu/io.cpp:51-54). Address
		// bookkeeping (OAMAddr increment, write-buffer toggle) still
		// runs unconditionally.
		if p.writesBlocked() {
			redir := p.latchOAMAddr & 0x03FF
			if (redir & 0x0200) != 0 {
				p.writeOAM(oamDataIndex(redir), val, 0x2104)
			} else if latchBit == 1 {
				base := redir &^ 1
				if int(base+1) < OAMSize {
					p.writeOAM(base, p.OAMWriteBuf, 0x2104)
					p.writeOAM(base+1, val, 0x2104)
				}
			}
			break
		}
		if (addr & 0x0200) != 0 {
			p.writeOAM(oamDataIndex(addr), val, 0x2104)
			break
		}
		if latchBit == 1 {
			base := addr &^ 1
			if int(base+1) < OAMSize {
				p.writeOAM(base, p.OAMWriteBuf, 0x2104)
				p.writeOAM(base+1, val, 0x2104)
			}
		}

	// CGRAM
	case 0x21: // CGADD
		p.CGRAMAddr = val
		p.CGRAMWritePair = false // Reset toggle

	case 0x22: // CGDATA
		// Writes word. Toggle low/high. During active display with
		// force-blank off bsnes/ares redirect the byte to
		// latch.cgramAddress (the most-recently-rendered palette
		// index) instead of dropping (sfc/ppu/io.cpp:64-70). Toggle
		// and CGRAMAddr auto-increment still run unconditionally.
		blocked := p.writesBlocked()
		writeAddr := p.CGRAMAddr
		if blocked {
			writeAddr = p.latchCGRAMAddr
		}
		idx := int(writeAddr) * 2
		if !p.CGRAMWritePair {
			p.writeCGRAM(uint16(idx), val, 0x2122)
			p.CGRAMWritePair = true
		} else {
			p.writeCGRAM(uint16(idx+1), val&0x7F, 0x2122)
			p.CGRAMWritePair = false
			p.CGRAMAddr++
		}

	// Display Control
	case 0x00: // INIDISP
		p.INIDISP = val

	case 0x05: // BGMODE
		p.BGMode = val

	case 0x06: // MOSAIC
		p.MOSAIC = val

	// ...
	case 0x1A: // M7SEL
		p.M7SEL = val
		p.M7Large = (val & 0x80) != 0
		p.M7Fill = (val & 0x40) != 0
		p.M7YFlip = (val & 0x02) != 0
		p.M7XFlip = (val & 0x01) != 0

	case 0x1B: // M7A
		p.M7A = (uint16(val) << 8) | uint16(p.M7Latch)
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x211B, p.M7A)
	case 0x1C: // M7B
		p.M7B = (uint16(val) << 8) | uint16(p.M7Latch)
		p.M7Latch = val
		p.noteMode7MatrixPairWrite(0x211B, p.M7A, 0x211C, p.M7B)
	case 0x1D: // M7C
		p.M7C = (uint16(val) << 8) | uint16(p.M7Latch)
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x211D, p.M7C)
	case 0x1E: // M7D
		p.M7D = (uint16(val) << 8) | uint16(p.M7Latch)
		p.M7Latch = val
		p.noteMode7MatrixPairWrite(0x211D, p.M7C, 0x211E, p.M7D)
	case 0x1F: // M7X
		p.M7X = ((uint16(val) << 8) | uint16(p.M7Latch)) & 0x1FFF
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x211F, p.M7X)
	case 0x20: // M7Y
		p.M7Y = ((uint16(val) << 8) | uint16(p.M7Latch)) & 0x1FFF
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x2120, p.M7Y)

	// Color Math
	case 0x30: // CGWSEL
		p.CGWSEL = val

	case 0x31: // CGADSUB
		p.CGADSUB = val

	case 0x32: // COLDATA
		p.COLDATA = val
		c := val & 0x1F
		if (val & 0x20) != 0 {
			p.FixedR = c
		}
		if (val & 0x40) != 0 {
			p.FixedG = c
		}
		if (val & 0x80) != 0 {
			p.FixedB = c
		}
	case 0x33: // SETINI
		p.SETINI = val
		if (val & 0x04) != 0 {
			p.Height = 240
		} else {
			p.Height = 224
		}

	// BG Registers
	case 0x07: // BG1SC
		p.BG1SC = val
	case 0x08: // BG2SC
		p.BG2SC = val
	case 0x09: // BG3SC
		p.BG3SC = val
	case 0x0A: // BG4SC
		p.BG4SC = val

	case 0x0B: // BG12NBA
		p.BG12NBA = val
	case 0x0C: // BG34NBA
		p.BG34NBA = val

	case 0x0D: // BG1HOFS
		p.M7HOFS = ((uint16(val) << 8) | uint16(p.M7Latch)) & 0x1FFF
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x210D, p.M7HOFS)
		p.BG1HOFS = bgHOffset(val, p.BGScrollPrev, p.BGScrollPrev2)
		p.setBGHScrollLatch(val)
	case 0x0E: // BG1VOFS
		p.M7VOFS = ((uint16(val) << 8) | uint16(p.M7Latch)) & 0x1FFF
		p.M7Latch = val
		p.noteMode7MatrixWrite(0x210E, p.M7VOFS)
		p.BG1VOFS = bgVOffset(val, p.BGScrollPrev)
		p.BGScrollPrev = val

	case 0x0F: // BG2HOFS
		p.BG2HOFS = bgHOffset(val, p.BGScrollPrev, p.BGScrollPrev2)
		p.setBGHScrollLatch(val)
	case 0x10: // BG2VOFS
		p.BG2VOFS = bgVOffset(val, p.BGScrollPrev)
		p.BGScrollPrev = val

	case 0x11: // BG3HOFS
		p.BG3HOFS = bgHOffset(val, p.BGScrollPrev, p.BGScrollPrev2)
		p.setBGHScrollLatch(val)
	case 0x12: // BG3VOFS
		p.BG3VOFS = bgVOffset(val, p.BGScrollPrev)
		p.BGScrollPrev = val

	case 0x13: // BG4HOFS
		p.BG4HOFS = bgHOffset(val, p.BGScrollPrev, p.BGScrollPrev2)
		p.setBGHScrollLatch(val)
	case 0x14: // BG4VOFS
		p.BG4VOFS = bgVOffset(val, p.BGScrollPrev)
		p.BGScrollPrev = val

	// Layer Control
	case 0x23: // W12SEL
		p.W12SEL = val
	case 0x24: // W34SEL
		p.W34SEL = val
	case 0x25: // WOBJSEL
		p.WOBJSEL = val
	case 0x26: // WH0
		p.WH0 = val
	case 0x27: // WH1
		p.WH1 = val
	case 0x28: // WH2
		p.WH2 = val
	case 0x29: // WH3
		p.WH3 = val
	case 0x2A: // WBGLOG
		p.WBGLOG = val
	case 0x2B: // WOBJLOG
		p.WOBJLOG = val
	case 0x2C: // TM
		p.TM = val
	case 0x2D: // TS
		p.TS = val
	case 0x2E: // TMW
		p.TMW = val
	case 0x2F: // TSW
		p.TSW = val
	}
}

func bgHOffset(high, latch1, latch2 uint8) uint16 {
	return uint16(high)<<8 | uint16(latch1&^0x07) | uint16(latch2&0x07)
}

func bgVOffset(high, latch1 uint8) uint16 {
	return uint16(high)<<8 | uint16(latch1)
}

func (p *PPU) setBGHScrollLatch(val uint8) {
	p.BGScrollPrev = val
	p.BGScrollPrev2 = val
}

func (p *PPU) incrementVRAMAddr() {
	// Logic based on VMAIN (Translation, Step size)
	// Bits 0-1: Step Size
	// 00: 1 word
	// 01: 32 words
	// 10: 128 words
	// 11: 128 words
	step := uint16(1)
	switch p.VRAMIncMode & 0x03 {
	case 0:
		step = 1
	case 1:
		step = 32
	case 2, 3:
		step = 128
	}

	// Address Translation? (Bits 2-3)
	// For now, assume linear or handle translation in Read/Write logic?
	// Usually translation remaps the address BEFORE usage, but increment is on the raw register.
	// But spec says: "Address Translation... 8-bit, 9-bit, 10-bit rotation".
	// Implementation note: Many emulators map this on access.
	// Let's implement Step first.

	p.VRAMAddr += step
}
