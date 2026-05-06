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
	if got := restored.ReadRegister(0x213C); got != 0xE3 {
		t.Fatalf("OPHCT after LoadState = %02X, want open-bus bits 1-7 plus high bit 1", got)
	}
	if got := restored.ReadRegister(0x213D); got != 0xE2 {
		t.Fatalf("OPVCT after LoadState = %02X, want open-bus bits 1-7 plus high bit 0", got)
	}
	if !restored.hvLatched {
		t.Fatalf("H/V latch flag not preserved")
	}
}

func TestPPUStatePreservesCounterTiming(t *testing.T) {
	p := NewPPU()
	p.FrameCount = 12
	p.hCounter = 87
	p.vCounter = ntscShortScanline
	p.ppuField = true
	p.ppuInterlace = false
	p.vPeriod = ntscVPeriod
	p.hPeriod = ntscShortHPeriod

	state := p.SaveState()
	restored := NewPPU()
	restored.LoadState(state)

	if restored.FrameCount != p.FrameCount || restored.hCounter != p.hCounter || restored.vCounter != p.vCounter {
		t.Fatalf("counter position after LoadState = frame:%d H:%d V:%d, want frame:%d H:%d V:%d",
			restored.FrameCount, restored.hCounter, restored.vCounter, p.FrameCount, p.hCounter, p.vCounter)
	}
	if restored.ppuField != p.ppuField || restored.ppuInterlace != p.ppuInterlace {
		t.Fatalf("field/interlace after LoadState = %v/%v, want %v/%v",
			restored.ppuField, restored.ppuInterlace, p.ppuField, p.ppuInterlace)
	}
	if restored.vPeriod != p.vPeriod || restored.hPeriod != p.hPeriod {
		t.Fatalf("periods after LoadState = V:%d H:%d, want V:%d H:%d",
			restored.vPeriod, restored.hPeriod, p.vPeriod, p.hPeriod)
	}
}

func TestPPUStatePreservesHiResFrameBuffer(t *testing.T) {
	p := NewPPU()
	p.BGMode = 5
	p.Height = 1
	p.hiresFrontBuffer[0] = 0x1234
	p.hiresFrontBuffer[1] = 0x5678

	state := p.SaveState()
	restored := NewPPU()
	restored.LoadState(state)

	got := restored.AppendFrameBGR555Size(nil, 512, 1)
	want := []byte{0x34, 0x12, 0x78, 0x56}
	if string(got[:4]) != string(want) {
		t.Fatalf("restored hi-res frame bytes = % X, want % X", got[:4], want)
	}
}
