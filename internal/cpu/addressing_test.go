package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestAddressing_DirectPage(t *testing.T) {
	b := bus.NewBus()
	c := NewCPU(b)

	// 1. Test Native Mode Wrapping (Bank 0 only)
	c.E = false
	c.P = 0      // Clear all
	c.D = 0xFFFF // Direct Page at very end of bank

	// Mock instruction fetch: Operand offset = 2
	b.Write(0x000000, 0x02) // PC at 0
	c.PC = 0
	c.PB = 0

	addr, _ := c.getEffectiveAddress(AddrDir)

	// Expected: FFFF + 2 = 10001 -> Wrapped to 0001
	if addr != 0x000001 {
		t.Errorf("Native Mode DP wrapping failed. Expected 0x000001, got 0x%06X", addr)
	}

	// 2. Test Emulation Mode Wrapping (Page 0 only if DL=0)
	c.E = true
	c.D = 0x0000 // DL=0
	c.PC = 0
	b.Write(0x000000, 0xFF) // Offset FF

	addr, _ = c.getEffectiveAddress(AddrDir)
	// FF + D(0000) = 00FF. Correct.
	if addr != 0x00FF {
		t.Errorf("Emulation Mode simple DP failed. Expected 0x00FF, got 0x%06X", addr)
	}

	// Now try wrapping with pointers?
	// Indirect tests are harder to setup without proper helpers,
	// but let's test getDirectPageAddress helper directly slightly?
	// It's private. We rely on public implementation.
}

func TestAddressing_DirectPage_Penalty(t *testing.T) {
	b := bus.NewBus()
	c := NewCPU(b)
	c.Cycles = 0

	c.D = 0x0100 // DL = 00. Aligned. No penalty.
	c.E = false

	b.Write(0x000000, 0x10) // Offset
	c.getEffectiveAddress(AddrDir)

	// Fetch adds cycles? No, fetchByte adds usage.
	// We want to see the specific penalty from getEffectiveAddress.
	// fetchByte calls read -> calls Bus.GetWaitStates -> adds cycles.
	// Standard fetchByte adds ~8 cycles.
	// We just want to see if the penalty adds *extra* 1.
	// This is hard to isolate from integration.
	// But we can check c.Cycles before and after, subtracting known fetch costs.
	// Actually, let's just make DL non-zero.

	c.Cycles = 0
	c.D = 0x0101 // DL = 01. Not Aligned. Penalty +1.
	c.PC = 0
	b.Write(0x000000, 0x10)

	c.getEffectiveAddress(AddrDir)

	// We expect +1 cycle from logic.
	// Note: fetchByte happened (cycles += 8).
	// +1 penalty.
	// Total cycles should be 8 + 1 = 9 (assuming 8 cycle wait states).
	// Actually wait states depend on address. PC=0 -> Bank 0 -> SlowROM (8).
	if c.Cycles != 9 {
		t.Errorf("Expected 9 cycles (8 fetch + 1 penalty), got %d", c.Cycles)
	}
}

// SimpleRAM for testing
type SimpleRAM struct {
	data map[uint32]uint8
}

func NewSimpleRAM() *SimpleRAM                                   { return &SimpleRAM{data: make(map[uint32]uint8)} }
func (m *SimpleRAM) Read(address uint32) uint8                   { return m.data[address] }
func (m *SimpleRAM) Write(address uint32, value uint8)           { m.data[address] = value }
func (m *SimpleRAM) BlockRead(address uint32, length int) []byte { return nil }

func TestAddressing_Indirect_Wrapping(t *testing.T) {
	// Test the specialized readWordDirectPage usage implicitly via an instruction
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram) // Map Bank 0

	c := NewCPU(b)
	c.E = true
	c.D = 0x0000

	// (dp)
	// We want to read pointer from $00FF.
	// Should read low from $00FF, high from $0000 (Wrapped).
	b.Write(0x0000FF, 0x12) // Low
	b.Write(0x000000, 0x34) // High

	// Instruction operand = FF
	// Avoid conflict with ZP $0000 so put code at $0200
	c.PC = 0x0200
	c.PB = 0x00
	b.Write(0x000200, 0xFF) // Operand at PC

	addr, _ := c.getEffectiveAddress(AddrDirInd)

	// Pointer = 3412.
	expected := uint32(0x3412)
	if (addr & 0xFFFF) != expected {
		t.Errorf("Indirect Wrapping failed. Expected pointer 0x3412, got 0x%04X", addr&0xFFFF)
	}
}

func TestAddressing_AbsoluteYBankCarry(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.DB = 0x64
	c.Y = 0xE1F1
	c.PC = 0x0200

	ram.Write(0x0200, 0x68)
	ram.Write(0x0201, 0xF1)

	addr, _ := c.getEffectiveAddress(AddrAbsY)
	if addr != 0x65D359 {
		t.Fatalf("absolute,Y address = %06X, want 65D359", addr)
	}
}

func TestAddressing_IndirectYBankCarry(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.DB = 0x64
	c.D = 0
	c.Y = 0xE1F1
	c.PC = 0x0200

	ram.Write(0x0200, 0xD3)
	ram.Write(0x00D3, 0x68)
	ram.Write(0x00D4, 0xF1)

	addr, _ := c.getEffectiveAddress(AddrIndY)
	if addr != 0x65D359 {
		t.Fatalf("(dp),Y address = %06X, want 65D359", addr)
	}
}

func TestAddressing_StackIndirectYBankCarry(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.DB = 0x64
	c.S = 0x481F
	c.Y = 0xE1F1
	c.PC = 0x0200

	ram.Write(0x0200, 0xD3)
	ram.Write(0x48F2, 0x68)
	ram.Write(0x48F3, 0xF1)

	addr, _ := c.getEffectiveAddress(AddrSrIndY)
	if addr != 0x65D359 {
		t.Fatalf("(sr,S),Y address = %06X, want 65D359", addr)
	}
}
