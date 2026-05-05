package ppu

import "testing"

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
// returns the produced FrontBuffer slice for the scanline.
func renderPixelWalk(p *PPU, y int) []uint16 {
	p.renderScanlinePixelWalk(y)
	start := y * p.Width
	return p.FrontBuffer[start : start+p.Width]
}

func setBG1Tile0Pixel(p *PPU, tile int, color byte) {
	tileBase := 0x2000 + tile*32
	if color&0x01 != 0 {
		p.VRAM[tileBase+0] = 0x80
	}
	if color&0x02 != 0 {
		p.VRAM[tileBase+1] = 0x80
	}
	if color&0x04 != 0 {
		p.VRAM[tileBase+16] = 0x80
	}
	if color&0x08 != 0 {
		p.VRAM[tileBase+17] = 0x80
	}
	p.CGRAM[int(color)*2+0] = color
	p.CGRAM[int(color)*2+1] = color << 4
}

func TestPixelWalkBGTilemapScreenSize(t *testing.T) {
	tests := []struct {
		name     string
		sc       uint8
		col      int
		row      int
		hofs     uint16
		vofs     uint16
		wantTile uint16
		want     uint16
	}{
		{"64x32 right", 0x01, 32, 0, 256, 0, 1, 0x1001},
		{"32x64 bottom", 0x02, 0, 32, 0, 256, 2, 0x2002},
		{"64x64 bottom-right", 0x03, 32, 32, 256, 256, 3, 0x3003},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPPU()
			p.INIDISP = 0x0F
			p.BGMode = 1
			p.TM = 0x01
			p.BG1SC = tt.sc
			p.BG12NBA = 0x01
			p.BG1HOFS = tt.hofs
			p.BG1VOFS = tt.vofs
			p.VRAM[bgTilemapEntryAddr(tt.sc, tt.col, tt.row)+0] = byte(tt.wantTile)
			p.VRAM[bgTilemapEntryAddr(tt.sc, tt.col, tt.row)+1] = byte(tt.wantTile >> 8)
			setBG1Tile0Pixel(p, int(tt.wantTile), byte(tt.wantTile))

			line := renderPixelWalk(p, 0)
			if line[0] != tt.want {
				t.Fatalf("screen-sized tilemap pixel = %04X, want %04X", line[0], tt.want)
			}
		})
	}
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
//
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
	p.BGMode = 3 // 8bpp BG1
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
	p.VRAM[tileBase+0] = 0xFF // plane 0 = all 1 -> pixel index 1 across the row
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

func TestPixelWalkOBJColorMathPalettes4Through7Only(t *testing.T) {
	for _, tt := range []struct {
		name    string
		palette byte
		want    uint16
	}{
		{"palette 3", 3, pack555(1, 0, 0)},
		{"palette 4", 4, pack555(2, 0, 0)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPPU()
			p.INIDISP = 0x0F
			p.BGMode = 1
			p.TM = 0x10
			p.CGADSUB = sourceOBJ
			p.WriteRegister(0x2132, 0x21) // fixed red = 1

			seedOBJ(p, 0, 0, 1, tt.palette, 3, 0)
			setCGRAMColor(p, 128+tt.palette*16+1, pack555(1, 0, 0))

			line := renderPixelWalk(p, 0)
			if got := line[0]; got != tt.want {
				t.Fatalf("OBJ palette %d color math = %04X, want %04X", tt.palette, got, tt.want)
			}
		})
	}
}

func TestPixelWalkOBJColorMathPaletteGateUsesColorWindow(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x10
	p.CGADSUB = sourceOBJ
	p.CGWSEL = 0x40  // main color math only inside color window
	p.WOBJSEL = 0x20 // color window 1 enabled
	p.WH0 = 0
	p.WH1 = 0
	p.WriteRegister(0x2132, 0x21) // fixed red = 1

	seedOBJ(p, 0, 0, 1, 4, 3, 0)
	setCGRAMColor(p, 128+4*16+1, pack555(1, 0, 0))

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != pack555(2, 0, 0) {
		t.Fatalf("OBJ palette 4 inside color window = %04X, want %04X", got, pack555(2, 0, 0))
	}
	if got := line[1]; got != pack555(1, 0, 0) {
		t.Fatalf("OBJ palette 4 outside color window = %04X, want unchanged %04X", got, pack555(1, 0, 0))
	}
}

