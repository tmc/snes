package apu

import "testing"

func TestMachineCycleFrequency(t *testing.T) {
	apu := NewAPU()
	if got, want := apu.Frequency(), uint64(spcMachineFrequency); got != want {
		t.Fatalf("Frequency = %d, want %d", got, want)
	}
}

func TestMachineCycleTimerDividers(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x01)
	apu.Write(0x00FA, 3)

	apu.TickTimers(uint64(timer01Divider * 3))
	if got := apu.Read(0x00FD); got != 1 {
		t.Fatalf("timer0 counter = %d, want 1", got)
	}

	apu = NewAPU()
	apu.Write(0x00F1, 0x04)
	apu.Write(0x00FC, 4)
	apu.TickTimers(uint64(timer2Divider * 4))
	if got := apu.Read(0x00FF); got != 1 {
		t.Fatalf("timer2 counter = %d, want 1", got)
	}
}
