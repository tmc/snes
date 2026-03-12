package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestJMPIndirectWrap(t *testing.T) {
	b := bus.NewBus()
	mem := newSparseRAM()
	b.Map(0x000000, 0xFFFFFF, mem)

	c := NewCPU(b)
	c.E = true
	c.PB = 0x3C
	c.PC = 0x527D

	mem.Write(0x3C527D, 0x6C)
	mem.Write(0x3C527E, 0xFF)
	mem.Write(0x3C527F, 0xFF)
	mem.Write(0xFFFF, 0x55)
	mem.Write(0x0000, 0x58)

	c.Step()

	if c.PC != 0x5855 {
		t.Fatalf("PC = %04X, want 5855", c.PC)
	}
	if c.PB != 0x3C {
		t.Fatalf("PB = %02X, want 3C", c.PB)
	}
}
