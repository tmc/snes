package ppu

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestAppendFrameBGR555UsesVisibleHeight(t *testing.T) {
	p := NewPPU()
	p.Width = 2
	p.Height = 2
	p.FrontBuffer[0] = 0x1234
	p.FrontBuffer[1] = 0xABCD
	p.FrontBuffer[2] = 0x0001
	p.FrontBuffer[3] = 0x7FFF
	p.FrontBuffer[4] = 0x5555

	got := p.AppendFrameBGR555([]byte{0xEE})
	want := []byte{0xEE, 0x34, 0x12, 0xCD, 0xAB, 0x01, 0x00, 0xFF, 0x7F}
	if string(got) != string(want) {
		t.Fatalf("AppendFrameBGR555 = % X, want % X", got, want)
	}
}

func TestAppendFrameBGR555ClampsToFrontBuffer(t *testing.T) {
	p := NewPPU()
	p.Width = 256
	p.Height = 240
	p.FrontBuffer = []uint16{0x00AA, 0x5500}

	got := p.AppendFrameBGR555(nil)
	want := []byte{0xAA, 0x00, 0x00, 0x55}
	if string(got) != string(want) {
		t.Fatalf("AppendFrameBGR555 short buffer = % X, want % X", got, want)
	}
}

func TestAppendFrameBGR555SizePadsReferenceDimensions(t *testing.T) {
	p := NewPPU()
	p.Width = 2
	p.Height = 2
	p.FrontBuffer[0] = 0x1234
	p.FrontBuffer[1] = 0xABCD
	p.FrontBuffer[2] = 0x0001
	p.FrontBuffer[3] = 0x7FFF

	got := p.AppendFrameBGR555Size(nil, 4, 3)
	want := []byte{
		0x34, 0x12, 0xCD, 0xAB, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0xFF, 0x7F, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	if string(got) != string(want) {
		t.Fatalf("AppendFrameBGR555Size = % X, want % X", got, want)
	}
}

func TestAppendFrameBGR555SizeMatchesReferenceBlackFrameHash(t *testing.T) {
	p := NewPPU()

	got := sha256.Sum256(p.AppendFrameBGR555Size(nil, 512, 224))
	want := "d2a23a7de3af6bf70d164a0ce45201371aca7624e30079d30cccab1cb6cf52e3"
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("512x224 black frame hash = %s, want %s", hex.EncodeToString(got[:]), want)
	}
}
