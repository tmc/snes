package snes

import (
	"errors"

	"github.com/tmc/snes/emulator"
)

var _ emulator.Memory = (*System)(nil)

// ReadWRAMAt copies work RAM bytes starting at off into p.
func (s *System) ReadWRAMAt(p []byte, off int64) (int, error) {
	if s.wram == nil {
		return 0, errors.New("read wram: system not initialized")
	}
	return s.wram.ReadAt(p, off)
}
