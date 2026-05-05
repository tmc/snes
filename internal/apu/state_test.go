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

func TestAPUStatePreservesPendingOutputPortWrite(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x0200
	a.Processor.A = 0xCC
	a.RAM[0x0200] = 0xC4 // MOV dp, A
	a.RAM[0x0201] = 0xF4

	a.Run()
	if got := a.ReadPort(0); got != 0 {
		t.Fatalf("port visible before state capture = %02X, want 00", got)
	}
	if a.pendingOutPortMask == 0 {
		t.Fatalf("test setup did not queue an output port write")
	}

	state := a.SaveState()
	restored := NewAPU()
	restored.LoadState(state)
	for restored.pending != 0 {
		restored.Run()
	}
	if got := restored.ReadPort(0); got != 0xCC {
		t.Fatalf("restored port after instruction boundary = %02X, want CC", got)
	}
}