func TestPixelWalkOBJColorMathPaletteGateUsesSubscreen(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x10 // OBJ on main
	p.TS = 0x02 // BG2 on sub
	p.BG12NBA = 0x10
	p.BG2SC = 0x04
	p.CGWSEL = 0x02
	p.CGADSUB = sourceOBJ

	seedOBJ(p, 0, 0, 1, 4, 3, 0)
	setCGRAMColor(p, 128+4*16+1, pack555(1, 0, 0))

	p.VRAM[0x800] = 0
	p.VRAM[0x801] = 0
	p.VRAM[0x2000] = 0xFF
	setCGRAMColor(p, 1, pack555(0, 1, 0))

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != pack555(1, 1, 0) {
		t.Fatalf("OBJ palette 4 plus subscreen = %04X, want %04X", got, pack555(1, 1, 0))
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
	p.TM = 0x01      // BG1 on main
	p.TS = 0x02      // BG2 on sub
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
	p.CGRAM[1*2] = 0x03 // R=3, G=0, B=0
	p.CGRAM[1*2+1] = 0x00
	p.CGRAM[17*2] = 0x00 // R=0, G=2, B=0
	p.CGRAM[17*2+1] = 0x00
	p.CGRAM[17*2] = 0x40 // low-byte bit6=1: G bit 1 = 1, R=0
	p.CGRAM[17*2+1] = 0x00

	// BG1 color = 0x0003 (R=3, G=0, B=0).
	// BG2 color = 0x0040 -> B=0, G=(0x40>>5)&0x1F = 2, R=0. So (R=0, G=2, B=0).
	// After CGADSUB=0x01 (BG1 source, no subtract, no half), result at x=0:
	// main (BG1 = R3,G0,B0) + sub (BG2 = R0,G2,B0) = (R3, G2, B0) -> 0x0043.
	p.CGWSEL = 0x02  // subscreen path for color math
	p.CGADSUB = 0x01 // apply to BG1 source, add, no half

	line := renderPixelWalk(p, 0)

	want := uint16(3) | uint16(2)<<5 // R=3, G=2, B=0 packed
	if line[0] != want {
		t.Fatalf("color math BG1+sub(BG2) at x=0 = %04X, want %04X "+
			"(R=3 G=2 B=0 = 0043)", line[0], want)
	}
}

func TestPixelWalkBrightnessAppliesAfterColorMath(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x07
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.CGADSUB = sourceBG1
	p.WriteRegister(0x2132, 0x28) // fixed red = 8

	p.VRAM[0] = 0
	p.VRAM[1] = 0
	p.VRAM[0x2000] = 0xFF
	setCGRAMColor(p, 1, pack555(8, 0, 0))

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != pack555(8, 0, 0) {
		t.Fatalf("brightness after color math = %04X, want %04X", got, pack555(8, 0, 0))
	}
}

func TestPixelWalkMode5ColorMathUsesFixedColorUnlessBlendMode(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	p.CGADSUB = sourceBG1
	p.WriteRegister(0x2132, 0x21) // fixed red = 1

	p.VRAM[0] = 0
	p.VRAM[1] = 0
	p.VRAM[0x2000] = 0xFF
	setCGRAMColor(p, 1, pack555(2, 0, 0))

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != pack555(3, 0, 0) {
		t.Fatalf("Mode 5 math with fixed color = %04X, want %04X", got, pack555(3, 0, 0))
	}
}

func TestPixelWalkMode5ColorMathUsesEvenSubpixelWithBlendMode(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	p.CGWSEL = 0x02
	p.CGADSUB = sourceBG1
	p.WriteRegister(0x2132, 0x21) // fixed red = 1

	p.VRAM[0] = 0
	p.VRAM[1] = 0
	p.VRAM[0x2000] = 0xFF
	setCGRAMColor(p, 1, pack555(2, 0, 0))

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != pack555(4, 0, 0) {
		t.Fatalf("Mode 5 math with even subpixel = %04X, want %04X", got, pack555(4, 0, 0))
	}
}

// TestPixelWalkMode1BG3PriorityInversion pins the observable end of the
// Mode 1 $2105 bit 3 priority override. With bit 3 clear, a BG3.1 pixel
// and an OBJ.3 pixel at the same screen-X resolve to OBJ (slot 10 beats
// slot 3). With bit 3 set, BG3.1 jumps to slot 10 and OBJ.3 demotes to
// slot 9, so the same scene resolves to BG3. Exercises the priority-
// table swap end-to-end, not just the table-lookup unit test.
func TestPixelWalkMode1BG3PriorityInversion(t *testing.T) {
	setup := func(p *PPU, bg3Hi bool) {
		p.INIDISP = 0x0F
		if bg3Hi {
			p.BGMode = 1 | 0x08 // bit 3 set
		} else {
			p.BGMode = 1 // bit 3 clear
		}
		p.TM = 0x14 // BG3 + OBJ on main screen
		// BG3 tilemap and tile data. BG3 is 2bpp under Mode 1.
		// Default BG3SC = 0, so tilemap at VRAM word 0.
		// BG34NBA default = 0 -> BG3 tile base at word 0x0000 too, but
		// that collides with the tilemap entry at word 0. Move BG3SC
		// to word 0x400 so the tilemap entry lives out of the way.
		p.BG3SC = 0x04 // BG3 tilemap at word 0x400 (byte 0x800)
		// Tilemap entry: tile 1, priority 1 (high), palette 0.
		entry := uint16(1) | (uint16(1) << 13)
		p.VRAM[0x800] = byte(entry & 0xFF)
		p.VRAM[0x801] = byte(entry >> 8)
		// Tile 1 at VRAM byte 0x0020 (2bpp, 16 bytes per tile -> tile 1 at byte 16).
		// 2bpp plane 0 = 0xFF -> pixel index 1 across the row.
		p.VRAM[16] = 0xFF
		// BG3 palette 0 idx 1 -> CGRAM[1]. Set to a distinct color.
		p.CGRAM[1*2] = 0x11
		p.CGRAM[1*2+1] = 0x11
		// OBJ at (0, 0) priority 3 with a distinct CGRAM seed.
		seedOBJ(p, 0, 0, 1, 0, 3, 0x22)
	}

	pDef := NewPPU()
	setup(pDef, false)
	lineDef := renderPixelWalk(pDef, 0)

	pHi := NewPPU()
	setup(pHi, true)
	lineHi := renderPixelWalk(pHi, 0)

	// Default: OBJ.3 (slot 10) beats BG3.1 (slot 3). Expect OBJ color.
	objIdx := 128 + 0*16 + 1
	wantOBJ := uint16(pDef.CGRAM[objIdx*2]) | uint16(pDef.CGRAM[objIdx*2+1])<<8
	if lineDef[0] != wantOBJ {
		t.Fatalf("default: x=0 = %04X, want OBJ color %04X (OBJ.3 slot 10 > BG3.1 slot 3)",
			lineDef[0], wantOBJ)
	}

	// Bit 3 set: BG3.1 (slot 10) beats OBJ.3 (slot 9). Expect BG3 color.
	wantBG3 := uint16(pHi.CGRAM[1*2]) | uint16(pHi.CGRAM[1*2+1])<<8
	if lineHi[0] != wantBG3 {
		t.Fatalf("bg3-hi: x=0 = %04X, want BG3 color %04X (BG3.1 slot 10 > OBJ.3 slot 9)",
			lineHi[0], wantBG3)
	}

	// Sanity: the two scenes must differ. If they match, the test's
	// fixture didn't actually distinguish them (e.g. same CGRAM value).
	if lineDef[0] == lineHi[0] {
		t.Fatalf("priority inversion invisible: default=%04X bg3hi=%04X",
			lineDef[0], lineHi[0])
	}
}

func TestPixelWalkMode1BG3UsesFourColorPaletteRows(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x04
	p.BG3SC = 0x04

	entry := uint16(1) | (uint16(2) << 10)
	p.VRAM[0x800] = byte(entry)
	p.VRAM[0x801] = byte(entry >> 8)
	p.VRAM[16] = 0xFF

	p.CGRAM[9*2] = 0x11
	p.CGRAM[9*2+1] = 0x11
	p.CGRAM[33*2] = 0x22
	p.CGRAM[33*2+1] = 0x22

	line := renderPixelWalk(p, 0)
	want := uint16(p.CGRAM[9*2]) | uint16(p.CGRAM[9*2+1])<<8
	if line[0] != want {
		t.Fatalf("mode1 BG3 palette row = %04X, want %04X from CGRAM[9]", line[0], want)
	}
}

func TestPixelWalkBG16x16TileSelectsRightAndBottomCells(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1 | 0x10 // BG1 uses 16x16 tiles.
	p.TM = 0x01
	p.BG1SC = 0x04
	p.BG12NBA = 0x01

	p.VRAM[0x800] = 0
	p.VRAM[0x801] = 0
	setBG1Tile0Pixel(p, 0, 1)
	setBG1Tile0Pixel(p, 1, 2)
	setBG1Tile0Pixel(p, 16, 3)

	line := renderPixelWalk(p, 0)
	wantRight := uint16(p.CGRAM[2*2]) | uint16(p.CGRAM[2*2+1])<<8
	if line[8] != wantRight {
		t.Fatalf("16x16 BG right cell = %04X, want tile 1 color %04X", line[8], wantRight)
	}

	line = renderPixelWalk(p, 8)
	wantBottom := uint16(p.CGRAM[3*2]) | uint16(p.CGRAM[3*2+1])<<8
	if line[0] != wantBottom {
		t.Fatalf("16x16 BG bottom cell = %04X, want tile 16 color %04X", line[0], wantBottom)
	}
}

func TestPixelWalkBGTileMirrorBits(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG1SC = 0x04
	p.BG12NBA = 0x01

	entry := uint16(0) | 0x4000 // h-flip
	p.VRAM[0x800] = byte(entry)
	p.VRAM[0x801] = byte(entry >> 8)
	p.VRAM[0x2000] = 0x01 // tile pixel 7, color 1
	p.CGRAM[1*2] = 0x1F

	line := renderPixelWalk(p, 0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if line[0] != want {
		t.Fatalf("h-flipped BG pixel = %04X, want mirrored color %04X", line[0], want)
	}
}

// TestPixelWalkMode5BG1Basic pins that Mode 5 (hi-res, 4bpp BG1 + 2bpp
// BG2) renders BG1 through the 512-sub-pixel walker. Tile 0 (left cell
// of the 16-wide pair) and tile 1 (right cell) both get a solid row of
// pixel-1 so every on-screen column has a non-zero sample. Verifies the
// hi-res BG path writes into pwAbove for the odd sub-pixel at every
// screen column.
func TestPixelWalkMode5BG1Basic(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	// Tilemap entry 0 -> tile 0, palette 2.
	entry := uint16(2) << 10
	p.VRAM[0] = byte(entry & 0xFF)
	p.VRAM[1] = byte(entry >> 8)
	// 4bpp tile 0 pixel 1 across the row. In Mode 5 a 16-wide BG1
	// tile pair reads tile 0 for screen sub-cols 0..7 and tile 1 for
	// sub-cols 8..15, so fill tile 1's plane 0 too.
	p.VRAM[0x2000] = 0xFF
	p.VRAM[0x2020] = 0xFF
	// Palette 2, pixel index 1 -> CGRAM[33].
	p.CGRAM[33*2] = 0x7F
	p.CGRAM[33*2+1] = 0x03

	line := renderPixelWalk(p, 0)

	want := uint16(p.CGRAM[33*2]) | uint16(p.CGRAM[33*2+1])<<8
	if line[0] != want {
		t.Fatalf("Mode 5 BG1 pixel x=0 = %04X, want %04X", line[0], want)
	}
	// x=4 is inside tile 0's on-screen cells (screen col 4 = sub-cols
	// 8/9 of the walk, which land in tile 0's right half).
	if line[4] != want {
		t.Fatalf("Mode 5 BG1 pixel x=4 = %04X, want %04X", line[4], want)
	}
	// x=7 lands in tile 1's sub-cells of the first 16-pixel pair.
	if line[7] != want {
		t.Fatalf("Mode 5 BG1 pixel x=7 = %04X, want %04X", line[7], want)
	}
}

func TestPixelWalkMode5TileSize16UsesLowerHalf(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5 | 0x10
	p.TM = 0x01
	p.BG12NBA = 0x01

	p.VRAM[0x2000+16*32] = 0x80
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x00

	line := renderPixelWalk(p, 4)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if line[0] != want {
		t.Fatalf("Mode 5 16x8 lower half = %04X, want %04X", line[0], want)
	}
}

func TestPixelWalkMode5VerticalMirror(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.BG12NBA = 0x01

	entry := uint16(0x8000)
	p.VRAM[0] = byte(entry)
	p.VRAM[1] = byte(entry >> 8)
	p.VRAM[0x2000+7*2] = 0x80
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x00

	line := renderPixelWalk(p, 0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if line[0] != want {
		t.Fatalf("Mode 5 vertical mirror = %04X, want %04X", line[0], want)
	}
}

// renderPixelWalkHiResSnapshot runs the pixel-walk renderer and returns
// slices into pwAbove/pwBelow so tests can assert on per-sub-pixel state.
// pwAbove[x] carries the odd sub-pixel (bsnes main-screen); pwBelow[x]
// carries the even sub-pixel (bsnes subscreen).
func renderPixelWalkHiResSnapshot(p *PPU, y int) (above []layerPixel, below []layerPixel) {
	p.renderScanlinePixelWalk(y)
	return p.pwAbove[:256], p.pwBelow[:256]
}

// TestPixelWalkMode5HiResSubPixelDistinct pins the defining hi-res
// property: at a single screen column X, the odd sub-pixel (pwAbove[X])
// and the even sub-pixel (pwBelow[X]) can carry different colors because
// they sample different cells of the 16-wide Mode 5 tile pair. Fixture:
// tile 0 has a distinct palette-2 pixel-1 across its row; tile 1 has a
// distinct palette-3 pixel-1. At screen column X=0, the walker's even
// sub-pixel 0 reads tile 0 cell 0 (pwBelow[0] = palette-2 color) and the
// odd sub-pixel 1 also reads tile 0 (since hoffset=1 maps to col 0, sub
// 0) — both come from tile 0. At X=4, sub-pixels 8 and 9 both read
// tile 0's right half (subCol=1 maps to tileIdx+1=1 since mirrorX=0,
// actually that's tile 1's right half per bsnes) so pwBelow[4] =
// palette-3 color. To force a same-column sub-pixel divergence, set BG1
// horizontal scroll to 1 sub-pixel, which offsets the even walk by one.
func TestPixelWalkMode5HiResSubPixelDistinct(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	// Tilemap entries 0 and 1 -> tile 0, palette 2 and palette 3.
	// Tilemap entry 0 (first 16-pixel pair): tile 0 ref, palette 2.
	e0 := uint16(0) | (uint16(2) << 10)
	p.VRAM[0] = byte(e0 & 0xFF)
	p.VRAM[1] = byte(e0 >> 8)
	// Tile 0: 4bpp plane 0 = 0xFF -> pixel index 1 across the row.
	// Tile 1: 4bpp plane 0 = 0x00, plane 1 = 0xFF -> pixel index 2
	// across the row (distinct from tile 0).
	p.VRAM[0x2000] = 0xFF
	p.VRAM[0x2000+1] = 0x00
	p.VRAM[0x2000+32] = 0x00 // tile 1 plane 0
	p.VRAM[0x2000+33] = 0xFF // tile 1 plane 1 -> index 2
	// Palette 2, pixel index 1 -> CGRAM[33] (distinct color A).
	p.CGRAM[33*2] = 0x11
	p.CGRAM[33*2+1] = 0x00
	// Palette 2, pixel index 2 -> CGRAM[34] (distinct color B).
	p.CGRAM[34*2] = 0x00
	p.CGRAM[34*2+1] = 0x10

	above, below := renderPixelWalkHiResSnapshot(p, 0)

	// At screen X=0, sub-pixels 0 (even -> pwBelow) and 1 (odd ->
	// pwAbove) both fall inside the first 16-pixel tile pair.
	// Sub 0: hoffset=0, col=0, subCol=0, tileIdx=0, pixel 0 of tile 0
	// -> plane0 bit 7 = 1, plane1 bit 7 = 0 -> c=1 -> CGRAM[33] (A).
	// Sub 1: hoffset=1, col=0, subCol=0, tileIdx=0, pixel 0 of tile 0
	// -> same as sub 0 -> CGRAM[33].
	wantA := uint16(p.CGRAM[33*2]) | uint16(p.CGRAM[33*2+1])<<8
	if below[0].color != wantA {
		t.Fatalf("pwBelow[0] = %04X, want %04X (tile 0 pixel 1)", below[0].color, wantA)
	}
	if above[0].color != wantA {
		t.Fatalf("pwAbove[0] = %04X, want %04X (tile 0 pixel 1)", above[0].color, wantA)
	}

	// At screen X=4, sub-pixels 8 (even) and 9 (odd). Sub 8: hoffset=8,
	// subCol=1, tileIdx=0+1=1 (right half of the pair). Reads tile 1's
	// pixel 0 -> c=2 -> CGRAM[34] (B). Sub 9: hoffset=9, subCol=1,
	// tileIdx=1, pixel 0 -> same -> CGRAM[34].
	wantB := uint16(p.CGRAM[34*2]) | uint16(p.CGRAM[34*2+1])<<8
	if below[4].color != wantB {
		t.Fatalf("pwBelow[4] = %04X, want %04X (tile 1 pixel 2)", below[4].color, wantB)
	}
	if above[4].color != wantB {
		t.Fatalf("pwAbove[4] = %04X, want %04X (tile 1 pixel 2)", above[4].color, wantB)
	}

	// The critical hi-res pin: the even sub-pixel at screen column X=3
	// (sub-pixel 6 -> tile 0) and the odd sub-pixel at X=3 (sub-pixel 7
	// -> tile 0) are both in tile 0; BUT at X=4, even (sub 8 -> tile 1)
	// differs from X=3 odd (sub 7 -> tile 0). So below[4] != above[3].
	// That confirms the per-sub-pixel routing is wiring correctly.
	if below[4].color == above[3].color {
		t.Fatalf("sub-pixel divergence absent: below[4]=%04X above[3]=%04X; "+
			"expected different because sub 8 is in tile 1 cell, sub 7 in tile 0",
			below[4].color, above[3].color)
	}
}

// TestPixelWalkMode6HiResBG1 pins that Mode 6 (BG1 only, hi-res, OPT
// available) renders via the 512 walker. Setup is a plain 4bpp BG1
// pixel; OPT is off (no BG3 tilemap entry); verify the line output is
// the palette color at screen X=0.
func TestPixelWalkMode6HiResBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 6
	p.TM = 0x01
	p.BG12NBA = 0x01
	// Tilemap entry 0 -> tile 0, palette 1.
	e := uint16(1) << 10
	p.VRAM[0] = byte(e & 0xFF)
	p.VRAM[1] = byte(e >> 8)
	// Fill both tile 0 and tile 1 planes so every sub-pixel sees the
	// same color — simplifies the expected value check.
	p.VRAM[0x2000] = 0xFF // tile 0 plane 0
	p.VRAM[0x2020] = 0xFF // tile 1 plane 0
	// Palette 1, pixel index 1 -> CGRAM[17].
	p.CGRAM[17*2] = 0x7F
	p.CGRAM[17*2+1] = 0x00

	line := renderPixelWalk(p, 0)

	want := uint16(p.CGRAM[17*2]) | uint16(p.CGRAM[17*2+1])<<8
	if line[0] != want {
		t.Fatalf("Mode 6 BG1 x=0 = %04X, want %04X", line[0], want)
	}
	// x=7 stays inside the first 16-sub-pixel tile pair (screen cols
	// 0..7 -> sub-pixels 0..15). Only tilemap entry 0 is set by this
	// fixture, so asserting at x=7 keeps the pin tight.
	if line[7] != want {
		t.Fatalf("Mode 6 BG1 x=7 = %04X, want %04X", line[7], want)
	}
}

// TestPixelWalkHiResOBJRespectsMainSubMasks pins bsnes ppu-fast/object.cpp:
// OBJ has separate aboveEnable/belowEnable gates in hi-res. TM alone plots
// only the odd/above stream; TS alone plots only the even/below stream.
func TestPixelWalkHiResOBJRespectsMainSubMasks(t *testing.T) {
	check := func(t *testing.T, tm, ts uint8, wantAbove, wantBelow bool) {
		t.Helper()
		p := NewPPU()
		p.INIDISP = 0x0F
		p.BGMode = 5
		p.TM = tm
		p.TS = ts
		seedOBJ(p, 16, 0, 1, 2, 3, 0x5A)

		above, below := renderPixelWalkHiResSnapshot(p, 0)

		cgIdx := 128 + 2*16 + 1
		want := uint16(p.CGRAM[cgIdx*2]) | uint16(p.CGRAM[cgIdx*2+1])<<8
		for x := 16; x < 24; x++ {
			if got := above[x].color == want; got != wantAbove {
				t.Fatalf("TM=%02X TS=%02X pwAbove[%d] hit=%v, want %v color=%04X",
					tm, ts, x, got, wantAbove, above[x].color)
			}
			if got := below[x].color == want; got != wantBelow {
				t.Fatalf("TM=%02X TS=%02X pwBelow[%d] hit=%v, want %v color=%04X",
					tm, ts, x, got, wantBelow, below[x].color)
			}
		}
	}

	t.Run("TM only", func(t *testing.T) { check(t, 0x10, 0x00, true, false) })
	t.Run("TS only", func(t *testing.T) { check(t, 0x00, 0x10, false, true) })
	t.Run("TM and TS", func(t *testing.T) { check(t, 0x10, 0x10, true, true) })
}

func TestPixelWalkHiResBGRespectsMainSubMasks(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TS = 0x01 // BG1 below/even only.
	p.BG12NBA = 0x01
	p.VRAM[0] = 0
	p.VRAM[1] = 0
	p.VRAM[0x2000] = 0xFF
	setCGRAMColor(p, 1, pack555(2, 0, 0))

	above, below := renderPixelWalkHiResSnapshot(p, 0)

	want := pack555(2, 0, 0)
	if above[0].color == want {
		t.Fatalf("TM clear still plotted BG1 above: %04X", above[0].color)
	}
	if below[0].color != want {
		t.Fatalf("TS set did not plot BG1 below: got %04X want %04X", below[0].color, want)
	}
}

// TestPixelWalkMode5MosaicDoublesCell pins bsnes's hi-res mosaic
// doubling (background.cpp:115: `io.mosaic.size << hires`). Mosaic
// size=2 in Mode 5 snaps sub-pixels on a 4-unit grid (not 2). The
// fixture has tile 0's pixel 0 with a distinct color and the rest
// transparent; with mosaic on, the anchor color at screen sub-pixel 0
// (X=0 even) replicates across sub-pixels 1, 2, 3 (covering screen X=0
// odd AND X=1 even+odd). Without mosaic doubling, only sub-pixels 0-1
// (X=0 even and odd) would replicate — leaving X=1 at a different
// sampled color.
func TestPixelWalkMode5MosaicDoublesCell(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	p.MOSAIC = 0x11 // size=1+1=2, BG1 enabled

	// Tilemap entry 0 -> tile 0, palette 2.
	e := uint16(2) << 10
	p.VRAM[0] = byte(e & 0xFF)
	p.VRAM[1] = byte(e >> 8)
	// Tile 0: only pixel 0 non-zero (plane 0 bit 7 = 1 -> c=1 at
	// tile-col 0; every other tile-col is 0 -> transparent).
	p.VRAM[0x2000] = 0x80
	// Tile 1: pixel 0 non-zero with a DIFFERENT palette-value slot.
	// Because the tilemap only references tile 0 at entry 0, the walker
	// only reads tile 0 when subCol=0 and tile 1 when subCol=1. Leave
	// tile 1 transparent so that without mosaic, sub-pixels landing in
	// subCol=1 have no BG pixel and fall back to backdrop.
	p.CGRAM[33*2] = 0x1F
	p.CGRAM[33*2+1] = 0x00

	above, below := renderPixelWalkHiResSnapshot(p, 0)

	want := uint16(p.CGRAM[33*2]) | uint16(p.CGRAM[33*2+1])<<8

	// With mosaic cell = 4 sub-pixels (size 2 << hires=1), the anchor
	// at sub-pixel 0 replicates across sub-pixels 1, 2, 3. Sub 0 (even
	// -> below[0]) is the anchor -> want. Sub 1 (odd -> above[0]) reuses
	// the anchor's scroll/opt state but still evaluates ITS OWN fetch
	// (just at the anchor's effX); since effX=0 -> same pixel -> want.
	// Sub 2 (even -> below[1]) reuses anchor -> want. Sub 3 (odd ->
	// above[1]) reuses anchor -> want.
	if below[0].color != want {
		t.Fatalf("mosaic anchor sub 0 (below[0]) = %04X, want %04X",
			below[0].color, want)
	}
	if above[0].color != want {
		t.Fatalf("mosaic anchor sub 1 (above[0]) = %04X, want %04X",
			above[0].color, want)
	}
	if below[1].color != want {
		t.Fatalf("mosaic replicate sub 2 (below[1]) = %04X, want %04X "+
			"(hi-res mosaic cell=4 must cover this)",
			below[1].color, want)
	}
	if above[1].color != want {
		t.Fatalf("mosaic replicate sub 3 (above[1]) = %04X, want %04X",
			above[1].color, want)
	}
}

func TestPixelWalkMode5InterlaceSelectsFieldRow(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.FrameCount = 1
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01

	p.VRAM[0x2000+1] = 0x80 // row 0, color 2: should be skipped on field 1.
	p.VRAM[0x2000+2] = 0x80 // row 1, color 1.
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x00
	p.CGRAM[2*2] = 0x22
	p.CGRAM[2*2+1] = 0x00

	above, below := renderPixelWalkHiResSnapshot(p, 0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if below[0].color != want {
		t.Fatalf("interlace field row below = %04X, want %04X", below[0].color, want)
	}
	if above[0].color != want {
		t.Fatalf("interlace field row above = %04X, want %04X", above[0].color, want)
	}
}

func TestPixelWalkMode6ForcesInterlaceFieldRow(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 6
	p.FrameCount = 1
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01

	p.VRAM[0x2000+1] = 0x80 // row 0, color 2: should be skipped on field 1.
	p.VRAM[0x2000+2] = 0x80 // row 1, color 1.
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x00
	p.CGRAM[2*2] = 0x22
	p.CGRAM[2*2+1] = 0x00

	above, below := renderPixelWalkHiResSnapshot(p, 0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if below[0].color != want {
		t.Fatalf("mode 6 forced interlace below = %04X, want %04X", below[0].color, want)
	}
	if above[0].color != want {
		t.Fatalf("mode 6 forced interlace above = %04X, want %04X", above[0].color, want)
	}
}

func TestPixelWalkMode5InterlaceMosaicSuppressesFieldRow(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.SETINI = 0x01
	p.FrameCount = 1
	p.TM = 0x01
	p.TS = 0x01
	p.BG12NBA = 0x01
	p.MOSAIC = 0x11

	p.VRAM[0x2000] = 0x80   // row 0, color 1.
	p.VRAM[0x2000+3] = 0x80 // row 1, color 2: would show if field were applied.
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x00
	p.CGRAM[2*2] = 0x22
	p.CGRAM[2*2+1] = 0x00

	above, below := renderPixelWalkHiResSnapshot(p, 0)
	want := uint16(p.CGRAM[1*2]) | uint16(p.CGRAM[1*2+1])<<8
	if below[0].color != want {
		t.Fatalf("interlace mosaic below = %04X, want %04X", below[0].color, want)
	}
	if above[0].color != want {
		t.Fatalf("interlace mosaic above = %04X, want %04X", above[0].color, want)
	}
}

// Pre-Slice-4 migrations: the tests below are direct pixel-walk ports of
// render_test.go's Mosaic/OPT/DirectColor pins. They exercise the same
// hardware invariants through renderScanlinePixelWalk so coverage survives
// when Slice 4 deletes the tile-walk path. Fixtures are intentionally
// close to the originals — drift is easier to audit that way.

// TestPixelWalkMode3DirectColorBackdropCarveOut mirrors the tile-walk
// backdrop carve-out pin: a zero tile pixel in Direct Color leaves the
// backdrop (CGRAM[0]) visible; Direct Color does not emit BGR=0 through
// the color pipeline for the transparent index.
func TestPixelWalkMode3DirectColorBackdropCarveOut(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 3
	p.TM = 0x01
	p.CGWSEL = 0x01
	p.BG12NBA = 0x01
	p.CGRAM[0] = 0x55
	p.CGRAM[1] = 0x2A
	wantBack := uint16(0x55) | uint16(0x2A)<<8
	line := renderPixelWalk(p, 0)
	if got := line[0]; got != wantBack {
		t.Fatalf("direct-color backdrop = %04X, want %04X", got, wantBack)
	}
}

// TestPixelWalkMode3DirectColorOffWhenCGWSELBitClear mirrors the tile-walk
// opt-in pin: clearing $2130 bit 0 falls back to the CGRAM palette path.
func TestPixelWalkMode3DirectColorOffWhenCGWSELBitClear(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 3
	p.TM = 0x01
	p.CGWSEL = 0x00
	p.BG12NBA = 0x01

	entry := uint16(7) << 10
	p.VRAM[0] = byte(entry & 0xFF)
	p.VRAM[1] = byte(entry >> 8)
	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+32] = 0x80
	p.CGRAM[17*2] = 0x34
	p.CGRAM[17*2+1] = 0x12
	want := uint16(0x34) | uint16(0x12)<<8

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != want {
		t.Fatalf("CGRAM path pixel = %04X, want %04X", got, want)
	}
}

// TestPixelWalkMosaicVerticalBG1 pins vertical mosaic snap: at size=4,
// scanlines 0..3 sample tile row 0 (y snapped), so line 2 renders the
// same row 0 pixel as line 0. Crossing the 4-scanline block resumes
// normal sampling.
func TestPixelWalkMosaicVerticalBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01

	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+5] = 0x80
	for i := 1; i <= 2; i++ {
		p.CGRAM[i*2] = byte(i)
		p.CGRAM[i*2+1] = byte(i) << 4
	}

	p.MOSAIC = 0x31
	p.renderScanlinePixelWalk(0)
	gotY0 := p.FrontBuffer[0]
	p.renderScanlinePixelWalk(2)
	gotY2 := p.FrontBuffer[2*p.Width+0]
	if gotY0 != gotY2 {
		t.Fatalf("mosaic V snap: y=0 %04X != y=2 %04X (should match inside block)",
			gotY0, gotY2)
	}
	p.renderScanlinePixelWalk(4)
	gotY4 := p.FrontBuffer[4*p.Width+0]
	if gotY4 == gotY0 {
		t.Fatalf("mosaic V snap: y=4 should NOT equal y=0 (block boundary crossed)")
	}
}

// TestPixelWalkMosaicLayerEnableGate pins that per-layer enable bits
// gate replication — BG2 mosaic must not affect BG1 pixels when only
// BG2's enable bit is set.
func TestPixelWalkMosaicLayerEnableGate(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	mosaicSet4pxHorizontalTile(p, 0x2000)

	p.MOSAIC = (3 << 4) | 0x02
	line := renderPixelWalk(p, 0)
	if line[0] == line[3] {
		t.Fatalf("mosaic leaked to non-enabled BG1: px0 == px3 (%04X)", line[0])
	}
}

// TestPixelWalkMosaicSizeOneIsIdentity pins that size=0 is a no-op even
// when the layer enable is set — the common "mosaic off" game state.
func TestPixelWalkMosaicSizeOneIsIdentity(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	mosaicSet4pxHorizontalTile(p, 0x2000)

	p.MOSAIC = 0x00
	base := append([]uint16(nil), renderPixelWalk(p, 0)[:8]...)
	p.MOSAIC = 0x01
	got := renderPixelWalk(p, 0)
	for x := 0; x < 8; x++ {
		if got[x] != base[x] {
			t.Fatalf("size=1 should be identity: x=%d %04X vs base %04X",
				x, got[x], base[x])
		}
	}
}

// TestPixelWalkOPTMode2VOffsetBG2 mirrors the tile-walk V-on-BG2 pin.
// BG3's vlookup entry with bit 14 set shifts BG2's tile column 1 by the
// lookup value vertically. Bit 13 (BG1 gate) is inactive.
func TestPixelWalkOPTMode2VOffsetBG2(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x02
	p.BG12NBA = 0x10
	p.BG2SC = 0x08
	p.BG3SC = 0x18

	bg2Map := 0x1000
	p.VRAM[bg2Map+2] = 0x00
	p.VRAM[bg2Map+3] = 0x00
	p.VRAM[bg2Map+64+2] = 0x01
	p.VRAM[bg2Map+64+3] = 0x00
	tileBase := 0x2000
	p.VRAM[tileBase+0] = 0x80
	p.VRAM[tileBase+0x20+1] = 0x80
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x01
	p.CGRAM[2*2] = 0x22
	p.CGRAM[2*2+1] = 0x02

	optBG3TilemapAt(p, 0x3000, 0, 1, (1<<14)|8)
	line := renderPixelWalk(p, 0)

	want := uint16(0x22) | uint16(0x02)<<8
	if got := line[8]; got != want {
		t.Fatalf("Mode 2 V-OPT BG2 @x=8 = %04X, want %04X", got, want)
	}
}

// TestPixelWalkOPTMode2BG1Bit14DoesNotApply pins the per-layer validity
// gate: bit 14 is the BG2 gate and must not shift BG1.
func TestPixelWalkOPTMode2BG1Bit14DoesNotApply(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	optSetDistinctBG1Tiles(p)

	optBG3TilemapAt(p, 0x800, 0, 0, (1<<14)|8)
	line := renderPixelWalk(p, 0)

	want := uint16(2) | uint16(2)<<12
	if got := line[8]; got != want {
		t.Fatalf("Mode 2 H-OPT BG1 with bit14-only @x=8 = %04X, want unchanged %04X",
			got, want)
	}
}

// TestPixelWalkOPTMode4HOnBG1 pins Mode 4 H-on-BG1: single BG3 fetch,
// bit 13 = BG1 validity, bit 15 = 0 -> H offset.
func TestPixelWalkOPTMode4HOnBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 4
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	for n := 0; n < 4; n++ {
		p.VRAM[n*2+0] = byte(n)
		p.VRAM[n*2+1] = 0
		off := 0x2000 + n*64
		if n&1 != 0 {
			p.VRAM[off+0] = 0x80
		}
		if n&2 != 0 {
			p.VRAM[off+1] = 0x80
		}
		if n > 0 {
			p.CGRAM[n*2] = byte(0x20 | n)
			p.CGRAM[n*2+1] = byte(n)
		}
	}
	p.VRAM[0x2000] = 0x80
	p.CGRAM[1*2] = 0x21
	p.CGRAM[1*2+1] = 0x01

	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)
	line := renderPixelWalk(p, 0)

	want := uint16(0x22) | uint16(0x02)<<8
	if got := line[8]; got != want {
		t.Fatalf("Mode 4 H-OPT BG1 @x=8 = %04X, want %04X", got, want)
	}
}

