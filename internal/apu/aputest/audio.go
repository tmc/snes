// Package aputest provides deterministic APU fixtures for emulator tests.
//
// The fixtures use the public APU scheduler and MMIO paths, not direct DSP
// calls, so parity tests can use them as Go-side evidence when preparing
// reference RMS goldens.
package aputest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"

	"github.com/tmc/snes/internal/apu"
)

const nonSilentDSPTicks = 64 * 16
const nonSilentSPCMaxTicks = 64 * 64
const nonSilentSPCPC = 0x0200

const (
	nonSilentSPCSampleCount = 32
	nonSilentSPCHash        = "e2f0b1ed72c91c38146e9c453832c9ee65ee40916701dbb769faa60e9b715ec3"
	nonSilentSPCRMS         = 0.08631674575031098
	nonSilentSPCRAMHash     = "7efb9532672c74e3fee42b4214c5721b730d2117e3d03407b8d505b318f7b7bf"
	nonSilentSPCFileHash    = "bc8e0e1108d49f0ba8d90e5b4b2a60a2c36990d683ef8c98b946797cbbc54275"
)

// AudioArtifact describes a deterministic APU audio payload that can be handed
// to a reference runner.
type AudioArtifact struct {
	Name        string
	Filename    string
	ContentType string
	SHA256      string
	Bytes       []byte
}

// NonSilentSPCDSPWrite describes one DSP register write made by the
// non-silent SPC program through $F2/$F3.
type NonSilentSPCDSPWrite struct {
	Register uint8
	Value    uint8
}

// NonSilentSPCInfo describes the deterministic non-silent SPC fixture. Parity
// ROM uploaders can use it to embed the APURAM image and to record reviewed
// Go-side expectations next to bsnes/snes9x goldens.
type NonSilentSPCInfo struct {
	PC              uint16
	PreKeyOnSamples int // Interleaved samples immediately before the KON write.
	SampleCount     int
	PCM16SHA256     string
	RMS             float64
	APURAMSHA256    string
	SPCFileSHA256   string
	DSPWrites       []NonSilentSPCDSPWrite
}

var nonSilentSPCDSPWrites = []NonSilentSPCDSPWrite{
	{Register: 0x6C, Value: 0x00}, // FLG
	{Register: 0x0C, Value: 0x7F}, // MVOLL
	{Register: 0x1C, Value: 0x7F}, // MVOLR
	{Register: 0x00, Value: 0x7F}, // V0 VOLL
	{Register: 0x01, Value: 0x7F}, // V0 VOLR
	{Register: 0x02, Value: 0x00}, // V0 pitch low
	{Register: 0x03, Value: 0x10}, // V0 pitch high
	{Register: 0x04, Value: 0x00}, // V0 SRCN
	{Register: 0x07, Value: 0x7F}, // V0 GAIN
	{Register: 0x5D, Value: 0x20}, // DIR
	{Register: 0x4C, Value: 0x01}, // KON
}

// NonSilentSPCFixtureInfo returns metadata for the non-silent SPC fixture.
func NonSilentSPCFixtureInfo() NonSilentSPCInfo {
	writes := append([]NonSilentSPCDSPWrite(nil), nonSilentSPCDSPWrites...)
	return NonSilentSPCInfo{
		PC:              nonSilentSPCPC,
		PreKeyOnSamples: 2,
		SampleCount:     nonSilentSPCSampleCount,
		PCM16SHA256:     nonSilentSPCHash,
		RMS:             nonSilentSPCRMS,
		APURAMSHA256:    nonSilentSPCRAMHash,
		SPCFileSHA256:   nonSilentSPCFileHash,
		DSPWrites:       writes,
	}
}

func writeDSP(a *apu.APU, reg, val uint8) {
	a.Write(0x00F2, reg)
	a.Write(0x00F3, val)
}

// ProgramNonSilentDSP configures a small BRR source and keys voice 0 through
// the APU's DSP MMIO path. The fixture is deterministic and produces non-zero
// stereo samples after a short scheduler run.
func ProgramNonSilentDSP(a *apu.APU) {
	a.RAM[0x2000] = 0x00
	a.RAM[0x2001] = 0x30
	a.RAM[0x2002] = 0x00
	a.RAM[0x2003] = 0x30
	a.RAM[0x3000] = 0xC0 // shift 12, filter 0
	for i := 0; i < 8; i++ {
		a.RAM[0x3001+i] = 0x11
	}

	writeDSP(a, 0x6C, 0x00) // unmute and enable echo clocking
	writeDSP(a, 0x0C, 0x7F)
	writeDSP(a, 0x1C, 0x7F)
	writeDSP(a, 0x00, 0x7F)
	writeDSP(a, 0x01, 0x7F)
	writeDSP(a, 0x02, 0x00)
	writeDSP(a, 0x03, 0x10)
	writeDSP(a, 0x04, 0x00)
	writeDSP(a, 0x07, 0x7F)
	writeDSP(a, 0x5D, 0x20)
	writeDSP(a, 0x4C, 0x01)
}

// NonSilentDSPAudio returns drained samples from ProgramNonSilentDSP. The
// output is deterministic across calls and suitable for stable RMS checks.
func NonSilentDSPAudio() []int16 {
	a := apu.NewAPU()
	ProgramNonSilentDSP(a)
	return runAndDrain(a, nonSilentDSPTicks)
}

