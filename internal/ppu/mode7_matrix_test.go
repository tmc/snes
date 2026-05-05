package ppu

import "testing"

func TestMode7MatrixIdentity(t *testing.T) {
	p := NewPPU()
	p.M7A = 0x0100
	p.M7D = 0x0100

	for _, tt := range []struct {
		name string
		x, y int
	}{
		{"origin", 0, 0},
		{"visible point", 17, 23},
		{"screen edge", 255, 223},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY := p.mode7TexelCoord(tt.x, tt.y)
			if gotX != tt.x || gotY != tt.y {
				t.Fatalf("mode7TexelCoord(%d,%d) = (%d,%d), want (%d,%d)",
					tt.x, tt.y, gotX, gotY, tt.x, tt.y)
			}
		})
	}
}

func TestMode7MatrixTranslated(t *testing.T) {
	p := NewPPU()
	p.M7A = 0x0100
	p.M7D = 0x0100
	p.M7HOFS = 5
	p.M7VOFS = 7

	gotX, gotY := p.mode7TexelCoord(11, 13)
	if gotX != 16 || gotY != 20 {
		t.Fatalf("translated mode7TexelCoord = (%d,%d), want (16,20)", gotX, gotY)
	}
}

func TestMode7MatrixCenter(t *testing.T) {
	p := NewPPU()
	p.M7A = 0x0100
	p.M7B = 0x0100
	p.M7D = 0x0100
	p.M7Y = 5

	gotX, gotY := p.mode7TexelCoord(2, 3)
	if gotX != 0 || gotY != 3 {
		t.Fatalf("centered mode7TexelCoord = (%d,%d), want (0,3)", gotX, gotY)
	}
}

func TestMode7RenderIdentity(t *testing.T) {
	p := newMode7RenderPPU()
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 2, 4, 9)
	setCGRAMColor(p, 9, 0x1234)

	line := renderPixelWalk(p, 4)
	if got := line[2]; got != 0x1234 {
		t.Fatalf("mode7 identity pixel = %04X, want 1234", got)
	}
}

func TestMode7RenderTranslated(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7HOFS = 5
	p.M7VOFS = 7
	setMode7Map(p, 0, 1, 4)
	setMode7TilePixel(p, 4, 7, 2, 11)
	setCGRAMColor(p, 11, 0x2345)

	line := renderPixelWalk(p, 3)
	if got := line[2]; got != 0x2345 {
		t.Fatalf("mode7 translated pixel = %04X, want 2345", got)
	}
}

func TestMode7RenderUsesLatchedMatrixPerScanline(t *testing.T) {
	p := newMode7RenderPPU()
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 9)
	setMode7TilePixel(p, 3, 1, 1, 10)
	setCGRAMColor(p, 9, 0x1234)
	setCGRAMColor(p, 10, 0x5678)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x1234 {
		t.Fatalf("mode7 pre-latch scanline pixel = %04X, want 1234", got)
	}

	p.WriteRegister(0x210D, 0x01)
	p.WriteRegister(0x210D, 0x00)
	line = renderPixelWalk(p, 1)
	if got := line[0]; got != 0x5678 {
		t.Fatalf("mode7 post-latch scanline pixel = %04X, want 5678", got)
	}
}

func TestMode7ScanlineHookRecordsRenderState(t *testing.T) {
	p := newMode7RenderPPU()
	p.FrameCount = 3
	p.hCounter = 12
	p.vCounter = 6
	p.M7A = 0x0100
	p.M7B = 0x0020
	p.M7C = 0x0010
	p.M7D = 0x0100
	p.M7X = 0x0004
	p.M7Y = 0x0005
	p.M7HOFS = 0x0006
	p.M7VOFS = 0x0007
	p.M7SEL = 0xC1
	var got []Mode7ScanlineEvent
	p.Mode7ScanlineHook = func(e Mode7ScanlineEvent) {
		got = append(got, e)
	}

	p.RenderScanline(9)
	if len(got) != 1 {
		t.Fatalf("Mode7ScanlineHook calls = %d, want 1", len(got))
	}
	e := got[0]
	if e.Y != 9 || e.FrameCount != 3 || e.HCounter != 12 || e.VCounter != 6 {
		t.Fatalf("Mode7ScanlineHook timing = y:%d frame:%d h:%d v:%d, want 9/3/12/6",
			e.Y, e.FrameCount, e.HCounter, e.VCounter)
	}
	if e.Matrix != [4]uint16{0x0100, 0x0020, 0x0010, 0x0100} {
		t.Fatalf("Mode7ScanlineHook matrix = %04X %04X %04X %04X",
			e.Matrix[0], e.Matrix[1], e.Matrix[2], e.Matrix[3])
	}
	if e.Center != [2]uint16{0x0004, 0x0005} || e.Scroll != [2]uint16{0x0006, 0x0007} || e.M7SEL != 0xC1 {
		t.Fatalf("Mode7ScanlineHook state = center:%04X/%04X scroll:%04X/%04X sel:%02X",
			e.Center[0], e.Center[1], e.Scroll[0], e.Scroll[1], e.M7SEL)
	}
	for i, x := range []int{0, 128, 255} {
		wantX, wantY := p.mode7TexelCoord(x, 9)
		point := e.Points[i]
		if point.X != x || point.TexelX != wantX || point.TexelY != wantY {
			t.Fatalf("Mode7ScanlineHook point[%d] = %+v, want x=%d texel=(%d,%d)",
				i, point, x, wantX, wantY)
		}
	}
}

