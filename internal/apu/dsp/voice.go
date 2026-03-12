package dsp

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
}

func (v *Voice) Reset() {
	v.VOLL = 0
	v.VOLR = 0
	v.PitchCounter = 0
	v.phase = 0
	v.envelope = 0
	v.keyed = false
}

func (v *Voice) KeyOn() {
	v.keyed = true
	if v.envelope == 0 {
		v.envelope = 1
	}
}

func (v *Voice) KeyOff() {
	v.keyed = false
}

func (v *Voice) stepEnvelope() {
	if v.keyed {
		if v.envelope < 0x7FF {
			v.envelope += 0x10
			if v.envelope > 0x7FF {
				v.envelope = 0x7FF
			}
		}
		return
	}
	if v.envelope > 0 {
		v.envelope -= 0x20
		if v.envelope < 0 {
			v.envelope = 0
		}
	}
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
