package framecap

import (
	"fmt"
	"image"
	"image/png"
	"io"
)

// EncodePNG writes BGR555 pixels as an RGB888 PNG. Each 5-bit channel c
// becomes c<<3 | c>>2, which is invertible.
func EncodePNG(w io.Writer, width, height int, px []uint16) error {
	if len(px) != width*height {
		return fmt.Errorf("encode png: %d pixels for %dx%d", len(px), width, height)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i, v := range px {
		img.Pix[4*i+0] = expand5(v)
		img.Pix[4*i+1] = expand5(v >> 5)
		img.Pix[4*i+2] = expand5(v >> 10)
		img.Pix[4*i+3] = 0xFF
	}
	if err := png.Encode(w, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}

func expand5(v uint16) uint8 {
	c := uint8(v & 0x1F)
	return c<<3 | c>>2
}
