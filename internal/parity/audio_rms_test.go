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

	"github.com/tmc/snes"
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
		wantDSPSamples = 16
		wantDSPHash    = "bdb84c50f56aa3d0f424318963a6c9e19d1d428f1ff116eafeaf6bf1a6a469a0"
		wantDSPRMS     = 0.1220703125

		wantSPCSamples  = 32
		wantSPCHash     = "033326f5fd356ba4b254b9592c45a9a780b50a47937b7a9aee1fd91b7a54a3f8"
		wantSPCRMS      = 0.11687995868402994
		wantAPURAMHash  = "7efb9532672c74e3fee42b4214c5721b730d2117e3d03407b8d505b318f7b7bf"
		wantSPCFileHash = "bc8e0e1108d49f0ba8d90e5b4b2a60a2c36990d683ef8c98b946797cbbc54275"

		tolerance = 0.000000000001
	)

	t.Run("direct-dsp", func(t *testing.T) {
		samples := aputest.NonSilentDSPAudio()
		checkNonSilentPCM(t, samples, wantDSPSamples, wantDSPHash, wantDSPRMS, tolerance)
	})

	t.Run("spc700-program", func(t *testing.T) {
		samples := aputest.NonSilentSPCAudio()
		checkNonSilentPCM(t, samples, wantSPCSamples, wantSPCHash, wantSPCRMS, tolerance)
	})

	t.Run("spc-artifacts", func(t *testing.T) {
		artifacts := aputest.NonSilentSPCArtifacts()
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
				hash:     wantAPURAMHash,
				size:     65536,
			},
			"non-silent-spc-dump": {
				filename: "non_silent_spc.spc",
				hash:     wantSPCFileHash,
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
				t.Fatalf("%s advertised hash = %s, want %s", artifact.Name, artifact.SHA256, w.hash)
			}
			if got := hashBytes(artifact.Bytes); got != w.hash {
				t.Fatalf("%s content hash = %s, want %s", artifact.Name, got, w.hash)
			}
		}
	})

	t.Run("lorom-uploader", func(t *testing.T) {
		info := aputest.NonSilentSPCFixtureInfo()
		rom := nonSilentAPULoROM(t)
		romPath := writeTempROM(t, "non_silent_apu.sfc", rom)

		goSamples, goState := goAudioFromROM(t, rom, 120)
		if rmsInt16(goSamples) == 0 {
			wantRAM := aputest.NonSilentSPCRAM()
			t.Fatalf("go uploader audio is silent: samples=%d cpu=%02X:%04X apu_pc=%04X apu_cycles=%d ports=%02X%02X%02X%02X program_hash=%s want_program=%s srcdir=%s want_srcdir=%s brr=%s want_brr=%s",
				len(goSamples), goState.CPUPB, goState.CPUPC, goState.PC, goState.Cycles,
				goState.OutPorts[0], goState.OutPorts[1], goState.OutPorts[2], goState.OutPorts[3],
				goState.ProgramHash, hashBytes(wantRAM[0x0200:0x0244]),
				goState.SourceDir, hex.EncodeToString(wantRAM[0x2000:0x2004]),
				goState.BRR, hex.EncodeToString(wantRAM[0x3000:0x3009]))
		}
		t.Logf("Go non_silent_apu.sfc samples=%d rms=%.6f hash=%s fixture_pc=%04X apuram=%s",
			len(goSamples), rmsInt16(goSamples), hashPCM16(goSamples), info.PC, info.APURAMSHA256)

		for _, core := range []struct {
			name string
			path string
		}{
			{name: "bsnes", path: bsnes.DefaultPath()},
			{name: "snes9x", path: snes9x.DefaultPath()},
		} {
			core := core
			t.Run(core.name, func(t *testing.T) {
				checkFile(t, core.path)
				samples := referenceAudioSamples(t, romPath, core.path, 120)
				checkNonSilentRMS(t, core.name, samples)
				t.Logf("%s non_silent_apu.sfc samples=%d rms=%.6f hash=%s",
					core.name, len(samples), rmsInt16(samples), hashPCM16(samples))
			})
		}
	})
}

