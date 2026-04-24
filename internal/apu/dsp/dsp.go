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
	FLG  uint8 // bits 0-4: Noise, 5: Echo disable, 6: Mute, 7: Reset
	DIR  uint8 // Sample directory base ($5D)
	EFB  uint8 // Echo feedback ($0D)
	EON  uint8 // Echo enable per voice ($4D)
	ESA  uint8 // Echo buffer start address high byte ($6D)
	EDL  uint8 // Echo delay ($7D)
	PMON uint8 // Pitch modulation enable per voice ($2D)
	NON  uint8 // Noise enable per voice ($3D)

	// FIR 8-tap echo filter coefficients ($xF for x in 0..7).
	FIR [8]int8

	// Output Buffer (Accumulator)
	SampleBuffer []int16

	// noise holds the 15-bit LFSR state; noiseCounter is a free-running
	// counter that fires the LFSR shift when it coincides with the rate-
	// table boundary selected by FLG bits 0-4. The LFSR advances by exactly
	// one bit per output sample tick, matching hardware.
	noise        uint16
	noiseCounter int

	// echoHist is the rolling 8-sample stereo history consumed by the FIR
	// filter. echoHistPos is the write index; the FIR reads from
	// echoHistPos+1..echoHistPos+8 (wrapped) for taps 0..7.
	echoHist    [8][2]int16
	echoHistPos int

	ramRead   func(uint16) uint8
	ramWrite  func(uint16, uint8)
	echoIndex uint16
}

