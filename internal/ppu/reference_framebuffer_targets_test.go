package ppu

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

type ppuFramebufferTarget struct {
	name   string
	reason string
	setup  func(*PPU)
	hash   string
}

func TestPPUReferenceFramebufferTargets(t *testing.T) {
	targets := []ppuFramebufferTarget{
		{
			name:   "Mode7Wrap",
			reason: "Mode 7 screen-over wraps at the character-plane edge",
			setup: func(p *PPU) {
				*p = *newMode7RenderPPU()
				p.M7A = 0x0500
				setMode7Map(p, 31, 0, 6)
				setMode7TilePixel(p, 6, 3, 0, 13)
				setCGRAMColor(p, 13, 0x4567)
				renderPixelWalk(p, 0)
			},
			hash: "143864700ad035dada6a6f460bd391fb42706673bc9e8bc18bc16d077740f930",
		},
		{
			name:   "Mode7FlipWrap",
			reason: "Mode 7 M7SEL flip plus wrap around the character plane",
			setup: func(p *PPU) {
				*p = *newMode7RenderPPU()
				p.WriteRegister(0x211A, 0x01)
				p.M7A = 0x0500
				setMode7Map(p, 31, 0, 6)
				setMode7TilePixel(p, 6, 3, 0, 13)
				setCGRAMColor(p, 13, 0x4567)
				renderPixelWalk(p, 0)
			},
			hash: "c9fa3718634d91d3f209fce365f758b5a808b95e0df6ec103471ee8394e0f043",
		},
		{
			name:   "Mode7ScanlineLatch",
			reason: "HDMA-style Mode 7 register latch between scanlines",
			setup: func(p *PPU) {
				*p = *newMode7RenderPPU()
				setMode7Map(p, 0, 0, 3)
				setMode7TilePixel(p, 3, 0, 0, 9)
				setMode7TilePixel(p, 3, 1, 1, 10)
				setCGRAMColor(p, 9, 0x1234)
				setCGRAMColor(p, 10, 0x5678)
				renderPixelWalk(p, 0)
				p.WriteRegister(0x210D, 0x01)
				p.WriteRegister(0x210D, 0x00)
				renderPixelWalk(p, 1)
			},
			hash: "1a9bc000617680737732e869f04f7668fccc25a2f876bd9472cf7b19d09251a5",
		},
		{
			name:   "OBJInterlace16x32",
			reason: "OBJ interlace 16x32 squash and bottom-half suppression",
			setup: func(p *PPU) {
				hideAllOBJ(p)
				p.SETINI = 0x02
				p.OBSEL = 6 << 5
				placeOBJ(p, 0, 0, 0, false)
				setOBJPlane0Pixel(p, 16, 6, 0)
				setOBJPlane0Pixel(p, 32, 0, 0)
				p.CGRAM[129*2] = 0x1F
				renderOBJScanline(p, 7)
				renderOBJScanline(p, 8)
			},
			hash: "47e767da87176b2935f93e0eea2799344c0ded8a6d92fe06a1e9d4656f1ccbce",
		},
	}

	for _, target := range targets {
		target := target
		t.Run(target.name, func(t *testing.T) {
			if target.reason == "" {
				t.Fatalf("target metadata incomplete: %+v", target)
			}
			p := NewPPU()
			target.setup(p)
			got := hashPPUFrontBuffer(p.FrontBuffer)
			if got != target.hash {
				t.Fatalf("synthetic framebuffer hash = %s, want %s", got, target.hash)
			}
		})
	}
}

func hashPPUFrontBuffer(buf []uint16) string {
	h := sha256.New()
	var scratch [2]byte
	for _, v := range buf {
		scratch[0] = byte(v)
		scratch[1] = byte(v >> 8)
		h.Write(scratch[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
