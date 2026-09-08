package libretro

import (
	"errors"
	"unsafe"
)

// SetInitialWRAM fills the SNES core's 128 KiB system RAM for a controlled
// startup experiment. It requires a loaded game that has not run yet. This
// overrides the core's native power-on RAM contents and must be reported as
// a test precondition, not as evidence about the core's default startup.
func (p *Bridge) SetInitialWRAM(value byte) error {
	if !p.gameLoaded || p.hasRun {
		return errors.New("initial wram requires a loaded game before first run")
	}
	const size = 128 * 1024
	if p.retroGetMemorySize(2) != size {
		return errors.New("initial wram requires 128 KiB system ram")
	}
	ptr := p.retroGetMemoryData(2)
	if ptr == nil {
		return errors.New("initial wram is unavailable")
	}
	ram := unsafe.Slice((*byte)(ptr), size)
	for i := range ram {
		ram[i] = value
	}
	return nil
}
