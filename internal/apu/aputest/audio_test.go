package aputest

import (
	"math"
	"testing"
)

const (
	nonSilentSPCSampleCount = 32
	nonSilentSPCHash        = "033326f5fd356ba4b254b9592c45a9a780b50a47937b7a9aee1fd91b7a54a3f8"
	nonSilentSPCRMS         = 0.11687995868402994
)

func equalSamples(got, want []int16) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestNonSilentDSPAudio(t *testing.T) {
	samples := NonSilentDSPAudio()
	if len(samples) == 0 {
		t.Fatalf("NonSilentDSPAudio returned no samples")
	}
	for _, sample := range samples {
		if sample != 0 {
			if got := RMS(samples); got <= 0 {
				t.Fatalf("RMS = %.6f, want positive", got)
			}
			return
		}
	}
	t.Fatalf("NonSilentDSPAudio returned only silent samples: %v", samples)
}

func TestNonSilentDSPAudioDeterministic(t *testing.T) {
	first := NonSilentDSPAudio()
	second := NonSilentDSPAudio()
	if !equalSamples(second, first) {
		t.Fatalf("NonSilentDSPAudio mismatch across runs:\nfirst=%v\nsecond=%v", first, second)
	}
	rms := RMS(first)
	if rms <= 0 {
		t.Fatalf("RMS = %.6f, want positive", rms)
	}
	if got := RMS(second); got != rms {
		t.Fatalf("RMS changed across runs: got %.12f want %.12f", got, rms)
	}
}

func TestNonSilentSPCAudioDeterministic(t *testing.T) {
	first := NonSilentSPCAudio()
	second := NonSilentSPCAudio()
	if got := len(first); got != nonSilentSPCSampleCount {
		t.Fatalf("sample count = %d, want %d", got, nonSilentSPCSampleCount)
	}
	if !equalSamples(second, first) {
		t.Fatalf("NonSilentSPCAudio mismatch across runs:\nfirst=%v\nsecond=%v", first, second)
	}
	if got := HashPCM16(first); got != nonSilentSPCHash {
		t.Fatalf("sample hash = %s, want %s", got, nonSilentSPCHash)
	}
	if got := RMS(first); math.Abs(got-nonSilentSPCRMS) > 1e-12 {
		t.Fatalf("RMS = %.17f, want %.17f", got, nonSilentSPCRMS)
	}
}
