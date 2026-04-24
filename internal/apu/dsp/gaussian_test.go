package dsp

import "testing"

// TestGaussianTable_Symmetry checks the hardware-documented mirror structure:
// gaussianTable is symmetric around index 256 modulo the ±1 quantisation from
// regenerating it at init time. If this fails the 4-point convolution windows
// we use in gaussianInterpolate (which pair T[i] with T[511-i]) won't sum to
// unity and the output will exhibit a fractional-phase-dependent envelope.
func TestGaussianTable_Symmetry(t *testing.T) {
	gaussianOnce.Do(gaussianInit)
	for i := 0; i < 256; i++ {
		a := gaussianTable[i]
		b := gaussianTable[511-i]
		diff := int(a) - int(b)
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			t.Fatalf("gaussianTable[%d]=%d not symmetric with [%d]=%d (diff=%d)", i, a, 511-i, b, diff)
		}
	}
}

// TestGaussianTable_UnityGain: for every fractional position the four taps
// used in gaussianInterpolate should sum to ~1024 (so that interpolation is
// unity gain before the >>10 shift). We allow a small tolerance because the
// regenerated table is ±1 LSB quantized.
func TestGaussianTable_UnityGain(t *testing.T) {
	gaussianOnce.Do(gaussianInit)
	for i := 0; i < 256; i++ {
		sum := int(gaussianTable[255-i]) +
			int(gaussianTable[511-i]) +
			int(gaussianTable[256+i]) +
			int(gaussianTable[i])
		if sum < 1016 || sum > 1032 {
			t.Fatalf("fractional pos %d: tap sum %d outside [1016,1032]", i, sum)
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
// when all four history entries are equal (unity gain passes the DC
// component). Tolerance reflects the ±1 LSB regen quantisation in the table.
func TestGaussianInterpolate_ConstantHistory(t *testing.T) {
	for _, v := range []int16{1000, -1000, 10000, -10000, 0x7000} {
		for frac := 0; frac < 256; frac += 31 {
			got := gaussianInterpolate(uint8(frac), v, v, v, v)
			diff := int(got) - int(v)
			if diff < 0 {
				diff = -diff
			}
			// ±1 LSB per tap × 4 taps = ±4
			if diff > 5 {
				t.Fatalf("v=%d frac=%d: got %d, want ~%d (diff=%d)", v, frac, got, v, diff)
			}
		}
	}
}

// TestGaussianInterpolate_3TapClip: the intermediate 15-bit clip should
// prevent wrap-around on saturated input. With 3 strongly-negative samples we
// should saturate at -0x8000 before the final tap, not wrap positive.
func TestGaussianInterpolate_3TapClip(t *testing.T) {
	// Fractional position 0 weights the outer taps most: T[255]+T[511]+T[256] ≈
	// most of the tap weight. s3,s2,s1 = -0x7FFF should drive acc well below
	// -0x8000 if not for the intermediate clip.
	got := gaussianInterpolate(0, -0x7FFF, -0x7FFF, -0x7FFF, 0x7FFF)
	// Should end up saturated negative (not positive wrap).
	if got > 0 {
		t.Fatalf("clip failed: got %d, expected negative (saturated)", got)
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
