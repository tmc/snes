package apu

import "testing"

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
