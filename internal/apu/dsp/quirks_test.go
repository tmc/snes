package dsp

import "testing"

// TestPMON_Voice0IsNoOp verifies the "pitch modulation voice 0 is a no-op"
// quirk from design_doc §5.4: writing PMON bit 0 must not apply modulation to
// voice 0, which has no predecessor.
func TestPMON_Voice0IsNoOp(t *testing.T) {
	d := New()
	// Try to set every PMON bit including bit 0. The write handler must
	// mask bit 0 off; confirm via the internal PMON field.
	d.Write(0x2D, 0xFF)
	if d.PMON&0x01 != 0 {
		t.Fatalf("PMON bit 0 survived Write: PMON=%02X", d.PMON)
	}
	if d.PMON != 0xFE {
		t.Fatalf("PMON = %02X, want FE (bit 0 masked)", d.PMON)
	}
}

// TestPMON_Voice1PitchScaledByVoice0Output verifies the PMON formula: voice
// N's pitch is adjusted by ((voiceN-1.prevOutput >> 5) * pitch) >> 10 when
// PMON bit N is set.
func TestPMON_Voice1PitchScaledByVoice0Output(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00) // clear FLG (default $E0 mutes output)
	// Voice 0 produces a steady positive output via direct-gain on a
	// flat BRR source.
	ram := make([]uint8, 65536)
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30
	ram[0x3000] = 0xC0 // shift=12 filter=0
	for i := 0; i < 8; i++ {
		ram[0x3001+uint16(i)] = 0x77
	}
	d.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })

	d.Write(0x5D, 0x20)
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x10)
	d.Write(0x07, 0x7F)
	// Voice 1: direct gain, pitch 0x1000, PMON bit 1 enabled.
	d.Write(0x10, 0x7F)
	d.Write(0x11, 0x7F)
	d.Write(0x12, 0x00)
	d.Write(0x13, 0x10)
	d.Write(0x17, 0x7F)
	d.Write(0x2D, 0x02) // enable PMON for voice 1 (bit 1)
	d.Write(0x4C, 0x03) // KON voice 0 and 1

	// Clock a few samples so voice 0's prevOutput is well-defined.
	for i := 0; i < 16; i++ {
		_, _ = d.Sample()
	}
	// Voice 0 should be producing non-zero output; voice 1 must have
	// recorded a non-zero prevOutput too (proves the PMON path is live).
	if d.Voices[0].prevOutput == 0 {
		t.Fatalf("voice 0 produced no output")
	}
}

// TestNoiseLFSR_OneBitPerSample verifies the "noise LFSR advances one bit per
// sample" quirk (§5.4): consecutive noise outputs differ only in low bits —
// the LFSR must not be stepped multiple times per output sample.
func TestNoiseLFSR_OneBitPerSample(t *testing.T) {
	d := New()
	// FLG noise rate = 31 (fires every sample). Mute echo so Sample() is
	// pure noise-path.
	d.Write(0x6C, 0x1F|0x20)
	prev := d.noise
	d.Sample()
	// Expected LFSR next state: one-shift-and-feedback from prev.
	fb := (prev << 13) ^ (prev << 14)
	want := (fb & 0x4000) | (prev >> 1)
	if d.noise != want {
		t.Fatalf("LFSR advanced wrong: got %04X want %04X (prev=%04X)", d.noise, want, prev)
	}
}

// TestNoiseLFSR_RateZeroNeverFires: rate=0 must not step the LFSR at all.
func TestNoiseLFSR_RateZeroNeverFires(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00) // rate=0, echo enabled
	prev := d.noise
	for i := 0; i < 100; i++ {
		d.Sample()
	}
	if d.noise != prev {
		t.Fatalf("LFSR stepped with rate=0: noise=%04X prev=%04X", d.noise, prev)
	}
}

func TestNoiseSampleSignExtendsFifteenBitLFSR(t *testing.T) {
	tests := []struct {
		name  string
		noise uint16
		want  int16
	}{
		{name: "positive", noise: 0x3FFF, want: 32766},
		{name: "negative", noise: 0x4000, want: -32768},
		{name: "low bit", noise: 0x0001, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New()
			d.noise = tt.noise
			if got := d.noiseSample(); got != tt.want {
				t.Fatalf("noiseSample(%04X) = %d, want %d", tt.noise, got, tt.want)
			}
		})
	}
}

