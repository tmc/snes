package aputest

import "testing"

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
