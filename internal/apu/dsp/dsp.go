package dsp

// DSP represents the Sony SPC700 Digital Signal Processor.
// It manages 8 voices, echo, and main volume.
type DSP struct {
	RAM [128]uint8 // Internal registers (0x00-0x7F)

	Voices [8]Voice

	// Master Volume
	MVOLL int8
	MVOLR int8
	EVOLL int8
	EVOLR int8

	// Key On/Off
	KON  uint8
	KOFF uint8

	// Flags
	FLG uint8 // bits 0-4: Noise, 5: Echo disable, 6: Mute, 7: Reset
	DIR uint8 // Sample directory base ($5D)
	EFB uint8 // Echo feedback ($0D)
	EON uint8 // Echo enable per voice ($4D)
	ESA uint8 // Echo buffer start address high byte ($6D)
	EDL uint8 // Echo delay ($7D)

	// Output Buffer (Accumulator)
	SampleBuffer []int16

	ramRead   func(uint16) uint8
	ramWrite  func(uint16, uint8)
	echoIndex uint16
}

func New() *DSP {
	d := &DSP{
		SampleBuffer: make([]int16, 2), // L/R
	}
	return d
}

func (d *DSP) SetRAMReader(read func(uint16) uint8) {
	d.ramRead = read
}

func (d *DSP) SetRAMWriter(write func(uint16, uint8)) {
	d.ramWrite = write
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
	case 0x0D:
		d.EFB = val
	case 0x0C:
		d.MVOLL = int8(val)
	case 0x1C:
		d.MVOLR = int8(val)
	case 0x2C:
		d.EVOLL = int8(val)
	case 0x3C:
		d.EVOLR = int8(val)
	case 0x4C:
		d.KON = val
		d.handleKeyOn(val)
	case 0x4D:
		d.EON = val
	case 0x5C:
		d.KOFF = val
		d.handleKeyOff(val)
	case 0x5D:
		d.DIR = val
	case 0x6C:
		d.FLG = val
	case 0x6D:
		d.ESA = val
	case 0x7D:
		d.EDL = val & 0x0F
	}
}

func (d *DSP) handleKeyOn(val uint8) {
	for i := 0; i < 8; i++ {
		if (val & (1 << i)) != 0 {
			d.Voices[i].KeyOn(d.ramRead, d.DIR)
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

func clampSample16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func (d *DSP) readEchoSample(addr uint16) int16 {
	if d.ramRead == nil {
		return 0
	}
	lo := uint16(d.ramRead(addr))
	hi := uint16(d.ramRead(addr + 1))
	return int16((hi << 8) | lo)
}

func (d *DSP) writeEchoSample(addr uint16, sample int16) {
	if d.ramWrite == nil {
		return
	}
	d.ramWrite(addr, uint8(sample))
	d.ramWrite(addr+1, uint8(uint16(sample)>>8))
}

func (d *DSP) echoBufferSizeBytes() uint16 {
	delay := uint16(d.EDL & 0x0F)
	if delay == 0 {
		return 0
	}
	return delay * 0x800
}

// Sample generates one sample pair (L, R)
func (d *DSP) Sample() (int16, int16) {
	if (d.FLG & 0x40) != 0 {
		return 0, 0
	}

	var outL, outR int32
	var echoInL, echoInR int32
	echoAddrBase := uint16(d.ESA) << 8
	echoAddr := echoAddrBase + d.echoIndex

	// Mix Voices
	for i := 0; i < 8; i++ {
		l, r := d.Voices[i].Render(d.ramRead)
		outL += l
		outR += r
		if (d.EON & (1 << i)) != 0 {
			echoInL += l
			echoInR += r
		}
	}

	if d.echoBufferSizeBytes() != 0 {
		// Stored echo sample is signed 16-bit stereo.
		echoL := int32(d.readEchoSample(echoAddr))
		echoR := int32(d.readEchoSample(echoAddr + 2))

		outL += (echoL * int32(d.EVOLL)) >> 7
		outR += (echoR * int32(d.EVOLR)) >> 7

		if (d.FLG & 0x20) == 0 {
			feedback := int32(int8(d.EFB))
			writeL := clampSample16(echoInL + ((echoL * feedback) >> 7))
			writeR := clampSample16(echoInR + ((echoR * feedback) >> 7))
			d.writeEchoSample(echoAddr, writeL)
			d.writeEchoSample(echoAddr+2, writeR)
		}

		d.echoIndex += 4
		if d.echoIndex >= d.echoBufferSizeBytes() {
			d.echoIndex = 0
		}
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
