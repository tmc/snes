package ppu

import "testing"

// TestNMIFlagRaisesAtVBlank pins the V-blank entry semantics used by the
// CPU-side NMI path: crossing from the last visible scanline into line
// (visible+1) with hCounter==0 sets $4210 bit 7 (modeled as p.NMIFlag).
//
// The scheduler separately gates the actual CPU TriggerNMI on NMITIMEN bit 7;
// that gate is tested in the scheduler package. Here we only pin the PPU
// state machine so the scheduler has a dependable edge to hook.
func TestNMIFlagRaisesAtVBlank(t *testing.T) {
	p := NewPPU()
	// 224-line mode (SETINI bit 2 = 0).
	p.SETINI = 0
	// Advance to (vCounter=225, hCounter=0) — the V-blank entry point.
	// Each call to Run() advances one dot; 341 dots per scanline.
	runUntil := func(vc, hc int) {
		for !(p.vCounter == vc && p.hCounter == hc) {
			p.Run()
			if p.FrameCount > 0 {
				t.Fatalf("overran the frame before reaching v=%d h=%d", vc, hc)
			}
		}
	}

	// First, walk up to just before V-blank — line 224 is the last visible.
	// NMIFlag must still be clear.
	runUntil(224, 0)
	if p.NMIFlag {
		t.Fatalf("NMIFlag set before V-blank entry")
	}

	// Cross into line 225 — NMIFlag should rise.
	runUntil(225, 0)
	if !p.NMIFlag {
		t.Fatalf("NMIFlag not raised on entry to line %d h=0", 225)
	}
}

func TestPPUScanlineLengthMatchesScheduler(t *testing.T) {
	p := NewPPU()

	for i := 0; i < 341; i++ {
		p.Run()
	}
	if p.vCounter != 1 || p.hCounter != 0 {
		t.Fatalf("after one scanline: v=%d h=%d, want v=1 h=0", p.vCounter, p.hCounter)
	}

	for i := 0; i < 341*261-1; i++ {
		p.Run()
	}
	if p.FrameCount != 0 {
		t.Fatalf("frame wrapped early at v=%d h=%d", p.vCounter, p.hCounter)
	}
	p.Run()
	if p.FrameCount != 1 || p.vCounter != 0 || p.hCounter != 0 {
		t.Fatalf("after one frame: frame=%d v=%d h=%d, want frame=1 v=0 h=0",
			p.FrameCount, p.vCounter, p.hCounter)
	}
}

func TestPPUPALScanlineLengthMatchesScheduler(t *testing.T) {
	p := NewPPU()
	p.SetPAL(true)

	for i := 0; i < 341*palVPeriod-1; i++ {
		p.Run()
	}
	if p.FrameCount != 0 {
		t.Fatalf("PAL frame wrapped early at v=%d h=%d", p.vCounter, p.hCounter)
	}
	p.Run()
	if p.FrameCount != 1 || p.vCounter != 0 || p.hCounter != 0 {
		t.Fatalf("after one PAL frame: frame=%d v=%d h=%d, want frame=1 v=0 h=0",
			p.FrameCount, p.vCounter, p.hCounter)
	}
}

func TestPPUCounterOddFieldShortScanline(t *testing.T) {
	p := NewPPU()
	p.ppuField = true
	p.vCounter = ntscShortScanline - 1
	p.hCounter = 340

	p.Run()
	if p.vCounter != ntscShortScanline || p.hCounter != 0 {
		t.Fatalf("entry to short scanline = V:%d H:%d, want V:%d H:0",
			p.vCounter, p.hCounter, ntscShortScanline)
	}
	if p.hPeriod != ntscShortHPeriod {
		t.Fatalf("short scanline hperiod = %d, want %d", p.hPeriod, ntscShortHPeriod)
	}

	for i := 0; i < 339; i++ {
		p.Run()
	}
	if p.vCounter != ntscShortScanline || p.hCounter != 339 {
		t.Fatalf("before short scanline end = V:%d H:%d, want V:%d H:339",
			p.vCounter, p.hCounter, ntscShortScanline)
	}
	p.Run()
	if p.vCounter != ntscShortScanline+1 || p.hCounter != 0 {
		t.Fatalf("after short scanline = V:%d H:%d, want V:%d H:0",
			p.vCounter, p.hCounter, ntscShortScanline+1)
	}
	if p.hPeriod != ntscHPeriod {
		t.Fatalf("post-short hperiod = %d, want %d", p.hPeriod, ntscHPeriod)
	}
}

