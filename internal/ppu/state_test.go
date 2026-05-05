package ppu

import "testing"

func TestPPUStatePreservesMode7Registers(t *testing.T) {
	p := NewPPU()
	p.M7SEL = 0xC3
	p.M7A = 0x0102
	p.M7B = 0x0304
	p.M7C = 0x0506
	p.M7D = 0x0708
	p.M7X = 0x1009
	p.M7Y = 0x100A
	p.M7HOFS = 0x100B
	p.M7VOFS = 0x100C
	p.M7Latch = 0x5A
	p.M7Large = true
	p.M7Fill = true
	p.M7XFlip = true
	p.M7YFlip = true

	state := p.SaveState()
	restored := NewPPU()
	restored.LoadState(state)

	if restored.PPURegisters != p.PPURegisters {
		t.Fatalf("Mode 7 registers after LoadState = %#v, want %#v",
			restored.PPURegisters, p.PPURegisters)
	}
}

func TestPPUStatePreservesReadLatches(t *testing.T) {
	p := NewPPU()
	p.PPU1OpenBus = 0x9A
	p.PPU2OpenBus = 0xE3
	p.latchedH = 0x0123
	p.latchedV = 0x00C0
	p.hReadHigh = true
	p.vReadHigh = true
	p.hvLatched = true

	state := p.SaveState()
	restored := NewPPU()
	restored.LoadState(state)

	if restored.PPU1OpenBus != 0x9A || restored.PPU2OpenBus != 0xE3 {
		t.Fatalf("open bus after LoadState = PPU1:%02X PPU2:%02X, want 9A/E3",
			restored.PPU1OpenBus, restored.PPU2OpenBus)
	}
	if got := restored.ReadRegister(0x213C); got != 0x01 {
		t.Fatalf("OPHCT after LoadState = %02X, want preserved high byte 01", got)
	}
	if got := restored.ReadRegister(0x213D); got != 0x00 {
		t.Fatalf("OPVCT after LoadState = %02X, want preserved high byte 00", got)
	}
	if !restored.hvLatched {
		t.Fatalf("H/V latch flag not preserved")
	}
}