func TestMode7ScanlineHookRecordsLastHDMAMatrixPair(t *testing.T) {
	p := newMode7RenderPPU()
	p.FrameCount = 4
	p.hCounter = 274
	p.vCounter = 12

	p.WriteRegister(0x211B, 0x34)
	p.WriteRegister(0x211B, 0x12)
	p.WriteRegister(0x211C, 0x78)
	p.WriteRegister(0x211C, 0x56)
	p.hCounter = 0
	p.vCounter = 13

	var got []Mode7ScanlineEvent
	p.Mode7ScanlineHook = func(e Mode7ScanlineEvent) {
		got = append(got, e)
	}

	p.RenderScanline(12)
	if len(got) != 1 {
		t.Fatalf("Mode7ScanlineHook calls = %d, want 1", len(got))
	}
	want := Mode7MatrixPairEvent{
		FirstAddr:  0x211B,
		FirstValue: 0x1234,
		NextAddr:   0x211C,
		NextValue:  0x5678,
		FrameCount: 4,
		HCounter:   274,
		VCounter:   12,
	}
	if got[0].LastPair != want {
		t.Fatalf("Mode7ScanlineHook LastPair = %+v, want %+v", got[0].LastPair, want)
	}
	if got[0].Matrix[0] != 0x1234 || got[0].Matrix[1] != 0x5678 {
		t.Fatalf("Mode7ScanlineHook matrix = %04X/%04X, want 1234/5678",
			got[0].Matrix[0], got[0].Matrix[1])
	}
}

type mode7HDMAWriter struct {
	p     *PPU
	calls []hdmaCall
}

func (w *mode7HDMAWriter) ExecuteHDMA() {
	w.calls = append(w.calls, hdmaCall{h: w.p.hCounter, v: w.p.vCounter})
	w.p.WriteRegister(0x210D, 0x01)
	w.p.WriteRegister(0x210D, 0x00)
}

func (w *mode7HDMAWriter) ResetHDMA() {}

func TestMode7HDMARegisterWritesAffectFollowingScanline(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7D = 0
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 9)
	setMode7TilePixel(p, 3, 1, 0, 10)
	setCGRAMColor(p, 9, 0x1234)
	setCGRAMColor(p, 10, 0x5678)

	dma := &mode7HDMAWriter{p: p}
	p.DMA = dma
	p.vCounter = 0
	p.hCounter = 340

	p.Run()
	if got := p.FrontBuffer[0]; got != 0x1234 {
		t.Fatalf("scanline 0 before HDMA = %04X, want 1234", got)
	}
	if len(dma.calls) != 0 {
		t.Fatalf("HDMA calls before H=274 = %d, want 0", len(dma.calls))
	}

	runDots(p, 274)
	if len(dma.calls) != 1 {
		t.Fatalf("HDMA calls at H=274 = %d, want 1", len(dma.calls))
	}
	if call := dma.calls[0]; call.h != 274 || call.v != 1 {
		t.Fatalf("HDMA call = H=%d V=%d, want H=274 V=1", call.h, call.v)
	}
	if got := p.FrontBuffer[0]; got != 0x1234 {
		t.Fatalf("scanline 0 after same-line HDMA = %04X, want unchanged 1234", got)
	}

	runDots(p, 67)
	if p.vCounter != 2 || p.hCounter != 0 {
		t.Fatalf("counters after next line start = H=%d V=%d, want H=0 V=2", p.hCounter, p.vCounter)
	}
	if got := p.FrontBuffer[p.Width]; got != 0x5678 {
		t.Fatalf("scanline 1 after HDMA = %04X, want 5678", got)
	}
}

func runDots(p *PPU, n int) {
	for i := 0; i < n; i++ {
		p.Run()
	}
}

