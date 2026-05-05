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

const nonSilentDSPTicks = 64 * 8
const nonSilentSPCTicks = 64 * 16
const nonSilentSPCPC = 0x0200

// AudioArtifact describes a deterministic APU audio payload that can be handed
// to a reference runner.
type AudioArtifact struct {
	Name        string
	Filename    string
	ContentType string
	SHA256      string
	Bytes       []byte
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

	program := []byte{
		0x8F, 0x6C, 0xF2, 0x8F, 0x00, 0xF3, // FLG
		0x8F, 0x0C, 0xF2, 0x8F, 0x7F, 0xF3, // MVOLL
		0x8F, 0x1C, 0xF2, 0x8F, 0x7F, 0xF3, // MVOLR
		0x8F, 0x00, 0xF2, 0x8F, 0x7F, 0xF3, // V0 VOLL
		0x8F, 0x01, 0xF2, 0x8F, 0x7F, 0xF3, // V0 VOLR
		0x8F, 0x02, 0xF2, 0x8F, 0x00, 0xF3, // V0 pitch low
		0x8F, 0x03, 0xF2, 0x8F, 0x10, 0xF3, // V0 pitch high
		0x8F, 0x04, 0xF2, 0x8F, 0x00, 0xF3, // V0 SRCN
		0x8F, 0x07, 0xF2, 0x8F, 0x7F, 0xF3, // V0 GAIN
		0x8F, 0x5D, 0xF2, 0x8F, 0x20, 0xF3, // DIR
		0x8F, 0x4C, 0xF2, 0x8F, 0x01, 0xF3, // KON
		0x2F, 0xFE, // idle
	}
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

// NonSilentSPCAudio returns drained samples from a live SPC700 program that
// configures the DSP through MMIO.
func NonSilentSPCAudio() []int16 {
	a := apu.NewAPU()
	ProgramNonSilentSPC(a)
	return runAndDrain(a, nonSilentSPCTicks)
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
