package ppu

import "testing"

func TestVRAMAccess(t *testing.T) {
	p := NewPPU()

	// Set VRAM Addr to $1000
	p.WriteRegister(0x2116, 0x00) // VMADDL
	p.WriteRegister(0x2117, 0x10) // VMADDH

	if p.VRAMAddr != 0x1000 {
		t.Errorf("VRAM Addr mismatch. Got %04X, expected 1000", p.VRAMAddr)
	}

	// Write Data $AABB
	// 2118 writes Low (AA), no inc (default VMAIN=0)
	// 2119 writes High (BB), inc
	p.WriteRegister(0x2118, 0xAA)
	p.WriteRegister(0x2119, 0xBB)

	// VRAM is word addressed. Index 0x1000 * 2 = 0x2000 / 0x2001.
	if p.VRAM[0x2000] != 0xAA {
		t.Errorf("VRAM Low byte mismatch")
	}
	if p.VRAM[0x2001] != 0xBB {
		t.Errorf("VRAM High byte mismatch")
	}

	if p.VRAMAddr != 0x1001 {
		t.Errorf("VRAM Addr should increment to 1001. Got %04X", p.VRAMAddr)
	}
}

func TestVRAMReadWordModeRequiresDummyRead(t *testing.T) {
	p := NewPPU()
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

func TestCGRAMAccess(t *testing.T) {
	p := NewPPU()

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

func TestOAMAccess(t *testing.T) {
	p := NewPPU()

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