// TestPixelWalkOPTMode4VOnBG1 pins Mode 4 V-on-BG1: bit 13 + bit 15 set,
// value is the V pixel offset.
func TestPixelWalkOPTMode4VOnBG1(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 4
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	p.VRAM[2] = 0x00
	p.VRAM[3] = 0x00
	p.VRAM[64+2] = 0x01
	p.VRAM[64+3] = 0x00
	p.VRAM[0x2000] = 0x80
	p.VRAM[0x2000+64+1] = 0x80
	p.CGRAM[1*2] = 0x11
	p.CGRAM[1*2+1] = 0x01
	p.CGRAM[2*2] = 0x22
	p.CGRAM[2*2+1] = 0x02

	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|(1<<15)|8)
	line := renderPixelWalk(p, 0)

	want := uint16(0x22) | uint16(0x02)<<8
	if got := line[8]; got != want {
		t.Fatalf("Mode 4 V-OPT BG1 @x=8 = %04X, want %04X", got, want)
	}
}

// TestPixelWalkOPTCarveOutLeftmost pins the offsetX>=8 carve-out for the
// pixel-walk path: column 0 never receives an OPT offset even with an
// aggressive BG3 (0,0) lookup.
func TestPixelWalkOPTCarveOutLeftmost(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 2
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	optSetDistinctBG1Tiles(p)

	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|16)
	line := renderPixelWalk(p, 0)

	want := uint16(1) | uint16(1)<<12
	if got := line[0]; got != want {
		t.Fatalf("OPT leaked into column 0: got %04X, want %04X", got, want)
	}
}

