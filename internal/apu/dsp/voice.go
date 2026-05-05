package dsp

type envelopeMode uint8

const (
	envAttack envelopeMode = iota
	envDecay
	envSustain
	envRelease
	envGain
)

// Voice represents one of the 8 DSP voices.
type Voice struct {
	// Registers
	VOLL  int8   // Volume Left ($00) (Signed 8-bit)
	VOLR  int8   // Volume Right ($01)
	P     uint16 // Pitch Low/High ($02/03)
	SRCN  uint8  // Source Number ($04)
	ADSR1 uint8  // ADSR Settings ($05)
	ADSR2 uint8  // ADSR Settings ($06)
	GAIN  uint8  // Gain Settings ($07)
	ENVX  uint8  // Current Envelope ($08) - Read Only usually
	OUTX  uint8  // Current Sample ($09) - Read Only usually

	// Internal State
	PitchCounter uint16
	SamplePtr    uint16 // Points to BRR sample in RAM
	phase        uint32
	envelope     int16
	envCounter   int
	hiddenEnv    int16
	keyed        bool
	envMode      envelopeMode
	brrAddr      uint16
	brrLoopAddr  uint16
	brrNibblePos int
	brrDecoded   [16]int16
	brrHist1     int16
	brrHist2     int16
	brrLoop      bool
	brrEnd       bool
	brrEnded     bool

	// sampleHist is the sliding 4-entry window used by Gaussian
	// interpolation. sampleHist[0] is the newest decoded BRR sample and
	// sampleHist[3] is three samples ago.
	sampleHist [4]int16

	// adsrPending is true when ADSR1 ($x5) was written in the current
	// sample window but a subsequent GAIN ($x7) write has not yet
	// cancelled the mode switch. Used to model the write-order race.
	adsrPending bool

	// gainPending is true when a GAIN ($x7) write won the current
	// ADSR/GAIN write-order race and must be observed at the next envelope
	// step or pending key-on.
	gainPending bool

	// prevOutput is the most recent envelope-scaled mono output from this
	// voice, used by pitch modulation on the following voice (PMON).
	prevOutput int16

	// useNoise selects the noise LFSR as the source instead of BRR.
	useNoise bool

	// primed is true once the Gaussian sample history has been filled with
	// three pre-fetched samples after key-on. Priming models the bsnes
	// "seed the convolution window" workaround and avoids a 3-sample
	// zero onset after every key-on.
	primed bool
}

func (v *Voice) Reset() {
	v.VOLL = 0
	v.VOLR = 0
	v.PitchCounter = 0
	v.phase = 0
	v.envelope = 0
	v.envCounter = 0
	v.hiddenEnv = 0
	v.keyed = false
	v.envMode = envRelease
	v.brrAddr = 0
	v.brrLoopAddr = 0
	v.brrNibblePos = 16
	v.brrHist1 = 0
	v.brrHist2 = 0
	v.brrLoop = false
	v.brrEnd = false
	v.brrEnded = false
}

func (v *Voice) KeyOn(read func(uint16) uint8, dir uint8) {
	v.keyed = true
	if v.gainPending || v.ADSR1&0x80 == 0 {
		v.envMode = envGain
	} else {
		v.envMode = envAttack
	}
	if v.envelope == 0 {
		v.envelope = 1
	}
	v.phase = 0
	v.envCounter = 0
	v.hiddenEnv = v.envelope
	v.brrHist1 = 0
	v.brrHist2 = 0
	v.brrNibblePos = 16
	v.brrLoop = false
	v.brrEnd = false
	v.brrEnded = false
	v.sampleHist = [4]int16{}
	v.adsrPending = false
	v.gainPending = false
	v.prevOutput = 0
	v.primed = false
	if read != nil {
		dirBase := uint16(dir) << 8
		entry := dirBase + (uint16(v.SRCN) * 4)
		start := uint16(read(entry)) | (uint16(read(entry+1)) << 8)
		loop := uint16(read(entry+2)) | (uint16(read(entry+3)) << 8)
		v.brrAddr = start
		v.brrLoopAddr = loop
		v.SamplePtr = start
	}
}

func (v *Voice) KeyOff() {
	v.keyed = false
	v.envMode = envRelease
}

func applySignedGain(v int16, step int16) int16 {
	next := int32(v) + int32(step)
	if next < 0 {
		return 0
	}
	if next > 0x7FF {
		return 0x7FF
	}
	return int16(next)
}