func TestMode7RenderFlip(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7XFlip = true
	p.M7YFlip = true
	setMode7Map(p, 31, 31, 5)
	setMode7TilePixel(p, 5, 5, 4, 12)
	setCGRAMColor(p, 12, 0x3456)

	line := renderPixelWalk(p, 3)
	if got := line[2]; got != 0x3456 {
		t.Fatalf("mode7 flipped pixel = %04X, want 3456", got)
	}
}

func TestMode7RenderM7SELFlipBits(t *testing.T) {
	p := newMode7RenderPPU()
	p.WriteRegister(0x211A, 0x03)
	setMode7Map(p, 31, 31, 5)
	setMode7TilePixel(p, 5, 5, 4, 12)
	setCGRAMColor(p, 12, 0x3456)

	line := renderPixelWalk(p, 3)
	if got := line[2]; got != 0x3456 {
		t.Fatalf("M7SEL flip bits pixel = %04X, want 3456", got)
	}
}

func TestMode7RenderM7SELXFlipWrapsCharacterPlane(t *testing.T) {
	p := newMode7RenderPPU()
	p.WriteRegister(0x211A, 0x01)
	p.M7A = 0x0500
	setMode7Map(p, 31, 0, 6)
	setMode7TilePixel(p, 6, 3, 0, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x4567 {
		t.Fatalf("M7SEL x-flip wrap pixel = %04X, want 4567", got)
	}
}

func TestMode7RenderM7SELYFlipWrapsCharacterPlane(t *testing.T) {
	p := newMode7RenderPPU()
	p.WriteRegister(0x211A, 0x02)
	p.M7D = 0x0500
	setMode7Map(p, 0, 31, 6)
	setMode7TilePixel(p, 6, 0, 3, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x4567 {
		t.Fatalf("M7SEL y-flip wrap pixel = %04X, want 4567", got)
	}
}

func TestMode7RenderScreenOverTransparent(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7Large = true
	p.M7A = 0x0500
	setCGRAMColor(p, 0, 0x0007)
	setMode7Map(p, 31, 0, 6)
	setMode7TilePixel(p, 6, 3, 0, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[255]; got != 0x0007 {
		t.Fatalf("mode7 transparent screen-over pixel = %04X, want backdrop 0007", got)
	}
}

func TestMode7RenderScreenOverWrap(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7A = 0x0500
	setMode7Map(p, 31, 0, 6)
	setMode7TilePixel(p, 6, 3, 0, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[255]; got != 0x4567 {
		t.Fatalf("mode7 wrapped screen-over pixel = %04X, want 4567", got)
	}
}

func TestMode7RenderScreenOverFill(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7Large = true
	p.M7Fill = true
	p.M7A = 0x0500
	setMode7TilePixel(p, 0, 3, 0, 14)
	setCGRAMColor(p, 14, 0x5678)

	line := renderPixelWalk(p, 0)
	if got := line[255]; got != 0x5678 {
		t.Fatalf("mode7 fill screen-over pixel = %04X, want 5678", got)
	}
}

func TestMode7RenderM7SELRepeatModes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		m7sel byte
		tile  byte
		color byte
		cgram uint16
		want  uint16
	}{
		{"repeat 0 wraps", 0x00, 6, 13, 0x4567, 0x4567},
		{"repeat 1 wraps", 0x40, 6, 13, 0x4567, 0x4567},
		{"repeat 2 transparent", 0x80, 6, 13, 0x4567, 0x0007},
		{"repeat 3 fills tile zero", 0xC0, 0, 14, 0x5678, 0x5678},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newMode7RenderPPU()
			p.WriteRegister(0x211A, tt.m7sel)
			p.M7A = 0x0500
			setCGRAMColor(p, 0, 0x0007)
			setMode7Map(p, 31, 0, 6)
			setMode7TilePixel(p, tt.tile, 3, 0, tt.color)
			setCGRAMColor(p, tt.color, tt.cgram)

			line := renderPixelWalk(p, 0)
			if got := line[255]; got != tt.want {
				t.Fatalf("M7SEL=%02X screen-over pixel = %04X, want %04X",
					tt.m7sel, got, tt.want)
			}
		})
	}
}

func TestMode7RenderEXTBGLowPriority(t *testing.T) {
	p := newMode7RenderPPU()
	p.SETINI = 0x40
	p.TM = 0x03
	setMode7Map(p, 0, 0, 7)
	setMode7TilePixel(p, 7, 0, 0, 2)
	setCGRAMColor(p, 2, 0x1111)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x1111 {
		t.Fatalf("mode7 extbg low-priority pixel = %04X, want BG1 1111", got)
	}
}

