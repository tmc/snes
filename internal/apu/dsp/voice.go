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
}

func (v *Voice) Reset() {
	v.VOLL = 0
	v.VOLR = 0
	v.PitchCounter = 0
}

// Render produces a sample pair (L, R) for this voice.
func (v *Voice) Render() (int32, int32) {
	// Stub: Return 0
	return 0, 0
}