func TestPPUPALCounterHasNoNTSCShortScanline(t *testing.T) {
	p := NewPPU()
	p.SetPAL(true)
	p.ppuField = true
	p.vCounter = ntscShortScanline - 1
	p.hCounter = 340

	p.Run()
	if p.vCounter != ntscShortScanline || p.hCounter != 0 {
		t.Fatalf("entry to PAL scanline = V:%d H:%d, want V:%d H:0",
			p.vCounter, p.hCounter, ntscShortScanline)
	}
	if p.hPeriod != ntscHPeriod {
		t.Fatalf("PAL scanline hperiod = %d, want %d", p.hPeriod, ntscHPeriod)
	}
}

func TestPPUPALInterlaceLongScanline(t *testing.T) {
	p := NewPPU()
	p.SetPAL(true)
	p.ppuInterlace = true
	p.ppuField = true
	p.vCounter = palVPeriod - 2
	p.hCounter = 340

	p.Run()
	if p.vCounter != palVPeriod-1 || p.hCounter != 0 {
		t.Fatalf("entry to PAL interlace long scanline = V:%d H:%d, want V:%d H:0",
			p.vCounter, p.hCounter, palVPeriod-1)
	}
	if p.hPeriod != ntscHPeriod+4 {
		t.Fatalf("PAL interlace long scanline hperiod = %d, want %d", p.hPeriod, ntscHPeriod+4)
	}
}

func TestPPUCounterCapturesInterlaceForLongField(t *testing.T) {
	p := NewPPU()
	p.SETINI = 0x01
	p.vCounter = 127
	p.hCounter = 340

	p.Run()
	if !p.ppuInterlace {
		t.Fatalf("interlace latch not captured at V=128")
	}
	if p.vPeriod != ntscVPeriod+1 {
		t.Fatalf("interlace even-field vperiod = %d, want %d", p.vPeriod, ntscVPeriod+1)
	}
}

func TestPPUPowerResetsCounterTiming(t *testing.T) {
	p := NewPPU()
	p.cycles = 1234
	p.FrameCount = 7
	p.hCounter = 87
	p.vCounter = ntscShortScanline
	p.ppuField = true
	p.ppuInterlace = true
	p.vPeriod = ntscVPeriod + 1
	p.hPeriod = ntscShortHPeriod
	p.NMIFlag = true
	p.RangeOver = true
	p.TimeOver = true

	p.Power(true)
	if p.cycles != 0 || p.FrameCount != 0 || p.hCounter != 0 || p.vCounter != 0 {
		t.Fatalf("counter after power = cycles:%d frame:%d H:%d V:%d, want zeros",
			p.cycles, p.FrameCount, p.hCounter, p.vCounter)
	}
	if p.ppuField || p.ppuInterlace {
		t.Fatalf("field/interlace after power = %v/%v, want false/false", p.ppuField, p.ppuInterlace)
	}
	if p.vPeriod != ntscVPeriod || p.hPeriod != ntscHPeriod {
		t.Fatalf("periods after power = V:%d H:%d, want V:%d H:%d",
			p.vPeriod, p.hPeriod, ntscVPeriod, ntscHPeriod)
	}
	if p.NMIFlag || p.RangeOver || p.TimeOver {
		t.Fatalf("flags after power = NMI:%v range:%v time:%v, want clear",
			p.NMIFlag, p.RangeOver, p.TimeOver)
	}
}

// TestReadRDNMIClearsFlag pins that reading $4210 clears bit 7 in place,
// and that the version-nibble low bits are preserved on subsequent reads.
func TestReadRDNMIClearsFlag(t *testing.T) {
	p := NewPPU()
	p.NMIFlag = true

	first := p.ReadRDNMI()
	if first&0x80 == 0 {
		t.Fatalf("first read did not observe NMIFlag set (got %02X)", first)
	}

	second := p.ReadRDNMI()
	if second&0x80 != 0 {
		t.Fatalf("NMIFlag not cleared after read (got %02X)", second)
	}

	// Version nibble (bit 1 set for CPU version 2) must still appear on the
	// follow-up read so software that polls RDNMI can still identify the
	// 5A22 revision.
	if second&0x02 == 0 {
		t.Fatalf("version nibble lost after NMI read-to-clear (got %02X)", second)
	}
}

