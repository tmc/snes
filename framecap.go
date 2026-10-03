package snes

import (
	"errors"

	"github.com/tmc/snes/internal/ppu"
)

// PixelFormatBGR555 is the pixel format of captured frames: one uint16
// per pixel, bits 0-4 red, 5-9 green, 10-14 blue, bit 15 zero. It is
// serialized little-endian.
const PixelFormatBGR555 = "bgr555le"

// A Frame is a copy of a completed frame, taken when the beam entered
// vertical blank after the frame's last visible line was rendered.
//
// A Frame owns its slices and is never modified after it is delivered.
//
// Cycles are master clocks on the CPU cycle timeline, so they can be
// compared with the entry cycles of observed instructions. Frame N
// starts at Start and ends where frame N+1 starts; its pixels were
// complete at VBlank.
//
// Width is 512 if any line was rendered in hires (BG mode 5 or 6), in
// which case each pixel of a 256-pixel line is doubled; otherwise it is
// 256. Height is 224, or 240 with overscan. In interlace mode, a Frame
// holds the single field the renderer produced; Field says which.
// Pseudo-hires is not rendered and is reported in PseudoHires.
type Frame struct {
	Number    int
	Field     int
	Interlace bool
	Overscan  bool

	Width, Height int
	Format        string
	Pixels        []uint16

	// HiresLines reports which lines were rendered in hires.
	HiresLines  []bool
	PseudoHires bool

	// FirstLine is nonzero when the frame resumed from a state load;
	// see ppu.FrameInfo.
	FirstLine int

	Start, VBlank uint64

	// Diagnostic layer and palette traces when ppu.LayerTraceActive was enabled.
	LayerSourceTrace  []byte
	LayerPaletteTrace []byte
}

// A FrameBoundary is reported for every completed frame, whether or
// not it is captured.
type FrameBoundary struct {
	Number        int
	Start, VBlank uint64
}

// ErrFrameCaptureActive is returned by CaptureFrames when a capture is
// already active.
var ErrFrameCaptureActive = errors.New("frame capture already active")

// CaptureFrames calls keep with the boundary of each completed frame and,
// if keep returns true, calls sink with a copy of that frame. Both run
// on the emulation goroutine and must not call back into s.
//
// Capture does not change emulation. Frames produced by run-ahead
// speculation are not reported. The returned function stops capture.
func (s *System) CaptureFrames(keep func(FrameBoundary) bool, sink func(*Frame)) (stop func(), err error) {
	if keep == nil || sink == nil {
		return nil, errors.New("nil frame capture function")
	}
	if s.PPU.FrameHook != nil {
		return nil, ErrFrameCaptureActive
	}
	p := s.PPU
	hook := func(fi *ppu.FrameInfo) {
		if !keep(FrameBoundary{Number: fi.Number, Start: fi.Start, VBlank: fi.VBlank}) {
			return
		}
		sink(newFrame(p, fi))
	}
	p.FrameHook = hook
	return func() { p.FrameHook = nil }, nil
}

func newFrame(p *ppu.PPU, fi *ppu.FrameInfo) *Frame {
	f := &Frame{
		Number:      fi.Number,
		Interlace:   fi.Interlace,
		Overscan:    fi.Overscan,
		Width:       256,
		Height:      fi.Height,
		Format:      PixelFormatBGR555,
		HiresLines:  append([]bool(nil), fi.Hires[:fi.Height]...),
		PseudoHires: fi.PseudoHires,
		FirstLine:   fi.FirstLine,
		Start:       fi.Start,
		VBlank:      fi.VBlank,
	}
	if fi.Field {
		f.Field = 1
	}
	if fi.AnyHires() {
		f.Width = 512
	}
	f.Pixels = p.CopyFrame(make([]uint16, 0, f.Width*f.Height), fi)
	if p.LayerTraceActive {
		h := fi.Height
		if h > 240 {
			h = 240
		}
		sources := make([]byte, 256*h)
		palettes := make([]byte, 256*h)
		for y := 0; y < h; y++ {
			copy(sources[y*256:(y+1)*256], p.LayerSourceTrace[y][:256])
			copy(palettes[y*256:(y+1)*256], p.LayerPaletteTrace[y][:256])
		}
		f.LayerSourceTrace = sources
		f.LayerPaletteTrace = palettes
	}
	return f
}
