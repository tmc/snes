// Package spc reads SPC700 v0.30 sound snapshots.
package spc

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// SPCData contains the processor registers, RAM and DSP register image.
type SPCData struct {
	PC               uint16
	A, X, Y, SP, PSW uint8
	RAM              [65536]byte
	DSPRAM           [128]byte
}

// Load reads an SPC700 v0.30 snapshot from path.
func Load(path string) (*SPCData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load spc: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

// Decode reads the required header, RAM and DSP registers from r. Optional
// trailing metadata and the extra IPL RAM image are not included in SPCData.
func Decode(r io.Reader) (*SPCData, error) {
	// SPC v0.30: header at 0, RAM at 0x100, DSP registers at 0x10100.
	var data [0x10180]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return nil, fmt.Errorf("decode spc: %w", err)
	}
	const signature = "SNES-SPC700 Sound File Data v0.30"
	if string(data[:len(signature)]) != signature || data[0x21] != 0x1a || data[0x22] != 0x1a {
		return nil, fmt.Errorf("decode spc: invalid v0.30 signature")
	}
	if data[0x23] != 0x1a && data[0x23] != 0x1b {
		return nil, fmt.Errorf("decode spc: invalid tag marker")
	}
	if data[0x24] != 30 {
		return nil, fmt.Errorf("decode spc: unsupported minor version %d", data[0x24])
	}
	out := &SPCData{
		PC: binary.LittleEndian.Uint16(data[0x25:0x27]),
		A:  data[0x27], X: data[0x28], Y: data[0x29], PSW: data[0x2a], SP: data[0x2b],
	}
	copy(out.RAM[:], data[0x100:0x10100])
	copy(out.DSPRAM[:], data[0x10100:])
	return out, nil
}
