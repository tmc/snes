package apu

import "testing"

func TestTimerEnable(t *testing.T) {
	apu := NewAPU()

	// Default: Disabled
	apu.TickTimers(timer01Divider)
	if apu.Timers[0].divider != 0 {
		t.Errorf("Timer 0 should not count when disabled")
	}

	// Enable Timer 0 ($F1 = 0x01)
	apu.Write(0x00F1, 0x01)
	apu.TickTimers(1)
	if apu.Timers[0].divider != 1 {
		t.Errorf("Timer 0 should count when enabled")
	}

	// Re-enabling resets stage2/stage3 but preserves the stage1 divider phase.
	apu.Timers[0].divider = 17
	apu.Timers[0].stage2 = 3
	apu.Timers[0].Counter = 4
	apu.Write(0x00F1, 0x00)
	apu.Write(0x00F1, 0x01)
	if apu.Timers[0].divider != 17 || apu.Timers[0].stage2 != 0 || apu.Timers[0].Counter != 0 {
		t.Fatalf("timer 0 enable state divider=%d stage2=%d counter=%d, want 17/0/0",
			apu.Timers[0].divider, apu.Timers[0].stage2, apu.Timers[0].Counter)
	}

	// Disable Timer 0 ($F1 = 0x00) -> Should preserve state.
	apu.Timers[0].divider = 9
	apu.Write(0x00F1, 0x00)
	if apu.Timers[0].divider != 9 {
		t.Errorf("Timer 0 divider changed on disable: got %d, want 9", apu.Timers[0].divider)
	}
}

func TestTimer0_Tick(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x01) // Enable Timer 0
	apu.Write(0x00FA, 100)  // Target = 100

	// Run 256 * 100 machine cycles => Should increment Counter by 1.
	cycles := uint64(timer01Divider * 100)
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

	// Run 32 * 50 machine cycles => Increment Counter.
	cycles := uint64(timer2Divider * 50)
	apu.TickTimers(cycles)

	val := apu.Read(0x00FF)
	if val != 1 {
		t.Errorf("Timer 2 Counter expected 1, got %d", val)
	}
}

func TestTimersAcceptLargeCycleBatches(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x05) // Enable timers 0 and 2.
	apu.Write(0x00FA, 100)
	apu.Write(0x00FC, 50)

	apu.TickTimers(uint64(timer01Divider * 100 * 3))
	if got := apu.Read(0x00FD); got != 3 {
		t.Fatalf("timer0 counter after large batch = %d, want 3", got)
	}

	apu.TickTimers(uint64(timer2Divider * 50 * 5))
	if got := apu.Read(0x00FF); got != 5 {
		t.Fatalf("timer2 counter after large batch = %d, want 5", got)
	}
}

func TestTimerCounterReadPreservesInternalPhase(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x01)
	apu.Write(0x00FA, 3)

	apu.TickTimers(uint64(timer01Divider*3 + timer01Divider/2))
	if got := apu.Read(0x00FD); got != 1 {
		t.Fatalf("timer0 counter before clear = %d, want 1", got)
	}
	if got := apu.Read(0x00FD); got != 0 {
		t.Fatalf("timer0 counter after clear = %d, want 0", got)
	}

	apu.TickTimers(uint64(timer01Divider*2 + timer01Divider/2))
	if got := apu.Read(0x00FD); got != 1 {
		t.Fatalf("timer0 counter after preserved phase = %d, want 1", got)
	}
}

func TestTimerReadBeforeSameCycleIncrement(t *testing.T) {
	apu := NewAPU()
	apu.Control = 0
	apu.Processor.PC = 0x0200
	apu.RAM[0x0200] = 0xE4 // MOV A, dp
	apu.RAM[0x0201] = 0xFD
	apu.Write(0x00F1, 0x01)
	apu.Write(0x00FA, 0x01)
	apu.Timers[0].divider = timer01Divider - 1

	apu.Run()
	if got := apu.Processor.A; got != 0 {
		t.Fatalf("same-cycle timer read A = %d, want pre-increment 0", got)
	}
	if got := apu.Read(0x00FD); got != 1 {
		t.Fatalf("timer counter after same-cycle increment = %d, want 1", got)
	}
}

func TestRunTimer2TargetZeroTicksAfter256Stage2Inputs(t *testing.T) {
	apu := NewAPU()
	apu.Control = 0
	apu.Processor.PC = 0x0200
	apu.Write(0x00F1, 0x04) // Enable Timer 2.
	apu.Write(0x00FC, 0x00) // Target 0 means 256 stage-2 inputs.

	for i := 0; i < timer2Divider*256-1; i++ {
		apu.Run()
	}
	if got := apu.Read(0x00FF); got != 0 {
		t.Fatalf("timer2 counter before target-zero wrap = %d, want 0", got)
	}

	apu.Run()
	if got := apu.Read(0x00FF); got != 1 {
		t.Fatalf("timer2 counter after target-zero wrap = %d, want 1", got)
	}
	if got := apu.Read(0x00FF); got != 0 {
		t.Fatalf("timer2 counter after read reset = %d, want 0", got)
	}
}

func TestFrequency(t *testing.T) {
	apu := NewAPU()
	if got, want := apu.Frequency(), uint64(spcMachineFrequency); got != want {
		t.Fatalf("Frequency = %d, want %d", got, want)
	}
}
