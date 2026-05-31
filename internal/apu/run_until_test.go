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

func TestRunUntilPortAccessModesPendingOutputWrite(t *testing.T) {
	tests := []struct {
		name      string
		mode      SyncMode
		wantYield YieldReason
		wantPort  uint8
	}{
		{name: "read", mode: SyncPortRead, wantYield: YieldAPUPortWrite, wantPort: 0x00},
		{name: "write", mode: SyncPortWrite, wantYield: YieldNone, wantPort: 0x5A},
		{name: "safety", mode: SyncSafety, wantYield: YieldNone, wantPort: 0x5A},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newPendingOutputPortAPU(t)

			result := a.RunUntilTarget(4, tt.mode)
			if result.Yield != tt.wantYield {
				t.Fatalf("RunUntilTarget yield = %d, want %d", result.Yield, tt.wantYield)
			}
			if got := a.ReadPort(0); got != tt.wantPort {
				t.Fatalf("port after %s sync = %02X, want %02X", tt.name, got, tt.wantPort)
			}
		})
	}
}

func TestRunUntilSafetySyncPublishesAfterReadStops(t *testing.T) {
	a := newPendingOutputPortAPU(t)

	result := a.RunUntilTarget(4, SyncPortRead)
	if result.Yield != YieldAPUPortWrite {
		t.Fatalf("read sync yield = %d, want %d", result.Yield, YieldAPUPortWrite)
	}
	if got := a.ReadPort(0); got != 0x00 {
		t.Fatalf("port after read sync = %02X, want 00", got)
	}

	result = a.RunUntilTarget(4, SyncSafety)
	if result.Yield != YieldNone {
		t.Fatalf("safety sync yield = %d, want %d", result.Yield, YieldNone)
	}
	if got := a.ReadPort(0); got != 0x5A {
		t.Fatalf("port after safety sync = %02X, want 5A", got)
	}
}

func newPendingOutputPortAPU(t *testing.T) *APU {
	t.Helper()

	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x0200
	a.Processor.A = 0x5A
	a.RAM[0x0200] = 0xC4 // MOV dp,A
	a.RAM[0x0201] = 0xF4

	result := a.RunUntilTarget(4, SyncPostCPU)
	if result.Yield != YieldAPUPortWrite {
		t.Fatalf("test setup yield = %d, want %d", result.Yield, YieldAPUPortWrite)
	}
	if a.OutPorts[0] != 0 || a.pendingOutPortMask&1 == 0 {
		t.Fatal("test setup did not leave port write pending")
	}
	return a
}
