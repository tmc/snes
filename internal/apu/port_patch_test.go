package apu

import "testing"

func TestWritePortPatchesPendingCMPY(t *testing.T) {
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
		t.Fatal("cmp instruction retired before port write")
	}
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0x11)
	if a.Processor.Z || !a.Processor.N || a.Processor.C {
		t.Fatalf("patched cmp flags Z=%v N=%v C=%v, want false true false", a.Processor.Z, a.Processor.N, a.Processor.C)
	}
}

func TestWritePortDoesNotPatchAfterNextInstructionStarts(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x7E // CMP Y, dp
	a.RAM[0x0201] = 0xF4
	a.RAM[0x0202] = 0x00 // NOP
	a.Processor.PC = 0x0200
	a.Processor.Y = 0x10
	a.InPorts[0] = 0x10
	a.SetPortComparePatch(true)

	a.Run()
	for a.pending != 0 {
		a.Run()
	}
	a.Run()

	a.WritePort(0, 0x11)
	if !a.Processor.Z || !a.Processor.C {
		t.Fatalf("cleared cmp flags Z=%v C=%v, want true true", a.Processor.Z, a.Processor.C)
	}
}

func TestWritePortPatchesPendingCMPDirectImmediate(t *testing.T) {
	a := NewAPU()
	a.Control = 0
	a.RAM[0x0200] = 0x78 // CMP dp, #imm
	a.RAM[0x0201] = 0xCC
	a.RAM[0x0202] = 0xF4
	a.Processor.PC = 0x0200
	a.SetPortComparePatch(true)

	a.Run()
	if a.pending == 0 {
		t.Fatal("cmp instruction retired before port write")
	}
	if a.Processor.Z || a.Processor.C {
		t.Fatalf("initial cmp flags Z=%v C=%v, want false false", a.Processor.Z, a.Processor.C)
	}

	a.WritePort(0, 0xCC)
	if !a.Processor.Z || !a.Processor.C || a.Processor.N {
		t.Fatalf("patched cmp flags Z=%v C=%v N=%v, want true true false", a.Processor.Z, a.Processor.C, a.Processor.N)
	}
}
