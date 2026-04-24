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

// optFillBG1Tile writes a solid-pixel-1 tile row (4bpp, all 8 pixels
// encode palette index 1) at VRAM byte offset tileBase. Used by mosaic
// composition tests to give every BG1 screen-x a non-transparent pixel.
func optFillBG1Tile(p *PPU, tileBase, paletteSlot int) {
	// 4bpp plane 0 = 0xFF -> bit 0 (c0 = 1) at every pixel of the row.
	p.VRAM[tileBase+0] = 0xFF
	// palette slot lookup (palette 0, index 1) = CGRAM[1]. Callers set
	// the CGRAM entry themselves via paletteSlot.
	p.CGRAM[paletteSlot*2] = byte(paletteSlot)
	p.CGRAM[paletteSlot*2+1] = byte(paletteSlot) << 4
}

// TestPixelWalkMosaicFirstThenOPT pins the composition order of mosaic
// horizontal snap and OPT carve-out: mosaic's effX = x - (x % size) is
// computed BEFORE the OPT carve-out test offsetX = effX + (scrollX & 7).
// The wrong order would fire OPT at x=5 (raw x+3 = 8) instead of
// x=6 (effX+3 = 9 is the first block-anchor to cross the threshold).
//
// Fixture: BG1 tile 0 fills with CGRAM[1] (solid), tile 1 fills with
// CGRAM[2] (solid). Tilemap cols 0..1 use tile 0, col 2 uses tile 1.
// OPT entry at BG3 (0,0) with bit 13 + value 8 shifts a target col's
// fetch forward 8 pixels, so an OPT-fired column sampling at fetch col
// k will instead sample col k+1's tile. With fineX=3, mosaic size 2:
//   - x=5: effX=4, hoffset=4+3=7 (tile col 0), offsetX=7 -> OPT SKIP
//     -> tile 0 -> CGRAM[1].
//   - x=6: effX=6, hoffset=6+3=9 (tile col 1), offsetX=9 -> OPT FIRE
//     -> fetch shifts forward to tile col 2 -> tile 1 -> CGRAM[2].
// The wrong ordering would put OPT FIRE at x=5 (raw 5+3=8) giving
// CGRAM[2] there too.
func TestPixelWalkMosaicFirstThenOPT(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	p.BG1HOFS = 3
	p.MOSAIC = 0x11 // size = 2, BG1 enable bit 0

	// Tilemap: col 0 = tile 0, col 1 = tile 0, col 2 = tile 1, rest = tile 0.
	p.VRAM[0*2+0], p.VRAM[0*2+1] = 0, 0
	p.VRAM[1*2+0], p.VRAM[1*2+1] = 0, 0
	p.VRAM[2*2+0], p.VRAM[2*2+1] = 1, 0

	// Tile 0 at 0x2000, tile 1 at 0x2020. Palette 0 throughout, so the
	// pixel CGRAM lookup = index 1 (tile pixel value 1).
	optFillBG1Tile(p, 0x2000, 1) // tile 0 -> CGRAM[1]
	// Tile 1: same row layout but we want a different CGRAM index. Use
	// plane 0 & 1 so pixel index = 0b11 = 3. Set CGRAM[3] distinctly.
	p.VRAM[0x2020+0] = 0xFF
	p.VRAM[0x2020+1] = 0xFF
	p.CGRAM[3*2] = 0x33
	p.CGRAM[3*2+1] = 0x33

	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)

	line := renderPixelWalk(p, 0)

	want1 := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	want3 := uint16(p.CGRAM[3*2]) | uint16(p.CGRAM[3*2+1])<<8
	if want1 == want3 {
		t.Fatalf("fixture sanity: CGRAM[1] == CGRAM[3] (%04X), test cannot distinguish", want1)
	}

	// x=5: mosaic snap -> effX=4, offsetX=7, OPT skipped, tile col 0,
	// tile 0 pixel 1 -> CGRAM[1]. Wrong ordering would fire OPT and
	// give CGRAM[3].
	if line[5] != want1 {
		t.Fatalf("mosaic+OPT: x=5 = %04X, want %04X (CGRAM[1]); mosaic "+
			"snap must precede OPT carve-out", line[5], want1)
	}
	// x=6: mosaic anchor -> effX=6, offsetX=9, OPT fires, tile col 1
	// fetch shifts to col 2 -> tile 1 pixel 3 -> CGRAM[3]. Proves OPT
	// still reaches mosaic-snapped anchors (i.e. mosaic doesn't silently
	// kill OPT).
	if line[6] != want3 {
		t.Fatalf("mosaic+OPT: x=6 = %04X, want %04X (CGRAM[3]); OPT "+
			"must fire at the mosaic anchor effX=6 offsetX=9", line[6], want3)
	}
}

