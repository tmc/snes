package apu

import "testing"

func TestAPUDrainAudio(t *testing.T) {
	a := NewAPU()
	a.audioBuffer[0] = 1
	a.audioBuffer[1] = 2
	a.audioBuffer[2] = 3
	a.audioBuffer[3] = 4
	a.audioCount = 4

	dst := make([]int16, 3)
	if got := a.DrainAudio(dst); got != 3 {
		t.Fatalf("DrainAudio count = %d, want 3", got)
	}
	if want := []int16{1, 2, 3}; !equalSamples(dst, want) {
		t.Fatalf("DrainAudio dst = %v, want %v", dst, want)
	}

	dst = dst[:1]
	if got := a.DrainAudio(dst); got != 1 {
		t.Fatalf("second DrainAudio count = %d, want 1", got)
	}
	if dst[0] != 4 {
		t.Fatalf("second DrainAudio dst[0] = %d, want 4", dst[0])
	}
}

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
