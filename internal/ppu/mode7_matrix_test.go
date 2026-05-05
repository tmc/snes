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

func TestMode7RenderScreenOverTransparent(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7Large = true
	p.M7HOFS = 1024
	setCGRAMColor(p, 0, 0x0007)
	setMode7Map(p, 0, 0, 6)
	setMode7TilePixel(p, 6, 0, 0, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x0007 {
		t.Fatalf("mode7 transparent screen-over pixel = %04X, want backdrop 0007", got)
	}
}

func TestMode7RenderScreenOverWrap(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7HOFS = 1024
	setMode7Map(p, 0, 0, 6)
	setMode7TilePixel(p, 6, 0, 0, 13)
	setCGRAMColor(p, 13, 0x4567)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x4567 {
		t.Fatalf("mode7 wrapped screen-over pixel = %04X, want 4567", got)
	}
}

func TestMode7RenderScreenOverFill(t *testing.T) {
	p := newMode7RenderPPU()
	p.M7Large = true
	p.M7Fill = true
	p.M7HOFS = 1024
	setMode7TilePixel(p, 0, 0, 0, 14)
	setCGRAMColor(p, 14, 0x5678)

	line := renderPixelWalk(p, 0)
	if got := line[0]; got != 0x5678 {
		t.Fatalf("mode7 fill screen-over pixel = %04X, want 5678", got)
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