// TestPixelWalkMosaicFeedsDirectColor pins that the mosaic H snap feeds
// the same pixel index into the Direct Color decoder for every pixel in
// the block — i.e. effX = anchorX makes x=0..3 all sample the anchor's
// 8bpp pixel byte and produce the same BGR555 output through Direct
// Color, not CGRAM.
func TestPixelWalkMosaicFeedsDirectColor(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 3  // 8bpp BG1
	p.TM = 0x01
	p.CGWSEL = 0x01 // Direct Color ON
	p.BG12NBA = 0x01
	p.MOSAIC = 0x31 // size = 4, BG1 enable bit 0

	// Tilemap entry 0 uses tile 0 palette 5 (b2b1b0 = 101).
	entry := uint16(5) << 10
	p.VRAM[0] = byte(entry & 0xFF)
	p.VRAM[1] = byte(entry >> 8)

	// Build an 8bpp tile row 0 with distinct pixel indices at x=0 and
	// x=2 so a non-mosaic render would show different Direct Color
	// outputs at those positions.
	tileBase := 0x2000
	// px 0: plane0,1,2,3,4,5,6,7 bit7 = 1 -> index 0xFF (high bits and
	// low bits all set).
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+1] = 0x80
	p.VRAM[tileBase+16] = 0x80
	p.VRAM[tileBase+17] = 0x80
	p.VRAM[tileBase+32] = 0x80
	p.VRAM[tileBase+33] = 0x80
	p.VRAM[tileBase+48] = 0x80
	p.VRAM[tileBase+49] = 0x80
	// px 2: only plane0 set (index 0x01).
	p.VRAM[tileBase+0] |= 0x20 // bit 5 (7-2) for px2 plane 0
	// Poison CGRAM so a mistaken CGRAM lookup would show up.
	p.CGRAM[0xFF*2] = 0xAA
	p.CGRAM[0xFF*2+1] = 0x55
	p.CGRAM[0x01*2] = 0x22
	p.CGRAM[0x01*2+1] = 0x33

	// Expected Direct Color for px=0xFF, palette=5: from the pure
	// Direct Color test, 0x739E.
	wantAnchor := uint16(0x1C<<10) | uint16(0x1C<<5) | uint16(0x1E)

	line := renderPixelWalk(p, 0)

	// With mosaic size 4, x=0..3 all sample the anchor (px0 = 0xFF)
	// and produce the same Direct Color value.
	for x := 0; x < 4; x++ {
		if line[x] != wantAnchor {
			t.Fatalf("mosaic+DirectColor: x=%d = %04X, want %04X "+
				"(all pixels in the block should equal the anchor's "+
				"Direct Color output)", x, line[x], wantAnchor)
		}
	}

	// Sanity: without mosaic the anchor and x=2 produce different
	// Direct Color outputs. If they didn't, the test couldn't
	// distinguish mosaic-on-anchor from no-mosaic.
	p.MOSAIC = 0x00
	baseline := renderPixelWalk(p, 0)
	if baseline[0] == baseline[2] {
		t.Fatalf("baseline sanity: non-mosaic px0 == px2 (%04X); test "+
			"fixture fails to distinguish anchor from block pixels",
			baseline[0])
	}
}

