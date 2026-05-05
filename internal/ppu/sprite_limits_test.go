package ppu

import "testing"

func hideAllOBJ(p *PPU) {
	for i := 0; i < 128; i++ {
		p.OAM[i*4+1] = 224
	}
}

func placeOBJ(p *PPU, i, x, y int, large bool) {
	addr := i * 4
	p.OAM[addr] = byte(x)
	p.OAM[addr+1] = byte(y)
	p.OAM[addr+2] = 0
	p.OAM[addr+3] = 0
	if large {
		p.OAM[512+(i/4)] |= 1 << ((i%4)*2 + 1)
	}
}

func stat77AfterOBJScan(p *PPU) uint8 {
	p.INIDISP = 0x0F
	p.TM = 0x10
	p.RenderScanline(0)
	return p.ReadRegister(0x213E)
}

func renderOBJScanline(p *PPU, y int) {
	p.INIDISP = 0x0F
	p.TM = 0x10
	p.RenderScanline(y)
}

func setOBJPlane0Pixel(p *PPU, tile, row, x int) {
	wordAddr := uint32(tile*16 + row)
	p.VRAM[wordAddr*2] |= 0x80 >> uint(x&7)
}

func TestOBJThirtyTwoSpritesDoesNotSetRangeOver(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	for i := 0; i < 32; i++ {
		placeOBJ(p, i, 0, 0, false)
	}

	if got := stat77AfterOBJScan(p); got&0x40 != 0 {
		t.Fatalf("STAT77 range over at 32 sprites = %02X, want clear", got)
	}
}

func TestOBJThirtyThreeSpritesSetsRangeOver(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	for i := 0; i < 33; i++ {
		placeOBJ(p, i, 0, 0, false)
	}

	if got := stat77AfterOBJScan(p); got&0x40 == 0 {
		t.Fatalf("STAT77 range over at 33 sprites = %02X, want set", got)
	}
}

func TestOBJThirtyFourSliversDoesNotSetTimeOver(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	for i := 0; i < 17; i++ {
		placeOBJ(p, i, 0, 0, true)
	}

	if got := stat77AfterOBJScan(p); got&0x80 != 0 {
		t.Fatalf("STAT77 time over at 34 slivers = %02X, want clear", got)
	}
}

func TestOBJThirtyFiveSliversSetsTimeOver(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	for i := 0; i < 17; i++ {
		placeOBJ(p, i, 0, 0, true)
	}
	placeOBJ(p, 17, 0, 0, false)

	if got := stat77AfterOBJScan(p); got&0x80 == 0 {
		t.Fatalf("STAT77 time over at 35 slivers = %02X, want set", got)
	}
}

func TestOBJX256ConsumesSliverBudget(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	for i := 0; i < 17; i++ {
		placeOBJ(p, i, 0, 0, true)
	}
	placeOBJ(p, 17, 0, 0, false)
	p.OAM[512+(17/4)] |= 1 << ((17 % 4) * 2) // x-high: x=256

	if got := stat77AfterOBJScan(p); got&0x80 == 0 {
		t.Fatalf("STAT77 time over with x=256 sliver = %02X, want set", got)
	}
}

func TestOBJX256DoesNotRenderAtLeftEdge(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	placeOBJ(p, 0, 0, 0, false)
	p.OAM[512] = 1 // x-high: x=256

	p.VRAM[0] = 0x80
	p.CGRAM[129*2] = 0x1F

	stat77AfterOBJScan(p)
	if got := p.FrontBuffer[0]; got != 0 {
		t.Fatalf("x=256 OBJ rendered at left edge: got %04X, want backdrop", got)
	}
}

func TestOBJInterlaceSmallBaseSixUsesEightVisibleLines(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	p.SETINI = 0x02
	p.OBSEL = 6 << 5
	placeOBJ(p, 0, 0, 0, false)
	setOBJPlane0Pixel(p, 16, 6, 0)
	p.CGRAM[129*2] = 0x1F

	renderOBJScanline(p, 7)
	if got := p.FrontBuffer[7*256]; got != 0x001F {
		t.Fatalf("interlace line 7 pixel = %04X, want 001F", got)
	}

	renderOBJScanline(p, 8)
	if got := p.FrontBuffer[8*256]; got != 0 {
		t.Fatalf("interlace line 8 pixel = %04X, want backdrop", got)
	}
}

func TestOBJInterlaceFieldSelectsOddSourceRow(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	p.SETINI = 0x02
	p.FrameCount = 1
	placeOBJ(p, 0, 0, 0, false)
	setOBJPlane0Pixel(p, 0, 1, 0)
	p.CGRAM[129*2] = 0x1F

	renderOBJScanline(p, 0)
	if got := p.FrontBuffer[0]; got != 0x001F {
		t.Fatalf("interlace field 1 pixel = %04X, want 001F", got)
	}
}

func TestOBJInterlaceVFlipSubtractsField(t *testing.T) {
	p := NewPPU()
	hideAllOBJ(p)
	p.SETINI = 0x02
	p.FrameCount = 1
	placeOBJ(p, 0, 0, 0, false)
	p.OAM[3] = 0x80
	setOBJPlane0Pixel(p, 0, 6, 0)
	p.CGRAM[129*2] = 0x1F

	renderOBJScanline(p, 0)
	if got := p.FrontBuffer[0]; got != 0x001F {
		t.Fatalf("interlace vflip field 1 pixel = %04X, want 001F", got)
	}
}
