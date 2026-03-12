package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestCPU_StackWrapping(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)
	c := NewCPU(b)

	// Emulation Mode
	c.E = true
	c.S = 0x0100 // Bottom of stack

	c.pushByte(0xAA)

	// Should wrap to 0x01FF
	if c.S != 0x01FF {
		t.Errorf("Stack should wrap to 0x01FF, got 0x%04X", c.S)
	}
	if ram.Read(0x0100) != 0xAA {
		t.Errorf("Written byte failed")
	}
}

func TestCPU_Interrupt_Native(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM() // Use SimpleRAM from addressing_test.go (we need to unify or redefined)
	// Since tests in same package share code if compiled together.
	// addressing_test.go defines SimpleRAM in package cpu. So it's available.
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.E = false
	c.PC = 0x1234
	c.PB = 0x05
	c.P = 0x00
	c.S = 0x01FF

	// Set Vector
	// NMI at FFEA
	b.Write(0x00FFEA, 0xCD)
	b.Write(0x00FFEB, 0xAB) // Target ABCD

	c.Interrupt(VectorNativeNMI)

	// 1. Verify PC Jump
	if c.PC != 0xABCD {
		t.Errorf("PC Jump failed. Got %04X, expected ABCD", c.PC)
	}

	// 2. Verify PB cleared
	if c.PB != 0x00 {
		t.Errorf("PB should be 00. Got %02X", c.PB)
	}

	// 3. Verify Stack Frame
	// Native: PBR, PCH, PCL, P
	// S started at 01FF. Pushed 1 byte (PB), 2 bytes (PC), 1 byte (P) -> 4 bytes.
	// End S = 01FB
	if c.S != 0x01FB {
		t.Errorf("Stack Pointer mismatch. Got %04X, expected 01FB", c.S)
	}

	// Read Stack
	// 01FF: PB (05)
	if ram.Read(0x01FF) != 0x05 {
		t.Errorf("Stack PB mismatch")
	}
	// 01FE: PCH (12)
	if ram.Read(0x01FE) != 0x12 {
		t.Errorf("Stack PCH mismatch")
	}
	// 01FD: PCL (34)
	if ram.Read(0x01FD) != 0x34 {
		t.Errorf("Stack PCL mismatch")
	}
	// 01FC: P (00)
	if ram.Read(0x01FC) != 0x00 {
		t.Errorf("Stack P mismatch")
	}
}

func TestCPU_Interrupt_Emulation(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.PC = 0x1234
	c.P = 0x00
	c.S = 0x01FF

	// Emulation Stack Frame: PCH, PCL, P (No PB)
	// Vector FFFA (NMI)
	b.Write(0x00FFFA, 0x99)
	b.Write(0x00FFFB, 0x88) // 8899

	c.Interrupt(VectorEmulationNMI)

	if c.PC != 0x8899 {
		t.Errorf("PC Jump failed")
	}

	if c.S != 0x01FC { // -3 bytes
		t.Errorf("Stack Pointer mismatch (E). Got %04X, expected 01FC", c.S)
	}

	// 01FF: PCH (12)
	if ram.Read(0x01FF) != 0x12 {
		t.Errorf("Stack PCH mismatch")
	}
}

func TestNMILeavesDirectPageUntouched(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.D = 0x1234
	c.P = 0x08 // Decimal set
	c.S = 0x01FF
	b.Write(0x00FFFA, 0x00)
	b.Write(0x00FFFB, 0x80)

	c.doNMI()

	if c.D != 0x1234 {
		t.Fatalf("D modified by NMI: got %04X want 1234", c.D)
	}
	if (c.P & 0x08) != 0 {
		t.Fatalf("decimal flag should be cleared by NMI")
	}
}
