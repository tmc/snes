package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

type MockMemory struct {
	Data [0x10000]uint8
}

func (m *MockMemory) Read(address uint32) uint8 {
	return m.Data[address&0xFFFF]
}

func (m *MockMemory) Write(address uint32, value uint8) {
	m.Data[address&0xFFFF] = value
}

func (m *MockMemory) BlockRead(address uint32, length int) []byte {
	return m.Data[address&0xFFFF : (address&0xFFFF)+uint32(length)]
}

func TestXCE(t *testing.T) {
	b := bus.NewBus()
	mem := &MockMemory{}
	b.Map(0x000000, 0x00FFFF, mem) // Map first 64KB

	c := NewCPU(b)
	c.PC = 0x8000
	c.PB = 0x00
	c.E = true // Start in Emulation
	c.P = 0x00 // Carry Clear

	// Test 1: Switch to Native (Exhange Carry=1 with Emulation=1 -> E=0)
	// SEC (Set Carry) -> Carry=1
	// XCE (Exchange) -> E becomes 1 (old C), C becomes 1 (old E). Wait.
	// Start: E=1, C=0.
	// Op: SEC (0x38). State: E=1, C=1.
	// Op: XCE (0xFB). Swap E, C. Result: E=1, C=1. NO CHANGE?
	// Wait. XCE swaps C and E bits.
	// If E=1, C=0. XCE -> E=0, C=1. (Native Mode entered)

	// Write program at 0x8000
	// CLC (0x18)
	// XCE (0xFB)
	mem.Write(0x8000, 0x18)
	mem.Write(0x8001, 0xFB)

	// Step 1: CLC
	c.Step()
	if (c.P & 0x01) != 0 {
		t.Fatal("CLC failed to clear carry")
	}

	// Step 2: XCE
	// Before: E=1, C=0
	// After:  E=0, C=1
	c.Step()

	if c.E {
		t.Error("XCE failed to switch to Native mode (E should be false)")
	}
	if (c.P & 0x01) == 0 {
		t.Error("XCE failed to swap E to C (C should be true)")
	}
}