func TestReadRDNMIDoesNotClearDuringNMIHold(t *testing.T) {
	p := NewPPU()
	p.SETINI = 0

	for !(p.vCounter == p.visibleLines()+1 && p.hCounter == 0) {
		p.Run()
		if p.FrameCount > 0 {
			t.Fatalf("overran frame before V-blank entry")
		}
	}

	if got := p.ReadRDNMI(); got&0x80 == 0 {
		t.Fatalf("RDNMI at NMI hold = %02X, want bit 7 set", got)
	}
	if !p.NMIFlag {
		t.Fatalf("NMIFlag cleared during hold")
	}

	p.Run()
	if got := p.ReadRDNMI(); got&0x80 == 0 {
		t.Fatalf("RDNMI after hold = %02X, want bit 7 set before clear", got)
	}
	if p.NMIFlag {
		t.Fatalf("NMIFlag still set after post-hold RDNMI read")
	}
}

func TestReadHVBJOYAutoJoypadBusyWindow(t *testing.T) {
	p := NewPPU()
	p.AutoJoypad = true
	p.SETINI = 0
	p.vCounter = p.visibleLines() + 1

	p.hCounter = 31
	if got := p.ReadHVBJOY(); got&0x01 != 0 {
		t.Fatalf("HVBJOY auto-joy before start = %02X, want bit0 clear", got)
	}

	p.hCounter = 32
	if got := p.ReadHVBJOY(); got&0x01 == 0 {
		t.Fatalf("HVBJOY auto-joy during first vblank line = %02X, want bit0 set", got)
	}

	p.vCounter = p.visibleLines() + 2
	p.hCounter = 0
	if got := p.ReadHVBJOY(); got&0x01 != 0 {
		t.Fatalf("HVBJOY auto-joy after busy window = %02X, want bit0 clear", got)
	}
}

func TestReadHVBJOYAutoJoypadDisabled(t *testing.T) {
	p := NewPPU()
	p.vCounter = p.visibleLines() + 1
	p.hCounter = 32

	if got := p.ReadHVBJOY(); got&0x01 != 0 {
		t.Fatalf("HVBJOY auto-joy disabled = %02X, want bit0 clear", got)
	}
}

func TestReadHVBJOYHBlankBitBoundary(t *testing.T) {
	p := NewPPU()
	p.vCounter = 12

	p.hCounter = 273
	if got := p.ReadHVBJOY(); got&0x40 != 0 {
		t.Fatalf("HVBJOY before HBlank = %02X, want bit6 clear", got)
	}

	p.hCounter = 0
	if got := p.ReadHVBJOY(); got&0x40 == 0 {
		t.Fatalf("HVBJOY at scanline start HBlank = %02X, want bit6 set", got)
	}

	p.hCounter = 1
	if got := p.ReadHVBJOY(); got&0x40 == 0 {
		t.Fatalf("HVBJOY at early scanline HBlank = %02X, want bit6 set", got)
	}

	p.hCounter = 2
	if got := p.ReadHVBJOY(); got&0x40 != 0 {
		t.Fatalf("HVBJOY after early scanline HBlank = %02X, want bit6 clear", got)
	}

	p.hCounter = 274
	if got := p.ReadHVBJOY(); got&0x40 != 0 {
		t.Fatalf("HVBJOY before late HBlank = %02X, want bit6 clear", got)
	}

	p.hCounter = 275
	if got := p.ReadHVBJOY(); got&0x40 == 0 {
		t.Fatalf("HVBJOY at HBlank = %02X, want bit6 set", got)
	}
}

// TestNMIFlagClearsAtFrameStart pins the start-of-frame reset so a game
// that forgets to read $4210 in the previous V-blank does not observe a
// stale flag on the next frame boundary.
func TestNMIFlagClearsAtFrameStart(t *testing.T) {
	p := NewPPU()
	p.SETINI = 0

	// Drive one full frame — NMIFlag should be set during V-blank, then
	// cleared when vCounter wraps back to 0.
	for p.FrameCount == 0 {
		p.Run()
	}
	if p.NMIFlag {
		t.Fatalf("NMIFlag still set after frame wrap (vCounter=%d)", p.vCounter)
	}
}