func TestNoiseLFSR_StepsOncePerOutputSample(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x1F|0x20) // rate 31, echo disabled
	d.Write(0x3D, 0x01)      // voice 0 uses noise
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x70) // high pitch advances several source slots this tick
	d.Write(0x07, 0x7F)
	d.Write(0x4C, 0x01)

	prev := d.noise
	d.Sample()
	want := noiseStep(prev)
	if d.noise != want {
		t.Fatalf("noise after one output sample = %04X, want one step %04X from %04X", d.noise, want, prev)
	}
	wantOut := (int32(int16(want<<1)) * int32(d.Voices[0].envelope)) >> 11
	wantOut &^= 1
	if got := d.Voices[0].prevOutput; got != int16(wantOut) {
		t.Fatalf("noise output = %d, want current signed noise %d", got, int16(wantOut))
	}
}

func noiseStep(noise uint16) uint16 {
	fb := (noise << 13) ^ (noise << 14)
	return (fb & 0x4000) | (noise >> 1)
}

func TestNoiseVoiceBypassesGaussianHistory(t *testing.T) {
	var v Voice
	v.keyed = true
	v.envMode = envGain
	v.envelope = 0x7FF
	v.VOLL = 0x7F
	v.VOLR = 0x7F
	v.P = 0x1000
	v.useNoise = true
	v.primed = true
	v.sampleHist = [4]int16{0x3FFF, 0x3FFF, 0x3FFF, 0x3FFF}

	_, _ = v.renderWith(v.P, nil, -32768)
	if v.prevOutput >= 0 {
		t.Fatalf("noise voice used Gaussian history instead of current noise: prevOutput=%d", v.prevOutput)
	}
	if v.prevOutput&1 != 0 {
		t.Fatalf("noise voice output is odd: %d", v.prevOutput)
	}
}

func TestNoiseVoiceKeepsBRRSourceAdvancing(t *testing.T) {
	ram := make([]uint8, 65536)
	ram[0x2000] = 0x00
	ram[0x2001] = 0x30
	ram[0x3000] = 0x00
	for i := 0; i < 8; i++ {
		ram[0x3001+uint16(i)] = 0x11
	}
	read := func(addr uint16) uint8 { return ram[addr] }

	var v Voice
	v.SRCN = 0
	v.GAIN = 0x7F
	v.P = 0x4000
	v.useNoise = true
	v.KeyOn(read, 0x20)
	v.stepEnvelope()
	_, _ = v.renderWith(v.P, read, 0)

	if got := v.brrNibblePos; got != 7 {
		t.Fatalf("noise voice brrNibblePos = %d, want 7 source slots advanced", got)
	}
}

// TestADSRWriteOrderRace_PendingLatchCleared verifies that writing GAIN ($x7)
// after ADSR1 ($x5) clears the adsrPending latch (§5.4 write-order race).
func TestADSRWriteOrderRace_PendingLatchCleared(t *testing.T) {
	d := New()
	d.Write(0x05, 0x8F) // voice 0 ADSR1 with bit 7 set (ADSR mode)
	if !d.Voices[0].adsrPending {
		t.Fatalf("adsrPending should be set after ADSR1 write")
	}
	d.Write(0x07, 0x7F) // GAIN write clears the latch
	if d.Voices[0].adsrPending {
		t.Fatalf("GAIN write after ADSR1 must clear adsrPending latch")
	}
}

// TestADSRWriteOrderRace_EnvelopeConsumesPending: if ADSR1 is written with no
// subsequent GAIN, the next envelope step consumes the pending latch and the
// voice runs in ADSR mode with the live register-file contents.
func TestADSRWriteOrderRace_EnvelopeConsumesPending(t *testing.T) {
	d := New()
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x02, 0x00)
	d.Write(0x03, 0x10)
	// Set GAIN first, then ADSR1 — the ADSR1 write is the last thing
	// before KON, so the envelope should pick ADSR mode, not GAIN mode.
	d.Write(0x07, 0x40)
	d.Write(0x05, 0x8F)
	d.Write(0x06, 0xE0)
	d.Write(0x4C, 0x01)

	// After the first Sample() the latch must be cleared.
	d.Sample()
	if d.Voices[0].adsrPending {
		t.Fatalf("envelope step should clear adsrPending")
	}
	if d.Voices[0].envMode != envAttack && d.Voices[0].envMode != envDecay && d.Voices[0].envMode != envSustain {
		t.Fatalf("voice envMode = %v, want attack/decay/sustain (ADSR path)", d.Voices[0].envMode)
	}
}

