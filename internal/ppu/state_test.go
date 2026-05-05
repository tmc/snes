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