// TestPixelWalkOPTIgnoredInMode1 pins that Modes other than 2/4/6 do not
// consult BG3 for OPT. Mode 1 with an otherwise-valid entry draws BG1
// identically to Mode 1 without it.
func TestPixelWalkOPTIgnoredInMode1(t *testing.T) {
	baselineP := NewPPU()
	baselineP.INIDISP = 0x0F
	baselineP.BGMode = 1
	baselineP.TM = 0x01
	baselineP.BG12NBA = 0x01
	baselineP.BG3SC = 0x04
	optSetDistinctBG1Tiles(baselineP)
	baseline := renderPixelWalk(baselineP, 0)[8]

	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	p.BG3SC = 0x04
	optSetDistinctBG1Tiles(p)
	optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|8)
	line := renderPixelWalk(p, 0)

	if got := line[8]; got != baseline {
		t.Fatalf("OPT leaked into Mode 1: @x=8 got %04X, want baseline %04X",
			got, baseline)
	}
}

// TestPixelWalkOPTMaskTruncation pins that the low 3 bits of the OPT
// lookup value are masked off: value 15 behaves like value 8 (15 & ~7 == 8).
func TestPixelWalkOPTMaskTruncation(t *testing.T) {
	render := func(entry uint16) uint16 {
		p := NewPPU()
		p.INIDISP = 0x0F
		p.BGMode = 2
		p.TM = 0x01
		p.BG12NBA = 0x01
		p.BG3SC = 0x04
		optSetDistinctBG1Tiles(p)
		optBG3TilemapAt(p, 0x800, 0, 0, entry)
		return renderPixelWalk(p, 0)[8]
	}
	withEight := render((1 << 13) | 8)
	withFifteen := render((1 << 13) | 15)
	if withEight != withFifteen {
		t.Fatalf("OPT value low-3 bits leaked: v=8 -> %04X, v=15 -> %04X (should be equal)",
			withEight, withFifteen)
	}
}

