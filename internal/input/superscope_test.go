package input

import "testing"

// fakeBeam records invocation counts and returns fixed coordinates.
type fakeBeam struct {
	calls int
	h, v  uint16
}

func (f *fakeBeam) LatchBeam() (uint16, uint16) {
	f.calls++
	return f.h, f.v
}

// TestSuperScopeReport32BitSerialIdle pins the idle report: no buttons, no
// beam latch taken (so off-screen flag is set).
func TestSuperScopeReport32BitSerialIdle(t *testing.T) {
	s := NewSuperScope(nil)
	s.Latch(true)
	s.Latch(false)
	got := readReport(s, 32)
	// frame=1, noise = frame & 0x4 != 0 -> 0 on frame 1.
	want := uint32(1 << 27) // off-screen only
	if got != want {
		t.Fatalf("idle report = %032b\n          want = %032b", got, want)
	}
}

// TestSuperScopeReport32BitSerialButtons exercises each button bit.
func TestSuperScopeReport32BitSerialButtons(t *testing.T) {
	cases := []struct {
		name                     string
		trig, curs, turbo, pause bool
		mask                     uint32
	}{
		{"trigger", true, false, false, false, 1 << 31},
		{"cursor", false, true, false, false, 1 << 30},
		{"turbo", false, false, true, false, 1 << 29},
		{"pause", false, false, false, true, 1 << 28},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use a fake latcher so a trigger pull doesn't force
			// off-screen on the non-trigger subtests.
			beam := &fakeBeam{h: 100, v: 50}
			s := NewSuperScope(beam)
			s.SetButtons(tc.trig, tc.curs, tc.turbo, tc.pause)
			s.Latch(true)
			s.Latch(false)
			got := readReport(s, 32)
			if got&tc.mask == 0 {
				t.Fatalf("%s bit not set: %032b", tc.name, got)
			}
		})
	}
}

// TestSuperScopeTriggerLatchesBeam pins the Super Scope latch flow: a
// trigger pull samples the PPU beam via the latcher, and the captured
// (h, v) appears in the report. This is the stand-in test for the $213F
// latch flow; the Conductor TODO in the agent handoff wires the real PPU
// into this interface.
func TestSuperScopeTriggerLatchesBeam(t *testing.T) {
	beam := &fakeBeam{h: 200, v: 150}
	s := NewSuperScope(beam)

	// No trigger yet: no latch, off-screen flag set.
	s.Latch(true)
	s.Latch(false)
	if beam.calls != 0 {
		t.Fatalf("latcher called %d times before trigger; want 0", beam.calls)
	}
	r := readReport(s, 32)
	if r&(1<<27) == 0 {
		t.Fatalf("expected off-screen flag set on idle report, got %032b", r)
	}

	// Pull the trigger: latcher samples at next rising edge.
	s.SetButtons(true, false, false, false)
	s.Latch(true)
	if beam.calls != 1 {
		t.Fatalf("latcher calls after trigger pull = %d, want 1", beam.calls)
	}
	s.Latch(false)
	r = readReport(s, 32)

	// Expect: trigger bit set, off-screen cleared (beam on-screen),
	// H=200 in bits 25..16, V=150 in bits 15..0.
	if r&(1<<31) == 0 {
		t.Fatalf("trigger bit not set: %032b", r)
	}
	if r&(1<<27) != 0 {
		t.Fatalf("off-screen bit set despite on-screen beam: %032b", r)
	}
	gotH := (r >> 16) & 0x3FF
	gotV := r & 0x1FF
	if gotH != 200 {
		t.Fatalf("latched H = %d, want 200", gotH)
	}
	if gotV != 150 {
		t.Fatalf("latched V = %d, want 150", gotV)
	}
}

// TestSuperScopeTriggerEdgeOnly pins that only the rising edge of the
// trigger triggers a beam latch — holding the trigger down across frames
// does not continually re-latch.
func TestSuperScopeTriggerEdgeOnly(t *testing.T) {
	beam := &fakeBeam{h: 1, v: 1}
	s := NewSuperScope(beam)
	s.SetButtons(true, false, false, false)

	// Three consecutive "trigger held" latch strobes.
	for range 3 {
		s.Latch(true)
		s.Latch(false)
	}
	if beam.calls != 1 {
		t.Fatalf("held trigger caused %d latcher calls; want 1", beam.calls)
	}

	// Release, then pull again — edge should re-latch.
	s.SetButtons(false, false, false, false)
	s.Latch(true)
	s.Latch(false)
	s.SetButtons(true, false, false, false)
	s.Latch(true)
	s.Latch(false)
	if beam.calls != 2 {
		t.Fatalf("edge re-pull caused %d total latcher calls; want 2", beam.calls)
	}
}

// TestSuperScopeOffScreenBeam pins the off-screen flag when the PPU
// reports a beam position outside the active display area (H >= 256 or
// V outside 1..224).
func TestSuperScopeOffScreenBeam(t *testing.T) {
	cases := []struct {
		name string
		h, v uint16
	}{
		{"H out of range", 300, 100},
		{"V below range", 50, 0},
		{"V above range", 100, 225},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beam := &fakeBeam{h: tc.h, v: tc.v}
			s := NewSuperScope(beam)
			s.SetButtons(true, false, false, false)
			s.Latch(true)
			s.Latch(false)
			r := readReport(s, 32)
			if r&(1<<27) == 0 {
				t.Fatalf("off-screen flag not set for %s: %032b", tc.name, r)
			}
		})
	}
}

// TestSuperScopeNoiseBitCycles pins the bsnes noise-bit quirk: the bit
// toggles on a fixed cadence of the frame counter rather than being
// zero.
func TestSuperScopeNoiseBitCycles(t *testing.T) {
	s := NewSuperScope(nil)
	seen := [2]bool{}
	for range 16 {
		s.Latch(true)
		s.Latch(false)
		r := readReport(s, 32)
		bit := (r >> 26) & 1
		seen[bit] = true
	}
	if !seen[0] || !seen[1] {
		t.Fatalf("noise bit did not observe both 0 and 1 across 16 latches: %v", seen)
	}
}

// TestSuperScopeSaveLoadRoundtrip pins that SaveState/LoadState are
// symmetric.
func TestSuperScopeSaveLoadRoundtrip(t *testing.T) {
	beam := &fakeBeam{h: 123, v: 45}
	s := NewSuperScope(beam)
	s.SetButtons(true, true, false, true)
	s.Latch(true)
	for range 7 {
		s.ReadSerial()
	}

	snap := s.SaveState()
	t2 := NewSuperScope(beam)
	t2.LoadState(snap)

	if t2.SaveState() != snap {
		t.Fatalf("round-trip mismatch:\n got = %#v\nwant = %#v", t2.SaveState(), snap)
	}
}
