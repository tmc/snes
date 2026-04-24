package input

import "testing"

// readReport drains `bits` bits out of d, MSB-first, and packs them into
// the low `bits` bits of the returned uint32.
func readReport(d Device, bits int) uint32 {
	var r uint32
	for i := 0; i < bits; i++ {
		r = (r << 1) | uint32(d.ReadSerial()&1)
	}
	return r
}

// TestMouseReport32BitSerial round-trips several live-state combinations
// through the 32-bit serial protocol against a bsnes-derived reference
// word. The reference words are the packing specified in
// sfc/controller/mouse/mouse.cpp read() (MSB-first, signature 0x1 at the
// top four bits, buttons next, then Y sign+mag, then X sign+mag).
func TestMouseReport32BitSerial(t *testing.T) {
	type want struct {
		name string
		pack uint32
	}

	cases := []struct {
		name        string
		buttons     [2]bool // {left, right}
		sensitivity uint8
		dx, dy      int32
		want        uint32
	}{
		{
			name: "idle",
			want: 0x10000000,
		},
		{
			name:    "left button",
			buttons: [2]bool{true, false},
			want:    0x10000000 | (1 << 22),
		},
		{
			name:    "right button",
			buttons: [2]bool{false, true},
			want:    0x10000000 | (1 << 23),
		},
		{
			name:        "sensitivity hi",
			sensitivity: MouseSensitivityHi,
			want:        0x10000000 | (uint32(MouseSensitivityHi) << 24),
		},
		{
			name: "dx=+1",
			dx:   1,
			want: 0x10000000 | 0x01,
		},
		{
			name: "dx=-1",
			dx:   -1,
			want: 0x10000000 | (1 << 7) | 0x01,
		},
		{
			name: "dy=+5",
			dy:   5,
			want: 0x10000000 | (5 << 8),
		},
		{
			name: "dy=-5",
			dy:   -5,
			want: 0x10000000 | (1 << 15) | (5 << 8),
		},
		{
			name:        "compound",
			buttons:     [2]bool{true, true},
			sensitivity: MouseSensitivityMid,
			dx:          -3,
			dy:          7,
			want: 0x10000000 |
				(uint32(MouseSensitivityMid) << 24) |
				(1 << 23) | (1 << 22) |
				(7 << 8) |
				(1 << 7) | 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMouse()
			m.SetButton(tc.buttons[0], tc.buttons[1])
			m.Sensitivity = tc.sensitivity
			m.SetDelta(tc.dx, tc.dy)

			m.Latch(true)
			m.Latch(false)
			got := readReport(m, 32)
			if got != tc.want {
				t.Fatalf("mouse report = %032b\n want = %032b", got, tc.want)
			}
		})
	}
	_ = want{}
}

// TestMouseSensitivityToggleCycles pins the Phase 8 sensitivity-toggle
// quirk: SenseToggle() advances sensitivity Lo -> Mid -> Hi -> Lo, and the
// cycle is observable in the serial report.
func TestMouseSensitivityToggleCycles(t *testing.T) {
	m := NewMouse()

	if m.Sensitivity != MouseSensitivityLo {
		t.Fatalf("initial sensitivity = %d, want Lo", m.Sensitivity)
	}

	steps := []struct {
		after uint8
	}{
		{MouseSensitivityMid},
		{MouseSensitivityHi},
		{MouseSensitivityLo},
	}

	for i, step := range steps {
		m.SenseToggle()
		if m.Sensitivity != step.after {
			t.Fatalf("step %d: sensitivity = %d, want %d", i, m.Sensitivity, step.after)
		}
		// Observe it in the report too: bits 25..24 of the 32-bit word.
		m.Latch(true)
		m.Latch(false)
		report := readReport(m, 32)
		got := uint8((report >> 24) & 0x3)
		if got != step.after {
			t.Fatalf("step %d: report sensitivity = %d, want %d", i, got, step.after)
		}
	}
}

// TestMouseDeltaResidualCarries pins that deltas larger than +/-127 split
// across multiple latches — the 7-bit magnitude limit drains the queue
// across frames rather than dropping motion.
func TestMouseDeltaResidualCarries(t *testing.T) {
	m := NewMouse()
	m.SetDelta(200, -200)

	m.Latch(true)
	m.Latch(false)
	r1 := readReport(m, 32)
	// bits 6-0 X magnitude should be 127; bit 7 X sign 0.
	if r1&0x7F != 127 || r1&0x80 != 0 {
		t.Fatalf("first X = %08b, want magnitude 127 sign 0", r1&0xFF)
	}
	// bits 15: Y sign = 1, bits 14-8: magnitude 127.
	if (r1>>8)&0x7F != 127 || (r1>>15)&1 != 1 {
		t.Fatalf("first Y = %08b, want magnitude 127 sign 1", (r1>>8)&0xFF)
	}

	m.Latch(true)
	m.Latch(false)
	r2 := readReport(m, 32)
	// Residual was 73 in both axes.
	if r2&0x7F != 73 || r2&0x80 != 0 {
		t.Fatalf("second X = %08b, want magnitude 73 sign 0", r2&0xFF)
	}
	if (r2>>8)&0x7F != 73 || (r2>>15)&1 != 1 {
		t.Fatalf("second Y = %08b, want magnitude 73 sign 1", (r2>>8)&0xFF)
	}

	// Third latch: queue drained.
	m.Latch(true)
	m.Latch(false)
	r3 := readReport(m, 32)
	if r3&0xFF != 0 || (r3>>8)&0xFF != 0 {
		t.Fatalf("third report X/Y = %02X/%02X, want 00/00", r3&0xFF, (r3>>8)&0xFF)
	}
}

// TestMouseReadPastEndReturns1 pins that reads after the 32-bit report is
// exhausted idle high, matching a disconnected/idle serial line.
func TestMouseReadPastEndReturns1(t *testing.T) {
	m := NewMouse()
	m.Latch(true)
	m.Latch(false)
	_ = readReport(m, 32)
	for i := 0; i < 4; i++ {
		if got := m.ReadSerial(); got != 1 {
			t.Fatalf("post-report read %d = %d, want 1", i, got)
		}
	}
}

// TestMouseSaveLoadRoundtrip pins that SaveState/LoadState are symmetric.
func TestMouseSaveLoadRoundtrip(t *testing.T) {
	m := NewMouse()
	m.SetButton(true, false)
	m.Sensitivity = MouseSensitivityHi
	m.SetDelta(42, -17)
	m.Latch(true)
	// Drain 5 bits so count != 0.
	for i := 0; i < 5; i++ {
		m.ReadSerial()
	}

	s := m.SaveState()
	n := NewMouse()
	n.LoadState(s)

	if n.SaveState() != s {
		t.Fatalf("round-trip mismatch:\n got = %#v\nwant = %#v", n.SaveState(), s)
	}
}
