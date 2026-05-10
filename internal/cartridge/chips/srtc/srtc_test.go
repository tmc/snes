package srtc

import (
	"testing"
	"time"
)

// fixedClock returns the same instant on every call. Used to keep
// SyncTime deterministic in tests.
func fixedClock(t time.Time) Clock {
	return func() time.Time { return t }
}

// fillCells issues the 12-write sequence to populate BCD cells with
// a representative time (12:34:56 on day 7 month 8 year 1995).
func fillCells(t *testing.T, d *Device) {
	t.Helper()
	if d.Write(0x002801, 0x0E) != true {
		t.Fatalf("enter Command rejected")
	}
	if d.Write(0x002801, 0x00) != true {
		t.Fatalf("enter Write rejected")
	}
	for _, b := range []uint8{6, 5, 4, 3, 2, 1, 7, 0, 8, 5, 9, 9} {
		// second=56, minute=34, hour=12, day=07, month=8, year=995.
		if !d.Write(0x002801, b) {
			t.Fatalf("BCD write rejected")
		}
	}
}

func TestProtocolFillThenRead(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	fillCells(t, d)
	// Enter Read mode and drain 14 nibbles (1 leading 15 + 13 cells).
	d.Write(0x002801, 0x0D)
	got := []uint8{}
	for i := 0; i < 14; i++ {
		v, ok := d.Read(0x002800)
		if !ok {
			t.Fatalf("Read[%d] not handled", i)
		}
		got = append(got, v)
	}
	// Leading sentinel + cells 0..12; cell 12 (weekday) was auto-
	// calculated for 0995-08-07, which calculateWeekday computes.
	want := []uint8{15, 6, 5, 4, 3, 2, 1, 7, 0, 8, 5, 9, 9, calculateWeekdayNibble(995, 8, 7)}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("read[%d] = %d, want %d (full=%v)", i, got[i], v, got)
		}
	}
}

func calculateWeekdayNibble(y, m, day uint) uint8 {
	return uint8(calculateWeekday(1000+y, m, day))
}

func TestResetCommandZerosCells(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	fillCells(t, d)
	d.Write(0x002801, 0x0E) // Command
	d.Write(0x002801, 0x04) // Reset
	if d.second != 0 || d.minute != 0 || d.hour != 0 || d.day != 0 || d.month != 0 || d.year != 0 || d.weekday != 0 {
		t.Errorf("reset: state = %+v, want all zero", *d)
	}
	if d.state != StateReady {
		t.Errorf("after reset state = %d, want Ready (%d)", d.state, StateReady)
	}
}

func TestReadAddressGate(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	if _, ok := d.Read(0x002800); !ok {
		t.Errorf("$002800 read not handled")
	}
	if _, ok := d.Read(0x802800); !ok {
		t.Errorf("$802800 mirror not handled")
	}
	if _, ok := d.Read(0x002801); ok {
		t.Errorf("$2801 (write port) handled as read")
	}
	if _, ok := d.Read(0xC02800); ok {
		t.Errorf("bank C0 (out of range) handled")
	}
}

func TestWriteAddressGate(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	if !d.Write(0x002801, 0x0F) {
		t.Errorf("$002801 write not handled")
	}
	if !d.Write(0x802801, 0x0F) {
		t.Errorf("$802801 mirror not handled")
	}
	if d.Write(0x002800, 0x0F) {
		t.Errorf("$2800 (read port) handled as write")
	}
}

func TestTickSecondCascadesToYear(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	d.second = 59
	d.minute = 59
	d.hour = 23
	d.day = 31
	d.month = 12
	d.year = 999
	d.TickSecond()
	if d.second != 0 || d.minute != 0 || d.hour != 0 || d.day != 1 || d.month != 1 || d.year != 1000 {
		t.Errorf("year-end cascade = sec=%d min=%d hr=%d day=%d mo=%d yr=%d, want all-zero with day=1 mo=1 yr=1000",
			d.second, d.minute, d.hour, d.day, d.month, d.year)
	}
}

func TestLeapYearFebHas29Days(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	d.day = 28
	d.month = 2
	d.year = 1004 // 1004 % 4 == 0, %100 != 0 → leap year
	d.tickDay()
	if d.day != 29 || d.month != 2 {
		t.Errorf("Feb 28 → 29 in leap year: day=%d month=%d, want 29/2", d.day, d.month)
	}
	d.tickDay()
	if d.day != 1 || d.month != 3 {
		t.Errorf("Feb 29 → Mar 1: day=%d month=%d, want 1/3", d.day, d.month)
	}
}

func TestNonLeapCenturyFebHas28Days(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	d.day = 28
	d.month = 2
	d.year = 1100 // %100 == 0 and %400 != 0 → NOT leap
	d.tickDay()
	if d.day != 1 || d.month != 3 {
		t.Errorf("Feb 28 → Mar 1 in non-leap century: day=%d month=%d, want 1/3", d.day, d.month)
	}
}

func TestCalculateWeekdayKnownDates(t *testing.T) {
	cases := []struct {
		y, m, d uint
		want    uint
	}{
		{1000, 1, 1, 3}, // bsnes epoch: Wednesday
		{2000, 1, 1, 6}, // 2000-01-01 = Saturday
		{2024, 1, 1, 1}, // Monday
	}
	for _, tc := range cases {
		got := calculateWeekday(tc.y, tc.m, tc.d)
		if got != tc.want {
			t.Errorf("weekday(%d, %d, %d) = %d, want %d", tc.y, tc.m, tc.d, got, tc.want)
		}
	}
}

func TestSyncTimeFromInjectedClock(t *testing.T) {
	// 1995-08-07 12:34:56 UTC.
	d := New(fixedClock(time.Date(1995, 8, 7, 12, 34, 56, 0, time.UTC)))
	d.SyncTime()
	if d.second != 56 || d.minute != 34 || d.hour != 12 || d.day != 7 || d.month != 8 || d.year != 995 {
		t.Errorf("sync = sec=%d min=%d hr=%d day=%d mo=%d yr=%d, want 56/34/12/7/8/995",
			d.second, d.minute, d.hour, d.day, d.month, d.year)
	}
}

func TestSerializeUnserializeRoundTrip(t *testing.T) {
	d := New(fixedClock(time.Unix(0, 0)))
	fillCells(t, d)
	d.state = StateRead
	d.index = 5
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	d2 := New(fixedClock(time.Unix(0, 0)))
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("unserialize: %v", err)
	}
	if d2.second != d.second || d2.year != d.year || d2.state != d.state || d2.index != d.index {
		t.Errorf("round-trip mismatch: got %+v want %+v", *d2, *d)
	}
}
