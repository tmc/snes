package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestADCDecimal8(t *testing.T) {
	b := bus.NewBus()
	mem := &MockMemory{}
	b.Map(0x000000, 0x00FFFF, mem)

	c := NewCPU(b)
	c.E = false
	c.P = 0x28
	c.A = 0x905F
	c.PC = 0x8000

	mem.Write(0x8000, 0x69)
	mem.Write(0x8001, 0x2C)

	c.Step()

	if c.A != 0x9081 {
		t.Fatalf("A = %04X, want 9081", c.A)
	}
	if c.P != 0xE8 {
		t.Fatalf("P = %02X, want E8", c.P)
	}
}

func TestADCDecimal16(t *testing.T) {
	b := bus.NewBus()
	mem := &MockMemory{}
	b.Map(0x000000, 0x00FFFF, mem)

	c := NewCPU(b)
	c.E = false
	c.P = 0x08
	c.A = 0x9999
	c.PC = 0x8000

	mem.Write(0x8000, 0x69)
	mem.Write(0x8001, 0x01)
	mem.Write(0x8002, 0x00)

	c.Step()

	if c.A != 0x0000 {
		t.Fatalf("A = %04X, want 0000", c.A)
	}
	if c.P != 0x0B {
		t.Fatalf("P = %02X, want 0B", c.P)
	}
}
