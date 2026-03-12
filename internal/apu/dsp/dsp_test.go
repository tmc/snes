package dsp

import "testing"

func TestDSP_Write_Volume(t *testing.T) {
	d := New()

	// Voice 0 Left Volume ($00)
	d.Write(0x00, 0x7F)
	if d.Voices[0].VOLL != 127 {
		t.Errorf("expected Voice 0 VOLL 127, got %d", d.Voices[0].VOLL)
	}

	// Voice 1 Right Volume ($11) -> 0x10 + 0x01
	d.Write(0x11, 0x80) // -128
	if d.Voices[1].VOLR != -128 {
		t.Errorf("expected Voice 1 VOLR -128, got %d", d.Voices[1].VOLR)
	}
}

func TestDSP_Sample_Mixing(t *testing.T) {
	d := New()

	// Set Master Volume to max
	d.Write(0x0C, 0x7F) // MVOLL
	d.Write(0x1C, 0x7F) // MVOLR
	d.Write(0x00, 0x7F) // Voice 0 left
	d.Write(0x01, 0x7F) // Voice 0 right
	d.Write(0x02, 0x01) // Pitch low
	d.Write(0x03, 0x00) // Pitch high
	d.Write(0x4C, 0x01) // KON voice 0

	l, r := d.Sample()
	if l == 0 && r == 0 {
		t.Errorf("expected non-zero sample after key-on, got %d,%d", l, r)
	}
}

func TestDSP_Sample_MuteAndKeyOff(t *testing.T) {
	d := New()
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x10)
	d.Write(0x4C, 0x01)

	_, _ = d.Sample()
	d.Write(0x6C, 0x40) // Mute
	l, r := d.Sample()
	if l != 0 || r != 0 {
		t.Fatalf("mute sample = %d,%d, want 0,0", l, r)
	}

	d.Write(0x6C, 0x00)
	d.Write(0x5C, 0x01) // KOFF voice 0
	// Drain envelope quickly
	for i := 0; i < 200; i++ {
		l, r = d.Sample()
	}
	if l != 0 || r != 0 {
		t.Fatalf("keyoff tail sample = %d,%d, want 0,0", l, r)
	}
}
