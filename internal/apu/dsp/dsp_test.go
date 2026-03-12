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
	d.Write(0x07, 0x7F) // Direct gain
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
	d.Write(0x07, 0x7F)
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

func TestVoice_GainDirectEnvelope(t *testing.T) {
	var v Voice
	v.GAIN = 0x40
	v.KeyOn(nil, 0)
	v.stepEnvelope()
	if v.envelope != 0x400 {
		t.Fatalf("envelope = %03X, want 400", v.envelope)
	}
	if v.ENVX != 0x40 {
		t.Fatalf("ENVX = %02X, want 40", v.ENVX)
	}
}

func TestVoice_ADSRAttackAndRelease(t *testing.T) {
	var v Voice
	v.ADSR1 = 0x8F // ADSR enable, fast attack
	v.ADSR2 = 0xE1 // high sustain level
	v.KeyOn(nil, 0)
	for i := 0; i < 64; i++ {
		v.stepEnvelope()
	}
	if v.envelope == 0 {
		t.Fatalf("envelope remained zero during ADSR attack")
	}
	if v.envMode != envDecay && v.envMode != envSustain {
		t.Fatalf("envMode = %v, want decay or sustain", v.envMode)
	}

	v.KeyOff()
	for i := 0; i < 256; i++ {
		v.stepEnvelope()
	}
	if v.envelope != 0 {
		t.Fatalf("release envelope = %03X, want 000", v.envelope)
	}
}

func TestVoice_BRRDecodeBlock(t *testing.T) {
	ram := make([]uint8, 65536)
	// DIR base 0x2000, SRCN 1 -> entry at 0x2004
	ram[0x2004] = 0x00
	ram[0x2005] = 0x30 // start 0x3000
	// BRR header shift=0 filter=0 loop=0 end=0
	ram[0x3000] = 0x00
	// data nibbles 0..15
	for i := 0; i < 8; i++ {
		ram[0x3001+uint16(i)] = uint8((i*2)<<4) | uint8(i*2+1)
	}

	read := func(addr uint16) uint8 { return ram[addr] }
	var v Voice
	v.SRCN = 1
	v.GAIN = 0x7F
	v.KeyOn(read, 0x20)
	v.decodeBRRBlock(read)

	if v.brrDecoded[0] != 0 || v.brrDecoded[1] != 0 || v.brrDecoded[15] != -1 {
		t.Fatalf("decoded samples unexpected: first=%d second=%d last=%d", v.brrDecoded[0], v.brrDecoded[1], v.brrDecoded[15])
	}
}

func TestDSP_Sample_UsesBRRSourceWhenReaderPresent(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	// directory for SRCN 0 at DIR=0x20 -> 0x2000
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30 // 0x3000
	ram[0x3000] = 0xC0 // shift=12, filter=0
	for i := 0; i < 8; i++ {
		ram[0x3001+uint16(i)] = 0x77 // strong positive nibble pattern
	}
	d.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })

	d.Write(0x5D, 0x20) // DIR
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x20)
	d.Write(0x03, 0x00)
	d.Write(0x04, 0x00) // SRCN
	d.Write(0x07, 0x7F) // direct gain
	d.Write(0x4C, 0x01)

	l, r := d.Sample()
	if l <= 0 || r <= 0 {
		t.Fatalf("expected positive BRR-based output, got %d,%d", l, r)
	}
}
