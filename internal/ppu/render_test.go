package ppu

import "testing"

func mosaicSet4pxHorizontalTile(p *PPU, tileBase int) {
	p.VRAM[0] = 0x00
	p.VRAM[1] = 0x00
	// 4bpp pixel indices, bit7 = leftmost pixel:
	//   px0=1 (c0=1), px1=2 (c1=1), px2=3 (c0,c1=1), px3=4 (c2=1),
	//   px4=5 (c0,c2=1), px5=6 (c1,c2=1), px6=7 (c0,c1,c2=1), px7=1
	p.VRAM[tileBase+0] = 0b10101011  // plane 0 (c0) for px0,2,4,6,7
	p.VRAM[tileBase+1] = 0b01101010  // plane 1 (c1) for px1,2,5,6
	p.VRAM[tileBase+16] = 0b00011110 // plane 2 (c2) for px3,4,5,6
	p.VRAM[tileBase+17] = 0b00000000
	// Palette 0; fill CGRAM indices 1..7 with distinctive values.
	for i := 1; i <= 7; i++ {
		p.CGRAM[i*2] = byte(i)
		p.CGRAM[i*2+1] = byte(i) << 4
	}
}

func optSetDistinctBG1Tiles(p *PPU) {
	tileBase := 0x2000
	for n := 0; n < 8; n++ {
		entry := uint16(n)
		p.VRAM[n*2+0] = byte(entry & 0xFF)
		p.VRAM[n*2+1] = byte(entry >> 8)
		// Encode index N+1 at pixel 0 of tile N, 4bpp.
		idx := n + 1
		off := tileBase + n*32
		if idx&0x01 != 0 {
			p.VRAM[off+0] = 0x80
		}
		if idx&0x02 != 0 {
			p.VRAM[off+1] = 0x80
		}
		if idx&0x04 != 0 {
			p.VRAM[off+16] = 0x80
		}
		if idx&0x08 != 0 {
			p.VRAM[off+17] = 0x80
		}
		// Distinctive CGRAM entry so the front buffer tells us which tile won.
		p.CGRAM[idx*2+0] = byte(idx)
		p.CGRAM[idx*2+1] = byte(idx) << 4
	}
}

// optBG3TilemapAt writes a 16-bit BG3 tilemap entry at tile (col, row) into
// BG3's tilemap area. BG3SC bits 7..2 hold the tilemap base in 1KB words
// (2KB bytes). A BG3SC of 0x04 puts BG3's tilemap at VRAM byte offset 0x800.
func optBG3TilemapAt(p *PPU, base int, col, row int, entry uint16) {
	addr := base + (row*32+col)*2
	p.VRAM[addr+0] = byte(entry & 0xFF)
	p.VRAM[addr+1] = byte(entry >> 8)
}

// optBG1BaselineColor runs a baseline render (OPT disabled via Mode 1) and
// returns the on-screen BG1 pixel at the given screen-X. Useful for asserting
// that "tile column C's pixel now appears at screen-X S" after an OPT shift.
func optBG1BaselineColor(t *testing.T, setup func(p *PPU), x int) uint16 {
	t.Helper()
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 1
	p.TM = 0x01
	p.BG12NBA = 0x01
	optSetDistinctBG1Tiles(p)
	if setup != nil {
		setup(p)
	}
	p.RenderScanline(0)
	return p.FrontBuffer[x]
}

// TestApplyBrightnessZeroIsBlack pins the bsnes ppu-fast lightTable behavior:
// brightness=0 attenuates every channel to exactly 0, regardless of the
// composed pixel value.
func TestApplyBrightnessZeroIsBlack(t *testing.T) {
	for c := uint16(0); c < 0x8000; c++ {
		if got := applyBrightness(c, 0); got != 0 {
			t.Fatalf("applyBrightness(%#04x, 0) = %#04x, want 0", c, got)
		}
	}
}

// TestApplyBrightnessFullIsIdentity pins brightness=15 as the identity map
// over BGR555 inputs so the lightTable extremes match bsnes.
func TestApplyBrightnessFullIsIdentity(t *testing.T) {
	for c := uint16(0); c < 0x8000; c++ {
		if got := applyBrightness(c, 15); got != c {
			t.Fatalf("applyBrightness(%#04x, 15) = %#04x, want %#04x", c, got, c)
		}
	}
}
