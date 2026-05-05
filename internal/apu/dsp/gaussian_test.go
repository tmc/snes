package dsp

import "testing"

// TestGaussianTable_ReferenceEntries checks representative byte-for-byte
// entries from the S-DSP Gaussian table. The full table is embedded in
// gaussian.go; these pins catch accidental regeneration or scale changes.
func TestGaussianTable_ReferenceEntries(t *testing.T) {
	tests := []struct {
		idx  int
		want int16
	}{
		{0, 0x000},
		{15, 0x000},
		{16, 0x001},
		{128, 0x03A},
		{255, 0x172},
		{256, 0x176},
		{396, 0x3FF},
		{397, 0x403},
		{511, 0x519},
	}
	for _, tt := range tests {
		if got := gaussianTable[tt.idx]; got != tt.want {
			t.Fatalf("gaussianTable[%d] = %03X, want %03X", tt.idx, got, tt.want)
		}
	}
}

// TestGaussianTable_UnityGain: for every fractional position the four taps
// used in gaussianInterpolate should sum to approximately 2048, matching the
// S-DSP table scale and the >>11 interpolation shift.
func TestGaussianTable_UnityGain(t *testing.T) {
	for i := 0; i < 256; i++ {
		sum := int(gaussianTable[255-i]) +
			int(gaussianTable[511-i]) +
			int(gaussianTable[256+i]) +
			int(gaussianTable[i])
		if sum < 2047 || sum > 2050 {
			t.Fatalf("fractional pos %d: tap sum %d outside [2047,2050]", i, sum)
		}
	}
}

// TestGaussianInterpolate_ZeroHistory returns zero regardless of fractional
// position when all four history samples are zero.
func TestGaussianInterpolate_ZeroHistory(t *testing.T) {
	for frac := 0; frac < 256; frac += 17 {
		if got := gaussianInterpolate(uint8(frac), 0, 0, 0, 0); got != 0 {
			t.Fatalf("frac=%d: got %d, want 0", frac, got)
		}
	}
}

// TestGaussianInterpolate_ConstantHistory returns approximately the constant
// when all four history entries are equal.
func TestGaussianInterpolate_ConstantHistory(t *testing.T) {
	for _, v := range []int16{1000, -1000, 10000, -10000, 0x7000} {
		for frac := 0; frac < 256; frac += 31 {
			got := gaussianInterpolate(uint8(frac), v, v, v, v)
			diff := int(got) - int(v)
			if diff < 0 {
				diff = -diff
			}
			if diff > 16 {
				t.Fatalf("v=%d frac=%d: got %d, want ~%d (diff=%d)", v, frac, got, v, diff)
			}
		}
	}
}

func TestGaussianInterpolate_OverflowVectors(t *testing.T) {
	tests := []struct {
		name           string
		frac           uint8
		s3, s2, s1, s0 int16
		want           int16
	}{
		{name: "negative intermediate wraps", frac: 0, s3: -0x7FFF, s2: -0x7FFF, s1: -0x7FFF, s0: 0x7FFF, want: 32752},
		{name: "positive intermediate wraps", frac: 0, s3: 0x7FFF, s2: 0x7FFF, s1: 0x7FFF, s0: -0x8000, want: -32755},
		{name: "middle negative", frac: 128, s3: -0x8000, s2: -0x8000, s1: -0x8000, s0: 0x7FFF, want: -30913},
		{name: "middle positive", frac: 128, s3: 0x7FFF, s2: 0x7FFF, s1: 0x7FFF, s0: -0x8000, want: 30909},
		{name: "final positive clamp", frac: 255, s3: 0x7FFF, s2: 0x7FFF, s1: 0x7FFF, s0: 0x7FFF, want: 32767},
		{name: "final negative clamp", frac: 255, s3: -0x8000, s2: -0x8000, s1: -0x8000, s0: -0x8000, want: -32768},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gaussianInterpolate(tt.frac, tt.s3, tt.s2, tt.s1, tt.s0)
			if got != tt.want {
				t.Fatalf("gaussianInterpolate = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestVoiceRender_InterpolatesSmoothly: a BRR block of identical decoded
// samples should produce a constant interpolated output (DC), confirming the
// convolution is unity-gain on a DC input once the history is primed.
func TestVoiceRender_InterpolatesSmoothly(t *testing.T) {
	ram := make([]uint8, 65536)
	// Directory entry at 0x2000 -> 0x3000.
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30
	// Header: shift=12, filter=0, loop=0, end=0.
	ram[0x3000] = 0xC0
	// All-4-nibbles pattern that decodes to the same sample each slot.
	// 0x11 in each byte with shift=12 filter=0: nibble 0x1 -> sign ext 1 -> <<12 = 4096, >>1 = 2048.
	for i := 0; i < 8; i++ {
		ram[0x3001+uint16(i)] = 0x11
	}
	read := func(addr uint16) uint8 { return ram[addr] }

	var v Voice
	v.SRCN = 0
	v.VOLL = 127
	v.VOLR = 127
	v.GAIN = 0x7F
	v.P = 0x1000 // advance 1 sample per render
	v.KeyOn(read, 0x20)

	// Drain the first few ticks to get past the priming transient.
	for i := 0; i < 4; i++ {
		v.stepEnvelope()
		_, _ = v.renderWith(v.P, read, 0)
	}
	// Now sample a few more; they should be equal (steady-state DC).
	v.stepEnvelope()
	l1, _ := v.renderWith(v.P, read, 0)
	v.stepEnvelope()
	l2, _ := v.renderWith(v.P, read, 0)
	if l1 == 0 {
		t.Fatalf("expected non-zero DC sample, got 0")
	}
	diff := l1 - l2
	if diff < 0 {
		diff = -diff
	}
	if diff > 8 {
		t.Fatalf("steady-state samples diverge: l1=%d l2=%d", l1, l2)
	}
}
