package parity

import (
	"testing"

	"github.com/tmc/snes/internal/input"
	"github.com/tmc/snes/internal/ppu"
)

// TestPPUSatisfiesBeamLatcher pins that *ppu.PPU implements the
// input.BeamLatcher contract with no adapter. The Super Scope holds a
// BeamLatcher reference directly, so this compile-time check is the
// Conductor-installed wire-up between the two packages.
func TestPPUSatisfiesBeamLatcher(t *testing.T) {
	var _ input.BeamLatcher = (*ppu.PPU)(nil)
}

// TestPPUBeamLatchReflectsCounters runs the PPU forward by a known number
// of dots and confirms LatchBeam() returns the expected (h, v) pair.
// Each ppu.Run() advances hCounter by 1; at 340 dots it wraps and bumps
// vCounter.
func TestPPUBeamLatchReflectsCounters(t *testing.T) {
	p := ppu.NewPPU()

	// Advance 500 dots: h=500%340=160, v=500/340=1.
	for range 500 {
		p.Run()
	}
	gotH, gotV := p.LatchBeam()
	if gotH != 160 || gotV != 1 {
		t.Fatalf("LatchBeam() = (%d, %d), want (160, 1)", gotH, gotV)
	}
}

// TestSuperScopeLatchesRealPPU is the end-to-end acceptance test for
// Phase 8: wire a real PPU into the Super Scope, advance it to a known
// dot/line, pull the trigger, and assert the scope's report carries the
// captured (h, v). This is the Phase 8 Verifiable Acceptance Criterion
// "Super Scope latch test that pins the PPU-beam-capture flow" landing
// against the real PPU rather than a fake.
func TestSuperScopeLatchesRealPPU(t *testing.T) {
	p := ppu.NewPPU()
	scope := input.NewSuperScope(p)

	// Advance PPU into the visible window: dot 120 of line 90.
	for range 340*90 + 120 {
		p.Run()
	}
	gotH, gotV := p.LatchBeam()
	if gotH != 120 || gotV != 90 {
		t.Fatalf("PPU pre-latch at (%d, %d), want (120, 90)", gotH, gotV)
	}

	scope.SetButtons(true, false, false, false)
	scope.Latch(true)
	scope.Latch(false)

	var report uint32
	for range 32 {
		report = (report << 1) | uint32(scope.ReadSerial()&1)
	}

	// Trigger bit (31) set; off-screen cleared (beam at 120, 90 — in-window);
	// H in bits 25..16; V in bits 15..0.
	if report&(1<<31) == 0 {
		t.Fatalf("trigger bit not set: %032b", report)
	}
	if report&(1<<27) != 0 {
		t.Fatalf("off-screen set despite on-screen beam at (120,90): %032b", report)
	}
	if h := (report >> 16) & 0x3FF; h != 120 {
		t.Fatalf("latched H = %d, want 120", h)
	}
	if v := report & 0x1FF; v != 90 {
		t.Fatalf("latched V = %d, want 90", v)
	}
}
