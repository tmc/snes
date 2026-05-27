package dsp1

import "testing"

// Fixtures harvested by re-running snes9x DSP1_Sin/Cos/Inverse in a host
// language against the embedded tables (DSP1ROM, DSP1_MulTable,
// DSP1_SinTable). Each row is a bit-exact match against snes9x dsp1.cpp.

func TestSinFP(t *testing.T) {
	cases := []struct {
		angle, want int16
	}{
		{0x0000, 0x0000},
		{0x0100, 0x0324},
		{0x4000, 0x7fff},
		{0x7fff, 0x0003},
		{-0x0100, -0x0324},
		{-0x4000, -0x7fff},
		{0x1234, 0x374d},
		{-0x1234, -0x374d},
		{0x7000, 0x30fb},
		{-0x7fff, -0x0003},
		{-32768, 0},
	}
	for _, tc := range cases {
		if got := sinFP(tc.angle); got != tc.want {
			t.Errorf("sinFP(%#06x) = %#06x, want %#06x", uint16(tc.angle), uint16(got), uint16(tc.want))
		}
	}
}

func TestCosFP(t *testing.T) {
	cases := []struct {
		angle, want int16
	}{
		{0x0000, 0x7fff},
		{0x0100, 0x7ff6},
		{0x4000, 0x0000},
		{0x7fff, -0x7fff},
		{-0x0100, 0x7ff6},
		{-0x4000, 0x0000},
		{0x1234, 0x7370},
		{-0x1234, 0x7370},
		{0x7000, -0x7641},
		{-0x7fff, -0x7fff},
		{-32768, -32768},
	}
	for _, tc := range cases {
		if got := cosFP(tc.angle); got != tc.want {
			t.Errorf("cosFP(%#06x) = %#06x, want %#06x", uint16(tc.angle), uint16(got), uint16(tc.want))
		}
	}
}

func TestInverse(t *testing.T) {
	cases := []struct {
		coef, exp         int16
		wantCoef, wantExp int16
	}{
		{0x0000, 0, 0x7fff, 0x002f},
		{0x4000, 0, 0x7fff, 1},
		{0x4001, 0, 0x7ffe, 1},
		{-0x4000, 0, -0x4000, 2},
		{0x7fff, 0, 0x4000, 1},
		{-0x7fff, 0, -0x4000, 1},
		{0x1234, 3, 0x7082, 0},
		{-0x1234, -2, -0x7082, 5},
		{0x0100, 0, 0x7fff, 7},
		{-0x0100, 0, -0x4000, 8},
	}
	for _, tc := range cases {
		gotC, gotE := inverse(tc.coef, tc.exp)
		if gotC != tc.wantCoef || gotE != tc.wantExp {
			t.Errorf("inverse(%#06x,%d) = (%#06x,%d), want (%#06x,%d)",
				uint16(tc.coef), tc.exp, uint16(gotC), gotE, uint16(tc.wantCoef), tc.wantExp)
		}
	}
}

// Sanity: sin/cos identities at table-aligned angles.
func TestSinCosIdentitiesAtCardinalAngles(t *testing.T) {
	// 0x4000 == 90°, 0x8000 (== -32768) == 180°, but DSP1 treats -32768 specially.
	cases := []struct {
		name            string
		angle, sin, cos int16
	}{
		{"0deg", 0x0000, 0, 0x7fff},
		{"90deg", 0x4000, 0x7fff, 0},
		{"-90deg", -0x4000, -0x7fff, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if s := sinFP(tc.angle); s != tc.sin {
				t.Errorf("sinFP = %#06x want %#06x", uint16(s), uint16(tc.sin))
			}
			if c := cosFP(tc.angle); c != tc.cos {
				t.Errorf("cosFP = %#06x want %#06x", uint16(c), uint16(tc.cos))
			}
		})
	}
}

func TestTablesShape(t *testing.T) {
	if len(dsp1ROM) != 1024 {
		t.Errorf("dsp1ROM len = %d, want 1024", len(dsp1ROM))
	}
	if len(dsp1MulTable) != 256 {
		t.Errorf("dsp1MulTable len = %d, want 256", len(dsp1MulTable))
	}
	if len(dsp1SinTable) != 256 {
		t.Errorf("dsp1SinTable len = %d, want 256", len(dsp1SinTable))
	}
	// Spot-check known anchor values from snes9x dsp1.cpp.
	// Anchor values lifted directly from snes9x dsp1.cpp:40+.
	if dsp1ROM[0x22] != 0x0001 || dsp1ROM[0x30] != 0x4000 || dsp1ROM[0x31] != 0x7fff {
		t.Errorf("dsp1ROM anchor mismatch: [0x22]=%#06x [0x30]=%#06x [0x31]=%#06x",
			dsp1ROM[0x22], dsp1ROM[0x30], dsp1ROM[0x31])
	}
	if dsp1SinTable[0x40] != 0x7fff {
		t.Errorf("dsp1SinTable[0x40] = %#06x, want 0x7fff", dsp1SinTable[0x40])
	}
	if dsp1MulTable[0x80] != 0x0192 {
		t.Errorf("dsp1MulTable[0x80] = %#06x, want 0x0192", dsp1MulTable[0x80])
	}
}

func TestNormalizeAndTruncateBasic(t *testing.T) {
	// truncate: e==0 returns C; e>0 saturates by sign; e<0 shifts via ROM[0x31+e].
	if got := truncate(1234, 0); got != 1234 {
		t.Errorf("truncate(1234,0) = %d, want 1234", got)
	}
	if got := truncate(1, 1); got != 32767 {
		t.Errorf("truncate(1,1) = %d, want 32767", got)
	}
	if got := truncate(-1, 1); got != -32767 {
		t.Errorf("truncate(-1,1) = %d, want -32767", got)
	}
	// normalize on m==0: e==15 path in C; our portable loop computes e=15 too.
	c, e := normalize(0, 0)
	if c != 0 || e != -15 {
		t.Errorf("normalize(0,0) = (%d,%d), want (0,-15)", c, e)
	}
	// normalizeDouble on product==0: m=0,n=0 -> e=15 path; coef=0.
	// normalizeDouble(0): m=0,n=0 -> outer loop drives e to 15, inner loop
	// (e<15 false branch, n==0) drives e another 15 -> 30. Matches snes9x C.
	c, e = normalizeDouble(0)
	if c != 0 || e != 30 {
		t.Errorf("normalizeDouble(0) = (%d,%d), want (0,30)", c, e)
	}
}
