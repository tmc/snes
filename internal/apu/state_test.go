package apu

import "testing"

func TestAPUStatePreservesPendingInstructionCycles(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x0200
	a.RAM[0x0200] = 0x00 // NOP, two cycles.

	a.Run()
	if a.pending == 0 {
		t.Fatal("test setup did not leave an instruction pending")
	}

	state := a.SaveState()
	a.pending = 0
	a.LoadState(state)

	if got := a.pending; got != state.Pending {
		t.Fatalf("pending after LoadState = %d, want %d", got, state.Pending)
	}

	pc := a.Processor.PC
	a.Run()
	if got := a.Processor.PC; got != pc {
		t.Fatalf("Run retired instruction early after LoadState: PC=%04X want %04X", got, pc)
	}
}
