package ppu

import "testing"

// TestRenderScanlinePixelWalkMode1BG1MatchesTileWalk pins that the Slice 1
// pixel-walk renderer produces an identical FrontBuffer to the tile-walk
// renderer for the minimal supported case — Mode 1 with BG1 enabled, no
// OPT/mosaic/direct-color/windowing/color-math. If these disagree, the
// refactor has already drifted before any real feature is wired.
//
// Tests call renderScanlinePixelWalk directly rather than setting
// usePixelWalk, so Slice 3's flag flip remains a one-line change in
// RenderScanline.
func TestRenderScanlinePixelWalkMode1BG1MatchesTileWalk(t *testing.T) {
	setup := func(p *PPU) {
		p.INIDISP = 0x0F
		p.BGMode = 1
		p.TM = 0x01
		p.BG12NBA = 0x01
		// Tilemap entry 0 -> tile 5, palette 2, priority 0.
		entry := uint16(5) | (uint16(2) << 10)
		p.VRAM[0] = byte(entry & 0xFF)
		p.VRAM[1] = byte(entry >> 8)
		// 4bpp tile 5 with pixel index 9 at x=3 (bit 4 from the left).
		// pixel 9 = 0b1001 -> planes 0 and 3 set for that column.
		tileBase := 0x2000 + 5*32
		p.VRAM[tileBase+0] = 0x10  // plane0 bit 4
		p.VRAM[tileBase+17] = 0x10 // plane3 bit 4
		// Palette 2 slot 9 -> CGRAM index 2*16+9 = 41.
		p.CGRAM[41*2] = 0xAB
		p.CGRAM[41*2+1] = 0x12
		// Backdrop.
		p.CGRAM[0] = 0x55
		p.CGRAM[1] = 0x00
	}

	tile := NewPPU()
	setup(tile)
	tile.RenderScanline(0) // tile-walk path (flag off)

	pixel := NewPPU()
	setup(pixel)
	pixel.renderScanlinePixelWalk(0)

	for x := 0; x < pixel.Width; x++ {
		if tile.FrontBuffer[x] != pixel.FrontBuffer[x] {
			t.Fatalf("pixel-walk vs tile-walk drift at x=%d: tile=%04X pixel=%04X",
				x, tile.FrontBuffer[x], pixel.FrontBuffer[x])
		}
	}
}

// TestPriorityTableMode1DefaultAndBG3HiOverride pins that the Mode 1
// priority tables match the bsnes slot assignments for the two $2105 bit
// 3 states: default (BG3.1 at slot 3) vs override (BG3.1 at slot 10).
// Hardcoded against the bsnes-fast io.cpp numbers so a transcription
// typo surfaces as a red test.
func TestPriorityTableMode1DefaultAndBG3HiOverride(t *testing.T) {
	p := NewPPU()
	p.BGMode = 1

	tbl := p.priorityTableFor()
	// bsnes Mode 1 default: BG3.1 below OBJ.3, slot 3.
	if tbl[slotBG3Hi] != 3 {
		t.Fatalf("Mode 1 default BG3.1 slot = %d, want 3", tbl[slotBG3Hi])
	}
	if tbl[slotOBJ3] != 10 {
		t.Fatalf("Mode 1 default OBJ.3 slot = %d, want 10", tbl[slotOBJ3])
	}

	// $2105 bit 3 promotes BG3.1 to slot 10 above OBJ.3 (which demotes to 9).
	p.BGMode = 1 | 0x08
	tbl = p.priorityTableFor()
	if tbl[slotBG3Hi] != 10 {
		t.Fatalf("Mode 1 BG3-hi BG3.1 slot = %d, want 10", tbl[slotBG3Hi])
	}
	if tbl[slotOBJ3] != 9 {
		t.Fatalf("Mode 1 BG3-hi OBJ.3 slot = %d, want 9", tbl[slotOBJ3])
	}
}

// TestPlotRespectsSlotWinnerTakeAll pins the compositor's strict >
// compare: a larger slot overwrites a smaller one, a smaller slot loses,
// and a zero slot is a no-op.
func TestPlotRespectsSlotWinnerTakeAll(t *testing.T) {
	var buf [512]layerPixel

	plot(&buf, 10, 5, 0x1111, sourceBG1)
	if buf[10].color != 0x1111 || buf[10].prio != 5 {
		t.Fatalf("initial plot lost: %+v", buf[10])
	}

	plot(&buf, 10, 3, 0x2222, sourceBG2) // lower slot -> dropped
	if buf[10].color != 0x1111 || buf[10].prio != 5 {
		t.Fatalf("lower-slot plot overwrote: %+v", buf[10])
	}

	plot(&buf, 10, 8, 0x3333, sourceOBJ) // higher slot -> wins
	if buf[10].color != 0x3333 || buf[10].prio != 8 {
		t.Fatalf("higher-slot plot didn't win: %+v", buf[10])
	}

	plot(&buf, 10, 0, 0x4444, sourceBG3) // zero slot -> no-op
	if buf[10].color != 0x3333 || buf[10].prio != 8 {
		t.Fatalf("zero-slot plot mutated buffer: %+v", buf[10])
	}
}

// TestRenderScanlinePixelWalkForceBlank pins that INIDISP bit 7 zeroes
// the scanline regardless of BG/OBJ state, matching the tile-walk
// renderer.
func TestRenderScanlinePixelWalkForceBlank(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x80 // force blank
	p.BGMode = 1
	p.TM = 0x01
	p.CGRAM[0] = 0x55
	p.CGRAM[1] = 0x2A

	p.renderScanlinePixelWalk(0)
	for x := 0; x < p.Width; x++ {
		if got := p.FrontBuffer[x]; got != 0 {
			t.Fatalf("force-blank x=%d = %04X, want 0", x, got)
		}
	}
}
