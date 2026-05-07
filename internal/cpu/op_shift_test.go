package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestShift16WritesHighThenLow(t *testing.T) {
	b := bus.NewBus()
	b.InitializeWaitStates()
	wram := bus.NewRAMDevice(0x2000)
	b.Map(0x000000, 0x001fff, wram)

	c := NewCPU(b)
	c.E = false
	c.P &^= 0x20
	c.PC = 0x1000
	wram.Write(0x1000, 0x46) // LSR $04
	wram.Write(0x1001, 0x04)
	wram.Write(0x0004, 0xf5)
	wram.Write(0x0005, 0x7c)

	var writes []struct {
		addr  uint32
		value uint8
		cycle uint64
	}
	b.WriteHook = func(addr uint32, value uint8) {
		if addr != 0x0004 && addr != 0x0005 {
			return
		}
		writes = append(writes, struct {
			addr  uint32
			value uint8
			cycle uint64
		}{addr: addr, value: value, cycle: c.Cycles})
	}

	c.Run()

	if len(writes) != 2 {
		t.Fatalf("writes = %v, want two writes", writes)
	}
	if writes[0].addr != 0x0005 || writes[0].value != 0x3e {
		t.Fatalf("first write = $%04X:%02X, want high byte $0005:3E", writes[0].addr, writes[0].value)
	}
	if writes[1].addr != 0x0004 || writes[1].value != 0x7a {
		t.Fatalf("second write = $%04X:%02X, want low byte $0004:7A", writes[1].addr, writes[1].value)
	}
	if writes[1].cycle != 54 {
		t.Fatalf("low-byte write cycle = %d, want 54", writes[1].cycle)
	}
}
