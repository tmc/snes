package ppu

import "testing"

// Renderer-internal sanity for the LayerSourceTrace hook. The unit
// test does not boot a ROM; it pokes pwAbove directly between
// pixelWalkResetBuffers and the trace copy so the test isolates the
// "copy pwAbove[x].source into LayerSourceTrace[y]" invariant.

func TestLayerSourceTraceCapturesPwAbove(t *testing.T) {
	p := NewPPU()
	p.EnableLayerTrace(true)

	// Force-blank path is taken when INIDISP bit 7 is set; clear it so
	// renderScanlinePixelWalk reaches the compositor.
	p.INIDISP = 0x0F // brightness max, no force-blank
	// Mode 0 keeps the renderer in standard (non-hires) layout, which
	// uses the simple post-composite copy path.
	p.BGMode = 0
	p.TM = 0 // disable all layers; backdrop wins everywhere

	// Render one scanline and leave the other trace rows untouched.
	p.renderScanlinePixelWalk(88)

	for x := 0; x < 256; x++ {
		if got := p.LayerSourceTrace[88][x]; got != SourceBackdrop {
			t.Fatalf("y=88 x=%d: trace=%#x want backdrop=%#x", x, got, SourceBackdrop)
		}
	}

	// Other y-rows should still be zero — no prior traces.
	for x := 0; x < 256; x++ {
		if got := p.LayerSourceTrace[0][x]; got != 0 {
			t.Fatalf("y=0 x=%d: trace=%#x want 0", x, got)
		}
	}
}

func TestLayerSourceTraceDisabledLeavesBufferUntouched(t *testing.T) {
	p := NewPPU()
	// Pre-stamp the trace so we can check the renderer doesn't write.
	for x := 0; x < 256; x++ {
		p.LayerSourceTrace[88][x] = 0xAA
	}
	p.INIDISP = 0x0F
	p.BGMode = 0
	p.TM = 0

	if p.LayerTraceActive {
		t.Fatalf("EnableLayerTrace not called; LayerTraceActive should be false")
	}
	p.renderScanlinePixelWalk(88)

	for x := 0; x < 256; x++ {
		if got := p.LayerSourceTrace[88][x]; got != 0xAA {
			t.Fatalf("trace stomped while disabled: x=%d got=%#x", x, got)
		}
	}
}

func TestLayerSourceTracePaletteForBG1Mode7(t *testing.T) {
	// Drive Mode-7 with a constant CGRAM mapping so the trace's
	// palette index for an opaque BG1 pixel equals the synthesized
	// pixel value c. Mode-7 sample reads from VRAM as low/high pairs;
	// poke a tiny chunk so mode7Sample returns a known c at (0,0).
	p := NewPPU()
	p.EnableLayerTrace(true)
	p.INIDISP = 0x0F
	p.BGMode = 7
	p.TM = 0x01 // BG1 enable
	// mode7Sample: tile byte at VRAM[(tileY*128+tileX)<<1] selects a tile;
	// pixel byte at VRAM[tile*128 + fineY*16 + fineX*2 + 1]. For tile=0,
	// fineX=fineY=0 → addr=1; set VRAM[1]=0x42 so c=0x42 at the BG1
	// origin.
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x42
	// Identity matrix (1,0,0,1).
	p.M7A, p.M7B, p.M7C, p.M7D = 0x100, 0, 0, 0x100
	// Make CGRAM[0x42] non-zero so the plot happens (transparent c=0
	// short-circuits the renderer).
	p.CGRAM[0x42*2] = 0x12
	p.CGRAM[0x42*2+1] = 0x34

	p.renderScanlinePixelWalk(88)

	if got := p.LayerSourceTrace[88][0]; got != SourceBG1 {
		t.Fatalf("y=88 x=0 source: got %#x, want BG1=%#x", got, SourceBG1)
	}
	if got := p.LayerPaletteTrace[88][0]; got != 0x42 {
		t.Fatalf("y=88 x=0 palette: got %d, want 0x42", got)
	}
}

