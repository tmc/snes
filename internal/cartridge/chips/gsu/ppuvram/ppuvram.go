// Package ppuvram bridges the GSU pixel-cache commit path to the PPU's
// 64 KiB VRAM. Neither internal/cartridge/chips/gsu nor internal/ppu imports
// the other; this adapter sits above both so a cartridge wiring layer can
// install it via gsu.Device.SetVRAMWriter.
package ppuvram

import (
	"github.com/tmc/snes/internal/cartridge/chips/gsu"
	"github.com/tmc/snes/internal/ppu"
)

// Writer routes gsu.VRAMWriter.WriteTileRow calls onto a *ppu.PPU's VRAM,
// committing all eight bytes of a plotted tile row in one shot. Addresses
// are the word-byte offset bsnes' PLOT math produces and are masked to the
// 64 KiB VRAM window.
type Writer struct {
	PPU *ppu.PPU
}

// New constructs a Writer targeting p.
func New(p *ppu.PPU) *Writer { return &Writer{PPU: p} }

// WriteTileRow implements gsu.VRAMWriter. It stores row[0..7] at
// vramAddr..vramAddr+7, wrapping to the VRAM window. The GSU accumulates
// pixels in the cache and only reaches this path when a PLOT crosses a
// tile row — so each call commits a whole tile row per bsnes plot.cpp.
func (w *Writer) WriteTileRow(vramAddr uint16, row [8]byte) {
	for i, b := range row {
		w.PPU.WriteVRAM((vramAddr+uint16(i))&(ppu.VRAMSize-1), b)
	}
}

// Ensure Writer satisfies the gsu.VRAMWriter interface at compile time.
var _ gsu.VRAMWriter = (*Writer)(nil)