func checkNonSilentPCM(t *testing.T, samples []int16, wantSamples int, wantHash string, wantRMS, tolerance float64) {
	t.Helper()
	if len(samples) == 0 {
		t.Fatal("fixture returned no samples")
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

func checkNonSilentRMS(t *testing.T, name string, samples []int16) {
	t.Helper()
	if len(samples) == 0 {
		t.Fatalf("%s produced no audio samples", name)
	}
	if got := rmsInt16(samples); got == 0 {
		t.Fatalf("%s RMS = 0, want non-silent uploader audio", name)
	}
	if got := hashPCM16(samples); got == zeroPCMHash(len(samples)) {
		t.Fatalf("%s PCM hash is all-zero silence: %s", name, got)
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
	return rmsInt16(referenceAudioSamples(t, romPath, corePath, frames))
}

func referenceAudioSamples(t *testing.T, romPath, corePath string, frames int) []int16 {
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
	return samples
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

func zeroPCMHash(samples int) string {
	h := sha256.New()
	h.Write(make([]byte, samples*2))
	return hex.EncodeToString(h.Sum(nil))
}

func nonSilentAPULoROM(t *testing.T) []byte {
	t.Helper()
	ram := aputest.NonSilentSPCRAM()
	blocks := []struct {
		addr uint16
		data []byte
	}{
		{addr: 0x0200, data: trimTrailingZeroes(ram[0x0200:0x0300])},
		{addr: 0x2000, data: trimTrailingZeroes(ram[0x2000:0x2010])},
		{addr: 0x3000, data: trimTrailingZeroes(ram[0x3000:0x3010])},
	}
	rom := make([]byte, 0x8000)
	var code []byte
	emit := func(bytes ...byte) {
		code = append(code, bytes...)
	}
	ldaImm := func(v byte) { emit(0xa9, v) }
	staAbs := func(addr uint16) { emit(0x8d, byte(addr), byte(addr>>8)) }
	ldaAbs := func(addr uint16) { emit(0xad, byte(addr), byte(addr>>8)) }
	cmpImm := func(v byte) { emit(0xc9, v) }
	bne := func(target int) {
		off := target - (0x8000 + len(code) + 2)
		if off < -128 || off > 127 {
			t.Fatalf("branch target out of range: pc=%04x target=%04x", 0x8000+len(code), target)
		}
		emit(0xd0, byte(int8(off)))
	}
	waitPort0 := func(v byte) {
		start := 0x8000 + len(code)
		ldaAbs(0x2140)
		cmpImm(v)
		bne(start)
	}
	waitBoot := func() {
		ldaImm(0)
		staAbs(0x2140)
		waitAA := 0x8000 + len(code)
		ldaAbs(0x2140)
		cmpImm(0xaa)
		bne(waitAA)
		waitBB := 0x8000 + len(code)
		ldaAbs(0x2141)
		cmpImm(0xbb)
		bne(waitBB)
	}
	beginUpload := func(addr uint16, token byte) {
		ldaImm(byte(addr))
		staAbs(0x2142)
		ldaImm(byte(addr >> 8))
		staAbs(0x2143)
		ldaImm(token)
		staAbs(0x2141)
		staAbs(0x2140)
		waitPort0(token)
	}
	uploadByte := func(index byte, value byte) {
		ldaImm(value)
		staAbs(0x2141)
		ldaImm(index)
		staAbs(0x2140)
		waitPort0(index)
	}
	execute := func(addr uint16, token byte) {
		ldaImm(byte(addr))
		staAbs(0x2142)
		ldaImm(byte(addr >> 8))
		staAbs(0x2143)
		ldaImm(0)
		staAbs(0x2141)
		ldaImm(token)
		staAbs(0x2140)
		waitPort0(token)
	}

	emit(0x78) // SEI
	waitBoot()
	token := byte(0xcc)
	for _, block := range blocks {
		if len(block.data) == 0 || len(block.data) > 255 {
			t.Fatalf("bad non-silent APU upload block addr=%04x len=%d", block.addr, len(block.data))
		}
		beginUpload(block.addr, token)
		for i, v := range block.data {
			uploadByte(byte(i), v)
		}
		token = byte(len(block.data)-1) + 0x22
		if token == 0 {
			token++
		}
	}
	execute(aputest.NonSilentSPCFixtureInfo().PC, token)
	loop := uint16(0x8000 + len(code))
	emit(0x4c, byte(loop), byte(loop>>8))
	if len(code) > 0x7fc0 {
		t.Fatalf("non-silent APU ROM code too large: %d", len(code))
	}
	copy(rom, code)
	copy(rom[0x7fc0:], []byte("NON SILENT APU       "))
	rom[0x7fd5] = 0x20 // LoROM
	rom[0x7fd6] = 0x00 // ROM only
	rom[0x7fd7] = 0x08 // 32 KiB
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func trimTrailingZeroes(data []byte) []byte {
	end := len(data)
	for end > 0 && data[end-1] == 0 {
		end--
	}
	out := make([]byte, end)
	copy(out, data[:end])
	return out
}

func writeTempROM(t *testing.T, name string, rom []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, rom, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

type goUploaderState struct {
	PC          uint16
	CPUPB       uint8
	CPUPC       uint16
	Cycles      uint64
	OutPorts    [4]uint8
	ProgramHash string
	SourceDir   string
	BRR         string
}

func goAudioFromROM(t *testing.T, rom []byte, frames int) ([]int16, goUploaderState) {
	t.Helper()
	sys := snes.NewSystem(nil)
	mapLoROM(sys, rom)
	if !sys.Load() {
		t.Fatal("Go System Load failed")
	}
	var samples []int16
	buf := make([]int16, 8192)
	for frame := 0; frame < frames; frame++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("Go frame %d: %v", frame, err)
		}
		for {
			n := sys.DrainAudio(buf)
			if n == 0 {
				break
			}
			samples = append(samples, buf[:n]...)
		}
	}
	return samples, goUploaderState{
		PC:          sys.APU.Processor.PC,
		CPUPB:       sys.CPU.PB,
		CPUPC:       sys.CPU.PC,
		Cycles:      sys.APU.GetCycles(),
		OutPorts:    sys.APU.OutPorts,
		ProgramHash: hashBytes(sys.APU.RAM[0x0200:0x0244]),
		SourceDir:   hex.EncodeToString(sys.APU.RAM[0x2000:0x2004]),
		BRR:         hex.EncodeToString(sys.APU.RAM[0x3000:0x3009]),
	}
}