func New() *DSP {
	d := &DSP{
		SampleBuffer: make([]int16, 2), // L/R
	}
	d.noise = 0x4000
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

// Write sets the value of a DSP register. The DSP register file is the
// authoritative live source for per-voice ADSR1/ADSR2/GAIN: the envelope step
// always re-reads the register file rather than a KON-time snapshot, so the
// S-CPU write ordering visible at the register file is the same ordering the
// envelope engine observes. The adsrPending latch records that ADSR1 was
// written in the current sample window so tests can inspect whether a
// subsequent GAIN write reached the envelope engine before the next step.
func (d *DSP) Write(addr uint8, val uint8) {
	d.RAM[addr&0x7F] = val

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
			// Mark that an ADSR1 write landed this window; cleared by a
			// subsequent GAIN write (register 0x07) or by the envelope
			// step consuming it at the next sample boundary.
			v.adsrPending = true
		case 0x06:
			v.ADSR2 = val
		case 0x07:
			v.GAIN = val
			// GAIN write after ADSR1 cancels the pending mode-switch
			// latch: the live register file now reflects the CPU's intent.
			v.adsrPending = false
		case 0x08:
			v.ENVX = val // Read-only usually; preserved for save-state parity.
		case 0x09:
			v.OUTX = val // Read-only usually.
		case 0x0F:
			d.FIR[voiceIdx] = int8(val)
		}
	}

	// Global Registers
	switch reg {
	case 0x0C:
		d.MVOLL = int8(val)
	case 0x1C:
		d.MVOLR = int8(val)
	case 0x2C:
		d.EVOLL = int8(val)
	case 0x2D:
		// Voice 0 has no predecessor; its PMON bit is always ignored.
		d.PMON = val & 0xFE
	case 0x3C:
		d.EVOLR = int8(val)
	case 0x3D:
		d.NON = val
		for i := 0; i < 8; i++ {
			d.Voices[i].useNoise = (val & (1 << i)) != 0
		}
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

// echoBufferSizeBytes returns the echo buffer length in bytes.
//
// EDL=0 yields a 4-byte window (the DSP still performs one stereo read/write
// pair — this is the "EDL=0 overwrites $0000-$0003" quirk described in
// design_doc §5.4). Non-zero EDL gives delay*0x800 bytes.
func (d *DSP) echoBufferSizeBytes() uint16 {
	delay := uint16(d.EDL & 0x0F)
	if delay == 0 {
		return 4
	}
	return delay * 0x800
}

// stepNoise advances the 15-bit noise LFSR by exactly one bit per call. The
// feedback taps match the S-DSP ROM behaviour used by bsnes/snes9x. The
// counter/rate gating that decides whether to call stepNoise on a given
// sample is handled by Sample().
func (d *DSP) stepNoise() {
	// feedback = (noise << 13) ^ (noise << 14) (taps 13 and 14 XORed).
	fb := (d.noise << 13) ^ (d.noise << 14)
	d.noise = (fb & 0x4000) | (d.noise >> 1)
}

// noiseSample returns the current noise output: the 15-bit LFSR value shifted
// left by one (i.e. sign-extended to 16-bit range). Because stepNoise only
// shifts one bit per sample, consecutive noise outputs differ by at most one
// low-bit toggle — the hardware's naturally highpass-filtered character.
// Matches bsnes `(int16_t)(m.noise * 2)`.
func (d *DSP) noiseSample() int16 {
	return int16(d.noise << 1)
}

// counterRates is the 32-entry noise-rate / envelope-rate period table used
// by the S-DSP. Index 0 (rate 0) never fires; indices 1..30 are the table
// from bsnes dsp/SPC_DSP.cpp `counter_rates`; index 31 fires every sample.
var counterRates = [32]int{
	0x7FFFFFFF,
	2048, 1536, 1280, 1024, 768, 640, 512,
	384, 320, 256, 192, 160, 128, 96, 80,
	64, 48, 40, 32, 24, 20, 16, 12,
	10, 8, 6, 5, 4, 3, 2, 1,
}

// Sample generates one sample pair (L, R)
func (d *DSP) Sample() (int16, int16) {
	if (d.FLG & 0x40) != 0 {
		return 0, 0
	}

	// Advance the noise LFSR at most once per sample, gated by the noise-
	// rate table. rate=0 is "never fires", rate=31 fires every sample.
	noiseRate := d.FLG & 0x1F
	if noiseRate != 0 {
		period := counterRates[noiseRate]
		d.noiseCounter++
		if d.noiseCounter >= period {
			d.noiseCounter = 0
			d.stepNoise()
		}
	}
	noise := d.noiseSample()

	var outL, outR int32
	var echoInL, echoInR int32

	// Voice render loop. PMON voice N reads the N-1 voice's prevOutput (if
	// its PMON bit is set) and scales its own pitch accordingly.
	for i := 0; i < 8; i++ {
		v := &d.Voices[i]
		v.stepEnvelope()
		// Envelope step consumed the pending ADSR1 write.
		v.adsrPending = false

		pitch := v.P
		if i > 0 && (d.PMON&(1<<i)) != 0 {
			factor := int32(d.Voices[i-1].prevOutput) >> 5
			delta := (factor * int32(pitch)) >> 10
			newPitch := int32(pitch) + delta
			if newPitch < 0 {
				newPitch = 0
			}
			if newPitch > 0x3FFF {
				newPitch = 0x3FFF
			}
			pitch = uint16(newPitch)
		}

		l, r := v.renderWith(pitch, d.ramRead, noise)
		outL += l
		outR += r
		if (d.EON & (1 << i)) != 0 {
			echoInL += l
			echoInR += r
		}
	}

	// Echo FIR. Read the oldest pending echo sample, shift into history,
	// convolve with the 8 coefficients applying the double-clip pattern
	// (clip after tap 7, clip after tap 8). Then feed back and write out.
	echoAddrBase := uint16(d.ESA) << 8
	echoAddr := echoAddrBase + d.echoIndex

	echoL := d.readEchoSample(echoAddr)
	echoR := d.readEchoSample(echoAddr + 2)
	d.echoHistPos = (d.echoHistPos + 1) & 7
	d.echoHist[d.echoHistPos][0] = echoL >> 1
	d.echoHist[d.echoHistPos][1] = echoR >> 1

	// FIR taps: tap i uses history slot (echoHistPos+1+i) & 7, coefficient
	// FIR[i] with >>6 shift (matching bsnes CALC_FIR).
	var firL, firR int32
	for i := 0; i < 7; i++ {
		slot := (d.echoHistPos + 1 + i) & 7
		firL += (int32(d.echoHist[slot][0]) * int32(d.FIR[i])) >> 6
		firR += (int32(d.echoHist[slot][1]) * int32(d.FIR[i])) >> 6
	}
	// First clip: truncate to 16 bits before the last tap so the 8th tap
	// adds into a clipped accumulator (FIR 8-tap clipped-sum quirk).
	firL = int32(int16(firL))
	firR = int32(int16(firR))
	slot := (d.echoHistPos + 1 + 7) & 7
	firL += (int32(d.echoHist[slot][0]) * int32(d.FIR[7])) >> 6
	firR += (int32(d.echoHist[slot][1]) * int32(d.FIR[7])) >> 6
	// Second clip: saturate to 15-bit range as the echo input to the main mix.
	firL = int32(clampSample16(firL)) & ^int32(1)
	firR = int32(clampSample16(firR)) & ^int32(1)

	// Mix filtered echo into main output.
	outL += (firL * int32(d.EVOLL)) >> 7
	outR += (firR * int32(d.EVOLR)) >> 7

	// Echo feedback write. The EDL=0 case still writes — to the 4-byte
	// window at ESA<<8, clobbering the DSP register file in APU RAM per
	// the hardware quirk.
	if (d.FLG & 0x20) == 0 {
		feedback := int32(int8(d.EFB))
		writeL := clampSample16(echoInL + ((firL * feedback) >> 7))
		writeR := clampSample16(echoInR + ((firR * feedback) >> 7))
		d.writeEchoSample(echoAddr, writeL)
		d.writeEchoSample(echoAddr+2, writeR)
	}

	d.echoIndex += 4
	if d.echoIndex >= d.echoBufferSizeBytes() {
		d.echoIndex = 0
	}

	// Apply Master Volume
	outL = (outL * int32(d.MVOLL)) >> 7
	outR = (outR * int32(d.MVOLR)) >> 7

	return clampSample16(outL), clampSample16(outR)
}