// TestPixelWalkOPTHScrollFineXCarveOut pins the carve-out under nonzero
// BG1HOFS & 7: the leftmost partial tile still satisfies offsetX<8 and
// stays at its baseline (no-OPT) pixel regardless of fineX.
func TestPixelWalkOPTHScrollFineXCarveOut(t *testing.T) {
	for _, fine := range []int{0, 3, 7} {
		fine := fine
		t.Run("fine", func(t *testing.T) {
			baseP := NewPPU()
			baseP.INIDISP = 0x0F
			baseP.BGMode = 1
			baseP.TM = 0x01
			baseP.BG12NBA = 0x01
			baseP.BG3SC = 0x04
			baseP.BG1HOFS = uint16(fine)
			optSetDistinctBG1Tiles(baseP)
			wantX0 := renderPixelWalk(baseP, 0)[0]

			p := NewPPU()
			p.INIDISP = 0x0F
			p.BGMode = 2
			p.TM = 0x01
			p.BG12NBA = 0x01
			p.BG3SC = 0x04
			p.BG1HOFS = uint16(fine)
			optSetDistinctBG1Tiles(p)
			optBG3TilemapAt(p, 0x800, 0, 0, (1<<13)|16)
			line := renderPixelWalk(p, 0)

			if got := line[0]; got != wantX0 {
				t.Fatalf("fine=%d: OPT leaked into x=0; got %04X want %04X",
					fine, got, wantX0)
			}
		})
	}
}
