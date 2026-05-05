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
