package dsp

// VoiceState captures a voice, including its active envelope and BRR decoder.
// Its exported fields survive the gob encoding used by system save states.
type VoiceState struct {
	VOLL         int8
	VOLR         int8
	P            uint16
	SRCN         uint8
	ADSR1        uint8
	ADSR2        uint8
	GAIN         uint8
	ENVX         uint8
	OUTX         uint8
	PitchCounter uint16
	SamplePtr    uint16
	Phase        uint32
	Envelope     int16
	EnvCounter   int
	HiddenEnv    int16
	Keyed        bool
	EnvMode      envelopeMode
	BRRAddr      uint16
	BRRLoopAddr  uint16
	BRRNibblePos int
	BRRDecoded   [16]int16
	BRRHist1     int16
	BRRHist2     int16
	BRRLoop      bool
	BRREnd       bool
	BRREnded     bool
	SampleHist   [4]int16
	ADSRPending  bool
	GainPending  bool
	PrevOutput   int16
	UseNoise     bool
	Primed       bool
	KONDelay     uint8
}

func (v *Voice) saveState() VoiceState {
	return VoiceState{
		VOLL:         v.VOLL,
		VOLR:         v.VOLR,
		P:            v.P,
		SRCN:         v.SRCN,
		ADSR1:        v.ADSR1,
		ADSR2:        v.ADSR2,
		GAIN:         v.GAIN,
		ENVX:         v.ENVX,
		OUTX:         v.OUTX,
		PitchCounter: v.PitchCounter,
		SamplePtr:    v.SamplePtr,
		Phase:        v.phase,
		Envelope:     v.envelope,
		EnvCounter:   v.envCounter,
		HiddenEnv:    v.hiddenEnv,
		Keyed:        v.keyed,
		EnvMode:      v.envMode,
		BRRAddr:      v.brrAddr,
		BRRLoopAddr:  v.brrLoopAddr,
		BRRNibblePos: v.brrNibblePos,
		BRRDecoded:   v.brrDecoded,
		BRRHist1:     v.brrHist1,
		BRRHist2:     v.brrHist2,
		BRRLoop:      v.brrLoop,
		BRREnd:       v.brrEnd,
		BRREnded:     v.brrEnded,
		SampleHist:   v.sampleHist,
		ADSRPending:  v.adsrPending,
		GainPending:  v.gainPending,
		PrevOutput:   v.prevOutput,
		UseNoise:     v.useNoise,
		Primed:       v.primed,
		KONDelay:     v.konDelay,
	}
}

func (v *Voice) loadState(s VoiceState) {
	*v = Voice{
		VOLL:         s.VOLL,
		VOLR:         s.VOLR,
		P:            s.P,
		SRCN:         s.SRCN,
		ADSR1:        s.ADSR1,
		ADSR2:        s.ADSR2,
		GAIN:         s.GAIN,
		ENVX:         s.ENVX,
		OUTX:         s.OUTX,
		PitchCounter: s.PitchCounter,
		SamplePtr:    s.SamplePtr,
		phase:        s.Phase,
		envelope:     s.Envelope,
		envCounter:   s.EnvCounter,
		hiddenEnv:    s.HiddenEnv,
		keyed:        s.Keyed,
		envMode:      s.EnvMode,
		brrAddr:      s.BRRAddr,
		brrLoopAddr:  s.BRRLoopAddr,
		brrNibblePos: s.BRRNibblePos,
		brrDecoded:   s.BRRDecoded,
		brrHist1:     s.BRRHist1,
		brrHist2:     s.BRRHist2,
		brrLoop:      s.BRRLoop,
		brrEnd:       s.BRREnd,
		brrEnded:     s.BRREnded,
		sampleHist:   s.SampleHist,
		adsrPending:  s.ADSRPending,
		gainPending:  s.GainPending,
		prevOutput:   s.PrevOutput,
		useNoise:     s.UseNoise,
		primed:       s.Primed,
		konDelay:     s.KONDelay,
	}
}