func (v *Voice) envelopeCounterFires(rate uint8) bool {
	period := counterRates[rate&0x1F]
	if period <= 1 {
		return true
	}
	if period >= 0x7FFFFFFF {
		return false
	}
	v.envCounter++
	if v.envCounter < period {
		return false
	}
	v.envCounter = 0
	return true
}

func (v *Voice) stepEnvelope() {
	if !v.keyed {
		v.envMode = envRelease
	} else if v.gainPending {
		v.envMode = envGain
		v.gainPending = false
		v.adsrPending = false
	} else if v.adsrPending {
		if v.ADSR1&0x80 == 0 {
			v.envMode = envGain
		} else if v.envMode == envGain {
			v.envMode = envAttack
			if v.envelope == 0 {
				v.envelope = 1
			}
		}
		v.adsrPending = false
	}

	switch v.envMode {
	case envAttack:
		rate := (v.ADSR1&0x0F)*2 + 1
		next := v.envelope
		if rate < 31 {
			next = applySignedGain(next, 0x20)
		} else {
			next = applySignedGain(next, 0x400)
		}
		if next >= 0x7FF {
			next = 0x7FF
			v.envMode = envDecay
		}
		v.hiddenEnv = next
		if v.envelopeCounterFires(rate) {
			v.envelope = next
		}
	case envDecay:
		next := int32(v.envelope)
		next--
		next -= next >> 8
		if next < 0 {
			next = 0
		}
		if (next >> 8) == int32((v.ADSR2>>5)&0x07) {
			v.envMode = envSustain
		}
		v.hiddenEnv = int16(next)
		rate := ((v.ADSR1 >> 3) & 0x0E) + 0x10
		if v.envelopeCounterFires(rate) {
			v.envelope = int16(next)
		}
	case envSustain:
		next := int32(v.envelope)
		next--
		next -= next >> 8
		if next < 0 {
			next = 0
		}
		v.hiddenEnv = int16(next)
		if v.envelopeCounterFires(v.ADSR2 & 0x1F) {
			v.envelope = int16(next)
		}
	case envGain:
		if (v.GAIN & 0x80) == 0 {
			v.envelope = int16(v.GAIN&0x7F) << 4
			v.hiddenEnv = v.envelope
		} else {
			mode := (v.GAIN >> 5) & 0x03
			rate := v.GAIN & 0x1F
			next := v.envelope
			switch mode {
			case 0x00:
				next = applySignedGain(next, -0x20)
			case 0x01:
				n := int32(next)
				n--
				n -= n >> 8
				if n < 0 {
					n = 0
				}
				next = int16(n)
			case 0x02:
				next = applySignedGain(next, 0x20)
			case 0x03:
				step := int16(0x20)
				if v.hiddenEnv >= 0x600 {
					step = 0x08
				}
				next = applySignedGain(next, step)
			}
			v.hiddenEnv = next
			if v.envelopeCounterFires(rate) {
				v.envelope = next
			}
		}
	case envRelease:
		if v.envelope > 0 {
			v.envelope = applySignedGain(v.envelope, -0x08)
		}
		v.hiddenEnv = v.envelope
	}

	v.ENVX = uint8((v.envelope >> 4) & 0x7F)
}

func triangle(phase uint32) int16 {
	step := int32((phase >> 5) & 0x3FF) // 0..1023
	if step < 512 {
		return int16(step*64 - 16384)
	}
	return int16((1023-step)*64 - 16384)
}

func clamp16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func decodeBRRNibble(nibble uint8, shift uint8, filter uint8, hist1, hist2 int16) int16 {
	s := int16(int8(nibble<<4) >> 4)
	var sample int32
	if shift <= 12 {
		sample = int32(s) << shift
		sample >>= 1
	} else {
		sample = int32(s) &^ 0x7FF
	}
	p1 := int32(hist1)
	p2 := int32(hist2) >> 1
	switch filter {
	case 1:
		sample += (p1 >> 1) + ((-p1) >> 5)
	case 2:
		sample += p1 - p2 + (p2 >> 4) + ((p1 * -3) >> 6)
	case 3:
		sample += p1 - p2 + ((p1 * -13) >> 7) + ((p2 * 3) >> 4)
	}
	return int16(clamp16(sample) << 1)
}

