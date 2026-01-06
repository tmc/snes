package apu

import "testing"

func TestTimerEnable(t *testing.T) {
	apu := NewAPU()

	// Default: Disabled
	apu.TickTimers(128)
	if apu.Timers[0].divider != 0 {
		t.Errorf("Timer 0 should not count when disabled")
	}

	// Enable Timer 0 ($F1 = 0x01)
	apu.Write(0x00F1, 0x01)
	apu.TickTimers(1)
	if apu.Timers[0].divider != 1 {
		t.Errorf("Timer 0 should count when enabled")
	}

	// Disable Timer 0 ($F1 = 0x00) -> Should reset
	apu.Write(0x00F1, 0x00)
	if apu.Timers[0].divider != 0 {
		t.Errorf("Timer 0 should reset when disabled")
	}
}

func TestTimer0_Tick(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x01) // Enable Timer 0
	apu.Write(0x00FA, 100)  // Target = 100

	// Run 128 * 100 cycles => Should increment Counter by 1
	cycles := uint64(128 * 100)
	apu.TickTimers(cycles)

	val := apu.Read(0x00FD) // Read Timer 0 Counter
	if val != 1 {
		t.Errorf("Timer 0 Counter expected 1, got %d", val)
	}

	// Read again should be 0 (reset on read)
	val = apu.Read(0x00FD)
	if val != 0 {
		t.Errorf("Timer 0 Counter expected 0 after read, got %d", val)
	}
}

func TestTimer2_Tick(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x04) // Enable Timer 2
	apu.Write(0x00FC, 50)   // Target = 50

	// Run 16 (Divider) * 50 (Target) cycles => Increment Counter
	cycles := uint64(16 * 50)
	apu.TickTimers(cycles)

	val := apu.Read(0x00FF)
	if val != 1 {
		t.Errorf("Timer 2 Counter expected 1, got %d", val)
	}
}
