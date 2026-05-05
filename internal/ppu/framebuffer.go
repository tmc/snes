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

// AppendFrameBGR555Size appends a little-endian BGR555 frame with the
// requested dimensions. Pixels outside the PPU framebuffer are padded with
// black; pixels inside it are copied without scaling.
func (p *PPU) AppendFrameBGR555Size(dst []byte, width, height int) []byte {
	if width <= 0 || height <= 0 {
		return dst
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var v uint16
			if width == 512 && p.hiResActive() {
				i := y*512 + x
				if y < p.Height && i < len(p.hiresFrontBuffer) {
					v = p.hiresFrontBuffer[i]
				}
			} else {
				if x < p.Width && y < p.Height {
					i := y*p.Width + x
					if i < len(p.FrontBuffer) {
						v = p.FrontBuffer[i]
					}
				}
			}
			dst = append(dst, byte(v), byte(v>>8))
		}
	}
	return dst
}

func (p *PPU) hiResActive() bool {
	mode := p.BGMode & 0x07
	return mode == 5 || mode == 6
}
