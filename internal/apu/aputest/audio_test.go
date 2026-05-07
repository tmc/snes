package aputest

import (
	"math"
	"testing"
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
	info := NonSilentSPCFixtureInfo()
	first := NonSilentSPCAudio()
	second := NonSilentSPCAudio()
	if got := len(first); got != info.SampleCount {
		t.Fatalf("sample count = %d, want %d", got, info.SampleCount)
	}
	if !equalSamples(second, first) {
		t.Fatalf("NonSilentSPCAudio mismatch across runs:\nfirst=%v\nsecond=%v", first, second)
	}
	if got := HashPCM16(first); got != info.PCM16SHA256 {
		t.Fatalf("sample hash = %s, want %s", got, info.PCM16SHA256)
	}
	if got := RMS(first); math.Abs(got-info.RMS) > 1e-12 {
		t.Fatalf("RMS = %.17f, want %.17f", got, info.RMS)
	}
}

func TestNonSilentSPCPayloadDeterministic(t *testing.T) {
	info := NonSilentSPCFixtureInfo()
	ram := NonSilentSPCRAM()
	if got := len(ram); got != 65536 {
		t.Fatalf("APURAM payload length = %d, want 65536", got)
	}
	if got := HashBytes(ram); got != info.APURAMSHA256 {
		t.Fatalf("APURAM payload hash = %s, want %s", got, info.APURAMSHA256)
	}

	spc := NonSilentSPCFile()
	if got := len(spc); got != 0x10180 {
		t.Fatalf("SPC payload length = %d, want %d", got, 0x10180)
	}
	if got := string(spc[:33]); got != "SNES-SPC700 Sound File Data v0.30" {
		t.Fatalf("SPC header = %q", got)
	}
	if got := HashBytes(spc); got != info.SPCFileSHA256 {
		t.Fatalf("SPC payload hash = %s, want %s", got, info.SPCFileSHA256)
	}
}

func TestNonSilentSPCArtifacts(t *testing.T) {
	info := NonSilentSPCFixtureInfo()
	artifacts := NonSilentSPCArtifacts()
	if got := len(artifacts); got != 2 {
		t.Fatalf("artifact count = %d, want 2", got)
	}
	want := map[string]struct {
		filename string
		hash     string
		size     int
	}{
		"non-silent-spc-apuram": {
			filename: "non_silent_spc_apuram.bin",
			hash:     info.APURAMSHA256,
			size:     65536,
		},
		"non-silent-spc-dump": {
			filename: "non_silent_spc.spc",
			hash:     info.SPCFileSHA256,
			size:     0x10180,
		},
	}
	for _, artifact := range artifacts {
		w, ok := want[artifact.Name]
		if !ok {
			t.Fatalf("unexpected artifact %q", artifact.Name)
		}
		if artifact.Filename != w.filename {
			t.Fatalf("%s filename = %q, want %q", artifact.Name, artifact.Filename, w.filename)
		}
		if got := len(artifact.Bytes); got != w.size {
			t.Fatalf("%s size = %d, want %d", artifact.Name, got, w.size)
		}
		if artifact.SHA256 != w.hash {
			t.Fatalf("%s declared hash = %s, want %s", artifact.Name, artifact.SHA256, w.hash)
		}
		if got := HashBytes(artifact.Bytes); got != w.hash {
			t.Fatalf("%s byte hash = %s, want %s", artifact.Name, got, w.hash)
		}
		delete(want, artifact.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing artifacts: %v", want)
	}
}

func TestNonSilentSPCFixtureInfo(t *testing.T) {
	info := NonSilentSPCFixtureInfo()
	if info.PC != 0x0200 {
		t.Fatalf("PC = %04X, want 0200", info.PC)
	}
	if info.SchedulerRuns != 64*16 {
		t.Fatalf("scheduler runs = %d, want %d", info.SchedulerRuns, 64*16)
	}
	if info.SampleCount != 32 {
		t.Fatalf("sample count = %d, want 32", info.SampleCount)
	}
	if info.PCM16SHA256 == "" || info.APURAMSHA256 == "" || info.SPCFileSHA256 == "" {
		t.Fatalf("fixture hashes must be populated: %+v", info)
	}
	if info.RMS <= 0 {
		t.Fatalf("RMS = %.17f, want positive", info.RMS)
	}
	wantWrites := []NonSilentSPCDSPWrite{
		{Register: 0x6C, Value: 0x00},
		{Register: 0x0C, Value: 0x7F},
		{Register: 0x1C, Value: 0x7F},
		{Register: 0x00, Value: 0x7F},
		{Register: 0x01, Value: 0x7F},
		{Register: 0x02, Value: 0x00},
		{Register: 0x03, Value: 0x10},
		{Register: 0x04, Value: 0x00},
		{Register: 0x07, Value: 0x7F},
		{Register: 0x5D, Value: 0x20},
		{Register: 0x4C, Value: 0x01},
	}
	if len(info.DSPWrites) != len(wantWrites) {
		t.Fatalf("DSP write count = %d, want %d", len(info.DSPWrites), len(wantWrites))
	}
	for i, want := range wantWrites {
		if info.DSPWrites[i] != want {
			t.Fatalf("DSP write %d = %+v, want %+v", i, info.DSPWrites[i], want)
		}
	}

	info.DSPWrites[0] = NonSilentSPCDSPWrite{Register: 0x7F, Value: 0x7F}
	if got := NonSilentSPCFixtureInfo().DSPWrites[0]; got != wantWrites[0] {
		t.Fatalf("DSPWrites is not defensively copied: got %+v want %+v", got, wantWrites[0])
	}
}