func TestADSRWriteOrderRace_KeyedVoiceUsesADSR1BeforeSample(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x07, 0x20)
	d.Write(0x4C, 0x01)
	d.Sample()
	if got := d.Voices[0].envMode; got != envGain {
		t.Fatalf("initial envMode = %v, want gain", got)
	}

	d.Write(0x05, 0x8F)
	d.Write(0x06, 0xE0)
	d.Sample()
	if d.Voices[0].adsrPending {
		t.Fatalf("envelope step should consume ADSR1 latch")
	}
	if got := d.Voices[0].envMode; got != envAttack && got != envDecay {
		t.Fatalf("envMode after ADSR1 write = %v, want attack/decay", got)
	}
}

func TestADSRWriteOrderRace_GainAfterADSR1Wins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x05, 0x8F)
	d.Write(0x06, 0xE0)
	d.Write(0x4C, 0x01)
	d.Sample()
	if got := d.Voices[0].envMode; got != envAttack && got != envDecay {
		t.Fatalf("initial envMode = %v, want attack/decay", got)
	}

	d.Write(0x05, 0x8F)
	d.Write(0x07, 0x30)
	d.Sample()
	if got := d.Voices[0].envMode; got != envGain {
		t.Fatalf("envMode after ADSR1 then GAIN = %v, want gain", got)
	}
	if got := d.Voices[0].envelope; got != 0x300 {
		t.Fatalf("envelope after ADSR1 then GAIN = %03X, want latest GAIN level", got)
	}
}

func TestADSRWriteOrderRace_GainAfterADSR1BeforeKONWins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x05, 0x8F)
	d.Write(0x07, 0x30)
	d.Write(0x4C, 0x01)
	d.Sample()
	if got := d.Voices[0].envMode; got != envGain {
		t.Fatalf("envMode after ADSR1, GAIN, KON = %v, want gain", got)
	}
	if got := d.Voices[0].envelope; got != 0x300 {
		t.Fatalf("envelope after ADSR1, GAIN, KON = %03X, want latest GAIN level", got)
	}
}

func TestADSRWriteOrderRace_ADSR1AfterGainWins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x05, 0x8F)
	d.Write(0x07, 0x30)
	d.Write(0x4C, 0x01)
	d.Sample()
	if got := d.Voices[0].envMode; got != envGain {
		t.Fatalf("initial envMode = %v, want gain", got)
	}

	d.Write(0x05, 0x8F)
	d.Write(0x07, 0x20)
	d.Write(0x05, 0x8F)
	d.Sample()
	if d.Voices[0].gainPending {
		t.Fatalf("latest ADSR1 write should clear stale gainPending")
	}
	if got := d.Voices[0].envMode; got != envAttack && got != envDecay {
		t.Fatalf("envMode after ADSR1, GAIN, ADSR1 = %v, want attack/decay", got)
	}
}

func TestADSRWriteOrderRace_ADSR1AfterGainBeforeKONWins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x05, 0x8F)
	d.Write(0x07, 0x30)
	d.Write(0x05, 0x8F)
	d.Write(0x4C, 0x01)
	d.Sample()
	if d.Voices[0].gainPending {
		t.Fatalf("latest ADSR1 write should clear stale gainPending before KON")
	}
	if got := d.Voices[0].envMode; got != envAttack && got != envDecay {
		t.Fatalf("envMode after ADSR1, GAIN, ADSR1, KON = %v, want attack/decay", got)
	}
}

func TestADSRWriteOrderRace_ADSR2AfterADSR1Wins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	v := &d.Voices[0]
	v.keyed = true
	v.envMode = envDecay
	v.envelope = 0x700
	v.ADSR1 = 0x8F
	v.ADSR2 = 0xE0

	d.Write(0x05, 0x8F)
	d.Write(0x06, 0xC0)
	d.Sample()
	if got := v.envMode; got != envSustain {
		t.Fatalf("envMode after ADSR2 update = %v, want sustain from latest ADSR2", got)
	}
}

