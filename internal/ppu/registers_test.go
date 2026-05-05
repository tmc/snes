package ppu

import "testing"

func TestVRAMAccess(t *testing.T) {
	p := NewPPU()
	// Force-blank (INIDISP bit 7) so VRAM writes land; without it the
	// active-display write-protection gate would drop the $2118/$2119
	// bytes at vCounter=0. Matches what real software does before any
	// VRAM upload.
	p.WriteRegister(0x2100, 0x80)

	// VMAIN = $80: increment after $2119 (high-byte) writes, step = 1 word.
	// This is the near-universal mode used by commercial software when
	// streaming word-pair DMA into VRAM.
	p.WriteRegister(0x2115, 0x80)

	// Set VRAM Addr to $1000
	p.WriteRegister(0x2116, 0x00) // VMADDL
	p.WriteRegister(0x2117, 0x10) // VMADDH

	if p.VRAMAddr != 0x1000 {
		t.Errorf("VRAM Addr mismatch. Got %04X, expected 1000", p.VRAMAddr)
	}

	// Write Data $AABB:
	//   $2118 writes the low byte (AA), no increment (VMAIN bit 7 is set).
	//   $2119 writes the high byte (BB) and increments.
	p.WriteRegister(0x2118, 0xAA)
	p.WriteRegister(0x2119, 0xBB)

	// VRAM is word-addressed. Word $1000 -> byte pair $2000/$2001.
	if p.VRAM[0x2000] != 0xAA {
		t.Errorf("VRAM low byte mismatch: got %02X", p.VRAM[0x2000])
	}
	if p.VRAM[0x2001] != 0xBB {
		t.Errorf("VRAM high byte mismatch: got %02X", p.VRAM[0x2001])
	}

	if p.VRAMAddr != 0x1001 {
		t.Errorf("VRAM Addr should increment to 1001. Got %04X", p.VRAMAddr)
	}
}

// TestVRAMIncrementBit7Polarity pins the VMAIN bit-7 selector:
//
//	bit 7 = 0 -> increment after $2118 (or $2139 read)
//	bit 7 = 1 -> increment after $2119 (or $213A read)
//
// Reference: bsnes sfc/ppu/io.cpp $2118/$2119 and $2139/$213A handlers.
func TestVRAMIncrementBit7Polarity(t *testing.T) {
	cases := []struct {
		vmain   uint8
		incOn28 bool // true if $2118 should increment; otherwise $2119 does.
	}{
		{0x00, true},  // inc on low
		{0x80, false}, // inc on high
	}
	for _, tc := range cases {
		p := NewPPU()
		p.WriteRegister(0x2100, 0x80) // force-blank
		p.WriteRegister(0x2115, tc.vmain)
		p.WriteRegister(0x2116, 0x00)
		p.WriteRegister(0x2117, 0x00)

		p.WriteRegister(0x2118, 0x11)
		after18 := p.VRAMAddr
		p.WriteRegister(0x2119, 0x22)
		after19 := p.VRAMAddr

		var wantAfter18, wantAfter19 uint16
		if tc.incOn28 {
			wantAfter18, wantAfter19 = 0x0001, 0x0001
		} else {
			wantAfter18, wantAfter19 = 0x0000, 0x0001
		}
		if after18 != wantAfter18 || after19 != wantAfter19 {
			t.Errorf("VMAIN=%02X: after18=%04X after19=%04X; want %04X %04X",
				tc.vmain, after18, after19, wantAfter18, wantAfter19)
		}
	}
}

// TestVRAMIncrementStepSizes pins the step-size field (VMAIN bits 0-1):
// 00 -> 1 word, 01 -> 32 words, 10 & 11 -> 128 words.
func TestVRAMIncrementStepSizes(t *testing.T) {
	cases := []struct {
		vmain uint8
		step  uint16
	}{
		{0x80, 1},   // bit 7 set + step 00
		{0x81, 32},  // step 01
		{0x82, 128}, // step 10
		{0x83, 128}, // step 11
	}
	for _, tc := range cases {
		p := NewPPU()
		p.WriteRegister(0x2100, 0x80) // force-blank
		p.WriteRegister(0x2115, tc.vmain)
		p.WriteRegister(0x2116, 0x00)
		p.WriteRegister(0x2117, 0x00)

		p.WriteRegister(0x2118, 0x00)
		p.WriteRegister(0x2119, 0x00) // triggers increment under bit 7 = 1
		if p.VRAMAddr != tc.step {
			t.Errorf("VMAIN=%02X: step got %04X, want %04X", tc.vmain, p.VRAMAddr, tc.step)
		}
	}
}

// TestVRAMAddressTranslation pins the VMAIN bits 2-3 address-remap modes.
// These reshuffle the low bits of VMADDR when the hardware fetches a word
// from VRAM, so that flat byte streams from DMA land in the bit-plane
// order 2bpp/4bpp/8bpp tiles require.
//
// Reference: bsnes sfc/ppu/io.cpp PPU::addressVRAM().
func TestVRAMAddressTranslation(t *testing.T) {
	cases := []struct {
		name       string
		mode       uint8 // bits 2-3 of VMAIN
		inAddr     uint16
		wantMapped uint16
	}{
		{"none", 0, 0x1234, 0x1234},
		// 2bpp: addr & 0xFF00 | addr<<3 & 0x00F8 | addr>>5 & 0x0007.
		// Pick an address whose low byte toggles both halves clearly.
		{"2bpp low byte 0x21", 1, 0x0021, 0x0009},
		// 4bpp: addr & 0xFE00 | addr<<3 & 0x01F8 | addr>>6 & 0x0007.
		{"4bpp 0x0041", 2, 0x0041, 0x0009},
		// 8bpp: addr & 0xFC00 | addr<<3 & 0x03F8 | addr>>7 & 0x0007.
		{"8bpp 0x0081", 3, 0x0081, 0x0009},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPPU()
			p.WriteRegister(0x2100, 0x80) // force-blank
			// Set the remap mode; bit 7 = 1 so that $2119 increments predictably.
			vmain := uint8(0x80 | (tc.mode << 2))
			p.WriteRegister(0x2115, vmain)

			// Load VMADDR.
			p.WriteRegister(0x2116, uint8(tc.inAddr&0xFF))
			p.WriteRegister(0x2117, uint8(tc.inAddr>>8))

			// Write a recognizable word.
			p.WriteRegister(0x2118, 0xAA)
			p.WriteRegister(0x2119, 0xBB)

			byteIdx := int(tc.wantMapped) * 2
			if p.VRAM[byteIdx] != 0xAA || p.VRAM[byteIdx+1] != 0xBB {
				t.Fatalf("mode=%d inAddr=%04X: VRAM at mapped byte %04X = %02X %02X; want AA BB",
					tc.mode, tc.inAddr, byteIdx, p.VRAM[byteIdx], p.VRAM[byteIdx+1])
			}
		})
	}
}