// ProgramNonSilentSPC installs a tiny SPC700 program that writes DSP registers
// through $F2/$F3, keys voice 0, then idles. This is closer to a reference ROM
// path than direct Go DSP programming while staying deterministic and small.
func ProgramNonSilentSPC(a *apu.APU) {
	a.Control = 0
	a.Processor.PC = nonSilentSPCPC
	a.Processor.Stopped = false
	copy(a.RAM[:], NonSilentSPCRAM())
}

// NonSilentSPCRAM returns the 64 KiB APURAM payload used by ProgramNonSilentSPC.
func NonSilentSPCRAM() []byte {
	ram := make([]byte, 65536)
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30
	ram[0x2002] = 0x00
	ram[0x2003] = 0x30
	ram[0x3000] = 0xC0 // shift 12, filter 0
	for i := 0; i < 8; i++ {
		ram[0x3001+i] = 0x11
	}

	var program []byte
	for _, write := range nonSilentSPCDSPWrites {
		program = append(program, 0x8F, write.Register, 0xF2, 0x8F, write.Value, 0xF3)
	}
	program = append(program, 0x2F, 0xFE) // idle
	copy(ram[nonSilentSPCPC:], program)
	return ram
}

// NonSilentSPCFile returns an SPC700 dump for tools that can consume .spc
// files. The mounted libretro bsnes and snes9x cores do not currently advertise
// .spc support, so this is a payload for a reference wrapper or SPC-capable
// reference path rather than the existing ROM-only parity bridge.
func NonSilentSPCFile() []byte {
	const ramOffset = 0x100
	data := make([]byte, ramOffset+65536+128)
	copy(data, "SNES-SPC700 Sound File Data v0.30")
	data[0x21] = 26
	data[0x22] = 26
	data[0x23] = 27
	data[0x24] = 30
	binary.LittleEndian.PutUint16(data[0x25:], nonSilentSPCPC)
	data[0x2B] = 0xEF
	copy(data[ramOffset:], NonSilentSPCRAM())
	return data
}

// NonSilentSPCArtifacts returns the deterministic payloads that a reference
// path needs to run the non-silent SPC fixture.
func NonSilentSPCArtifacts() []AudioArtifact {
	ram := NonSilentSPCRAM()
	spc := NonSilentSPCFile()
	return []AudioArtifact{
		{
			Name:        "non-silent-spc-apuram",
			Filename:    "non_silent_spc_apuram.bin",
			ContentType: "application/octet-stream",
			SHA256:      HashBytes(ram),
			Bytes:       ram,
		},
		{
			Name:        "non-silent-spc-dump",
			Filename:    "non_silent_spc.spc",
			ContentType: "audio/x-spc",
			SHA256:      HashBytes(spc),
			Bytes:       spc,
		},
	}
}

// NonSilentSPCAudio captures the last stereo sample before KON and the next
// fifteen stereo samples from the live SPC700 program. This is the waveform
// window measured by the original fixture hash; startup latency is tested
// separately. No samples are synthesized or changed.
func NonSilentSPCAudio() []int16 {
	a := apu.NewAPU()
	ProgramNonSilentSPC(a)
	return captureSPCKeyOn(a)
}

func captureSPCKeyOn(a *apu.APU) []int16 {
	samples, start := 0, -1
	a.Trace = func(e apu.TimingEvent) {
		switch e.Kind {
		case "sample":
			samples += 2
		case "dsp-write":
			if e.Address == 0x4c && e.Value == 1 && start < 0 && samples >= 2 {
				start = samples - 2
			}
		}
	}
	defer func() { a.Trace = nil }()
	for tick := 0; tick < nonSilentSPCMaxTicks; tick++ {
		a.Run()
		if start >= 0 && samples >= start+nonSilentSPCSampleCount {
			pcm := make([]int16, samples)
			n := a.DrainAudio(pcm)
			if n < start+nonSilentSPCSampleCount {
				return nil
			}
			return pcm[start : start+nonSilentSPCSampleCount]
		}
	}
	return nil
}

// RMS returns the normalized RMS of signed 16-bit PCM samples.
func RMS(samples []int16) float64 {
	var sum float64
	for _, sample := range samples {
		v := float64(sample) / 32768
		sum += v * v
	}
	if len(samples) == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// HashPCM16 returns the SHA-256 of little-endian signed 16-bit PCM samples.
func HashPCM16(samples []int16) string {
	return HashBytes(pcm16Bytes(samples))
}

// HashBytes returns the SHA-256 of data as lowercase hex.
func HashBytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func pcm16Bytes(samples []int16) []byte {
	data := make([]byte, 2*len(samples))
	var buf [2]byte
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(buf[:], uint16(sample))
		copy(data[2*i:], buf[:])
	}
	return data
}

func runAndDrain(a *apu.APU, ticks int) []int16 {
	for i := 0; i < ticks; i++ {
		a.Run()
	}

	var samples []int16
	buf := make([]int16, 256)
	for {
		n := a.DrainAudio(buf)
		if n == 0 {
			break
		}
		samples = append(samples, buf[:n]...)
	}
	return samples
}
