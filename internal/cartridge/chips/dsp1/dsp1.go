package dsp1

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

type MapType uint8

const (
	MapLoROMSmall MapType = iota
	MapLoROMLarge
	MapHiROM
)

// Device emulates the DSP-1 coprocessor's bus-facing command/parameter/result
// state machine. Behaviour mirrors snes9x dsp1.cpp DSP1SetByte/DSP1GetByte:
// even addresses access the data port; odd addresses always read 0x80 and
// ignore writes. Op math (Op 02/0A/06/04 etc.) is not implemented in this
// slice; unknown commands fall through to the waiting-for-command state.
type Device struct {
	MapType MapType

	command         uint8
	waiting4command bool
	firstParameter  bool
	parameters      [16]uint8
	inIndex         uint8
	inCount         uint8
	output          [32]uint8
	outIndex        uint8
	outCount        uint16
}

func New() *Device {
	d := &Device{}
	d.reset()
	return d
}

func (d *Device) reset() {
	d.command = 0
	d.waiting4command = true
	d.firstParameter = true
	d.inIndex = 0
	d.inCount = 0
	d.outIndex = 0
	d.outCount = 0
	for i := range d.parameters {
		d.parameters[i] = 0
	}
	for i := range d.output {
		d.output[i] = 0
	}
}

func (d *Device) SetMapType(mapType MapType) {
	d.MapType = mapType
}

