package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestCPU_XCE(t *testing.T) {
	b := bus.NewBus()
	c := NewCPU(b)

	// Reset state: E=1, P=...
	c.E = true
	c.P = 0x01 // Carry Set

	// XCE should swap C and E.
	// C=1, E=1 -> C=1, E=1? No.
	// Carry is 1. E becomes true.
	// Emulation was true. Carry becomes 1.

	// Let's clear Carry first to see change.
	c.P = 0x00
	// E=1.

	// XCE
	opXCE(c, AddrImpl)

	if c.E != false { // Carry was 0, so E should be false (Native)
		t.Errorf("Expected Native Mode (E=false), got E=%v", c.E)
	}
	if (c.P & 0x01) == 0 { // Emulation was 1, so Carry should be set
		t.Errorf("Expected Carry Set (from old E), got P=%02X", c.P)
	}

	// Swap back
	opXCE(c, AddrImpl)
	if c.E != true {
		t.Errorf("Expected Emulation Mode (E=true), got E=%v", c.E)
	}
}

func TestCPU_REP_SEP(t *testing.T) {
	b := bus.NewBus()
	c := NewCPU(b)

	// Switch to Native to test M/X behavior
	c.E = false
	c.P = 0x00 // Clear all

	// Set X/Y to 16-bit values
	c.X = 0x1234
	c.Y = 0x5678

	// SEP #$10 (Set X fit -> 8-bit Index)
	// We need to simulate fetching the immediate byte.
	// We'll write to memory at PC
	c.PC = 0x0000
	c.PB = 0x00
	b.Write(0x000000, 0x10) // Operand for SEP

	opSEP(c, AddrImm)

	if (c.P & 0x10) == 0 {
		t.Errorf("Expected X flag set (8-bit index), got P=%02X", c.P)
	}

	// Verify truncation
	if c.X != 0x0034 {
		t.Errorf("Expected X truncated to 0x0034, got 0x%04X", c.X)
	}
	if c.Y != 0x0078 {
		t.Errorf("Expected Y truncated to 0x0078, got 0x%04X", c.Y)
	}
}

func TestCPU_XBA(t *testing.T) {
	b := bus.NewBus()
	c := NewCPU(b)

	c.A = 0x1234
	c.P = 0x00

	opXBA(c, AddrImpl)

	if c.A != 0x3412 {
		t.Errorf("Expected A swapped to 0x3412, got 0x%04X", c.A)
	}

	// Check Flags (Computed on Low Byte 0x12)
	// 0x12 is non-zero, positive.
	if (c.P & 0x02) != 0 {
		t.Errorf("Z flag should be clear for 0x12")
	}
	if (c.P & 0x80) != 0 {
		t.Errorf("N flag should be clear for 0x12")
	}

	// Swap back
	opXBA(c, AddrImpl)
	if c.A != 0x1234 {
		t.Errorf("Expected A restored to 0x1234, got 0x%04X", c.A)
	}

	// Test with Zero/Negative result
	c.A = 0x0080       // High=00, Low=80
	opXBA(c, AddrImpl) // -> 8000
	// Low byte is 00. Z set, N clear.
	if (c.P & 0x02) == 0 {
		t.Errorf("Z flag should be set for Low Byte 0x00")
	}
}
