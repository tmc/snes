package parity

import (
	"testing"

	"github.com/tmc/snes"
)

func TestDMACompletionObserverWaitsForDestinationWrites(t *testing.T) {
	rom := makeIdleLoROM()
	rom[0] = 0xea
	s := snes.NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	s.Power()
	s.PPU.WriteRegister(0x2100, 0x80)
	s.PPU.WriteRegister(0x2115, 0x80)
	c := &s.DMA.Channels[0]
	c.Control = 1
	c.Target = 0x18
	c.SrcBank = 0x7e
	c.SrcAddr = 0x100
	c.Size = 2
	s.Bus.Write(0x7e0100, 0xaa)
	s.Bus.Write(0x7e0101, 0xbb)
	pending := false
	writes, completed := 0, 0
	s.Bus.WriteHook = func(addr uint32, v uint8) {
		if addr == 0x420b {
			pending = true
		}
		if addr == 0x2118 || addr == 0x2119 {
			writes++
			if !pending {
				t.Error("observer completed before destination writes")
			}
		}
	}
	restore := observeDMACompletion(s, func() {
		if !pending {
			return
		}
		completed++
		if writes != 2 || s.PPU.VRAM[0] != 0xaa || s.PPU.VRAM[1] != 0xbb {
			t.Fatalf("premature completion writes=%d VRAM=%x", writes, s.PPU.VRAM[:2])
		}
		pending = false
	})
	defer restore()
	s.Bus.Write(0x420b, 1)
	s.CPU.Run()
	if pending || completed != 1 {
		t.Fatalf("pending=%v completed=%d", pending, completed)
	}
}

func TestDMAPreviewDoesNotReadMMIOOrChangeMDR(t *testing.T) {
	rom := makeIdleLoROM()
	s := snes.NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	s.Power()
	s.Bus.Write(0x7e0100, 0x37)
	s.Bus.MDR = 0x91
	reads := 0
	s.Bus.ReadHook = func(uint32, uint8) { reads++ }
	for _, tt := range []struct {
		addr uint32
		want uint8
		ok   bool
	}{{0x7e0100, 0x37, true}, {0x008000, 0x80, true}, {0x00213f, 0, false}} {
		got, ok := dmaPreviewByte(s, rom, tt.addr)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("preview %06x=%02x/%v", tt.addr, got, ok)
		}
	}
	if reads != 0 || s.Bus.MDR != 0x91 {
		t.Fatalf("observer changed bus: reads=%d MDR=%02x", reads, s.Bus.MDR)
	}
}
