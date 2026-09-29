package parity

import (
	"math"
	"testing"

	"github.com/tmc/snes"
)

// TestRunFrameAudioSmoke is the slice #2 counterpart to
// TestReferenceAudioRMSNonSilentAPUFixture's lorom-uploader/go subtest:
// it drives the same in-process LoROM SFC through System.RunFrame
// (the public entry point used by cmd/snes and the godoc example) and
// pins three properties:
//
//  1. RunFrame produces deterministic nonzero PCM after the same
//     onset as the Run()-based reference.
//  2. The first 4096 samples after onset hash to the same value as the
//     Run()-driven Go path and the snes9x reference, proving the
//     scheduler entry point does not perturb DSP output.
//  3. WRAM at $7E:0000..0x40 is unchanged across 120 frames of audio
//     production — the "no boot RAM clobber" criterion from the
//     roadmap.
//
// The post-onset hash matches the snes9x reference recorded in
// TestReferenceAudioRMSNonSilentAPUFixture/lorom-uploader/snes9x.
// The onset is the live snes9x core's (312 interleaved samples at
// 32 kHz, about 4.87 ms), which bsnes also reports; the Go path is
// currently about 9 stereo samples early.
func TestRunFrameAudioSmoke(t *testing.T) {
	const (
		wantRMS           = 0.001852
		rmsTolerance      = 0.000002
		wantOnset         = 312
		wantPostOnsetHash = "0bc1c7661db7d6ece1cba592c875a60faa99e33d87c54bbe7827cbf21f45a289"
		wantWRAMClobber   = 0
		frames            = 120
	)

	rom := nonSilentAPULoROM(t)
	sys := snes.NewSystem(nil)
	mapLoROM(sys, rom)
	if !sys.Load() {
		t.Fatal("System.Load failed")
	}

	if sys.APU.GetCycles() != 0 || sys.APU.SaveState().DSPCycles != 0 {
		t.Fatal("audio cadence fixture must start before the first APU clock")
	}
	t.Logf("initial CPU reset clocks=%d, APU clocks=%d", sys.CPU.Cycles, sys.APU.GetCycles())
	var preWRAM [0x40]byte
	for i := uint32(0); i < uint32(len(preWRAM)); i++ {
		preWRAM[i] = sys.Bus.Read(0x7E0000 | i)
	}

	var samples []int16
	buf := make([]int16, 8192)
	for f := 0; f < frames; f++ {
		if err := sys.RunFrame(); err != nil {
			t.Fatalf("RunFrame frame=%d: %v", f, err)
		}
		for {
			n := sys.DrainAudio(buf)
			if n == 0 {
				break
			}
			samples = append(samples, buf[:n]...)
		}
	}

	wantSamples := audioSamplesAtCPUClock(sys.CPU.Cycles)
	t.Logf("CPU clocks=%d SMP clocks=%d expected interleaved samples=%d", sys.CPU.Cycles, sys.APU.GetCycles(), wantSamples)
	if got := len(samples); got != wantSamples {
		t.Fatalf("RunFrame sample count = %d, want %d", got, wantSamples)
	}
	if got := rmsInt16(samples); math.Abs(got-wantRMS) > rmsTolerance {
		t.Fatalf("RunFrame RMS = %.6f, want %.6f tolerance %.6f", got, wantRMS, rmsTolerance)
	}
	if got := firstNonZeroSample(samples); got != wantOnset {
		t.Fatalf("RunFrame onset = %d, want %d", got, wantOnset)
	}
	checkPostOnsetPCMHash(t, "RunFrame", samples, 4096, wantPostOnsetHash)

	clobber := 0
	for i := uint32(0); i < uint32(len(preWRAM)); i++ {
		if sys.Bus.Read(0x7E0000|i) != preWRAM[i] {
			clobber++
		}
	}
	if clobber != wantWRAMClobber {
		t.Fatalf("RunFrame post-frame WRAM[$7E:0000..%02X] clobber count = %d, want %d",
			len(preWRAM), clobber, wantWRAMClobber)
	}
	t.Logf("RunFrame audio: samples=%d rms=%.6f onset=%d post_onset_4096=%s wram_clobber=%d",
		len(samples), rmsInt16(samples), firstNonZeroSample(samples), wantPostOnsetHash, clobber)
}
