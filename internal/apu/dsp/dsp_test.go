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
	d.Write(0x6C, 0x00) // clear FLG (mute/echo-disable/reset all off)

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
	d.Write(0x6C, 0x00) // clear FLG so the first Sample is unmuted
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
	for i := 0; i < 1024; i++ {
		l, r = d.Sample()
	}
	if l != 0 || r != 0 {
		t.Fatalf("keyoff tail sample = %d,%d, want 0,0", l, r)
	}
}

func TestDSP_Sample_MuteClocksEnvelopeAndEcho(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	writes := 0
	d.SetRAMWriter(func(addr uint16, val uint8) {
		writes++
		ram[addr] = val
	})

	d.Write(0x6C, 0x00)
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x10)
	d.Write(0x07, 0x7F)
	d.Write(0x4D, 0x01)
	d.Write(0x6D, 0x20)
	d.Write(0x7D, 0x01)
	d.Write(0x4C, 0x01)
	d.Sample()

	d.Write(0x5C, 0x01)
	d.Write(0x6C, 0x40) // mute only; echo writeback remains enabled
	envBefore := d.Voices[0].envelope
	echoBefore := d.echoIndex
	writesBefore := writes
	l, r := d.Sample()
	if l != 0 || r != 0 {
		t.Fatalf("mute sample = %d,%d, want 0,0", l, r)
	}
	if got := d.Voices[0].envelope; got >= envBefore {
		t.Fatalf("mute stopped envelope release: before=%03X after=%03X", envBefore, got)
	}
	if d.echoIndex == echoBefore {
		t.Fatalf("mute stopped echo index")
	}
	if writes == writesBefore {
		t.Fatalf("mute stopped echo writeback")
	}
	if ram[0x2000] == 0 && ram[0x2001] == 0 && ram[0x2002] == 0 && ram[0x2003] == 0 {
		t.Fatalf("mute echo writeback left buffer zero")
	}
}

func TestDSP_Sample_EchoReadsFromRAM(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	d.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })
	d.SetRAMWriter(func(addr uint16, val uint8) { ram[addr] = val })

	d.Write(0x6C, 0x00) // clear FLG; echo reads gated on neither mute nor ECEN here, but mute would zero output
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x2C, 0x7F)
	d.Write(0x3C, 0x7F)
	d.Write(0x7F, 0x7F) // FIR[7] = newest tap, so this sample's read bleeds through immediately
	d.Write(0x6D, 0x20) // ESA
	d.Write(0x7D, 0x01) // EDL

	ram[0x2000] = 0x00
	ram[0x2001] = 0x40 // +16384
	ram[0x2002] = 0x00
	ram[0x2003] = 0x40

	l, r := d.Sample()
	if l == 0 || r == 0 {
		t.Fatalf("expected echo-mixed output, got %d,%d", l, r)
	}
}

func TestDSP_Sample_EchoWritesToRAM(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	d.SetRAMWriter(func(addr uint16, val uint8) { ram[addr] = val })

	d.Write(0x6C, 0x00) // clear FLG; ECEN must be off for the echo writeback path
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x10)
	d.Write(0x07, 0x7F)
	d.Write(0x4D, 0x01) // EON voice 0
	d.Write(0x6D, 0x20) // ESA
	d.Write(0x7D, 0x01) // EDL
	d.Write(0x4C, 0x01) // KON voice 0

	_, _ = d.Sample()
	if ram[0x2000] == 0 && ram[0x2001] == 0 && ram[0x2002] == 0 && ram[0x2003] == 0 {
		t.Fatalf("expected echo write into RAM, buffer remained zero")
	}
}

func TestDSP_Sample_EchoWritebackClearsLowBit(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	d.SetRAMWriter(func(addr uint16, val uint8) { ram[addr] = val })

	d.Write(0x6C, 0x00) // clear FLG; ECEN must be off for echo writeback
	d.Write(0x4D, 0x01) // EON voice 0
	d.Write(0x6D, 0x20) // ESA
	d.Write(0x7D, 0x01) // EDL

	v := &d.Voices[0]
	v.keyed = true
	v.primed = true
	v.envMode = envGain
	v.GAIN = 0x40
	v.envelope = 0x400
	v.VOLL = 1
	v.VOLR = 1
	v.P = 0x1000
	v.sampleHist = [4]int16{0x7fff, 0x7fff, 0x7fff, 0x7fff}

	_, _ = d.Sample()
	gotL := int16(uint16(ram[0x2000]) | uint16(ram[0x2001])<<8)
	gotR := int16(uint16(ram[0x2002]) | uint16(ram[0x2003])<<8)
	if gotL == 0 || gotR == 0 {
		t.Fatalf("echo writeback = %d,%d, want non-zero samples", gotL, gotR)
	}
	if gotL&1 != 0 || gotR&1 != 0 {
		t.Fatalf("echo writeback = %d,%d, want low bit clear", gotL, gotR)
	}
}