func TestVRAMReadWordModeRequiresDummyRead(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80) // force-blank so VRAM reads are valid.
	p.WriteRegister(0x2115, 0x80) // increment on high-byte access

	// Word address $0010 -> bytes $0020/$0021, $0011 -> $0022/$0023.
	p.VRAM[0x20] = 0xAA
	p.VRAM[0x21] = 0xBB
	p.VRAM[0x22] = 0xCC
	p.VRAM[0x23] = 0xDD

	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)

	got1 := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	got2 := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	got3 := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8

	if got1 != 0xBBAA {
		t.Fatalf("first VRAM read = %04X, want BBAA", got1)
	}
	if got2 != 0xBBAA {
		t.Fatalf("second VRAM read = %04X, want duplicate BBAA without dummy read", got2)
	}
	if got3 != 0xDDCC {
		t.Fatalf("third VRAM read = %04X, want DDCC after latch advances", got3)
	}
}

func TestVRAMReadBlockedDuringActiveDisplay(t *testing.T) {
	p := NewPPU()
	p.vCounter = 100
	p.WriteRegister(0x2115, 0x80)
	p.VRAM[0x20] = 0xAA
	p.VRAM[0x21] = 0xBB
	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)

	got := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if got != 0 {
		t.Fatalf("active-display VRAM read = %04X, want blocked 0000", got)
	}
	if p.VRAMAddr != 0x0011 {
		t.Fatalf("VRAMAddr after blocked read = %04X, want 0011", p.VRAMAddr)
	}
}

func TestPPUOpenBusLatchesAreSeparate(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x9A)
	if got := p.ReadRegister(0x2100); got != 0x9A {
		t.Fatalf("PPU1 write-only read = %02X, want 9A", got)
	}
	if got := p.ReadRegister(0x2137); got != 0x00 {
		t.Fatalf("PPU2 open bus after PPU1 write = %02X, want 00", got)
	}

	p.WriteRegister(0x2134, 0x5C)
	if got := p.ReadRegister(0x2137); got != 0x5C {
		t.Fatalf("PPU2 open bus after PPU2 write = %02X, want 5C", got)
	}
	if got := p.ReadRegister(0x2100); got != 0x9A {
		t.Fatalf("PPU1 open bus after PPU2 write = %02X, want 9A", got)
	}
}

func TestPPUOpenBusUnmappedReadsUseOwningLatch(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2124, 0x35)
	p.WriteRegister(0x2137, 0xC6)

	if got := p.ReadRegister(0x2124); got != 0x35 {
		t.Fatalf("PPU1 unmapped read = %02X, want 35", got)
	}
	if got := p.ReadRegister(0x2140); got != 0xC6 {
		t.Fatalf("PPU2 unmapped read = %02X, want C6", got)
	}
}

func TestVRAMBlockedReadUsesPPU2OpenBus(t *testing.T) {
	p := NewPPU()
	p.vCounter = 100
	p.PPU2OpenBus = 0x5A
	p.WriteRegister(0x2115, 0x80)
	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)

	got := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if got != 0x5A5A {
		t.Fatalf("active-display VRAM read = %04X, want PPU2 open bus 5A5A", got)
	}
	if p.VRAMAddr != 0x0011 {
		t.Fatalf("VRAMAddr after blocked read = %04X, want 0011", p.VRAMAddr)
	}
}

func TestVRAMReadPreloadBlockedDuringActiveDisplay(t *testing.T) {
	p := NewPPU()
	p.vCounter = 100
	p.WriteRegister(0x2115, 0x80) // increment on high-byte access
	p.VRAM[0x20] = 0xAA
	p.VRAM[0x21] = 0xBB

	// Loading VMADDR during active display preloads the read buffer from the
	// display-blocked fetch path. The blocked value remains latched even if
	// force-blank is enabled before the first data-port read.
	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)
	p.WriteRegister(0x2100, 0x80)

	gotBlocked := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	gotActual := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if gotBlocked != 0 {
		t.Fatalf("first read after blocked preload = %04X, want 0000", gotBlocked)
	}
	if gotActual != 0xBBAA {
		t.Fatalf("second read after blocked preload = %04X, want BBAA", gotActual)
	}
}

func TestVRAMReadAllowedDuringForceBlank(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80)
	p.vCounter = 100
	p.WriteRegister(0x2115, 0x80)
	p.VRAM[0x20] = 0xAA
	p.VRAM[0x21] = 0xBB
	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)

	got := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if got != 0xBBAA {
		t.Fatalf("force-blank VRAM read = %04X, want BBAA", got)
	}
}

func TestVRAMReadAllowedDuringVBlank(t *testing.T) {
	p := NewPPU()
	p.vCounter = p.visibleLines()
	p.WriteRegister(0x2115, 0x80)
	p.VRAM[0x20] = 0xAA
	p.VRAM[0x21] = 0xBB
	p.WriteRegister(0x2116, 0x10)
	p.WriteRegister(0x2117, 0x00)

	got := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if got != 0xBBAA {
		t.Fatalf("vblank VRAM read = %04X, want BBAA", got)
	}
}

func TestCGRAMAccess(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80) // force-blank; otherwise $2122 is dropped.

	// Addr 0
	p.WriteRegister(0x2121, 0x00)

	// Write Color $7FFF (White)
	// Low: FF, High: 7F
	p.WriteRegister(0x2122, 0xFF) // Low
	p.WriteRegister(0x2122, 0x7F) // High

	if p.CGRAM[0] != 0xFF {
		t.Errorf("CGRAM Low mismatch")
	}
	if p.CGRAM[1] != 0x7F {
		t.Errorf("CGRAM High mismatch")
	}

	// Should increment address to 1
	if p.CGRAMAddr != 1 {
		t.Errorf("CGRAM Addr should increment. Got %d", p.CGRAMAddr)
	}
}

func TestCGRAMWriteMasksHighBit(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80)
	p.WriteRegister(0x2121, 0x00)

	p.WriteRegister(0x2122, 0x34)
	p.WriteRegister(0x2122, 0x92)

	if p.CGRAM[0] != 0x34 {
		t.Fatalf("CGRAM low byte = %02X, want 34", p.CGRAM[0])
	}
	if p.CGRAM[1] != 0x12 {
		t.Fatalf("CGRAM high byte = %02X, want masked 12", p.CGRAM[1])
	}
}

func TestOAMAccess(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80) // force-blank; otherwise $2104 is dropped.

	p.WriteRegister(0x2102, 0x10) // Addr $10
	p.WriteRegister(0x2103, 0x00)

	p.WriteRegister(0x2104, 0xCC) // latch only
	p.WriteRegister(0x2104, 0xDD) // commit pair

	if p.OAM[0x20] != 0xCC || p.OAM[0x21] != 0xDD {
		t.Errorf("OAM paired write failed: %02X %02X", p.OAM[0x20], p.OAM[0x21])
	}
	if p.OAMAddr != 0x22 {
		t.Errorf("OAM Addr should increment to 0x22. Got %04X", p.OAMAddr)
	}
}

func TestOAMDataReadIncrementsAddress(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2102, 0x10)
	p.WriteRegister(0x2103, 0x00)
	p.OAM[0x20] = 0xAB
	p.OAM[0x21] = 0xCD

	if got := p.ReadRegister(0x2138); got != 0xAB {
		t.Fatalf("first OAMDATAREAD = %02X, want AB", got)
	}
	if got := p.ReadRegister(0x2138); got != 0xCD {
		t.Fatalf("second OAMDATAREAD = %02X, want CD", got)
	}
	if p.OAMAddr != 0x22 {
		t.Fatalf("OAMAddr after reads = %04X, want 0022", p.OAMAddr)
	}
}

