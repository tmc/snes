package dsp

// DSPState captures the serializable DSP state.
type DSPState struct {
	RAM [128]uint8

	Voices [8]Voice

	MVOLL int8
	MVOLR int8
	KON   uint8
	KOFF  uint8
	FLG   uint8

	SampleBuffer []int16
}

// SaveState returns a snapshot of the DSP state.
func (d *DSP) SaveState() DSPState {
	return DSPState{
		RAM:          d.RAM,
		Voices:       d.Voices,
		MVOLL:        d.MVOLL,
		MVOLR:        d.MVOLR,
		KON:          d.KON,
		KOFF:         d.KOFF,
		FLG:          d.FLG,
		SampleBuffer: append([]int16(nil), d.SampleBuffer...),
	}
}

// LoadState restores a previously saved DSP state.
func (d *DSP) LoadState(state DSPState) {
	d.RAM = state.RAM
	d.Voices = state.Voices
	d.MVOLL = state.MVOLL
	d.MVOLR = state.MVOLR
	d.KON = state.KON
	d.KOFF = state.KOFF
	d.FLG = state.FLG
	d.SampleBuffer = append(d.SampleBuffer[:0], state.SampleBuffer...)
	if d.SampleBuffer == nil {
		d.SampleBuffer = make([]int16, 2)
	}
}
