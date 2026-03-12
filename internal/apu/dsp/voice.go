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
}

func (v *Voice) Reset() {
	v.VOLL = 0
	v.VOLR = 0
	v.PitchCounter = 0
	v.phase = 0
	v.envelope = 0
	v.keyed = false
	v.envMode = envRelease
}

func (v *Voice) KeyOn() {
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

// Render produces a sample pair (L, R) for this voice using a basic keyed oscillator.
func (v *Voice) Render() (int32, int32) {
	v.stepEnvelope()
	if v.envelope == 0 {
		return 0, 0
	}

	pitch := v.P
	if pitch == 0 {
		pitch = 1
	}
	v.phase += uint32(pitch)
	sample := int32(triangle(v.phase))
	sample = (sample * int32(v.envelope)) >> 11
	v.OUTX = uint8((sample >> 8) & 0xFF)

	l := (sample * int32(v.VOLL)) >> 7
	r := (sample * int32(v.VOLR)) >> 7
	return l, r
}
