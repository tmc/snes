// Package aputest provides deterministic APU fixtures for emulator tests.
package aputest

import (
	"math"

	"github.com/tmc/snes/internal/apu"
)

const nonSilentDSPTicks = 64 * 8

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

// NonSilentDSPAudio returns drained samples from ProgramNonSilentDSP.
func NonSilentDSPAudio() []int16 {
	a := apu.NewAPU()
	ProgramNonSilentDSP(a)
	for i := 0; i < nonSilentDSPTicks; i++ {
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
