package parity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/apu/aputest"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

type audioRMSGolden struct {
	ROM          string  `json:"rom"`
	ROMHash      string  `json:"rom_sha256"`
	Core         string  `json:"core"`
	CoreVersion  string  `json:"core_version"`
	CorePathHint string  `json:"core_path_hint"`
	Frames       int     `json:"frames"`
	RMS          float64 `json:"rms"`
	Tolerance    float64 `json:"tolerance,omitempty"`
	Comment      string  `json:"comment,omitempty"`
}

type audioRMSGoldens struct {
	Cases map[string]audioRMSGolden `json:"cases"`
}

type audioRMSCase struct {
	name   string
	rom    string
	frames int
}

var audioRMSCases = []audioRMSCase{
	{name: "SPCSMP", rom: "spc_smp.sfc", frames: 60},
	{name: "SPCTimer", rom: "spc_timer.sfc", frames: 60},
}

func TestReferenceAudioRMSGoldens(t *testing.T) {
	goldens := readAudioRMSGoldens(t)
	cores := []struct {
		name string
		path string
	}{
		{name: "bsnes", path: bsnes.DefaultPath()},
		{name: "snes9x", path: snes9x.DefaultPath()},
	}

	for _, tc := range audioRMSCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			romPath := filepath.Join("testdata", tc.rom)
			checkFile(t, romPath)
			for _, core := range cores {
				core := core
				t.Run(core.name, func(t *testing.T) {
					checkFile(t, core.path)
					key := tc.name + "/" + core.name
					golden, ok := goldens.Cases[key]
					if !ok {
						t.Skipf("no audio RMS golden for %s in %s", key, audioRMSGoldensPath)
					}
					if golden.ROM != "" && golden.ROM != tc.rom {
						t.Fatalf("%s golden names rom %q, want %q", key, golden.ROM, tc.rom)
					}
					if golden.Core != "" && golden.Core != core.name {
						t.Fatalf("%s golden names core %q, want %q", key, golden.Core, core.name)
					}
					frames := tc.frames
					if golden.Frames != 0 {
						frames = golden.Frames
					}
					got := referenceAudioRMS(t, romPath, core.path, frames)
					tolerance := golden.Tolerance
					if tolerance == 0 {
						tolerance = 0.0001
					}
					if diff := math.Abs(got - golden.RMS); diff > tolerance {
						t.Errorf("audio RMS drift: got %.6f want %.6f tolerance %.6f", got, golden.RMS, tolerance)
					}
				})
			}
		})
	}
}

func TestReferenceAudioRMSNonSilentAPUFixture(t *testing.T) {
	const (
		wantSamples = 16
		wantHash    = "bdb84c50f56aa3d0f424318963a6c9e19d1d428f1ff116eafeaf6bf1a6a469a0"
		wantRMS     = 0.1220703125
		tolerance   = 0.000000000001
	)

	samples := aputest.NonSilentDSPAudio()
	if len(samples) == 0 {
		t.Fatal("NonSilentDSPAudio returned no samples")
	}
	if got := len(samples); got != wantSamples {
		t.Fatalf("sample count = %d, want %d", got, wantSamples)
	}
	gotHash := hashPCM16(samples)
	if gotHash != wantHash {
		t.Fatalf("sample hash = %s, want %s", gotHash, wantHash)
	}
	gotRMS := rmsInt16(samples)
	if gotRMS == 0 {
		t.Fatal("RMS = 0, want non-silent fixture")
	}
	if diff := math.Abs(gotRMS - wantRMS); diff > tolerance {
		t.Fatalf("RMS = %.12f, want %.12f tolerance %.12f", gotRMS, wantRMS, tolerance)
	}
}

const audioRMSGoldensPath = "testdata/reference_audio_rms_goldens.json"

func readAudioRMSGoldens(t *testing.T) audioRMSGoldens {
	raw, err := os.ReadFile(audioRMSGoldensPath)
	if err != nil {
		t.Skipf("no audio RMS goldens at %s: %v", audioRMSGoldensPath, err)
	}
	var goldens audioRMSGoldens
	if err := json.Unmarshal(raw, &goldens); err != nil {
		t.Fatalf("parse %s: %v", audioRMSGoldensPath, err)
	}
	if err := validateAudioRMSGoldens(goldens); err != nil {
		t.Fatalf("validate %s: %v", audioRMSGoldensPath, err)
	}
	return goldens
}

func referenceAudioRMS(t *testing.T, romPath, corePath string, frames int) float64 {
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatalf("load libretro core: %v", err)
	}
	core.Logger = t
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("load %s", romPath)
	}

	var samples []int16
	buf := make([]int16, 8192)
	for frame := 0; frame < frames; frame++ {
		core.Run()
		for {
			n := core.DrainAudio(buf)
			if n == 0 {
				break
			}
			samples = append(samples, buf[:n]...)
		}
	}
	if len(samples) == 0 {
		t.Fatalf("no audio samples captured after %d frames", frames)
	}
	return rmsInt16(samples)
}

func rmsInt16(samples []int16) float64 {
	var sum float64
	for _, sample := range samples {
		v := float64(sample) / 32768
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func hashPCM16(samples []int16) string {
	h := sha256.New()
	var buf [2]byte
	for _, sample := range samples {
		binary.LittleEndian.PutUint16(buf[:], uint16(sample))
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
