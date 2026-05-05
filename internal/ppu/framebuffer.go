package ppu

// AppendFrameBGR555 appends the visible framebuffer as little-endian BGR555
// words and returns the extended buffer. The format matches the PPU's
// FrontBuffer storage and the libretro 16-bit frame buffers used by parity
// tests.
func (p *PPU) AppendFrameBGR555(dst []byte) []byte {
	n := p.Width * p.Height
	if n > len(p.FrontBuffer) {
		n = len(p.FrontBuffer)
	}
	for _, v := range p.FrontBuffer[:n] {
		dst = append(dst, byte(v), byte(v>>8))
	}
	return dst
}
