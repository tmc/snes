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

func TestJSL_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := newSparseRAM()
	b.Map(0x000000, 0xFFFFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.PB = 0xDD
	c.PC = 0x0200
	c.S = 0x0100

	ram.Write(0xDD0200, 0x22)
	ram.Write(0xDD0201, 0x77)
	ram.Write(0xDD0202, 0xA3)
	ram.Write(0xDD0203, 0xE4)

	c.Step()

	if got := ram.Read(0x0100); got != 0xDD {
		t.Fatalf("stack bank byte = %02X, want DD", got)
	}
	if got := ram.Read(0x00FF); got != 0x02 {
		t.Fatalf("stack return high = %02X, want 02", got)
	}
	if got := ram.Read(0x00FE); got != 0x03 {
		t.Fatalf("stack return low = %02X, want 03", got)
	}
	if c.S != 0x01FD {
		t.Fatalf("stack pointer = %04X, want 01FD", c.S)
	}
	if c.PC != 0xA377 || c.PB != 0xE4 {
		t.Fatalf("target = %02X:%04X, want E4:A377", c.PB, c.PC)
	}
}

func TestRTL_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := newSparseRAM()
	b.Map(0x000000, 0xFFFFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.PB = 0x91
	c.PC = 0x0200
	c.S = 0x01FF

	ram.Write(0x910200, 0x6B)
	ram.Write(0x0200, 0x3A)
	ram.Write(0x0201, 0xFE)
	ram.Write(0x0202, 0xD0)

	c.Step()

	if c.PC != 0xFE3B || c.PB != 0xD0 {
		t.Fatalf("return target = %02X:%04X, want D0:FE3B", c.PB, c.PC)
	}
	if c.S != 0x0102 {
		t.Fatalf("stack pointer = %04X, want 0102", c.S)
	}
}

func TestPER_EmulationWrap(t *testing.T) {
	b := bus.NewBus()
	ram := newSparseRAM()
	b.Map(0x000000, 0xFFFFFF, ram)

	c := NewCPU(b)
	c.E = true
	c.PB = 0x20
	c.PC = 0xAE8E
	c.S = 0x1D00

	ram.Write(0x20AE8E, 0x62)
	ram.Write(0x20AE8F, 0x2A)
	ram.Write(0x20AE90, 0x00)

	c.Step()

	if got := ram.Read(0x0100); got != 0xAE {
		t.Fatalf("RAM[0100] = %02X, want AE", got)
	}
	if got := ram.Read(0x00FF); got != 0xBB {
		t.Fatalf("RAM[00FF] = %02X, want BB", got)
	}
	if c.S != 0x01FE {
		t.Fatalf("stack pointer = %04X, want 01FE", c.S)
	}
}
