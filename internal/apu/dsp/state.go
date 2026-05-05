package dsp

// DSPState captures the serializable DSP state.
type DSPState struct {
	RAM [128]uint8

	Voices [8]Voice

	MVOLL    int8
	MVOLR    int8
	EVOLL    int8
	EVOLR    int8
	KON      uint8
	KOFF     uint8
	ENDX     uint8
	KeyEvent [8]keyEvent
	FLG      uint8
	DIR      uint8
	EFB      uint8
	EON      uint8
	ESA      uint8
	EDL      uint8
	PMON     uint8
	NON      uint8

	FIR [8]int8

	Noise        uint16
	NoiseCounter int
	EchoHist     [8][2]int16
	EchoHistPos  int

	EchoIndex uint16

	SampleBuffer []int16
}

// SaveState returns a snapshot of the DSP state.
func (d *DSP) SaveState() DSPState {
	return DSPState{
		RAM:          d.RAM,
		Voices:       d.Voices,
		MVOLL:        d.MVOLL,
		MVOLR:        d.MVOLR,
		EVOLL:        d.EVOLL,
		EVOLR:        d.EVOLR,
		KON:          d.KON,
		KOFF:         d.KOFF,
		ENDX:         d.ENDX,
		KeyEvent:     d.keyEvent,
		FLG:          d.FLG,
		DIR:          d.DIR,
		EFB:          d.EFB,
		EON:          d.EON,
		ESA:          d.ESA,
		EDL:          d.EDL,
		PMON:         d.PMON,
		NON:          d.NON,
		FIR:          d.FIR,
		Noise:        d.noise,
		NoiseCounter: d.noiseCounter,
		EchoHist:     d.echoHist,
		EchoHistPos:  d.echoHistPos,
		EchoIndex:    d.echoIndex,
		SampleBuffer: append([]int16(nil), d.SampleBuffer...),
	}
}

// LoadState restores a previously saved DSP state.
func (d *DSP) LoadState(state DSPState) {
	d.RAM = state.RAM
	d.Voices = state.Voices
	d.MVOLL = state.MVOLL
	d.MVOLR = state.MVOLR
	d.EVOLL = state.EVOLL
	d.EVOLR = state.EVOLR
	d.KON = state.KON
	d.KOFF = state.KOFF
	d.ENDX = state.ENDX
	d.keyEvent = state.KeyEvent
	d.FLG = state.FLG
	d.DIR = state.DIR
	d.EFB = state.EFB
	d.EON = state.EON
	d.ESA = state.ESA
	d.EDL = state.EDL
	d.PMON = state.PMON
	d.NON = state.NON
	d.FIR = state.FIR
	d.noise = state.Noise
	d.noiseCounter = state.NoiseCounter
	d.echoHist = state.EchoHist
	d.echoHistPos = state.EchoHistPos
	d.echoIndex = state.EchoIndex
	d.SampleBuffer = append(d.SampleBuffer[:0], state.SampleBuffer...)
	if d.SampleBuffer == nil {
		d.SampleBuffer = make([]int16, 2)
	}
}
