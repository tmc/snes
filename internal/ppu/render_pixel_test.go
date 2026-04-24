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

// renderPixelWalk is a thin helper that runs the Phase 2.5 renderer and
// returns the produced FrontBuffer slice for the scanline. Tests for
// Phase 2 feature parity use this rather than mutating usePixelWalk.
func renderPixelWalk(p *PPU, y int) []uint16 {
	p.renderScanlinePixelWalk(y)
	start := y * p.Width
	return p.FrontBuffer[start : start+p.Width]
}

// TestPixelWalkMode3DirectColor mirrors TestRenderScanlineMode3DirectColor
// against the pixel-walk path: 8bpp BG1 pixel with $2130 bit 0 set
// produces the bit-encoded BGR555 via the Direct Color branch without
// consulting CGRAM.
func TestPixelWalkMode3DirectColor(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 3
	p.TM = 0x01
	p.CGWSEL = 0x01
	p.BG12NBA = 0x01

	entry := uint16(5) << 10
	p.VRAM[0] = byte(entry & 0xFF)
	p.VRAM[1] = byte(entry >> 8)
	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+1] = 0x80
	p.VRAM[tileBase+16] = 0x80
	p.VRAM[tileBase+17] = 0x80
	p.VRAM[tileBase+32] = 0x80
	p.VRAM[tileBase+33] = 0x80
	p.VRAM[tileBase+48] = 0x80
	p.VRAM[tileBase+49] = 0x80
	p.CGRAM[0xFF*2] = 0xAA
	p.CGRAM[0xFF*2+1] = 0x55

	want := uint16(0x1C<<10) | uint16(0x1C<<5) | uint16(0x1E)
	line := renderPixelWalk(p, 0)
	if got := line[0]; got != want {
		t.Fatalf("pixel-walk Direct Color pixel = %04X, want %04X", got, want)
	}
}

// TestPixelWalkMosaicHorizontalBG1 pins the pixel-walk version of
// horizontal mosaic: enabling size=4 on BG1 replicates the anchor's
// fetch across the block without any post-pass (the inner loop snaps
// effX to the anchor, so every pixel in the block samples the same
// underlying tile pixel — transparency and all).
func TestPixelWalkMosaicHorizontalBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	mosaicSet4pxHorizontalTile(p, 0x2000)

	p.MOSAIC = 0x00
	base := renderPixelWalk(p, 0)
	base0, base3, base4 := base[0], base[3], base[4]
	if base0 == base3 || base0 == base4 {
		t.Fatalf("baseline sanity: px0=%04X px3=%04X px4=%04X", base0, base3, base4)
	}

	p.MOSAIC = 0x31
	got := renderPixelWalk(p, 0)
	for x := 0; x < 4; x++ {
		if got[x] != base0 {
			t.Fatalf("H block[0] x=%d = %04X, want %04X", x, got[x], base0)
		}
	}
	for x := 4; x < 8; x++ {
		if got[x] != base4 {
			t.Fatalf("H block[1] x=%d = %04X, want %04X", x, got[x], base4)
		}
	}
}

// TestPixelWalkOPTMode2HOffsetBG1 mirrors the tile-walk OPT Mode 2 test:
// BG3 tilemap entry at (0, 0) with bit 13 set and value 8 shifts BG1's
// column-1 tile fetch, so the pixel at screen-x 8 exposes what column-2
// of BG1's tilemap would have drawn.
func TestPixelWalkOPTMode2HOffsetBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	optSetDistinctBG1Tiles(p)
	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)

	line := renderPixelWalk(p, 0)

	wantIdx := 3
	want := uint16(wantIdx) | uint16(wantIdx)<<12
	if line[8] != want {
		t.Fatalf("pixel-walk Mode 2 H-OPT @x=8 = %04X, want %04X", line[8], want)
	}

	// Carve-out sanity: column 0 must be unchanged vs a Mode-1 baseline
	// (OPT disabled) — OPT only fires at offsetX >= 8.
	baseline := optBG1BaselineColor(t, nil, 0)
	if line[0] != baseline {
		t.Fatalf("pixel-walk Mode 2 OPT leaked into x=0; got %04X want %04X",
			line[0], baseline)
	}
}

// TestPixelWalkOPTPixelGranularCarveOut pins the one OPT behavior the
// tile-walk renderer DOES NOT get right: when scrollX & 7 is nonzero,
// bsnes's offsetX = x + (scrollX & 7) >= 8 means OPT fires partway
// through tile column 0, not uniformly from column 1. The pixel-walk
// path gets this correctly because its carve-out check is per-pixel.
//
// Setup: scrollX = 3 -> fineX = 3. The first screen column (x = 0..2)
// still satisfies offsetX = x + 3 < 8 so OPT is skipped; x = 5 hits
// offsetX = 8 and the BG3 lookup at bg3Col = ((8-8) + 0) >> 3 = 0
// delivers a shift.
func TestPixelWalkOPTPixelGranularCarveOut(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	p.BG1HOFS = 3 // fineX = 3
	optSetDistinctBG1Tiles(p)
	// bg3 entry at (0,0) with bit 13 and value 8: shift BG1 column by 8.
	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)

	line := renderPixelWalk(p, 0)

	// x = 0..2: offsetX = 3..5 < 8, OPT SKIPPED. Should match the
	// Mode-1 baseline at those positions with the same BG1HOFS.
	baseP := NewPPU()
	baseP.INIDISP = 0x0F
	baseP.BGMode = 1
	baseP.TM = 0x01
	baseP.BG12NBA = 0x01
	baseP.BG1HOFS = 3
	optSetDistinctBG1Tiles(baseP)
	baseLine := renderPixelWalk(baseP, 0)

	for x := 0; x <= 2; x++ {
		if line[x] != baseLine[x] {
			t.Fatalf("carve-out leak at x=%d: got %04X want %04X (baseline)",
				x, line[x], baseLine[x])
		}
	}
	// x = 5: first pixel where offsetX >= 8; OPT should have fired.
	// With OPT, the BG1 tile column is shifted so a different pixel
	// appears here vs baseline.
	if line[5] == baseLine[5] {
		t.Fatalf("carve-out missed at x=5: OPT should have fired (got identical %04X)",
			line[5])
	}
}