// TestPixelWalkOPTAndDirectColorMode4 pins the one combination that
// actually ships in commercial ROMs: Mode 4 BG1 is 8bpp, OPT applies to
// BG1/BG2, and Direct Color ($2130 bit 0) decodes the 8bpp BG1 palette.
// The per-pixel path must: (a) apply OPT to compute the shifted tile
// fetch, then (b) run the shifted tile's 8bpp pixel byte + palette bits
// through Direct Color.
//
// Setup: a BG1 tile at column-1 slot with palette 0, and a second tile
// at column-2 slot with palette 5. OPT shifts column-1's fetch to
// column-2, so the pixel at screen-x 8 should carry palette-5's Direct
// Color decode, not palette-0's.
func TestPixelWalkOPTAndDirectColorMode4(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 4 // 8bpp BG1, 2bpp BG2, OPT-capable
	p.TM = 0x01
	p.CGWSEL = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04

	// BG1 tilemap: col 0 tile 0 palette 0, col 1 tile 1 palette 0, col
	// 2 tile 2 palette 5. (palette at BG1 tilemap bits 10..12)
	for col := 0; col < 3; col++ {
		palette := uint16(0)
		tile := uint16(col)
		if col == 2 {
			palette = 5
		}
		entry := tile | (palette << 10)
		addr := col * 2
		p.VRAM[addr] = byte(entry & 0xFF)
		p.VRAM[addr+1] = byte(entry >> 8)
	}

	// 8bpp tile 2 with pixel byte 0xFF at column 0 of the tile row.
	// tile 2 base = 0x2000 + 2*64 = 0x2080. 8bpp row-0 plane 0..7 each
	// get their bit-7 set to produce c = 0xFF.
	t2 := 0x2000 + 2*64
	p.VRAM[t2+0] = 0x80
	p.VRAM[t2+1] = 0x80
	p.VRAM[t2+16] = 0x80
	p.VRAM[t2+17] = 0x80
	p.VRAM[t2+32] = 0x80
	p.VRAM[t2+33] = 0x80
	p.VRAM[t2+48] = 0x80
	p.VRAM[t2+49] = 0x80

	// Poison CGRAM[0xFF] so a non-Direct-Color path would show.
	p.CGRAM[0xFF*2] = 0x11
	p.CGRAM[0xFF*2+1] = 0x22

	// OPT Mode 4: BG3 entry at col 0 with bit 13 (BG1 valid) + bit 15=0
	// (H offset) + value 8 -> BG1 column 1 shifts by 8 pixels, fetching
	// BG1's tile 2 (palette 5) instead of tile 1.
	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)

	line := renderPixelWalk(p, 0)

	// Expected Direct Color for px = 0xFF, palette = 5 (b2b1b0 = 101):
	// R = (7<<2) | (1<<1) = 0x1E
	// G = (7<<2) | 0      = 0x1C
	// B = (3<<3) | 4      = 0x1C
	want := uint16(0x1C<<10) | uint16(0x1C<<5) | uint16(0x1E)
	if line[8] != want {
		t.Fatalf("OPT+DirectColor Mode 4: x=8 = %04X, want %04X "+
			"(OPT should have shifted BG1 column-1 fetch to tile 2 "+
			"palette 5, and Direct Color should decode that)",
			line[8], want)
	}
}

// seedOBJ places a single 8x8 sprite at OAM[0] with the given (x,y),
// tile index, palette (0..7), and priority (0..3). Palette slot N in
// OAM is 128 + N*16 + pixelIndex in CGRAM. The sprite's tile row 0 is
// filled with a solid pixel index 1.
func seedOBJ(p *PPU, x, y int, tile uint8, palette, priority, cgramSeed byte) {
	p.OAM[0] = byte(x & 0xFF)
	p.OAM[1] = byte(y)
	p.OAM[2] = tile
	p.OAM[3] = (palette << 1) | (priority << 4)
	p.OAM[512] = 0 // xHigh=0, sizeBit=0 for sprite 0

	// OBJ tile table at $0000 via OBSEL (default). Tile N is at
	// word 0x1000*nameSel + tile*32 bytes. OBSEL = 0, nameSel = 0.
	tileBase := int(tile) * 32
	p.VRAM[tileBase+0] = 0xFF  // plane 0 = all 1 -> pixel index 1 across the row
	p.VRAM[tileBase+1] = 0x00
	p.VRAM[tileBase+16] = 0x00 // plane 2 = 0
	p.VRAM[tileBase+17] = 0x00

	// OBJ palette bases at CGRAM index 128. Palette 0 + index 1 = 129.
	cgIdx := 128 + int(palette)*16 + 1
	p.CGRAM[cgIdx*2] = cgramSeed
	p.CGRAM[cgIdx*2+1] = cgramSeed
}

// TestPixelWalkOBJPlotsWithPriorityTable pins OBJ's thin adapter:
// renderOBJ's evaluateOBJ fills p.objColor/p.objPrio, then the
// pixel-walk OBJ plot routes each priority into the mode's OBJ.N slot
// from the priority table. A single sprite at (16, 0) with priority 3
// should win against the backdrop (OBJ.3 is slot 10 in Mode 1 default).
func TestPixelWalkOBJPlotsWithPriorityTable(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x10 // OBJ main screen only
	seedOBJ(p, 16, 0, 1, 2, 3, 0x5A)

	line := renderPixelWalk(p, 0)

	cgIdx := 128 + 2*16 + 1
	want := uint16(p.CGRAM[cgIdx*2]) | uint16(p.CGRAM[cgIdx*2+1])<<8
	for x := 16; x < 24; x++ {
		if line[x] != want {
			t.Fatalf("OBJ plot x=%d = %04X, want %04X (priority-3 sprite "+
				"should beat backdrop in Mode 1)", x, line[x], want)
		}
	}
}