func TestOAMHighTableReadMirrorsEvery32Bytes(t *testing.T) {
	p := NewPPU()
	p.OAM[0x200] = 0xA0
	p.OAM[0x21F] = 0xBF

	p.OAMAddr = 0x0200
	if got := p.ReadRegister(0x2138); got != 0xA0 {
		t.Fatalf("OAM high read 0200 = %02X, want A0", got)
	}

	p.OAMAddr = 0x021F
	if got := p.ReadRegister(0x2138); got != 0xBF {
		t.Fatalf("OAM high read 021F = %02X, want BF", got)
	}
	if got := p.ReadRegister(0x2138); got != 0xA0 {
		t.Fatalf("OAM high read 0220 = %02X, want mirror A0", got)
	}
}

func TestOAMHighTableWriteMirrorsEvery32Bytes(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2100, 0x80)
	p.OAMAddr = 0x021F

	p.WriteRegister(0x2104, 0x11)
	p.WriteRegister(0x2104, 0x22)

	if got := p.OAM[0x21F]; got != 0x11 {
		t.Fatalf("OAM high write 021F = %02X, want 11", got)
	}
	if got := p.OAM[0x200]; got != 0x22 {
		t.Fatalf("OAM high write 0220 mirror = %02X, want 22", got)
	}
}

func TestRenderScanlineRefreshesOAMAddress(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2102, 0x10)
	p.WriteRegister(0x2103, 0x00)
	p.OAM[0x20] = 0xAA
	p.OAM[0x21] = 0xBB

	if got := p.ReadRegister(0x2138); got != 0xAA {
		t.Fatalf("OAMDATAREAD before refresh = %02X, want AA", got)
	}
	if p.OAMAddr != 0x21 {
		t.Fatalf("OAMAddr after read = %04X, want 0021", p.OAMAddr)
	}

	p.INIDISP = 0x0F
	p.RenderScanline(0)
	if p.OAMAddr != p.OAMBaseAddr {
		t.Fatalf("OAMAddr after visible scanline = %04X, want base %04X",
			p.OAMAddr, p.OAMBaseAddr)
	}
}

func TestRenderScanlineForceBlankDoesNotRefreshOAMAddress(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2102, 0x10)
	p.WriteRegister(0x2103, 0x00)
	p.OAM[0x20] = 0xAA
	p.ReadRegister(0x2138)

	p.INIDISP = 0x80
	p.RenderScanline(0)
	if p.OAMAddr != 0x21 {
		t.Fatalf("force-blank OAMAddr after scanline = %04X, want 0021", p.OAMAddr)
	}
}

func TestCGRAMDataReadPair(t *testing.T) {
	p := NewPPU()
	p.CGRAM[0x24] = 0x34
	p.CGRAM[0x25] = 0x92
	p.CGRAM[0x26] = 0x56
	p.CGRAM[0x27] = 0xA1
	p.WriteRegister(0x2121, 0x12)

	if got := p.ReadRegister(0x213B); got != 0x34 {
		t.Fatalf("first CGDATAREAD low = %02X, want 34", got)
	}
	if got := p.ReadRegister(0x213B); got != 0x34 {
		t.Fatalf("first CGDATAREAD high = %02X, want PPU2 open bus 34", got)
	}
	if p.CGRAMAddr != 0x13 {
		t.Fatalf("CGRAMAddr after first color = %02X, want 13", p.CGRAMAddr)
	}
	if got := p.ReadRegister(0x213B); got != 0x56 {
		t.Fatalf("second CGDATAREAD low = %02X, want 56", got)
	}
	if got := p.ReadRegister(0x213B); got != 0x57 {
		t.Fatalf("second CGDATAREAD high = %02X, want PPU2 open bus 56 with data bit 1", got)
	}
}

func TestCGRAMHighReadKeepsPPU2OpenBusBits(t *testing.T) {
	p := NewPPU()
	p.CGRAM[0] = 0x20
	p.CGRAM[1] = 0x01
	p.WriteRegister(0x2121, 0x00)

	if got := p.ReadRegister(0x213B); got != 0x20 {
		t.Fatalf("CGDATAREAD low = %02X, want 20", got)
	}
	if got := p.ReadRegister(0x213B); got != 0x21 {
		t.Fatalf("CGDATAREAD high = %02X, want open-bus bits 20 plus data bit 1", got)
	}
	if p.PPU2OpenBus != 0x21 {
		t.Fatalf("PPU2 open bus after CGDATAREAD high = %02X, want 21", p.PPU2OpenBus)
	}
}

// TestVRAMWriteProtection pins the active-display gate on $2118/$2119.
// Hardware drops the byte when vCounter is inside the visible range and
// force-blank (INIDISP bit 7) is off; it commits the byte during VBlank
// (vCounter >= visibleLines) or any time force-blank is on. The address
// increment runs regardless — bsnes sfc/ppu/io.cpp bumps vramAddress
// unconditionally; only writeVRAM() short-circuits.
func TestVRAMWriteProtection(t *testing.T) {
	cases := []struct {
		name       string
		vCounter   int
		forceBlank bool
		wantLands  bool
	}{
		{"active display, display on", 100, false, false},
		{"active display, force-blank", 100, true, true},
		{"vblank, display on", 230, false, true},
		{"pre-render line 0, display on", 0, false, false},
		{"last visible line, display on", 223, false, false},
		{"first vblank line, display on", 224, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPPU()
			if tc.forceBlank {
				p.WriteRegister(0x2100, 0x80)
			}
			p.vCounter = tc.vCounter
			p.WriteRegister(0x2115, 0x80) // increment on high
			p.WriteRegister(0x2116, 0x00)
			p.WriteRegister(0x2117, 0x00)
			p.WriteRegister(0x2118, 0xAA)
			p.WriteRegister(0x2119, 0xBB)

			got := p.VRAM[0] == 0xAA && p.VRAM[1] == 0xBB
			if got != tc.wantLands {
				t.Errorf("VRAM bytes landed=%v, want %v (VRAM[0..1]=%02X %02X)",
					got, tc.wantLands, p.VRAM[0], p.VRAM[1])
			}
			if p.VRAMAddr != 0x0001 {
				t.Errorf("VRAMAddr should increment regardless of gate: got %04X, want 0001",
					p.VRAMAddr)
			}
		})
	}
}

func TestVRAMWriteDropDoesNotPoisonReadBuffer(t *testing.T) {
	p := NewPPU()
	p.vCounter = 100
	p.WriteRegister(0x2115, 0x80) // increment on high
	p.WriteRegister(0x2116, 0x00)
	p.WriteRegister(0x2117, 0x00)

	p.WriteRegister(0x2118, 0xAA)
	p.WriteRegister(0x2119, 0xBB)
	if p.VRAM[0] != 0 || p.VRAM[1] != 0 {
		t.Fatalf("active-display VRAM write landed: %02X %02X", p.VRAM[0], p.VRAM[1])
	}
	if p.VRAMAddr != 1 {
		t.Fatalf("VRAMAddr after dropped write = %04X, want 0001", p.VRAMAddr)
	}

	p.VRAM[2] = 0xCC
	p.VRAM[3] = 0xDD
	p.WriteRegister(0x2100, 0x80)
	p.WriteRegister(0x2116, 0x01)
	p.WriteRegister(0x2117, 0x00)

	got := uint16(p.ReadRegister(0x2139)) | uint16(p.ReadRegister(0x213A))<<8
	if got != 0xDDCC {
		t.Fatalf("VRAM read after dropped write = %04X, want DDCC", got)
	}
}

