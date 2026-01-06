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
