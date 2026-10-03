package ppu

import "testing"

// TestCGDATA_ActiveDisplayRedirectsToLatch verifies bsnes/ares
// semantics for $2122 writes during active display: instead of
// dropping the byte, the write is redirected to latch.cgramAddress
// (the most-recently-rendered palette index, kept by the renderer).
// The byte still lands — just at the corrupted address. CGRAMAddr
// and the word-pair toggle still advance unconditionally.
//
// References:
//   - bsnes/sfc/ppu/io.cpp:64-70 writeCGRAM
//   - bsnes/sfc/ppu-fast/io.cpp:69-75 writeCGRAM
//   - ares/ares/sfc/ppu/io.cpp:55-61 writeCGRAM
//   - bsnes/sfc/ppu/screen.cpp:148 latch.cgramAddress = palette
func TestCGDATA_ActiveDisplayRedirectsToLatch(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F // brightness on, force-blank off
	p.vCounter = 100 // inside visible region

	if !p.writesBlocked() {
		t.Fatalf("setup error: writesBlocked() should be true with INIDISP=0x0F and vCounter inside visible region; visibleLines=%d",
			p.visibleLines())
	}

	// Seed the latch to a known palette index — in production this
	// is updated by the renderer at every CGRAM read site.
	p.latchCGRAMAddr = 0x55

	// Pre-stamp CGRAM[0..3] (the requested CGADD destination) and
	// CGRAM[latch*2..+1] (the redirect destination) to detect
	// where the write landed.
	p.CGRAM[0] = 0xAA
	p.CGRAM[1] = 0xBB
	p.CGRAM[0x55*2] = 0xCC
	p.CGRAM[0x55*2+1] = 0xDD

	p.WriteRegister(0x2121, 0x00)
	p.WriteRegister(0x2122, 0x11) // first byte
	p.WriteRegister(0x2122, 0x22) // second byte

	// Requested CGADD destination must NOT have been written.
	if p.CGRAM[0] == 0x11 || p.CGRAM[1] == 0x22 {
		t.Errorf("active-display CGDATA should NOT land at requested CGADD: CGRAM[0..1]=%02X %02X",
			p.CGRAM[0], p.CGRAM[1])
	}
	// Redirect target latchCGRAMAddr*2 MUST hold the word.
	if p.CGRAM[0x55*2] != 0x11 || p.CGRAM[0x55*2+1] != 0x22&0x7F {
		t.Errorf("redirect target latchCGRAMAddr*2 should hold the word: CGRAM[0xAA..0xAB]=%02X %02X (want 11 22&7F)",
			p.CGRAM[0x55*2], p.CGRAM[0x55*2+1])
	}
	// CGRAMAddr advances unconditionally (matches bsnes).
	if p.CGRAMAddr != 1 {
		t.Errorf("CGRAMAddr should advance: got %d want 1", p.CGRAMAddr)
	}
	// Toggle returns to false after the pair.
	if p.CGRAMWritePair {
		t.Errorf("CGRAMWritePair toggle should be false after the pair")
	}
}

// TestCGDATA_ForceBlankStillWrites verifies the existing happy
// path: with force-blank set (INIDISP bit 7), CGDATA writes
// always land regardless of vCounter. This isolates the
// active-display behavior from the force-blank behavior so
// future fixes don't accidentally regress force-blank writes.
func TestCGDATA_ForceBlankStillWrites(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x80 // force-blank on
	p.vCounter = 100

	if p.writesBlocked() {
		t.Fatalf("setup error: writesBlocked() should be false when force-blank is on")
	}

	p.WriteRegister(0x2121, 0x10)
	p.WriteRegister(0x2122, 0x55)
	p.WriteRegister(0x2122, 0x66)

	if got := p.CGRAM[0x10*2]; got != 0x55 {
		t.Errorf("force-blank CGDATA: CGRAM[0x20]=%#02x, want 0x55", got)
	}
	if got := p.CGRAM[0x10*2+1]; got != 0x66&0x7F {
		t.Errorf("force-blank CGDATA: CGRAM[0x21]=%#02x, want 0x66 & 0x7F", got)
	}
}

// TestCGDATA_VBlankWrites verifies the second happy path:
// during VBlank (vCounter past visible region), writes always
// land.
func TestCGDATA_VBlankWrites(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F                  // not force-blank
	p.vCounter = p.visibleLines() + 1 // past visible region

	if p.writesBlocked() {
		t.Fatalf("setup error: writesBlocked() should be false during VBlank")
	}

	p.WriteRegister(0x2121, 0x20)
	p.WriteRegister(0x2122, 0x77)
	p.WriteRegister(0x2122, 0x88)

	if got := p.CGRAM[0x20*2]; got != 0x77 {
		t.Errorf("VBlank CGDATA: CGRAM[0x40]=%#02x, want 0x77", got)
	}
	if got := p.CGRAM[0x20*2+1]; got != 0x88&0x7F {
		t.Errorf("VBlank CGDATA: CGRAM[0x41]=%#02x, want 0x88 & 0x7F", got)
	}
}
