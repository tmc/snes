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

	// Voice Rendering is currently stubbed to 0,0
	// So Sample should return 0,0
	l, r := d.Sample()
	if l != 0 || r != 0 {
		t.Errorf("expected 0,0 (stub), got %d,%d", l, r)
	}
}