func TestMode7RenderEXTBGHighPriority(t *testing.T) {
	p := newMode7RenderPPU()
	p.SETINI = 0x40
	p.TM = 0x03
	setMode7Map(p, 0, 0, 7)
	setMode7TilePixel(p, 7, 0, 0, 0x82)
	setCGRAMColor(p, 0x82, 0x1111)
	setCGRAMColor(p, 0x02, 0x2222)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x2222 {
		t.Fatalf("mode7 extbg high-priority pixel = %04X, want BG2 2222", got)
	}
}

func TestMode7RenderEXTBGRequiresBG2Mask(t *testing.T) {
	p := newMode7RenderPPU()
	p.SETINI = 0x40
	p.TM = 0x01
	setMode7Map(p, 0, 0, 7)
	setMode7TilePixel(p, 7, 0, 0, 0x82)
	setCGRAMColor(p, 0x82, 0x1111)
	setCGRAMColor(p, 0x02, 0x2222)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x1111 {
		t.Fatalf("mode7 extbg masked BG2 pixel = %04X, want BG1 1111", got)
	}
}

func TestMode7RenderEXTBGUsesBG2ColorMathSource(t *testing.T) {
	p := newMode7RenderPPU()
	p.SETINI = 0x40
	p.TM = 0x02
	p.CGADSUB = sourceBG2
	p.WriteRegister(0x2132, 0x21) // fixed red = 1
	setMode7Map(p, 0, 0, 7)
	setMode7TilePixel(p, 7, 0, 0, 2)
	setCGRAMColor(p, 2, pack555(2, 0, 0))

	line := renderPixelWalk(p, 0)
	if got, want := line[0], pack555(3, 0, 0); got != want {
		t.Fatalf("mode7 extbg color math = %04X, want %04X", got, want)
	}
}

func TestMode7RenderDirectColor(t *testing.T) {
	p := newMode7RenderPPU()
	p.CGWSEL = 0x01
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 0xFF)
	setCGRAMColor(p, 0xFF, 0x55AA)

	line := renderPixelWalk(p, 0)
	want := directColor555(0xFF, 0)
	if got := line[0]; got != want {
		t.Fatalf("mode7 direct color pixel = %04X, want %04X", got, want)
	}
}

func TestMode7RenderMosaicHorizontal(t *testing.T) {
	p := newMode7RenderPPU()
	p.MOSAIC = 0x11 // BG1 mosaic, 2-pixel cells.
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 9)
	setMode7TilePixel(p, 3, 1, 0, 10)
	setCGRAMColor(p, 9, 0x1234)
	setCGRAMColor(p, 10, 0x5678)

	line := renderPixelWalk(p, 0)
	if got := line[1]; got != 0x1234 {
		t.Fatalf("mode7 horizontal mosaic pixel = %04X, want anchor 1234", got)
	}
}

func TestMode7RenderMosaicVertical(t *testing.T) {
	p := newMode7RenderPPU()
	p.MOSAIC = 0x11 // BG1 mosaic, 2-pixel cells.
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 9)
	setMode7TilePixel(p, 3, 0, 1, 10)
	setCGRAMColor(p, 9, 0x1234)
	setCGRAMColor(p, 10, 0x5678)

	line := renderPixelWalk(p, 1)
	if got := line[0]; got != 0x1234 {
		t.Fatalf("mode7 vertical mosaic pixel = %04X, want anchor 1234", got)
	}
}

func TestMode7RenderEXTBGMosaicUsesBG2Enable(t *testing.T) {
	p := newMode7RenderPPU()
	p.SETINI = 0x40
	p.TM = 0x02
	p.MOSAIC = 0x12 // BG2 mosaic, 2-pixel cells.
	setMode7Map(p, 0, 0, 3)
	setMode7TilePixel(p, 3, 0, 0, 9)
	setMode7TilePixel(p, 3, 1, 0, 10)
	setCGRAMColor(p, 9, 0x1234)
	setCGRAMColor(p, 10, 0x5678)

	line := renderPixelWalk(p, 0)
	if got := line[1]; got != 0x1234 {
		t.Fatalf("mode7 extbg horizontal mosaic pixel = %04X, want anchor 1234", got)
	}
}

func newMode7RenderPPU() *PPU {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 7
	p.TM = 0x01
	p.M7A = 0x0100
	p.M7D = 0x0100
	return p
}

func setMode7Map(p *PPU, tileX, tileY int, tile byte) {
	p.VRAM[((tileY*128+tileX)<<1)&(VRAMSize-1)] = tile
}

func setMode7TilePixel(p *PPU, tile byte, x, y int, color byte) {
	p.VRAM[(int(tile)<<7+y*16+x*2+1)&(VRAMSize-1)] = color
}

func setCGRAMColor(p *PPU, index byte, color uint16) {
	p.CGRAM[int(index)*2] = byte(color)
	p.CGRAM[int(index)*2+1] = byte(color >> 8)
}
