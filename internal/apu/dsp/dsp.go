package dsp

// DSP represents the Sony SPC700 Digital Signal Processor.
// It manages 8 voices, echo, and main volume.
type DSP struct {
	RAM [128]uint8 // Internal registers (0x00-0x7F)

	Voices [8]Voice

	// Master Volume
	MVOLL int8
	MVOLR int8

	// Key On/Off
	KON  uint8
	KOFF uint8

	// Flags
	FLG uint8 // bits 0-4: Noise, 5: Echo, 6: Mute, 7: Reset

	// Output Buffer (Accumulator)
	SampleBuffer []int16
}

func New() *DSP {
	d := &DSP{
		SampleBuffer: make([]int16, 2), // L/R
	}
	return d
}

// Read returns the value of a DSP register.
func (d *DSP) Read(addr uint8) uint8 {
	return d.RAM[addr&0x7F]
}

// Write sets the value of a DSP register.
func (d *DSP) Write(addr uint8, val uint8) {
	d.RAM[addr&0x7F] = val

	// Identify register type
	// 00-7F: Voice/Global
	// Voices: 0x00-0x09 + (N*16)

	reg := addr & 0x7F
	voiceIdx := reg / 16
	voiceOffset := reg % 16

	if voiceIdx < 8 {
		v := &d.Voices[voiceIdx]
		switch voiceOffset {
		case 0x00:
			v.VOLL = int8(val)
		case 0x01:
			v.VOLR = int8(val)
		case 0x02:
			v.P = (v.P & 0xFF00) | uint16(val)
		case 0x03:
			v.P = (v.P & 0x00FF) | (uint16(val) << 8)
		case 0x04:
			v.SRCN = val
		case 0x05:
			v.ADSR1 = val
		case 0x06:
			v.ADSR2 = val
		case 0x07:
			v.GAIN = val
		case 0x08:
			v.ENVX = val // Read-only?
		case 0x09:
			v.OUTX = val // Read-only?
		}
	}

	// Global Registers
	switch reg {
	case 0x0C:
		d.MVOLL = int8(val)
	case 0x1C:
		d.MVOLR = int8(val)
	case 0x4C:
		d.KON = val
		d.handleKeyOn(val)
	case 0x5C:
		d.KOFF = val
		d.handleKeyOff(val)
	case 0x6C:
		d.FLG = val
	}
}

func (d *DSP) handleKeyOn(val uint8) {
	for i := 0; i < 8; i++ {
		if (val & (1 << i)) != 0 {
			d.Voices[i].KeyOn()
		}
	}
}

func (d *DSP) handleKeyOff(val uint8) {
	for i := 0; i < 8; i++ {
		if (val & (1 << i)) != 0 {
			d.Voices[i].KeyOff()
		}
	}
}

// Sample generates one sample pair (L, R)
func (d *DSP) Sample() (int16, int16) {
	if (d.FLG & 0x40) != 0 {
		return 0, 0
	}

	var outL, outR int32

	// Mix Voices
	for i := 0; i < 8; i++ {
		l, r := d.Voices[i].Render()
		outL += l
		outR += r
	}

	// Apply Master Volume
	outL = (outL * int32(d.MVOLL)) >> 7
	outR = (outR * int32(d.MVOLR)) >> 7

	// Clamp
	if outL > 32767 {
		outL = 32767
	}
	if outL < -32768 {
		outL = -32768
	}
	if outR > 32767 {
		outR = 32767
	}
	if outR < -32768 {
		outR = -32768
	}

	return int16(outL), int16(outR)
}
