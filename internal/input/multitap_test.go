package input

import "testing"

// newTestStandard returns a StandardController with a button word already
// staged. The returned controller will emit that word as its 16-bit report
// once latched.
func newTestStandard(buttons uint16) *StandardController {
	c := NewStandardController()
	c.SetState(buttons)
	return c
}

// TestMultitapRoutesBySelectLine pins the Phase 8 "Select line toggles
// which pair is currently reporting" behavior. With select=false, reads
// drain slots 0+1; with select=true, reads drain slots 2+3.
func TestMultitapRoutesBySelectLine(t *testing.T) {
	c0 := newTestStandard(ButtonB)
	c1 := newTestStandard(ButtonY)
	c2 := newTestStandard(ButtonA)
	c3 := newTestStandard(ButtonX)

	m := NewMultitap()
	m.Connect(0, c0)
	m.Connect(1, c1)
	m.Connect(2, c2)
	m.Connect(3, c3)

	// Select low: slotA reads c0.
	m.SetSelect(false)
	m.Latch(true)
	m.Latch(false)
	a, b := m.ReadSerialPair()
	if a != 1 {
		t.Fatalf("select=false, slotA first bit = %d, want 1 (B pressed on c0)", a)
	}
	if b != 0 {
		t.Fatalf("select=false, slotB first bit = %d, want 0 (Y pressed on c1 next bit)", b)
	}
	// c1's B bit is 0 since we set Y; but first bit out of c1 is B bit (bit 15) = 0.
	// The above comment about "Y pressed next bit" is wrong — correct:
	// First bit of c1's shift is B bit = 0 (c1 has Y set, not B). So b=0 ✓.

	// Select high: slotA reads c2.
	c2.Latch(true)
	c2.Latch(false)
	c3.Latch(true)
	c3.Latch(false)
	m.SetSelect(true)
	m.Latch(true)
	m.Latch(false)
	a2, b2 := m.ReadSerialPair()
	// c2 has A pressed (bit 7). First bit (B = bit 15) = 0.
	// c3 has X pressed (bit 6). First bit (B = bit 15) = 0.
	if a2 != 0 || b2 != 0 {
		t.Fatalf("select=true first-bit pair = (%d,%d), want (0,0)", a2, b2)
	}
}

// TestMultitapEachSlotFullStandardReport walks all 16 bits of each
// sub-controller's report via the multitap and verifies byte-for-byte
// equivalence with a reference drain.
func TestMultitapEachSlotFullStandardReport(t *testing.T) {
	cases := []struct {
		slot    int
		select_ bool
		pair    int // 0 for slotA, 1 for slotB
	}{
		{0, false, 0},
		{1, false, 1},
		{2, true, 0},
		{3, true, 1},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			// Distinct pattern per slot.
			pattern := uint16(0xABCD) ^ uint16(tc.slot)<<4
			// Mask to the bits StandardController exposes (top 12 only).
			pattern &= 0xFFF0

			// Reference: directly latch a StandardController and
			// drain 16 bits.
			ref := newTestStandard(pattern)
			ref.Latch(true)
			ref.Latch(false)
			refBits := make([]uint8, 16)
			for i := range refBits {
				refBits[i] = ref.ReadSerial()
			}

			// Through multitap.
			mc := newTestStandard(pattern)
			m := NewMultitap()
			m.Connect(tc.slot, mc)
			m.SetSelect(tc.select_)
			m.Latch(true)
			m.Latch(false)
			gotBits := make([]uint8, 16)
			for i := range gotBits {
				a, b := m.ReadSerialPair()
				if tc.pair == 0 {
					gotBits[i] = a
				} else {
					gotBits[i] = b
				}
			}

			for i := range refBits {
				if refBits[i] != gotBits[i] {
					t.Fatalf("slot %d select=%v pair=%d bit %d: got %d, want %d",
						tc.slot, tc.select_, tc.pair, i, gotBits[i], refBits[i])
				}
			}
		})
	}
}

// TestMultitapUnpluggedSlotReadsOne pins that a nil sub-controller slot
// reports as idle high (1), which is how real hardware signals an
// unplugged port on a multitap — this is the information a game uses
// to count plugged controllers (the "Multitap ID bits" quirk).
func TestMultitapUnpluggedSlotReadsOne(t *testing.T) {
	m := NewMultitap()
	// Only slot 0 connected.
	m.Connect(0, newTestStandard(0))
	m.SetSelect(false)
	m.Latch(true)
	m.Latch(false)
	for range 16 {
		_, b := m.ReadSerialPair()
		if b != 1 {
			t.Fatalf("empty slot 1 read = %d, want 1", b)
		}
	}
	// Select high: both slots empty.
	m.SetSelect(true)
	m.Latch(true)
	m.Latch(false)
	for range 16 {
		a, b := m.ReadSerialPair()
		if a != 1 || b != 1 {
			t.Fatalf("empty slots 2/3 read = (%d,%d), want (1,1)", a, b)
		}
	}
}

// TestMultitapLatchPropagates pins that the multitap forwards latch to all
// sub-controllers, not just the currently selected pair — games often
// latch once per frame even though they multiplex across pairs mid-frame.
func TestMultitapLatchPropagates(t *testing.T) {
	c0 := newTestStandard(ButtonB)
	c1 := newTestStandard(ButtonY)
	c2 := newTestStandard(ButtonStart)
	c3 := newTestStandard(ButtonA)

	m := NewMultitap()
	m.Connect(0, c0)
	m.Connect(1, c1)
	m.Connect(2, c2)
	m.Connect(3, c3)

	m.SetSelect(false)
	m.Latch(true)
	m.Latch(false)

	// All four sub-controllers should have shifted from their live state;
	// verify by first-bit drain directly against each sub-controller.
	if bit := c0.ReadSerial(); bit != 1 {
		t.Fatalf("c0 first bit = %d, want 1 (B latched)", bit)
	}
	if bit := c1.ReadSerial(); bit != 0 {
		t.Fatalf("c1 first bit = %d, want 0", bit)
	}
	if bit := c2.ReadSerial(); bit != 0 {
		t.Fatalf("c2 first bit = %d, want 0", bit)
	}
	if bit := c3.ReadSerial(); bit != 0 {
		t.Fatalf("c3 first bit = %d, want 0", bit)
	}
}

// TestMultitapSaveLoad pins that the multitap's own state serializes.
func TestMultitapSaveLoad(t *testing.T) {
	m := NewMultitap()
	m.SetSelect(true)
	m.Latch(true)
	s := m.SaveState()
	n := NewMultitap()
	n.LoadState(s)
	if n.SaveState() != s {
		t.Fatalf("round-trip = %#v, want %#v", n.SaveState(), s)
	}
}
