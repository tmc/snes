package snes

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
)

// StateHashes returns deterministic hashes for major emulator components.
func (s *System) StateHashes() (map[string]string, error) {
	if s.wram == nil {
		return nil, errors.New("state hashes: system not initialized")
	}
	hashes := map[string]string{
		"cpu":         hashGob(s.CPU.SaveState()),
		"wram":        hashBytes(s.wram.Data()),
		"vram":        hashBytes(s.PPU.VRAM[:]),
		"cgram":       hashBytes(s.PPU.CGRAM[:]),
		"oam":         hashBytes(s.PPU.OAM[:]),
		"ppu":         hashGob(s.PPU.SaveState()),
		"dma":         hashGob(s.DMA.SaveState()),
		"apu":         hashGob(s.APU.SaveState()),
		"scheduler":   hashGob(s.Scheduler.SaveState()),
		"framebuffer": hashUint16LE(s.FrameBuffer()),
	}
	if sram := s.SaveRAM(); len(sram) > 0 {
		hashes["sram"] = hashBytes(sram)
	}
	return hashes, nil
}

func hashGob(v any) string {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return fmt.Sprintf("gob-error:%x", sha256.Sum256([]byte(err.Error())))
	}
	return hashBytes(buf.Bytes())
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashUint16LE(v []uint16) string {
	buf := make([]byte, 0, len(v)*2)
	for _, x := range v {
		buf = append(buf, byte(x), byte(x>>8))
	}
	return hashBytes(buf)
}
