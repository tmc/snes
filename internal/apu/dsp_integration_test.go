package apu

import "testing"

func writeDSP(a *APU, reg, val uint8) {
	a.Write(0x00F2, reg)
	a.Write(0x00F3, val)
}

func TestDSPIntegration(t *testing.T) {
	apu := NewAPU()

	// Write to DSP Register 0x0C (Main Volume Left, maybe? Just testing RAM)
	targetReg := uint8(0x0C)
	targetVal := uint8(0x7F)

	// Set DSP Address ($00F2)
	apu.Write(0x00F2, targetReg)

	// Write DSP Data ($00F3)
	apu.Write(0x00F3, targetVal)

	// Verify internal DSP state
	if apu.DSP.Read(targetReg) != targetVal {
		t.Errorf("DSP Register Write failed. Expected %02X, got %02X", targetVal, apu.DSP.Read(targetReg))
	}

	// Verify Read back via MMIO
	// Reset Address (just to be sure, though it persists)
	apu.Write(0x00F2, targetReg)
	readVal := apu.Read(0x00F3)
	if readVal != targetVal {
		t.Errorf("DSP Register Read via MMIO failed. Expected %02X, got %02X", targetVal, readVal)
	}
}

func TestDSPIntegration_ProducesNonSilentDrainAudio(t *testing.T) {
	apu := NewAPU()

	apu.RAM[0x2000] = 0x00
	apu.RAM[0x2001] = 0x30
	apu.RAM[0x2002] = 0x00
	apu.RAM[0x2003] = 0x30
	apu.RAM[0x3000] = 0xC0 // shift 12, filter 0
	for i := 0; i < 8; i++ {
		apu.RAM[0x3001+i] = 0x11
	}

	writeDSP(apu, 0x6C, 0x00) // unmute and enable echo clocking
	writeDSP(apu, 0x0C, 0x7F)
	writeDSP(apu, 0x1C, 0x7F)
	writeDSP(apu, 0x00, 0x7F)
	writeDSP(apu, 0x01, 0x7F)
	writeDSP(apu, 0x02, 0x00)
	writeDSP(apu, 0x03, 0x10)
	writeDSP(apu, 0x04, 0x00)
	writeDSP(apu, 0x07, 0x7F)
	writeDSP(apu, 0x5D, 0x20)
	writeDSP(apu, 0x4C, 0x01)

	for i := 0; i < dspSampleDivider*8; i++ {
		apu.Run()
	}

	buf := make([]int16, 32)
	n := apu.DrainAudio(buf)
	if n == 0 {
		t.Fatalf("DrainAudio returned no samples")
	}
	for _, sample := range buf[:n] {
		if sample != 0 {
			return
		}
	}
	t.Fatalf("DrainAudio returned only silent samples: %v", buf[:n])
}

func TestDSPIntegration_AddressMirrorsHighBit(t *testing.T) {
	apu := NewAPU()

	apu.Write(0x00F2, 0x8C)
	apu.Write(0x00F3, 0x55)
	if got := apu.DSP.Read(0x0C); got != 0x55 {
		t.Fatalf("DSP mirrored write = %02X, want 55", got)
	}

	apu.Write(0x00F2, 0x8C)
	if got := apu.Read(0x00F3); got != 0x55 {
		t.Fatalf("DSP mirrored read through $F3 = %02X, want 55", got)
	}
}

func TestDSPIntegration_StatePreservesSelectedAddress(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F2, 0x0C)
	state := apu.SaveState()

	apu.Write(0x00F2, 0x1C)
	apu.LoadState(state)
	if got := apu.Read(0x00F2); got != 0x0C {
		t.Fatalf("DSP address after LoadState = %02X, want 0C", got)
	}

	apu.Write(0x00F3, 0x55)
	if got := apu.DSP.Read(0x0C); got != 0x55 {
		t.Fatalf("DSP write after LoadState at 0C = %02X, want 55", got)
	}
	if got := apu.DSP.Read(0x1C); got == 0x55 {
		t.Fatalf("DSP write after LoadState used stale address 1C")
	}
}