func (v *Voice) decodeBRRBlock(read func(uint16) uint8) {
	if read == nil {
		v.brrNibblePos = 16
		return
	}
	header := read(v.brrAddr)
	shift := (header >> 4) & 0x0F
	filter := (header >> 2) & 0x03
	v.brrLoop = (header & 0x02) != 0
	v.brrEnd = (header & 0x01) != 0
	dataAddr := v.brrAddr + 1
	for i := 0; i < 8; i++ {
		b := read(dataAddr + uint16(i))
		hi := decodeBRRNibble((b>>4)&0x0F, shift, filter, v.brrHist1, v.brrHist2)
		v.brrHist2 = v.brrHist1
		v.brrHist1 = hi
		v.brrDecoded[i*2] = hi

		lo := decodeBRRNibble(b&0x0F, shift, filter, v.brrHist1, v.brrHist2)
		v.brrHist2 = v.brrHist1
		v.brrHist1 = lo
		v.brrDecoded[i*2+1] = lo
	}
	v.brrAddr += 9
	v.brrNibblePos = 0
	if v.brrEnd {
		v.brrEnded = true
		if v.brrLoop {
			v.brrAddr = v.brrLoopAddr
			v.SamplePtr = v.brrAddr
			return
		}
		// Non-looped end decays to silence.
		v.keyed = false
		v.envMode = envRelease
	}
	v.SamplePtr = v.brrAddr
}

// pushSample shifts the 4-entry history and inserts a new sample at index 0.
func (v *Voice) pushSample(s int16) {
	v.sampleHist[3] = v.sampleHist[2]
	v.sampleHist[2] = v.sampleHist[1]
	v.sampleHist[1] = v.sampleHist[0]
	v.sampleHist[0] = s
}

// advanceSource steps the BRR decoder forward by n sample slots, pushing each
// produced sample into the Gaussian history. Noise voices still advance this
// source state; NON overrides the interpolated source after interpolation.
func (v *Voice) advanceSource(n int, read func(uint16) uint8) {
	for i := 0; i < n; i++ {
		var s int16
		if read != nil {
			if v.brrNibblePos >= 16 {
				v.decodeBRRBlock(read)
			}
			if v.brrNibblePos < 16 {
				s = v.brrDecoded[v.brrNibblePos]
				v.brrNibblePos++
			}
		} else {
			s = triangle(v.phase)
		}
		v.pushSample(s)
	}
}

// renderWith produces a sample pair using the given pre-computed pitch step
// (already adjusted for pitch modulation by the caller) and the current noise
// sample. Caller is expected to call stepEnvelope() before render as the
// envelope decision for this sample.
func (v *Voice) renderWith(pitch uint16, read func(uint16) uint8, noise int16) (int32, int32) {
	if v.envelope == 0 {
		v.prevOutput = 0
		return 0, 0
	}

	// On the first render after key-on, prime the Gaussian sample
	// history with three samples so the convolution produces a non-zero
	// response on tick 0 instead of bleeding from an all-zero window.
	if !v.primed {
		v.advanceSource(3, read)
		v.primed = true
	}

	if pitch == 0 {
		pitch = 1
	}
	v.phase += uint32(pitch)
	step := int(v.phase >> 12)
	if step > 0 {
		v.advanceSource(step, read)
		v.phase &= 0x0FFF
	}

	frac := uint8((v.phase >> 4) & 0xFF)
	interp := gaussianInterpolate(frac,
		v.sampleHist[3], v.sampleHist[2], v.sampleHist[1], v.sampleHist[0])
	if v.useNoise {
		interp = noise
	}

	sample := (int32(interp) * int32(v.envelope)) >> 11
	if sample > 32767 {
		sample = 32767
	}
	if sample < -32768 {
		sample = -32768
	}
	sample &^= 1
	v.OUTX = uint8((sample >> 8) & 0xFF)
	v.prevOutput = int16(sample)

	l := (sample * int32(v.VOLL)) >> 7
	r := (sample * int32(v.VOLR)) >> 7
	return l, r
}

// Render produces a sample pair (L, R) for this voice, preserving the
// original DSP.Sample() call signature (no modulation, no noise). It advances
// the envelope as a side effect, matching pre-Phase-6 behaviour for any
// caller that bypasses the per-sample DSP loop.
func (v *Voice) Render(read func(uint16) uint8) (int32, int32) {
	v.stepEnvelope()
	return v.renderWith(v.P, read, 0)
}
