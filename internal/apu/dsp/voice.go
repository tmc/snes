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
	keyed        bool
	envMode      envelopeMode
	brrAddr      uint16
	brrNibblePos int
	brrDecoded   [16]int16
	brrHist1     int16
	brrHist2     int16
	brrLoop      bool
	brrEnd       bool
}

func (v *Voice) Reset() {
	v.VOLL = 0
	v.VOLR = 0
	v.PitchCounter = 0
	v.phase = 0
	v.envelope = 0
	v.keyed = false
	v.envMode = envRelease
	v.brrAddr = 0
	v.brrNibblePos = 16
	v.brrHist1 = 0
	v.brrHist2 = 0
	v.brrLoop = false
	v.brrEnd = false
}

func (v *Voice) KeyOn(read func(uint16) uint8, dir uint8) {
	v.keyed = true
	if v.ADSR1&0x80 != 0 {
		v.envMode = envAttack
	} else {
		v.envMode = envGain
	}
	if v.envelope == 0 {
		v.envelope = 1
	}
	v.phase = 0
	v.brrHist1 = 0
	v.brrHist2 = 0
	v.brrNibblePos = 16
	v.brrLoop = false
	v.brrEnd = false
	if read != nil {
		dirBase := uint16(dir) << 8
		entry := dirBase + (uint16(v.SRCN) * 4)
		start := uint16(read(entry)) | (uint16(read(entry+1)) << 8)
		v.brrAddr = start
		v.SamplePtr = start
	}
}

func (v *Voice) KeyOff() {
	v.keyed = false
	v.envMode = envRelease
}

func attackStep(rate uint8) int16 {
	if rate >= 0x0E {
		return 0x80
	}
	return int16(rate+1) << 3
}

func decayStep(rate uint8) int16 {
	return int16(rate+1) << 1
}

func sustainTarget(level uint8) int16 {
	target := int16(level+1) << 8
	if target > 0x7FF {
		return 0x7FF
	}
	return target
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

func (v *Voice) stepEnvelope() {
	if !v.keyed {
		v.envMode = envRelease
	}

	switch v.envMode {
	case envAttack:
		v.envelope = applySignedGain(v.envelope, attackStep(v.ADSR1&0x0F))
		if v.envelope >= 0x7FF {
			v.envelope = 0x7FF
			v.envMode = envDecay
		}
	case envDecay:
		v.envelope = applySignedGain(v.envelope, -decayStep((v.ADSR1>>4)&0x07))
		if v.envelope <= sustainTarget((v.ADSR2>>5)&0x07) {
			v.envMode = envSustain
		}
	case envSustain:
		v.envelope = applySignedGain(v.envelope, -decayStep(v.ADSR2&0x1F))
	case envGain:
		if (v.GAIN & 0x80) == 0 {
			v.envelope = int16(v.GAIN&0x7F) << 4
		} else {
			mode := (v.GAIN >> 5) & 0x03
			rate := v.GAIN & 0x1F
			step := int16(rate + 1)
			switch mode {
			case 0x00:
				v.envelope = applySignedGain(v.envelope, -step)
			case 0x01:
				v.envelope = applySignedGain(v.envelope, -step*4)
			case 0x02:
				v.envelope = applySignedGain(v.envelope, step)
			case 0x03:
				v.envelope = applySignedGain(v.envelope, step*4)
			}
		}
	case envRelease:
		if v.envelope > 0 {
			v.envelope = applySignedGain(v.envelope, -0x20)
		}
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
		sample = int32(s&^0x07) << 12
		sample >>= 1
	}
	switch filter {
	case 1:
		sample += int32(hist1) + ((-int32(hist1)) >> 4)
	case 2:
		sample += (int32(hist1) << 1) + ((-(int32(hist1) * 3)) >> 5) - int32(hist2) + (int32(hist2) >> 4)
	case 3:
		sample += (int32(hist1) << 1) + ((-(int32(hist1) * 13)) >> 6) - int32(hist2) + ((int32(hist2) * 3) >> 4)
	}
	return clamp16(sample)
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
	v.SamplePtr = v.brrAddr
	v.brrNibblePos = 0
	if v.brrEnd {
		if v.brrLoop {
			// Keep stepping on looped stream.
			return
		}
		// Non-looped end decays to silence.
		v.keyed = false
		v.envMode = envRelease
	}
}

func (v *Voice) currentSample(read func(uint16) uint8) int16 {
	if read == nil {
		return triangle(v.phase)
	}
	if v.brrNibblePos >= 16 {
		v.decodeBRRBlock(read)
	}
	if v.brrNibblePos >= 16 {
		return 0
	}
	return v.brrDecoded[v.brrNibblePos]
}

// Render produces a sample pair (L, R) for this voice using a basic keyed oscillator.
func (v *Voice) Render(read func(uint16) uint8) (int32, int32) {
	v.stepEnvelope()
	if v.envelope == 0 {
		return 0, 0
	}

	pitch := v.P
	if pitch == 0 {
		pitch = 1
	}
	v.phase += uint32(pitch)
	step := int(v.phase >> 12)
	if step > 0 {
		adv := step
		if read != nil {
			v.brrNibblePos += adv
			if v.brrNibblePos >= 16 {
				v.decodeBRRBlock(read)
			}
		}
		v.phase &= 0x0FFF
	}
	sample := int32(v.currentSample(read))
	sample = (sample * int32(v.envelope)) >> 11
	v.OUTX = uint8((sample >> 8) & 0xFF)

	l := (sample * int32(v.VOLL)) >> 7
	r := (sample * int32(v.VOLR)) >> 7
	return l, r
}
