package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestTransferMixedWidthRules(t *testing.T) {
	c := NewCPU(bus.NewBus())
	c.E = false

	c.P = 0x10 // 16-bit A, 8-bit index.
	c.A = 0x1234
	c.X = 0xABCD
	opTAX(c, AddrImpl)
	if c.X != 0x0034 {
		t.Fatalf("TAX X = %04X, want 0034", c.X)
	}

	c.Y = 0xABCD
	opTAY(c, AddrImpl)
	if c.Y != 0x0034 {
		t.Fatalf("TAY Y = %04X, want 0034", c.Y)
	}

	c.P = 0x20 // 8-bit A, 16-bit index.
	c.A = 0x1200
	c.X = 0xABCD
	opTXA(c, AddrImpl)
	if c.A != 0x12CD {
		t.Fatalf("TXA A = %04X, want 12CD", c.A)
	}

	c.A = 0x1200
	c.Y = 0xABCD
	opTYA(c, AddrImpl)
	if c.A != 0x12CD {
		t.Fatalf("TYA A = %04X, want 12CD", c.A)
	}
}

func TestIndex8HighByteForcedZero(t *testing.T) {
	b := bus.NewBus()
	ram := bus.NewRAMDevice(0x10000)
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.E = false
	c.P = 0x10

	c.PC = 0x1000
	b.Write(0x001000, 0x80)
	c.X = 0xABCD
	opLDX(c, AddrImm)
	if c.X != 0x0080 {
		t.Fatalf("LDX X = %04X, want 0080", c.X)
	}

	c.X = 0x12FF
	opINX(c, AddrImpl)
	if c.X != 0x0000 {
		t.Fatalf("INX X = %04X, want 0000", c.X)
	}

	c.Y = 0x1200
	opDEY(c, AddrImpl)
	if c.Y != 0x00FF {
		t.Fatalf("DEY Y = %04X, want 00FF", c.Y)
	}

	c.S = 0x01FF
	c.pushByte(0x7F)
	c.X = 0xABCD
	opPLX(c, AddrImpl)
	if c.X != 0x007F {
		t.Fatalf("PLX X = %04X, want 007F", c.X)
	}
}
