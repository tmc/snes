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
