package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

type TestMemory struct {
	data [65536]uint8
}

func (m *TestMemory) Read(addr uint32) uint8                   { return m.data[addr&0xFFFF] }
func (m *TestMemory) Write(addr uint32, val uint8)             { m.data[addr&0xFFFF] = val }
func (m *TestMemory) BlockRead(addr uint32, length int) []byte { return nil }

func TestCMPAbs_PCIncrement(t *testing.T) {
	// Setup
	b := bus.NewBus()
	mem := &TestMemory{}
	// Map Bank 00
	b.Map(0x000000, 0x00FFFF, mem)

	c := NewCPU(b)
	c.Power(true)

	// Write code at 0x8000
	// CD 40 21: CMP $2140
	b.Write(0x8000, 0xCD)
	b.Write(0x8001, 0x40)
	b.Write(0x8002, 0x21)

	// Setup CPU
	c.PC = 0x8000
	c.PB = 0x00
	c.P = 0x30 // m=1, x=1
	c.E = false

	// Step
	c.Step()

	// Verify constants
	t.Logf("Opcode CD Mode: %d (Expected AddrAbs: %d)", Opcodes[0xCD].Mode, AddrAbs)
	t.Logf("Opcode CD Size: %d", Opcodes[0xCD].Size)

	// Verify PC
	if c.PC != 0x8003 {
		t.Errorf("PC misaligned after CMP Abs. Expected 0x8003, got 0x%04X", c.PC)
	}
}