// TestPixelWalkBGMaskedByWindow pins that a BG1 pixel inside the main-
// window mask is suppressed in the pixel-walk renderer, matching
// tile-walk semantics (layerMaskedByWindow already works per-pixel).
// At x=8 the BG1 layer is masked -> backdrop shows; at x=20 the layer
// is not masked -> BG1 color shows.
func TestPixelWalkBGMaskedByWindow(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01

	// Tile 0 with pixel 1 across all 8 columns (plane 0 = 0xFF).
	p.VRAM[0] = 0
	p.VRAM[1] = 0
	p.VRAM[0x2000] = 0xFF
	p.CGRAM[1*2] = 0x12
	p.CGRAM[1*2+1] = 0x34
	// Backdrop.
	p.CGRAM[0] = 0x55
	p.CGRAM[1] = 0x22

	// Window 1 covers x=0..15, BG1 windowed on main screen.
	p.WH0 = 0
	p.WH1 = 15
	p.W12SEL = 0x02
	p.WBGLOG = 0x00
	p.TMW = 0x01

	line := renderPixelWalk(p, 0)

	wantBG := uint16(0x12) | uint16(0x34)<<8
	wantBack := uint16(0x55) | uint16(0x22)<<8
	if line[8] != wantBack {
		t.Fatalf("BG1 at x=8 should be masked -> backdrop; got %04X want %04X",
			line[8], wantBack)
	}
	if line[20] != wantBG {
		t.Fatalf("BG1 at x=20 outside window should draw; got %04X want %04X",
			line[20], wantBG)
	}
}

// TestPixelWalkColorMathAgainstSubscreen pins that the pixel-walk
// renderer populates pwBelow via the TS pass, and applyColorMathLine
// reads subscreen operands from that buffer through the
// pixelWalkApplyColorMath shim. Setup: BG1 on main (solid color A),
// BG2 on sub (solid color B), CGWSEL bit 1 + CGADSUB add of sub vs
// main. Expected output at the shared pixel is the per-channel
// clamped-sum of A and B.
func TestPixelWalkColorMathAgainstSubscreen(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01 // BG1 on main
	p.TS = 0x02 // BG2 on sub
	p.BG12NBA = 0x11 // BG1 tiles at 0x2000, BG2 tiles at 0x2000 too (shared OK since different tilemap cols)

	// BG1 tilemap entry 0 -> tile 0.
	p.VRAM[0] = 0
	p.VRAM[1] = 0
	// BG2 tilemap at BG2SC = default 0 -> same base, but BG2SC=0 shares
	// BG1's tilemap area. Use BG2SC=0x04 for 0x800.
	p.BG2SC = 0x04
	p.VRAM[0x800] = 0
	p.VRAM[0x801] = 0

	// Tile 0 pixel 1 across the row (both layers share tile 0).
	p.VRAM[0x2000] = 0xFF

	// CGRAM entries: BG1 palette 0 idx 1, BG2 palette 0 idx 1.
	// Mode 1 uses 4bpp for BG1/BG2 -> palette lookup = palette*16 + c.
	// Palette 0, c=1 -> CGRAM[1]. Both layers want index 1.
	// Distinct BG1 and BG2 via palette selection from the tilemap.
	// Both tilemap entries use palette 0, so both layers hit CGRAM[1].
	// That won't distinguish them, so set BG2SC's tilemap entry to
	// palette 1 (shifted 10).
	entryBG2 := uint16(1) << 10
	p.VRAM[0x800] = byte(entryBG2 & 0xFF)
	p.VRAM[0x801] = byte(entryBG2 >> 8)

	// BG1 pixel 1 -> CGRAM[1]. BG2 pixel 1 -> CGRAM[17].
	// Use RGB with known channel bits to make the math visible.
	p.CGRAM[1*2] = 0x03  // R=3, G=0, B=0
	p.CGRAM[1*2+1] = 0x00
	p.CGRAM[17*2] = 0x00 // R=0, G=2, B=0
	p.CGRAM[17*2+1] = 0x00
	p.CGRAM[17*2] = 0x40 // low-byte bit6=1: G bit 1 = 1, R=0
	p.CGRAM[17*2+1] = 0x00

	// BG1 color = 0x0003 (R=3, G=0, B=0).
	// BG2 color = 0x0040 -> B=0, G=(0x40>>5)&0x1F = 2, R=0. So (R=0, G=2, B=0).
	// After CGADSUB=0x01 (BG1 source, no subtract, no half), result at x=0:
	// main (BG1 = R3,G0,B0) + sub (BG2 = R0,G2,B0) = (R3, G2, B0) -> 0x0043.
	p.CGWSEL = 0x02 // subscreen path for color math
	p.CGADSUB = 0x01 // apply to BG1 source, add, no half

	line := renderPixelWalk(p, 0)

	want := uint16(3) | uint16(2)<<5 // R=3, G=2, B=0 packed
	if line[0] != want {
		t.Fatalf("color math BG1+sub(BG2) at x=0 = %04X, want %04X "+
			"(R=3 G=2 B=0 = 0043)", line[0], want)
	}
}