func TestDSP_NewDisablesBootEchoWriteback(t *testing.T) {
	d := New()
	if d.FLG != 0xE0 {
		t.Fatalf("new DSP FLG = %02X, want E0", d.FLG)
	}
	if d.ESA != 0 || d.EDL != 0 {
		t.Fatalf("new DSP ESA/EDL = %02X/%02X, want 00/00", d.ESA, d.EDL)
	}

	ram := []uint8{0x12, 0x34, 0x56, 0x78}
	writes := 0
	d.SetRAMWriter(func(addr uint16, val uint8) {
		writes++
		if int(addr) < len(ram) {
			ram[addr] = val
		}
	})

	_, _ = d.Sample()
	if writes != 0 {
		t.Fatalf("boot sample performed %d echo RAM writes; FLG=%02X", writes, d.FLG)
	}
	if got := ram; got[0] != 0x12 || got[1] != 0x34 || got[2] != 0x56 || got[3] != 0x78 {
		t.Fatalf("boot echo writeback clobbered ESA=0 EDL=0 memory: %02X %02X %02X %02X",
			got[0], got[1], got[2], got[3])
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

	v.envelope = 0x20
	v.KeyOff()
	v.stepEnvelope()
	if v.envelope != 0x18 {
		t.Fatalf("release envelope after one step = %03X, want 018", v.envelope)
	}
	for i := 0; i < 3; i++ {
		v.stepEnvelope()
	}
	if v.envelope != 0 {
		t.Fatalf("release envelope after four steps = %03X, want 000", v.envelope)
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

	if v.brrDecoded[0] != 0 || v.brrDecoded[1] != 0 || v.brrDecoded[15] != -2 {
		t.Fatalf("decoded samples unexpected: first=%d second=%d last=%d", v.brrDecoded[0], v.brrDecoded[1], v.brrDecoded[15])
	}
}

func TestVoice_BRRLoopUsesDirectoryLoopAddress(t *testing.T) {
	ram := make([]uint8, 65536)
	// DIR base 0x2000, SRCN 1 -> entry at 0x2004.
	ram[0x2004] = 0x00
	ram[0x2005] = 0x30 // start 0x3000
	ram[0x2006] = 0x00
	ram[0x2007] = 0x40 // loop 0x4000
	ram[0x3000] = 0x03 // end + loop
	ram[0x4000] = 0x00 // next block should decode from loop address

	read := func(addr uint16) uint8 { return ram[addr] }
	var v Voice
	v.SRCN = 1
	v.KeyOn(read, 0x20)
	v.decodeBRRBlock(read)

	if v.brrAddr != 0x4000 || v.SamplePtr != 0x4000 {
		t.Fatalf("looped BRR next addr = %04X sample ptr = %04X, want 4000", v.brrAddr, v.SamplePtr)
	}
	if !v.keyed || v.envMode == envRelease {
		t.Fatalf("looped BRR ended voice: keyed=%v envMode=%v", v.keyed, v.envMode)
	}
}

func TestDSP_Sample_UsesBRRSourceWhenReaderPresent(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00) // clear FLG so output isn't muted
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

func TestDSP_ReadVoiceOutputRegisters(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x10)
	d.Write(0x07, 0x7F)
	d.Write(0x4C, 0x01)

	_, _ = d.Sample()
	if got := d.Read(0x08); got != d.Voices[0].ENVX {
		t.Fatalf("ENVX read = %02X, want voice ENVX %02X", got, d.Voices[0].ENVX)
	}
	if got := d.Read(0x09); got != d.Voices[0].OUTX {
		t.Fatalf("OUTX read = %02X, want voice OUTX %02X", got, d.Voices[0].OUTX)
	}
}

func TestDSP_ENDXSetClearAndKeyOnClear(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	ram := make([]uint8, 65536)
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30
	ram[0x3000] = 0x01 // end, no loop
	d.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })

	d.Write(0x5D, 0x20)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x40)
	d.Write(0x07, 0x7F)
	d.Write(0x4C, 0x01)
	for i := 0; i < 8 && d.Read(0x7C)&0x01 == 0; i++ {
		_, _ = d.Sample()
	}
	if got := d.Read(0x7C); got&0x01 == 0 {
		t.Fatalf("ENDX did not set after end block, got %02X", got)
	}

	d.Write(0x7C, 0xFF)
	if got := d.Read(0x7C); got != 0 {
		t.Fatalf("ENDX write clear = %02X, want 00", got)
	}

	d.ENDX = 0xFF
	d.Write(0x4C, 0x01)
	if got := d.Read(0x7C); got != 0xFE {
		t.Fatalf("KON did not clear keyed voice ENDX bit: got %02X, want FE", got)
	}
}
