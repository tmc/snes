package spc

import (
	"encoding/binary"
	"fmt"
	"os"
)

// SPCData represents the content of an .spc file
type SPCData struct {
	// Header Info (optional)

	// Registers
	PC  uint16
	A   uint8
	X   uint8
	Y   uint8
	SP  uint8
	PSW uint8

	// Memory
	RAM [65536]byte

	// DSP Registers (128 bytes)
	DSPRAM [128]byte

	// IPL Enabled? Usually implied by RAM content at FFC0...
	// But valid SPC dumps often have RAM populated at FFC0 with IPL or Game Code.
	// Standard allows ignoring IPL ROM.
}

func Load(path string) (*SPCData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// SPC File Format:
	// 00000-00024: Header String "SNES-SPC700 Sound File Data v0.30" (37 bytes usually, header size 0x100)
	// 00025: PC (16-bit)
	// 00027: A (8-bit)
	// 00028: X (8-bit)
	// 00029: Y (8-bit)
	// 0002A: PSW (8-bit)
	// 0002B: SP (8-bit)
	// 0002C-000FF: Reserved / ID666 Tag
	// 00100-100FF: 64KB RAM
	// 10100-1017F: DSP Registers (128 bytes)
	// 10180-101BF: Unused (64 bytes)
	// 101C0-101FF: Extra RAM (IPL ROM) (64 bytes)

	data := make([]byte, 0x10200) // Read enough for registers + RAM + DSP
	// Minimum size: 0x100 (Header) + 0x10000 (RAM) + 0x80 (DSP) = 0x10180.

	n, err := f.Read(data)
	if err != nil {
		return nil, err
	}
	if n < 0x10180 {
		return nil, fmt.Errorf("invalid SPC file size: %d", n)
	}

	spc := &SPCData{}

	// Header String Check? Skip for now.

	// Registers
	spc.PC = binary.LittleEndian.Uint16(data[0x25:0x27])
	spc.A = data[0x27]
	spc.X = data[0x28]
	spc.Y = data[0x29]
	spc.PSW = data[0x2A]
	spc.SP = data[0x2B]

	// RAM
	copy(spc.RAM[:], data[0x100:0x10100])

	// DSP
	copy(spc.DSPRAM[:], data[0x10100:0x10180])

	return spc, nil
}