// TestOAMWriteProtection pins the active-display gate on $2104. The byte
// does not land during active display with force-blank off, but the OAM
// address pointer still advances — matches bsnes sfc/ppu/io.cpp $2104 where
// io.oamAddress++ is unconditional and writeOAM() is the gated path.
func TestOAMWriteProtection(t *testing.T) {
	setup := func(forceBlank bool, vCounter int) *PPU {
		p := NewPPU()
		if forceBlank {
			p.WriteRegister(0x2100, 0x80)
		}
		p.vCounter = vCounter
		p.WriteRegister(0x2102, 0x10)
		p.WriteRegister(0x2103, 0x00)
		return p
	}

	t.Run("active display drops the paired write", func(t *testing.T) {
		p := setup(false, 100)
		p.WriteRegister(0x2104, 0xCC)
		p.WriteRegister(0x2104, 0xDD)
		if p.OAM[0x20] == 0xCC || p.OAM[0x21] == 0xDD {
			t.Errorf("OAM should not have committed during active display: %02X %02X",
				p.OAM[0x20], p.OAM[0x21])
		}
		if p.OAMAddr != 0x22 {
			t.Errorf("OAMAddr should still advance: got %04X, want 0022", p.OAMAddr)
		}
	})
	t.Run("force-blank lets the pair land", func(t *testing.T) {
		p := setup(true, 100)
		p.WriteRegister(0x2104, 0xCC)
		p.WriteRegister(0x2104, 0xDD)
		if p.OAM[0x20] != 0xCC || p.OAM[0x21] != 0xDD {
			t.Errorf("OAM paired write should land under force-blank: %02X %02X",
				p.OAM[0x20], p.OAM[0x21])
		}
	})
	t.Run("vblank lets the pair land", func(t *testing.T) {
		p := setup(false, 230)
		p.WriteRegister(0x2104, 0xCC)
		p.WriteRegister(0x2104, 0xDD)
		if p.OAM[0x20] != 0xCC || p.OAM[0x21] != 0xDD {
			t.Errorf("OAM paired write should land in VBlank: %02X %02X",
				p.OAM[0x20], p.OAM[0x21])
		}
	})
}

// TestCGRAMWriteProtection pins the active-display gate on $2122. Under
// active display the byte is dropped, but CGRAMAddr and the word-pair
// toggle still advance — otherwise a dropped write would wedge the toggle
// and every subsequent CGRAM write would land at the wrong byte half for
// the rest of the frame.
func TestCGRAMWriteProtection(t *testing.T) {
	t.Run("active display drops the byte but still toggles", func(t *testing.T) {
		p := NewPPU()
		p.vCounter = 100
		p.WriteRegister(0x2121, 0x00)
		p.WriteRegister(0x2122, 0xFF)
		p.WriteRegister(0x2122, 0x7F)

		if p.CGRAM[0] == 0xFF || p.CGRAM[1] == 0x7F {
			t.Errorf("CGRAM should not have committed during active display: %02X %02X",
				p.CGRAM[0], p.CGRAM[1])
		}
		if p.CGRAMAddr != 1 {
			t.Errorf("CGRAMAddr should still advance after the high-byte write: got %d, want 1",
				p.CGRAMAddr)
		}
		if p.CGRAMWritePair {
			t.Errorf("CGRAMWritePair toggle should have cycled back to false")
		}
	})
	t.Run("force-blank lets both bytes land", func(t *testing.T) {
		p := NewPPU()
		p.WriteRegister(0x2100, 0x80)
		p.vCounter = 100
		p.WriteRegister(0x2121, 0x00)
		p.WriteRegister(0x2122, 0xFF)
		p.WriteRegister(0x2122, 0x7F)
		if p.CGRAM[0] != 0xFF || p.CGRAM[1] != 0x7F {
			t.Errorf("CGRAM should commit under force-blank: %02X %02X",
				p.CGRAM[0], p.CGRAM[1])
		}
	})
}

// TestForceBlankDecodeBit7 pins that INIDISP bit 7 is the sole force-blank
// selector. Brightness bits 0-3 must not affect the gate.
func TestForceBlankDecodeBit7(t *testing.T) {
	p := NewPPU()
	if p.ForceBlank() {
		t.Fatal("INIDISP should default to 0 (not force-blank)")
	}
	p.WriteRegister(0x2100, 0x0F) // max brightness, bit 7 clear
	if p.ForceBlank() {
		t.Error("brightness-only INIDISP should not force-blank")
	}
	p.WriteRegister(0x2100, 0x80)
	if !p.ForceBlank() {
		t.Error("INIDISP=$80 should force-blank")
	}
	p.WriteRegister(0x2100, 0x8F)
	if !p.ForceBlank() {
		t.Error("INIDISP=$8F should still force-blank (brightness bits ignored)")
	}
}

func TestCOLDATAFixedColorComponents(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2132, 0x2A) // R=10
	p.WriteRegister(0x2132, 0x55) // G=21
	p.WriteRegister(0x2132, 0x9F) // B=31

	if p.FixedR != 0x0A {
		t.Fatalf("FixedR = %d, want 10", p.FixedR)
	}
	if p.FixedG != 0x15 {
		t.Fatalf("FixedG = %d, want 21", p.FixedG)
	}
	if p.FixedB != 0x1F {
		t.Fatalf("FixedB = %d, want 31", p.FixedB)
	}
}

func TestMode7MatrixWriteLatchAndMultiply(t *testing.T) {
	p := NewPPU()

	// M7A = -0x0100, M7B high byte = 0x02, product = -0x0200.
	p.WriteRegister(0x211B, 0x00)
	p.WriteRegister(0x211B, 0xFF)
	p.WriteRegister(0x211C, 0x34)
	p.WriteRegister(0x211C, 0x02)

	if p.M7A != 0xFF00 {
		t.Fatalf("M7A = %04X, want FF00", p.M7A)
	}
	if p.M7B != 0x0234 {
		t.Fatalf("M7B = %04X, want 0234", p.M7B)
	}

	got := uint32(p.ReadRegister(0x2134)) |
		uint32(p.ReadRegister(0x2135))<<8 |
		uint32(p.ReadRegister(0x2136))<<16
	if got != 0xFFFE00 {
		t.Fatalf("mode7 multiply = %06X, want FFFE00", got)
	}
}

func TestMode7MultiplySignExtensionEdges(t *testing.T) {
	for _, tt := range []struct {
		name string
		m7a  uint16
		m7b  uint16
		want uint32
	}{
		{"positive max", 0x7FFF, 0x7F00, 0x3F7F81},
		{"negative multiplier", 0x0100, 0x8000, 0xFF8000},
		{"both negative", 0x8000, 0x8000, 0x400000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPPU()
			p.M7A = tt.m7a
			p.M7B = tt.m7b

			got := uint32(p.ReadRegister(0x2134)) |
				uint32(p.ReadRegister(0x2135))<<8 |
				uint32(p.ReadRegister(0x2136))<<16
			if got != tt.want {
				t.Fatalf("mode7 multiply %04X*%02X = %06X, want %06X",
					tt.m7a, byte(tt.m7b>>8), got, tt.want)
			}
		})
	}
}

