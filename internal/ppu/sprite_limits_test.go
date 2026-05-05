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
