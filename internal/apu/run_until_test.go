package apu

import "testing"

func TestRunUntilRoundsUpSubcycleTarget(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0xE4 // MOV A, dp
	a.RAM[0x0201] = 0xF4
	a.Processor.PC = 0x0200

	a.WritePort(0, 0x7B)
	result := a.RunUntil(6, 21477272, 2048000, SyncSafety)
	if result.Yield != YieldNone {
		t.Fatalf("RunUntil yield = %d, want %d", result.Yield, YieldNone)
	}
	if got := a.Processor.A; got != 0x7B {
		t.Fatalf("APU A = %02X, want 7B", got)
	}
	if got := a.cycles; got != 1 {
		t.Fatalf("APU cycles = %d, want 1", got)
	}
}