// TestLayerSourceTraceCOLStampOnColorMath pins the COL-boundary
// parity contract chosen for layer-source attribution: COL replaces
// the trace's source label only when the underlying main-screen
// pixel is the backdrop, color math fires, and the sub-screen
// contributes a non-backdrop pixel. Pixels already attributed to BG or OBJ
// retain their source after color math.
//
// Setup uses Mode-7 BG1 driven through the sub-screen pass to seed
// pwBelow with a non-backdrop source, while the main pass leaves
// pwAbove at backdrop (TM=0, no BG/OBJ enabled on main).
func TestLayerSourceTraceCOLStampOnColorMath(t *testing.T) {
	p := NewPPU()
	p.EnableLayerTrace(true)
	p.INIDISP = 0x0F
	p.BGMode = 7
	p.TM = 0          // main: no BG/OBJ → pwAbove stays at backdrop
	p.TS = 0x01       // sub: BG1 enabled
	p.CGADSUB = 0x20  // backdrop eligible
	p.CGWSEL = 0x02   // route sub-screen as math operand
	p.CGRAM[0] = 0x1F // non-black backdrop on main
	p.CGRAM[1] = 0x00
	p.CGRAM[0x42*2] = 0x12 // non-zero color for the BG1 sub-screen pixel
	p.CGRAM[0x42*2+1] = 0x34
	// Mode-7 sample wiring: tile 0 at (0,0) → c=0x42.
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x42
	p.M7A, p.M7B, p.M7C, p.M7D = 0x100, 0, 0, 0x100

	p.renderScanlinePixelWalk(88)

	// At least one pixel should have SourceCOL stamped.
	gotCOL, gotBackdrop := 0, 0
	for x := 0; x < 256; x++ {
		switch p.LayerSourceTrace[88][x] {
		case SourceCOL:
			gotCOL++
		case sourceBackdrop:
			gotBackdrop++
		}
	}
	if gotCOL == 0 {
		t.Fatalf("no SourceCOL stamp on a backdrop+sub-screen+CGADSUB scanline; "+
			"trace[88][0..7]=%v gotBackdrop=%d", p.LayerSourceTrace[88][0:8], gotBackdrop)
	}
}

// TestLayerSourceTraceCOLDoesNotEraseBG1 pins the other half of the
// contract: when BG1 wrote the pixel, color math is allowed to
// alter the color but must NOT replace the trace's source. Throwing
// away BG1 attribution makes the layer-source CSV unusable for
// commercial-framebuffer parity.
//
// Setup: Mode-7 BG1 on main with color math enabled (CGADSUB bit 0 +
// fixed-color operand). pwBelow stays at backdrop because no
// sub-screen layer is enabled — under the broad definition this
// would erase BG1 with COL because math fired and the color changed,
// but the narrow contract keeps BG1.
func TestLayerSourceTraceCOLDoesNotEraseBG1(t *testing.T) {
	p := NewPPU()
	p.EnableLayerTrace(true)
	p.INIDISP = 0x0F
	p.BGMode = 7
	p.TM = 0x01      // BG1 on main
	p.TS = 0x00      // sub disabled
	p.CGADSUB = 0x01 // BG1 eligible for math
	p.CGWSEL = 0x00  // fixed-color operand (no sub)
	p.FixedR, p.FixedG, p.FixedB = 0x10, 0x10, 0x10
	p.CGRAM[0] = 0x1F
	p.CGRAM[1] = 0x00
	p.CGRAM[0x42*2] = 0x12
	p.CGRAM[0x42*2+1] = 0x34
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x42
	p.M7A, p.M7B, p.M7C, p.M7D = 0x100, 0, 0, 0x100

	p.renderScanlinePixelWalk(88)

	gotBG1, gotCOL := 0, 0
	for x := 0; x < 256; x++ {
		switch p.LayerSourceTrace[88][x] {
		case SourceBG1:
			gotBG1++
		case SourceCOL:
			gotCOL++
		}
	}
	if gotCOL != 0 {
		t.Fatalf("BG1+fixed-color math erased BG1 attribution with COL: "+
			"BG1=%d COL=%d (want COL=0)", gotBG1, gotCOL)
	}
	if gotBG1 == 0 {
		t.Fatalf("expected BG1 pixels in trace, got BG1=%d COL=%d", gotBG1, gotCOL)
	}
}

func TestLayerSourceCSVMapping(t *testing.T) {
	cases := []struct {
		bits uint8
		want string
	}{
		{SourceBG1, "BG1"},
		{SourceBG2, "BG2"},
		{SourceBG3, "BG3"},
		{SourceBG4, "BG4"},
		{SourceOBJ, "OBJ"},
		{SourceOBJ1, "OBJ"},
		{SourceBackdrop, "BACK"},
		{0, "BACK"},
		// BG1 wins when multiple bits are set (the live source byte
		// only ever holds one bit, but be explicit about precedence).
		{SourceBG1 | SourceOBJ, "BG1"},
	}
	for _, tc := range cases {
		if got := LayerSourceCSV(tc.bits); got != tc.want {
			t.Errorf("LayerSourceCSV(%#x) = %q, want %q", tc.bits, got, tc.want)
		}
	}
}
