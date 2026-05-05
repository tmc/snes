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

func TestAPUStatePreservesPendingPortComparePatch(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x7E // CMP Y, dp
	a.RAM[0x0201] = 0xF4
	a.Processor.PC = 0x0200
	a.Processor.Y = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before state capture")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	state := a.SaveState()
	restored := NewAPU()
	restored.LoadState(state)
	restored.SetPortComparePatch(true)

	restored.WritePort(0, 0x11)
	if restored.Processor.Z || !restored.Processor.N || restored.Processor.C {
		t.Fatalf("patched cmp flags after LoadState Z=%v N=%v C=%v, want false true false",
			restored.Processor.Z, restored.Processor.N, restored.Processor.C)
	}
}
