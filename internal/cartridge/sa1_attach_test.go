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
	d := c.coprocessor.(*sa1.Device)
	d.SignalCPUIRQ(0x0d)

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
	if got := restored.Read(0x00_2300); got != 0x8d {
		t.Fatalf("restored SA-1 $2300 = %02X, want 8D", got)
	}
	restored.Write(0x00_2202, 0x80)
	if got := restored.Read(0x00_2300); got != 0x0d {
		t.Fatalf("cleared SA-1 $2300 = %02X, want 0D", got)
	}
}

func TestSA1BWRAMCPUWindows(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	if c.RAMSize != 256*1024 {
		t.Fatalf("RAMSize=%d, want 262144", c.RAMSize)
	}

	b := bus.NewBus()
	c.MapToBus(b)
	b.Write(0x00_2224, 0x02)
	b.Write(0x00_6000, 0xa5)
	if got := c.RAM[0x4000]; got != 0xa5 {
		t.Fatalf("BMAPS RAM[4000]=%02X, want A5", got)
	}
	if got := b.Read(0x80_6000); got != 0xa5 {
		t.Fatalf("mirrored BMAPS read=%02X, want A5", got)
	}

	b.Write(0x40_1234, 0x5a)
	if got := c.RAM[0x1234]; got != 0x5a {
		t.Fatalf("linear BW-RAM RAM[1234]=%02X, want 5A", got)
	}
	if got := b.Read(0x40_1234); got != 0x5a {
		t.Fatalf("linear BW-RAM read=%02X, want 5A", got)
	}
}

func TestSA1BWRAMWriteProtection(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2228, 0x02)
	b.Write(0x40_03ff, 0xa5)
	if got := b.Read(0x40_03ff); got != 0x00 {
		t.Fatalf("protected BW-RAM write read=%02X, want 00", got)
	}
	b.Write(0x40_0400, 0x5a)
	if got := b.Read(0x40_0400); got != 0x5a {
		t.Fatalf("unprotected BW-RAM write read=%02X, want 5A", got)
	}
	b.Write(0x00_2226, 0x80)
	b.Write(0x40_03ff, 0xc3)
	if got := b.Read(0x40_03ff); got != 0xc3 {
		t.Fatalf("SWEN protected BW-RAM write read=%02X, want C3", got)
	}
}

func TestSA1SuperMMCROMWindow(t *testing.T) {
	rom := makeROM(0x400000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[0x001234] = 0x11
	rom[0x101234] = 0x22
	rom[0x201234] = 0x33
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	if got := b.Read(0xc0_1234); got != 0x11 {
		t.Fatalf("SA-1 C bank read=%02X, want 11", got)
	}
	if got := b.Read(0xd0_1234); got != 0x22 {
		t.Fatalf("SA-1 D bank read=%02X, want 22", got)
	}

	b.Write(0x00_2220, 0x82)
	if got := b.Read(0xc0_1234); got != 0x33 {
		t.Fatalf("SA-1 remapped C bank read=%02X, want 33", got)
	}
}
