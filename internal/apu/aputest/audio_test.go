package aputest

import (
	"math"
	"testing"
)

const (
	nonSilentSPCSampleCount = 32
	nonSilentSPCHash        = "033326f5fd356ba4b254b9592c45a9a780b50a47937b7a9aee1fd91b7a54a3f8"
	nonSilentSPCRMS         = 0.11687995868402994
	nonSilentSPCRAMHash     = "7efb9532672c74e3fee42b4214c5721b730d2117e3d03407b8d505b318f7b7bf"
	nonSilentSPCFileHash    = "bc8e0e1108d49f0ba8d90e5b4b2a60a2c36990d683ef8c98b946797cbbc54275"
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

func TestNonSilentSPCPayloadDeterministic(t *testing.T) {
	ram := NonSilentSPCRAM()
	if got := len(ram); got != 65536 {
		t.Fatalf("APURAM payload length = %d, want 65536", got)
	}
	if got := HashBytes(ram); got != nonSilentSPCRAMHash {
		t.Fatalf("APURAM payload hash = %s, want %s", got, nonSilentSPCRAMHash)
	}

	spc := NonSilentSPCFile()
	if got := len(spc); got != 0x10180 {
		t.Fatalf("SPC payload length = %d, want %d", got, 0x10180)
	}
	if got := string(spc[:33]); got != "SNES-SPC700 Sound File Data v0.30" {
		t.Fatalf("SPC header = %q", got)
	}
	if got := HashBytes(spc); got != nonSilentSPCFileHash {
		t.Fatalf("SPC payload hash = %s, want %s", got, nonSilentSPCFileHash)
	}
}

func TestNonSilentSPCArtifacts(t *testing.T) {
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
			hash:     nonSilentSPCRAMHash,
			size:     65536,
		},
		"non-silent-spc-dump": {
			filename: "non_silent_spc.spc",
			hash:     nonSilentSPCFileHash,
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