func (d *Device) mapped(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	off := uint16(addr & 0xFFFF)
	switch d.MapType {
	case MapLoROMLarge:
		if !((bank >= 0x60 && bank <= 0x6F) || (bank >= 0xE0 && bank <= 0xEF)) {
			return false
		}
		return off <= 0x7FFF
	case MapHiROM:
		if !((bank >= 0x00 && bank <= 0x1F) || (bank >= 0x80 && bank <= 0x9F)) {
			return false
		}
		return off >= 0x6000 && off <= 0x7FFF
	default:
		if !((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) {
			return false
		}
		return off >= 0x8000 && off <= 0xFFFF
	}
}

func (d *Device) Read(addr uint32) (uint8, bool) {
	if !d.mapped(addr) {
		return 0, false
	}
	if (addr & 1) != 0 {
		return 0x80, true
	}
	return d.getByte(), true
}

func (d *Device) Write(addr uint32, val uint8) bool {
	if !d.mapped(addr) {
		return false
	}
	if (addr & 1) == 0 {
		d.setByte(val)
		return true
	}
	return false
}

// getByte mirrors snes9x DSP1GetByte. Returns 0x80 when no result bytes are
// queued; otherwise drains the output buffer one byte at a time. Op 0A/1A
// re-loads its raster output and Op 1F re-fills from DSP1ROM, but those
// branches are stage-3 work; this slice keeps the empty-queue path only.
func (d *Device) getByte() uint8 {
	if d.outCount == 0 {
		return 0x80
	}
	t := d.output[d.outIndex]
	d.outIndex++
	d.outCount--
	if d.outCount == 0 {
		d.waiting4command = true
	}
	return t
}

// setByte mirrors snes9x DSP1SetByte. Either accepts a new command byte and
// programs the parameter byte count, or stores a parameter byte and triggers
// command execution when the parameter buffer fills.
func (d *Device) setByte(b uint8) {
	if d.waiting4command {
		d.command = b
		d.inIndex = 0
		d.waiting4command = false
		d.firstParameter = true
		d.inCount = paramWordCount(b)
		// snes9x rewrites aliases for 0x?A and 0x17/37/3F.
		switch b {
		case 0x1a, 0x2a, 0x3a:
			d.command = 0x1a
		case 0x17, 0x37, 0x3f:
			d.command = 0x1f
		}
		if d.inCount == 0 {
			// snes9x default + case 0x80: no-op, return to waiting4command.
			d.waiting4command = true
			d.firstParameter = true
		}
		d.inCount <<= 1 // word count -> byte count
		// Command byte itself does not consume a parameter slot; snes9x
		// passes the post-switch first_parameter && in_count!=0 silent
		// clause for this case.
		return
	}
	d.parameters[d.inIndex] = b
	wasFirst := d.firstParameter
	d.firstParameter = false
	d.inIndex++
	if wasFirst && b == 0x80 {
		// snes9x dsp1.cpp:1244 escape: bare 0x80 mid-stream returns to wait.
		d.waiting4command = true
		d.firstParameter = false
		return
	}
	if d.inCount > 0 {
		d.inCount--
		if d.inCount == 0 {
			d.waiting4command = true
			d.outIndex = 0
			d.execute()
		}
	}
}

// paramWordCount returns the parameter word count for a command byte, or 0
// for unknown / 0x80 / pure-status commands. Mirrors the switch in snes9x
// DSP1SetByte (dsp1.cpp:1154+). Word counts; setByte shifts to bytes.
func paramWordCount(b uint8) uint8 {
	switch b {
	case 0x00, 0x10, 0x20, 0x30, 0x04, 0x24, 0x0e, 0x1e, 0x2e, 0x3e:
		return 2
	case 0x08, 0x28, 0x06, 0x16, 0x26, 0x36, 0x0c, 0x2c, 0x0d, 0x09, 0x39, 0x3d,
		0x1d, 0x19, 0x2d, 0x29, 0x03, 0x33, 0x13, 0x23, 0x0b, 0x3b, 0x1b, 0x2b:
		return 3
	case 0x18, 0x38, 0x01, 0x05, 0x35, 0x31, 0x11, 0x15, 0x21, 0x25:
		return 4
	case 0x1c, 0x3c, 0x14, 0x34:
		return 6
	case 0x02, 0x12, 0x22, 0x32:
		return 7
	case 0x0a, 0x1a, 0x2a, 0x3a, 0x07, 0x0f, 0x17, 0x27, 0x2f, 0x37, 0x3f, 0x1f:
		return 1
	default:
		return 0
	}
}

// execute dispatches the completed command. Stage-3 adds Op 0x04/0x24
// (Sin/Cos*radius). Other math ops remain no-ops until subsequent slices.
func (d *Device) execute() {
	switch d.command {
	case 0x04, 0x24:
		// snes9x dsp1.cpp DSP1_Op04:
		//   Op04Angle  = (int16) READ_WORD(&parameters[0])
		//   Op04Radius = (uint16)READ_WORD(&parameters[2])
		//   Op04Sin = DSP1_Sin(angle) * radius >> 15
		//   Op04Cos = DSP1_Cos(angle) * radius >> 15
		//   out_count = 4; output[0..1]=Sin, output[2..3]=Cos.
		angle := int16(uint16(d.parameters[0]) | uint16(d.parameters[1])<<8)
		radius := uint16(d.parameters[2]) | uint16(d.parameters[3])<<8
		sin := int16(int32(sinFP(angle)) * int32(radius) >> 15)
		cos := int16(int32(cosFP(angle)) * int32(radius) >> 15)
		d.output[0] = uint8(uint16(sin) & 0xff)
		d.output[1] = uint8(uint16(sin) >> 8)
		d.output[2] = uint8(uint16(cos) & 0xff)
		d.output[3] = uint8(uint16(cos) >> 8)
		d.outCount = 4
	case 0x0f, 0x07, 0x2f, 0x27:
		// Identity / status. snes9x writes the version word; we leave it as
		// no-op until a downstream gate needs it.
		d.outCount = 0
	default:
		d.outCount = 0
	}
}

func (d *Device) Step(masterCycles uint64) {}

type state struct {
	MapType         MapType
	Command         uint8
	Waiting4command bool
	FirstParameter  bool
	Parameters      [16]uint8
	InIndex         uint8
	InCount         uint8
	Output          [32]uint8
	OutIndex        uint8
	OutCount        uint16
}

func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		MapType:         d.MapType,
		Command:         d.command,
		Waiting4command: d.waiting4command,
		FirstParameter:  d.firstParameter,
		Parameters:      d.parameters,
		InIndex:         d.inIndex,
		InCount:         d.inCount,
		Output:          d.output,
		OutIndex:        d.outIndex,
		OutCount:        d.outCount,
	}); err != nil {
		return nil, fmt.Errorf("serialize dsp1: %w", err)
	}
	return buf.Bytes(), nil
}

func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize dsp1: %w", err)
	}
	d.MapType = s.MapType
	d.command = s.Command
	d.waiting4command = s.Waiting4command
	d.firstParameter = s.FirstParameter
	d.parameters = s.Parameters
	d.inIndex = s.InIndex
	d.inCount = s.InCount
	d.output = s.Output
	d.outIndex = s.OutIndex
	d.outCount = s.OutCount
	return nil
}
