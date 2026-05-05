package cartridge

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge/chips/sa1"
)

func TestSA1DetectsAndMapsRegisterWindow(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	if c.CoprocessorID != "sa1" {
		t.Fatalf("CoprocessorID = %q, want sa1", c.CoprocessorID)
	}
	if _, ok := c.coprocessor.(*sa1.Device); !ok {
		t.Fatalf("coprocessor type = %T, want *sa1.Device", c.coprocessor)
	}

	b := bus.NewBus()
	c.MapToBus(b)
	b.Write(0x00_2200, 0x80)
	if got := b.Read(0x80_2200); got != 0x80 {
		t.Fatalf("SA-1 mirrored control read = %02X, want 80", got)
	}
	if got := b.Read(0x40_2200); got == 0x80 {
		t.Fatalf("SA-1 control register leaked into bank 40")
	}
}

func TestSA1StateRoundTrip(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x35
	c := New(rom)
	c.Write(0x00_2200, 0x80)
	c.Write(0x00_2209, 0x20)

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(rom)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if restored.CoprocessorID != "sa1" {
		t.Fatalf("restored CoprocessorID = %q, want sa1", restored.CoprocessorID)
	}
	if got := restored.Read(0x00_2200); got != 0x80 {
		t.Fatalf("restored SA-1 $2200 = %02X, want 80", got)
	}
	if got := restored.Read(0x00_2209); got != 0x20 {
		t.Fatalf("restored SA-1 $2209 = %02X, want 20", got)
	}
}