func TestMode7ControlAnd13BitRegisters(t *testing.T) {
	p := NewPPU()

	p.WriteRegister(0x211A, 0xC1)
	if !p.M7Large || !p.M7Fill || !p.M7XFlip || p.M7YFlip {
		t.Fatalf("M7SEL flags = large:%v fill:%v xflip:%v yflip:%v, want true true true false",
			p.M7Large, p.M7Fill, p.M7XFlip, p.M7YFlip)
	}

	p.WriteRegister(0x211F, 0xFF)
	p.WriteRegister(0x211F, 0x7F)
	if p.M7X != 0x1FFF {
		t.Fatalf("M7X = %04X, want 1FFF", p.M7X)
	}

	p.WriteRegister(0x210D, 0xFE)
	p.WriteRegister(0x210D, 0x7F)
	if p.M7HOFS != 0x1FFE {
		t.Fatalf("M7HOFS = %04X, want 1FFE", p.M7HOFS)
	}
}

func TestMode7LatchHookRecordsLatchedRegisters(t *testing.T) {
	p := NewPPU()
	var got []struct {
		addr  uint16
		value uint16
	}
	p.Mode7LatchHook = func(addr uint16, value uint16) {
		got = append(got, struct {
			addr  uint16
			value uint16
		}{addr, value})
	}

	p.WriteRegister(0x211B, 0x34)
	p.WriteRegister(0x211B, 0x12)
	p.WriteRegister(0x210D, 0xFE)
	p.WriteRegister(0x210D, 0x7F)
	p.WriteRegister(0x211F, 0xFF)
	p.WriteRegister(0x211F, 0x7F)

	want := []struct {
		addr  uint16
		value uint16
	}{
		{0x211B, 0x3400},
		{0x211B, 0x1234},
		{0x210D, 0x1E12},
		{0x210D, 0x1FFE},
		{0x211F, 0x1F7F},
		{0x211F, 0x1FFF},
	}
	if len(got) != len(want) {
		t.Fatalf("Mode7LatchHook calls = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Mode7LatchHook[%d] = {%04X %04X}, want {%04X %04X}",
				i, got[i].addr, got[i].value, want[i].addr, want[i].value)
		}
	}
}

func TestMode7LatchEventHookRecordsBeamTiming(t *testing.T) {
	p := NewPPU()
	p.FrameCount = 7
	p.hCounter = 123
	p.vCounter = 45
	var got []Mode7LatchEvent
	p.Mode7LatchEventHook = func(e Mode7LatchEvent) {
		got = append(got, e)
	}

	p.WriteRegister(0x211B, 0x34)
	p.WriteRegister(0x211B, 0x12)

	if len(got) != 2 {
		t.Fatalf("Mode7LatchEventHook calls = %d, want 2", len(got))
	}
	want := Mode7LatchEvent{
		Addr:       0x211B,
		Value:      0x1234,
		FrameCount: 7,
		HCounter:   123,
		VCounter:   45,
	}
	if got[1] != want {
		t.Fatalf("Mode7LatchEventHook[1] = %+v, want %+v", got[1], want)
	}
}