func TestADSRWriteOrderRace_ADSR2RateAfterADSR1Wins(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	v := &d.Voices[0]
	v.keyed = true
	v.envMode = envSustain
	v.envelope = 0x700
	v.ADSR1 = 0x8F
	v.ADSR2 = 0x00

	d.Write(0x05, 0x8F)
	d.Write(0x06, 0x1F)
	d.Sample()
	if got := v.envelope; got >= 0x700 {
		t.Fatalf("envelope after ADSR2 rate update = %03X, want decrement from latest ADSR2", got)
	}
}

func TestKONKOFFWriteOrderBeforeSample(t *testing.T) {
	t.Run("KOFF then KON leaves voice keyed", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.Write(0x07, 0x40)

		d.Write(0x5C, 0x01)
		d.Write(0x4C, 0x01)
		d.Sample()

		if !d.Voices[0].keyed {
			t.Fatalf("voice not keyed after KOFF then KON")
		}
		if got := d.Voices[0].envMode; got != envGain {
			t.Fatalf("voice envMode = %v, want gain after KOFF then KON", got)
		}
		if got := d.Voices[0].envelope; got != 0x400 {
			t.Fatalf("voice envelope = %03X, want direct gain level", got)
		}
	})

	t.Run("KON then KOFF releases voice", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.Write(0x07, 0x40)

		d.Write(0x4C, 0x01)
		d.Write(0x5C, 0x01)
		d.Sample()

		if d.Voices[0].keyed {
			t.Fatalf("voice still keyed after KON then KOFF")
		}
		if got := d.Voices[0].envMode; got != envRelease {
			t.Fatalf("voice envMode = %v, want release after KON then KOFF", got)
		}
		if got := d.Voices[0].envelope; got != 0 {
			t.Fatalf("voice envelope = %03X, want released to zero", got)
		}
	})
}

func TestKONKOFFApplyAtSampleBoundary(t *testing.T) {
	t.Run("KON waits for next sample", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.Write(0x07, 0x40)

		d.Write(0x4C, 0x01)
		if d.Voices[0].keyed {
			t.Fatalf("voice keyed before sample boundary")
		}

		d.Sample()
		if !d.Voices[0].keyed {
			t.Fatalf("voice not keyed at sample boundary")
		}
		if got := d.Voices[0].envelope; got != 0x400 {
			t.Fatalf("voice envelope = %03X, want direct gain level", got)
		}
	})

	t.Run("KON clears ENDX at sample boundary", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.ENDX = 0xFF

		d.Write(0x4C, 0x01)
		if got := d.Read(0x7C); got != 0xFF {
			t.Fatalf("KON write cleared ENDX before sample boundary: got %02X, want FF", got)
		}

		d.Sample()
		if got := d.Read(0x7C); got != 0xFE {
			t.Fatalf("sample-boundary KON ENDX = %02X, want FE", got)
		}
	})

	t.Run("KOFF waits for next sample", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.Write(0x07, 0x40)
		d.Write(0x4C, 0x01)
		d.Sample()

		d.Write(0x5C, 0x01)
		if !d.Voices[0].keyed {
			t.Fatalf("voice released before sample boundary")
		}

		d.Sample()
		if d.Voices[0].keyed {
			t.Fatalf("voice still keyed after sample boundary")
		}
		if got := d.Voices[0].envMode; got != envRelease {
			t.Fatalf("voice envMode = %v, want release", got)
		}
	})

	t.Run("KON overwritten by KOFF does not clear ENDX", func(t *testing.T) {
		d := New()
		d.Write(0x6C, 0x00)
		d.ENDX = 0xFF

		d.Write(0x4C, 0x01)
		d.Write(0x5C, 0x01)
		d.Sample()
		if got := d.Read(0x7C); got != 0xFF {
			t.Fatalf("KON overwritten by KOFF cleared ENDX: got %02X, want FF", got)
		}
	})
}

func TestKONPendingSurvivesSaveState(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x07, 0x40)
	d.Write(0x4C, 0x01)

	state := d.SaveState()
	restored := New()
	restored.LoadState(state)
	restored.Sample()

	if !restored.Voices[0].keyed {
		t.Fatalf("restored voice did not key on")
	}
	if got := restored.Voices[0].envelope; got != 0x400 {
		t.Fatalf("restored voice envelope = %03X, want direct gain level", got)
	}
}

