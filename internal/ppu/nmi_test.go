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
	// Each call to Run() advances one dot; 340 dots per scanline.
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