func TestMode7MatrixPairHookRecordsHDMAStylePairs(t *testing.T) {
	p := NewPPU()
	p.FrameCount = 9
	p.hCounter = 274
	p.vCounter = 37
	var got []Mode7MatrixPairEvent
	p.Mode7MatrixPairHook = func(e Mode7MatrixPairEvent) {
		got = append(got, e)
	}

	p.WriteRegister(0x211B, 0x34)
	p.WriteRegister(0x211B, 0x12)
	p.WriteRegister(0x211C, 0x78)
	p.WriteRegister(0x211C, 0x56)
	p.WriteRegister(0x211D, 0xBC)
	p.WriteRegister(0x211D, 0x9A)
	p.WriteRegister(0x211E, 0xF0)
	p.WriteRegister(0x211E, 0xDE)

	want := []Mode7MatrixPairEvent{
		{
			FirstAddr:  0x211B,
			FirstValue: 0x1234,
			NextAddr:   0x211C,
			NextValue:  0x5678,
			FrameCount: 9,
			HCounter:   274,
			VCounter:   37,
		},
		{
			FirstAddr:  0x211D,
			FirstValue: 0x9ABC,
			NextAddr:   0x211E,
			NextValue:  0xDEF0,
			FrameCount: 9,
			HCounter:   274,
			VCounter:   37,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("Mode7MatrixPairHook calls = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Mode7MatrixPairHook[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBGScrollRegistersUseSeparateHorizontalFineLatch(t *testing.T) {
	p := NewPPU()

	p.WriteRegister(0x210D, 0x05)
	p.WriteRegister(0x210D, 0x12)
	if got, want := p.BG1HOFS, uint16(0x1205); got != want {
		t.Fatalf("BG1HOFS first pair = %04X, want %04X", got, want)
	}

	p.WriteRegister(0x210E, 0x3F)
	if got, want := p.BG1VOFS, uint16(0x3F12); got != want {
		t.Fatalf("BG1VOFS after horizontal write = %04X, want %04X", got, want)
	}

	p.WriteRegister(0x210D, 0xA9)
	if got, want := p.BG1HOFS, uint16(0xA93A); got != want {
		t.Fatalf("BG1HOFS combines coarse latch1 and fine latch2 = %04X, want %04X", got, want)
	}
}

func TestWindowRegisterWrites(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2123, 0x12)
	p.WriteRegister(0x2124, 0x34)
	p.WriteRegister(0x2125, 0x56)
	p.WriteRegister(0x2126, 0x01)
	p.WriteRegister(0x2127, 0x02)
	p.WriteRegister(0x2128, 0x03)
	p.WriteRegister(0x2129, 0x04)
	p.WriteRegister(0x212A, 0x78)
	p.WriteRegister(0x212B, 0x9A)
	p.WriteRegister(0x212E, 0x0F)
	p.WriteRegister(0x212F, 0xF0)

	if p.W12SEL != 0x12 || p.W34SEL != 0x34 || p.WOBJSEL != 0x56 {
		t.Fatalf("window select registers mismatch")
	}
	if p.WH0 != 0x01 || p.WH1 != 0x02 || p.WH2 != 0x03 || p.WH3 != 0x04 {
		t.Fatalf("window range registers mismatch")
	}
	if p.WBGLOG != 0x78 || p.WOBJLOG != 0x9A || p.TMW != 0x0F || p.TSW != 0xF0 {
		t.Fatalf("window control registers mismatch")
	}
}

func TestSETINIOverscanUpdatesHeight(t *testing.T) {
	p := NewPPU()
	if p.Height != 224 {
		t.Fatalf("default height = %d, want 224", p.Height)
	}
	if len(p.FrontBuffer) < 256*240 {
		t.Fatalf("frontbuffer too small for overscan: len=%d", len(p.FrontBuffer))
	}

	p.WriteRegister(0x2133, 0x04) // overscan on
	if p.SETINI != 0x04 {
		t.Fatalf("SETINI = %02X, want 04", p.SETINI)
	}
	if p.Height != 240 {
		t.Fatalf("height = %d, want 240", p.Height)
	}

	p.WriteRegister(0x2133, 0x00) // overscan off
	if p.Height != 224 {
		t.Fatalf("height = %d, want 224 after clearing overscan", p.Height)
	}
}

func TestHVBJOYOverscanVBlankThreshold(t *testing.T) {
	p := NewPPU()
	p.WriteRegister(0x2133, 0x04) // overscan on, visible lines=240
	p.vCounter = 240
	if got := p.ReadHVBJOY(); (got & 0x80) != 0 {
		t.Fatalf("vblank set too early at line 240: %02X", got)
	}
	p.vCounter = 241
	if got := p.ReadHVBJOY(); (got & 0x80) == 0 {
		t.Fatalf("vblank not set at line 241: %02X", got)
	}
}

func TestLayerMaskedByWindowMainAndSubscreen(t *testing.T) {
	p := NewPPU()
	p.WH0 = 0
	p.WH1 = 15
	p.W12SEL = 0x02 // BG1: window1 enable
	p.WBGLOG = 0x00 // OR
	p.TMW = 0x01    // BG1 main windowing enabled

	if !p.layerMaskedByWindow(8, sourceBG1, false) {
		t.Fatalf("expected BG1 main pixel to be masked inside window")
	}
	if p.layerMaskedByWindow(20, sourceBG1, false) {
		t.Fatalf("unexpected BG1 main masking outside window")
	}
	if p.layerMaskedByWindow(8, sourceBG1, true) {
		t.Fatalf("unexpected BG1 sub masking when TSW bit is clear")
	}

	p.TSW = 0x01 // enable BG1 subscreen windowing
	if !p.layerMaskedByWindow(8, sourceBG1, true) {
		t.Fatalf("expected BG1 sub pixel to be masked after enabling TSW")
	}
}

func TestColorMathWindowMaskFromCGWSEL(t *testing.T) {
	p := NewPPU()
	p.WH0 = 0
	p.WH1 = 15
	p.WOBJSEL = 0x20 // color: window1 enable
	p.WOBJLOG = 0x00 // color mask OR

	p.CGWSEL = 0x40 // above mask=1 (inside), below mask=0 (always)
	if !p.colorMathWindowEnabledAt(8, false) {
		t.Fatalf("expected color math enabled inside color window")
	}
	if p.colorMathWindowEnabledAt(20, false) {
		t.Fatalf("expected color math disabled outside color window")
	}
	if !p.colorMathWindowEnabledAt(20, true) {
		t.Fatalf("expected below mask=0 to always enable")
	}

	p.CGWSEL = 0x80 // above mask=2 (outside)
	if p.colorMathWindowEnabledAt(8, false) {
		t.Fatalf("expected color math disabled inside window for invert mode")
	}
	if !p.colorMathWindowEnabledAt(20, false) {
		t.Fatalf("expected color math enabled outside window for invert mode")
	}
}

func TestRenderScanlineColorMath(t *testing.T) {
	p := NewPPU()
	p.TM = 0
	p.INIDISP = 0x0F
	p.CGRAM[0] = 0x21 // R=1,G=1,B=1
	p.CGRAM[1] = 0x04

	// Fixed color R=1, G=2, B=3
	p.WriteRegister(0x2132, 0x21)
	p.WriteRegister(0x2132, 0x42)
	p.WriteRegister(0x2132, 0x83)
	p.CGADSUB = 0x20 // add fixed color to backdrop

	p.RenderScanline(0)
	got := p.FrontBuffer[0]
	r := got & 0x1F
	g := (got >> 5) & 0x1F
	b := (got >> 10) & 0x1F
	if r != 2 || g != 3 || b != 4 {
		t.Fatalf("color math result rgb = %d,%d,%d want 2,3,4", r, g, b)
	}
}

func TestRenderScanlineColorMathRespectsLayerMask(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0
	p.CGRAM[0] = 0x21 // R=1,G=1,B=1
	p.CGRAM[1] = 0x04
	p.WriteRegister(0x2132, 0x21)
	p.WriteRegister(0x2132, 0x42)
	p.WriteRegister(0x2132, 0x83)
	p.CGADSUB = 0x01 // BG1 only, no backdrop bit

	p.RenderScanline(0)
	got := p.FrontBuffer[0]
	if got != 0x0421 {
		t.Fatalf("backdrop should not be color-mathed, got %04X want 0421", got)
	}
}

func TestRenderScanlineMode3BG1Uses8BPP(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 3
	p.TM = 0x01 // BG1 only
	p.BG12NBA = 0x01

	// Tilemap entry 0 -> tile 0.
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x00

	// 8bpp tile pixel value 5 at x=0.
	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80 // plane 0
	p.VRAM[tileBase+1] = 0x00 // plane 1
	p.VRAM[tileBase+16] = 0x80
	p.VRAM[tileBase+17] = 0x00
	p.VRAM[tileBase+32] = 0x00
	p.VRAM[tileBase+33] = 0x00
	p.VRAM[tileBase+48] = 0x00
	p.VRAM[tileBase+49] = 0x00

	p.CGRAM[5*2] = 0x1F
	p.CGRAM[5*2+1] = 0x00

	p.RenderScanline(0)
	want := uint16(p.CGRAM[5*2]) | uint16(p.CGRAM[5*2+1])<<8
	if got := p.FrontBuffer[0]; got != want {
		t.Fatalf("mode3 BG1 8bpp pixel = %04X, want %04X", got, want)
	}
}

func TestRenderScanlineMode4BG2Uses2BPP(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 4
	p.TM = 0x02 // BG2 only

	// BG2 tilemap entry 0 -> tile 0.
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x00

	// 2bpp tile pixel value 1 at x=0.
	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+1] = 0x00

	p.CGRAM[1*2] = 0x00
	p.CGRAM[1*2+1] = 0x7C

	p.BG2SC = 0x00
	p.BG12NBA = 0x10 // BG2 tiles at word addr 0x1000 -> byte addr 0x2000

	p.RenderScanline(0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if got := p.FrontBuffer[0]; got != want {
		t.Fatalf("mode4 BG2 2bpp pixel = %04X, want %04X", got, want)
	}
}

func TestApplyColorMathLineUsesSubscreenOperand(t *testing.T) {
	p := NewPPU()
	p.CGWSEL = 0x02
	p.CGADSUB = 0x01 // apply to BG1 source
	p.FrontBuffer[0] = pack555(1, 0, 0)
	mainSource := make([]uint8, p.Width)
	mainSource[0] = sourceBG1
	sub := make([]uint16, p.Width)
	sub[0] = pack555(0, 1, 0)

	p.applyColorMathLine(0, mainSource, sub, true)
	got := p.FrontBuffer[0]
	r := got & 0x1f
	g := (got >> 5) & 0x1f
	if r != 1 || g != 1 {
		t.Fatalf("subscreen math rgb = %d,%d, want 1,1", r, g)
	}
}

func TestApplyColorMathClipMainToBlack(t *testing.T) {
	p := NewPPU()
	p.FrontBuffer[0] = pack555(3, 0, 0)
	p.CGADSUB = sourceBG1
	p.CGWSEL = 0x80  // main enabled outside color window.
	p.WOBJSEL = 0x20 // color window 1 enabled.
	p.WH0 = 0
	p.WH1 = 0
	p.WriteRegister(0x2132, 0x21) // fixed red = 1
	mainSource := make([]uint8, p.Width)
	mainSource[0] = sourceBG1

	p.applyColorMathLine(0, mainSource, nil, false)
	if got := p.FrontBuffer[0]; got != pack555(1, 0, 0) {
		t.Fatalf("color math clipped main = %04X, want fixed red", got)
	}
}

func TestApplyColorMathPreventMathCanClipToBlack(t *testing.T) {
	p := NewPPU()
	p.FrontBuffer[0] = pack555(3, 0, 0)
	p.CGADSUB = sourceBG1
	p.CGWSEL = 0xA0  // main outside, operand outside: inside clips and prevents.
	p.WOBJSEL = 0x20 // color window 1 enabled.
	p.WH0 = 0
	p.WH1 = 0
	p.WriteRegister(0x2132, 0x21)
	mainSource := make([]uint8, p.Width)
	mainSource[0] = sourceBG1

	p.applyColorMathLine(0, mainSource, nil, false)
	if got := p.FrontBuffer[0]; got != 0 {
		t.Fatalf("color math clipped/prevented = %04X, want black", got)
	}
}

func TestRenderScanlineHasNoAllocs(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10
	p.TS = 0x10
	p.CGWSEL = 0x02

	if got := testing.AllocsPerRun(100, func() {
		p.RenderScanline(0)
	}); got != 0 {
		t.Fatalf("RenderScanline allocations = %.2f, want 0", got)
	}
}

func TestSTAT77RangeOverFlag(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	for i := 0; i < 33; i++ {
		addr := i * 4
		p.OAM[addr] = 0
		p.OAM[addr+1] = 0
		p.OAM[addr+2] = 0
		p.OAM[addr+3] = 0
	}

	p.RenderScanline(0)
	if got := p.ReadRegister(0x213E); got&0x40 == 0 {
		t.Fatalf("STAT77 range over flag not set: %02X", got)
	}
}

func TestSTAT77TimeOverFlag(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	for i := 0; i < 18; i++ {
		addr := i * 4
		p.OAM[addr] = 0
		p.OAM[addr+1] = 0
		p.OAM[addr+2] = 0
		p.OAM[addr+3] = 0
		p.OAM[512+(i/4)] |= 1 << ((i%4)*2 + 1) // size bit: 16x16 in OBSEL mode 0
	}

	p.RenderScanline(0)
	if got := p.ReadRegister(0x213E); got&0x80 == 0 {
		t.Fatalf("STAT77 time over flag not set: %02X", got)
	}
}

func TestSTAT77OpenBusAndVersionBits(t *testing.T) {
	p := NewPPU()
	p.PPU1OpenBus = 0x30
	p.PPU2OpenBus = 0x20
	p.RangeOver = true
	p.TimeOver = true

	if got := p.ReadRegister(0x213E); got != 0xD1 {
		t.Fatalf("STAT77 = %02X, want D1", got)
	}
	if p.PPU1OpenBus != 0xD1 {
		t.Fatalf("PPU1 open bus after STAT77 = %02X, want D1", p.PPU1OpenBus)
	}
	if p.PPU2OpenBus != 0x20 {
		t.Fatalf("STAT77 changed PPU2 open bus to %02X, want 20", p.PPU2OpenBus)
	}
}

func TestHVCounterLatchRegisters(t *testing.T) {
	p := NewPPU()
	p.hCounter = 0x0123
	p.vCounter = 0x00C0

	if got := p.ReadRegister(0x2137); got != 0 {
		t.Fatalf("SLHV read = %02X, want open bus 00", got)
	}
	if got := p.ReadRegister(0x213F); got&0x40 == 0 {
		t.Fatalf("STAT78 latch flag not set: %02X", got)
	}
	if got := p.ReadRegister(0x213D); got != 0xC0 {
		t.Fatalf("OPVCT low = %02X, want C0", got)
	}
	if got := p.ReadRegister(0x213D); got != 0x00 {
		t.Fatalf("OPVCT high = %02X, want 00", got)
	}
	if got := p.ReadRegister(0x213C); got != 0x23 {
		t.Fatalf("OPHCT low = %02X, want 23", got)
	}
	if got := p.ReadRegister(0x213C); got != 0x01 {
		t.Fatalf("OPHCT high = %02X, want 01", got)
	}
}

func TestSTAT78OpenBusAndVersionBits(t *testing.T) {
	p := NewPPU()
	p.PPU1OpenBus = 0x10
	p.PPU2OpenBus = 0x20
	p.FrameCount = 1
	p.hvLatched = true

	if got := p.ReadRegister(0x213F); got != 0xE3 {
		t.Fatalf("STAT78 = %02X, want E3", got)
	}
	if p.PPU2OpenBus != 0xE3 {
		t.Fatalf("PPU2 open bus after STAT78 = %02X, want E3", p.PPU2OpenBus)
	}
	if p.PPU1OpenBus != 0x10 {
		t.Fatalf("STAT78 changed PPU1 open bus to %02X, want 10", p.PPU1OpenBus)
	}
	if p.hvLatched {
		t.Fatalf("STAT78 read did not clear H/V latch flag")
	}
}

func TestSTAT78ReportsField(t *testing.T) {
	p := NewPPU()
	if got := p.ReadRegister(0x213F); got&0x80 != 0 {
		t.Fatalf("STAT78 field bit at frame 0 = %02X, want clear", got)
	}

	p.FrameCount = 1
	if got := p.ReadRegister(0x213F); got&0x80 == 0 {
		t.Fatalf("STAT78 field bit at frame 1 = %02X, want set", got)
	}
}

func TestOBJX256CountsTowardRangeOver(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	for i := 0; i < 32; i++ {
		addr := i * 4
		p.OAM[addr] = 0
		p.OAM[addr+1] = 0
		p.OAM[addr+2] = 0
		p.OAM[addr+3] = 0
	}

	// Sprite 32 at x=256 should still count toward range/time overflow.
	addr := 32 * 4
	p.OAM[addr] = 0
	p.OAM[addr+1] = 0
	p.OAM[addr+2] = 0
	p.OAM[addr+3] = 0
	p.OAM[512+(32/4)] |= 1 << ((32 % 4) * 2)

	p.RenderScanline(0)
	if got := p.ReadRegister(0x213E); got&0x40 == 0 {
		t.Fatalf("STAT77 range over flag not set for x=256 sprite quirk: %02X", got)
	}
}

func TestOBJYWrapRendersAtTop(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	// Sprite 0 at X=10, Y=251 should wrap and be visible on scanline 2.
	p.OAM[0] = 10
	p.OAM[1] = 251
	p.OAM[2] = 0
	p.OAM[3] = 0

	// Tile row for relY=7, first pixel set.
	p.VRAM[14] = 0x80
	p.VRAM[15] = 0x00
	p.VRAM[30] = 0x00
	p.VRAM[31] = 0x00

	// OBJ palette color entry (index 129).
	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00

	p.RenderScanline(2)
	if got := p.FrontBuffer[2*p.Width+10]; got == 0 {
		t.Fatalf("wrapped OBJ pixel not rendered")
	}
}

func TestOBJRectangularSmallSpriteRendersLowerRows(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10      // OBJ only
	p.OBSEL = 6 << 5 // small sprites are 16x32

	for i := 0; i < 128; i++ {
		p.OAM[i*4+1] = 224
	}
	p.OAM[0] = 0
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0

	// Scanline 24 of a 16x32 sprite uses tile row 3, column 0.
	tile := 3 * 16
	p.VRAM[tile*32] = 0x80
	p.CGRAM[129*2] = 0x1F

	p.RenderScanline(24)
	if got := p.FrontBuffer[24*p.Width]; got == 0 {
		t.Fatalf("16x32 OBJ lower row not rendered")
	}
}

func TestOBJRectangularVFlipMirrorsWithinHalves(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10      // OBJ only
	p.OBSEL = 6 << 5 // small sprites are 16x32

	for i := 0; i < 128; i++ {
		p.OAM[i*4+1] = 224
	}
	p.OAM[0] = 0
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0x80 // v-flip

	// In 16x32 mode, vertical flip mirrors each 16x16 half. Scanline 24
	// therefore reads tile row 2, not row 0 or row 3.
	row2Tile := 2 * 16
	p.VRAM[row2Tile*32+14] = 0x80
	p.CGRAM[129*2] = 0x1F

	p.RenderScanline(24)
	if got := p.FrontBuffer[24*p.Width]; got == 0 {
		t.Fatalf("16x32 OBJ v-flip did not mirror within the lower half")
	}
}

func TestOBJTimeOverDropsEarliestSprite(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	for i := 0; i < 18; i++ {
		addr := i * 4
		p.OAM[addr] = 0
		p.OAM[addr+1] = 0
		p.OAM[addr+2] = 2
		p.OAM[addr+3] = 0
		p.OAM[512+(i/4)] |= 1 << ((i%4)*2 + 1) // size bit: 16x16
	}

	// Sprite 0 would normally win, but it should be dropped once 36 slivers exceed the 34-sliver limit.
	p.OAM[2] = 0

	// Tile 0 renders color index 1, tile 2 renders color index 3 at x=0.
	p.VRAM[0] = 0x80
	p.VRAM[1] = 0x00
	p.VRAM[16] = 0x00
	p.VRAM[17] = 0x00

	base2 := 16 * 4
	p.VRAM[base2] = 0x80
	p.VRAM[base2+1] = 0x80
	p.VRAM[base2+16] = 0x00
	p.VRAM[base2+17] = 0x00

	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00
	p.CGRAM[131*2] = 0x00
	p.CGRAM[131*2+1] = 0x7C

	p.RenderScanline(0)
	dropped := uint16(p.CGRAM[129*2]) | uint16(p.CGRAM[129*2+1])<<8
	if got := p.FrontBuffer[0]; got == dropped {
		t.Fatalf("time-over did not drop earliest sprite, got %04X", got)
	}
	if got := p.ReadRegister(0x213E); got&0x80 == 0 {
		t.Fatalf("STAT77 time over flag not set after earliest-sprite drop: %02X", got)
	}
}

func TestOBJXHighBitWrapsNegative(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	// Sprite at x=0x1FF should wrap to screen x=-1 and still draw visible pixels at left edge.
	p.OAM[0] = 0xFF
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0
	p.OAM[512] = 0x01 // sprite0 x-high=1, size=0

	// Tile row for y=0, second pixel set (maps to screen x=0 when x=-1).
	p.VRAM[0] = 0x40
	p.VRAM[1] = 0x00
	p.VRAM[16] = 0x00
	p.VRAM[17] = 0x00

	// OBJ palette color entry (index 129).
	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00

	p.RenderScanline(0)
	if got := p.FrontBuffer[0]; got == 0 {
		t.Fatalf("wrapped OBJ x pixel not rendered at left edge")
	}
}

func TestOBJXAbove255DoesNotWrapToLeft(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	// x=300 (0x12C) should be offscreen; must not appear at x=44.
	p.OAM[0] = 0x2C
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0
	p.OAM[512] = 0x01 // x-high=1, size=0

	p.VRAM[0] = 0x80
	p.VRAM[1] = 0x00
	p.VRAM[16] = 0x00
	p.VRAM[17] = 0x00
	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00

	p.RenderScanline(0)
	if got := p.FrontBuffer[44]; got != 0 {
		t.Fatalf("offscreen OBJ wrapped into visible area at x=44")
	}
}

func TestOAMPriorityRotationSelectsFirstSprite(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	// Two overlapping sprites at x=0. With priority rotation enabled and base at sprite 1,
	// sprite 1 should have higher priority and be visible.
	p.OAM[0] = 0
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0

	p.OAM[4] = 0
	p.OAM[5] = 0
	p.OAM[6] = 1
	p.OAM[7] = 0

	// Tile 0 renders color index 1, tile 1 renders color index 2 at x=0.
	p.VRAM[0] = 0x80
	p.VRAM[1] = 0x00
	p.VRAM[16] = 0x00
	p.VRAM[17] = 0x00

	base1 := 16 * 2
	p.VRAM[base1] = 0x00
	p.VRAM[base1+1] = 0x80
	p.VRAM[base1+16] = 0x00
	p.VRAM[base1+17] = 0x00

	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00
	p.CGRAM[130*2] = 0x00
	p.CGRAM[130*2+1] = 0x03

	// Base OAM address points to sprite 1 (byte addr 4), enable priority rotation.
	p.WriteRegister(0x2102, 0x02)
	p.WriteRegister(0x2103, 0x80)
	p.RenderScanline(0)

	if got := p.FrontBuffer[0]; got != (uint16(p.CGRAM[130*2]) | uint16(p.CGRAM[130*2+1])<<8) {
		t.Fatalf("priority rotation did not select sprite 1 at x=0, got %04X", got)
	}
}

func TestOBJAttributePriorityBeatsOrder(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.TM = 0x10 // OBJ only

	// Two overlapping sprites at x=0.
	// Sprite 0 has lower attr priority, sprite 1 has higher attr priority.
	p.OAM[0] = 0
	p.OAM[1] = 0
	p.OAM[2] = 0
	p.OAM[3] = 0x00 // priority=0

	p.OAM[4] = 0
	p.OAM[5] = 0
	p.OAM[6] = 1
	p.OAM[7] = 0x30 // priority=3

	// Tile 0 renders color index 1, tile 1 renders color index 2 at x=0.
	p.VRAM[0] = 0x80
	p.VRAM[1] = 0x00
	p.VRAM[16] = 0x00
	p.VRAM[17] = 0x00

	base1 := 16 * 2
	p.VRAM[base1] = 0x00
	p.VRAM[base1+1] = 0x80
	p.VRAM[base1+16] = 0x00
	p.VRAM[base1+17] = 0x00

	p.CGRAM[129*2] = 0x1F
	p.CGRAM[129*2+1] = 0x00
	p.CGRAM[130*2] = 0x00
	p.CGRAM[130*2+1] = 0x03

	p.RenderScanline(0)
	want := uint16(p.CGRAM[130*2]) | uint16(p.CGRAM[130*2+1])<<8
	if got := p.FrontBuffer[0]; got != want {
		t.Fatalf("OBJ attr priority not respected: got %04X want %04X", got, want)
	}
}
