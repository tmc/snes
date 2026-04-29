package dsp

// DSPState captures the serializable DSP state.
type DSPState struct {
	RAM [128]uint8

	Voices [8]Voice

	MVOLL int8
	MVOLR int8
	EVOLL int8
	EVOLR int8
	KON   uint8
	KOFF  uint8
	ENDX  uint8
	FLG   uint8
	DIR   uint8
	EFB   uint8
	EON   uint8
	ESA   uint8
	EDL   uint8

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
		FLG:          d.FLG,
		DIR:          d.DIR,
		EFB:          d.EFB,
		EON:          d.EON,
		ESA:          d.ESA,
		EDL:          d.EDL,
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
	d.FLG = state.FLG
	d.DIR = state.DIR
	d.EFB = state.EFB
	d.EON = state.EON
	d.ESA = state.ESA
	d.EDL = state.EDL
	d.echoIndex = state.EchoIndex
	d.SampleBuffer = append(d.SampleBuffer[:0], state.SampleBuffer...)
	if d.SampleBuffer == nil {
		d.SampleBuffer = make([]int16, 2)
	}
}
