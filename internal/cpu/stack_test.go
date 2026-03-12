package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestPHD_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.S = 0x0100
	c.D = 0xC825

	opPHD(c, AddrImpl)

	if got := ram.Read(0x0100); got != 0xC8 {
		t.Fatalf("stack high byte = %02X, want C8", got)
	}
	if got := ram.Read(0x00FF); got != 0x25 {
		t.Fatalf("stack low byte = %02X, want 25", got)
	}
	if c.S != 0x01FE {
		t.Fatalf("stack pointer = %04X, want 01FE", c.S)
	}
}

func TestPLD_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	ram.Write(0x01FF, 0x25)
	ram.Write(0x0200, 0xC8)

	c := NewCPU(b)
	c.E = true
	c.S = 0x01FE

	opPLD(c, AddrImpl)

	if c.D != 0xC825 {
		t.Fatalf("D = %04X, want C825", c.D)
	}
	if c.S != 0x0100 {
		t.Fatalf("stack pointer = %04X, want 0100", c.S)
	}
}

func TestPLB_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := NewSimpleRAM()
	b.Map(0x000000, 0x00FFFF, ram)

	ram.Write(0x0200, 0x7F)

	c := NewCPU(b)
	c.E = true
	c.S = 0x01FF

	opPLB(c, AddrImpl)

	if c.DB != 0x7F {
		t.Fatalf("DB = %02X, want 7F", c.DB)
	}
	if c.S != 0x0100 {
		t.Fatalf("stack pointer = %04X, want 0100", c.S)
	}
}
