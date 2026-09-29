package ppu

// FrameInfo describes the frame whose visible lines the PPU has just
// finished rendering.
//
// Cycles are PPU cycles, which count master clocks on the same timeline
// as the CPU cycle counter. The frame began at Start (line 0, dot 0) and
// its visible lines were complete at VBlank.
type FrameInfo struct {
	Number    int  // PPU frame counter
	Field     bool // PPU field flag during the frame
	Interlace bool // SETINI interlace, as latched at line 128
	Overscan  bool // SETINI overscan; Height is 240
	Height    int  // visible lines, 224 or 240

	Start, VBlank uint64

	// FirstLine is the first line rendered since Start, or since the
	// state load that resumed this frame. Lines above it were rendered
	// before the state was saved; their pixels are restored with the
	// state but their Hires flags are unknown and reported false.
	FirstLine int

	// Hires reports the lines rendered in BG mode 5 or 6.
	Hires [240]bool

	// PseudoHires reports that SETINI pseudo-hires was set on a
	// rendered line. The renderer does not implement it; such lines
	// are rendered at 256 pixels.
	PseudoHires bool
}

// AnyHires reports whether any rendered line is hires.
func (f *FrameInfo) AnyHires() bool {
	for _, h := range f.Hires[:f.Height] {
		if h {
			return true
		}
	}
	return false
}

// noteFrameLine records the rendering mode of line y.
func (p *PPU) noteFrameLine(y int) {
	if y < 0 || y >= len(p.frame.Hires) {
		return
	}
	if !p.frameStarted {
		p.frameStarted = true
		p.frame.FirstLine = y
	}
	mode := p.BGMode & 0x07
	lit := p.INIDISP&0x80 == 0
	p.frame.Hires[y] = lit && (mode == 5 || mode == 6)
	if lit && p.SETINI&0x08 != 0 {
		p.frame.PseudoHires = true
	}
}

// startFrame resets per-frame capture state at line 0.
func (p *PPU) startFrame() {
	p.frame = FrameInfo{}
	p.frameStarted = false
	p.frameDone = false
}

// completeFrame reports the frame to FrameHook once, at the first
// vblank entry of the frame.
func (p *PPU) completeFrame() {
	if p.frameDone {
		return
	}
	p.frameDone = true
	f := &p.frame
	f.Number = p.FrameCount
	f.Field = p.ppuField
	f.Interlace = p.ppuInterlace
	f.Overscan = p.SETINI&0x04 != 0
	f.Height = p.visibleLines()
	f.Start = p.beamFrameStart
	f.VBlank = p.cycles
	if !p.frameStarted {
		f.FirstLine = f.Height
	}
	p.FrameHook(f)
}

// CopyFrame copies the completed frame described by f into dst as
// row-major BGR555 pixels and returns the extended slice.
//
// If any line of f is hires, the frame is 512 pixels wide and each
// pixel of a 256-pixel line is doubled. Otherwise it is 256 pixels wide.
// CopyFrame must be called from FrameHook, before the PPU runs again.
func (p *PPU) CopyFrame(dst []uint16, f *FrameInfo) []uint16 {
	if !f.AnyHires() {
		return append(dst, p.FrontBuffer[:256*f.Height]...)
	}
	for y := 0; y < f.Height; y++ {
		if f.Hires[y] {
			dst = append(dst, p.hiresFrontBuffer[y*512:(y+1)*512]...)
			continue
		}
		for _, v := range p.FrontBuffer[y*256 : (y+1)*256] {
			dst = append(dst, v, v)
		}
	}
	return dst
}