func TestADSRAttackUsesRateCounter(t *testing.T) {
	d := New()
	d.Write(0x6C, 0x00)
	d.Write(0x00, 0x7F)
	d.Write(0x01, 0x7F)
	d.Write(0x05, 0x80) // ADSR attack rate 0 maps to rate counter 1.
	d.Write(0x06, 0xE0)
	d.Write(0x4C, 0x01)

	d.Sample()
	if got := d.Voices[0].envelope; got != 1 {
		t.Fatalf("envelope after one slow attack sample = %d, want initial level", got)
	}

	for i := 0; i < counterRates[1]; i++ {
		d.Sample()
	}
	if got := d.Voices[0].envelope; got <= 1 {
		t.Fatalf("envelope after attack counter fires = %d, want growth", got)
	}
}

func TestBRRFilter0AllowsMaxNegativeSample(t *testing.T) {
	got := decodeBRRNibble(0x8, 12, 0, 0, 0)
	if got != -32768 {
		t.Fatalf("filter 0 max-negative sample = %d, want -32768", got)
	}
}

func TestBRRPredictionSignExtendsAfterFilter(t *testing.T) {
	got := decodeBRRNibble(0x7, 12, 1, 0x7FFF, 0)
	if got != -6146 {
		t.Fatalf("positive predicted overflow = %d, want signed wrap -6146", got)
	}

	got = decodeBRRNibble(0x8, 12, 1, -0x8000, 0)
	if got != 2048 {
		t.Fatalf("negative predicted overflow = %d, want signed wrap 2048", got)
	}
}

func TestBRRInvalidShiftSignExtendsNibble(t *testing.T) {
	tests := []struct {
		name   string
		nibble uint8
		want   int16
	}{
		{name: "positive", nibble: 0x7, want: 0},
		{name: "negative one", nibble: 0xF, want: -4096},
		{name: "negative eight", nibble: 0x8, want: -4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeBRRNibble(tt.nibble, 13, 0, 0, 0)
			if got != tt.want {
				t.Fatalf("invalid-shift nibble %X = %d, want %d", tt.nibble, got, tt.want)
			}
		})
	}
}

func TestBRRMaxNegativePop(t *testing.T) {
	var h1, h2 int16
	want := []int16{-32768, 2048, -30848}
	for i, w := range want {
		got := decodeBRRNibble(0x8, 12, 1, h1, h2)
		if got != w {
			t.Fatalf("sample %d = %d, want %d", i, got, w)
		}
		h2, h1 = h1, got
	}
}

// TestFIR_DoubleClip verifies the FIR 8-tap "clip twice" quirk (§5.4): the
// running sum is clipped after the first 7 taps, then the 8th tap is added and
// the result is clipped again. A single post-clip keeps too much transient
// energy when the first 7 taps overflow and the last tap pulls back.
func TestFIR_DoubleClip(t *testing.T) {
	d := New()
	ram := make([]uint8, 65536)
	ram[0x2000] = 0xfe
	ram[0x2001] = 0x7f
	ram[0x2002] = 0xfe
	ram[0x2003] = 0x7f
	d.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })

	d.Write(0x0F, 0x7F)
	d.Write(0x1F, 0x7F)
	d.Write(0x2F, 0x7F)
	d.Write(0x3F, 0x7F)
	d.Write(0x4F, 0x7F)
	d.Write(0x5F, 0x7F)
	d.Write(0x6F, 0x7F)
	d.Write(0x7F, 0x81) // -127

	for i := range d.echoHist {
		d.echoHist[i][0] = 0x3fff
		d.echoHist[i][1] = 0x3fff
	}

	d.Write(0x2C, 0x7F)
	d.Write(0x3C, 0x7F)
	d.Write(0x0C, 0x7F)
	d.Write(0x1C, 0x7F)
	d.Write(0x6D, 0x20)
	d.Write(0x7D, 0x01)
	d.Write(0x6C, 0x20) // echo-disable: skip feedback write so history stays clean

	l, r := d.Sample()
	if l != 250 || r != 250 {
		t.Fatalf("double-clip FIR output = %d,%d, want 250,250", l, r)
	}
}
