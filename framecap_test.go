package snes

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"testing"
)

func newTestSystem(t testing.TB, rom []byte) *System {
	t.Helper()
	s := NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	s.Power()
	return s
}

// captureRun runs rom for n frames, capturing every frame if capture
// is set, and returns the system and the captured frames.
func captureRun(t *testing.T, rom []byte, n int, capture bool) (*System, []*Frame) {
	t.Helper()
	s := newTestSystem(t, rom)
	var frames []*Frame
	if capture {
		stop, err := s.CaptureFrames(func(FrameBoundary) bool { return true }, func(f *Frame) {
			frames = append(frames, f)
		})
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
	}
	for range n {
		if err := s.Run(); err != nil {
			t.Fatal(err)
		}
	}
	return s, frames
}

func checkCaptureInvariance(t *testing.T, rom []byte, n int) []*Frame {
	t.Helper()
	plain, _ := captureRun(t, rom, n, false)
	captured, frames := captureRun(t, rom, n, true)
	if len(frames) != n {
		t.Fatalf("captured %d frames in %d runs", len(frames), n)
	}
	if a, b := plain.CPU.Cycles, captured.CPU.Cycles; a != b {
		t.Fatalf("CPU cycles: %d without capture, %d with", a, b)
	}
	if !slices.Equal(plain.FrameBuffer(), captured.FrameBuffer()) {
		t.Fatalf("frame buffers differ")
	}
	a, err := plain.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	b, err := captured.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("serialized states differ with capture enabled")
	}
	// The last completed frame is what the display shows.
	last := frames[n-1]
	if !slices.Equal(last.Pixels, captured.FrameBuffer()[:len(last.Pixels)]) {
		t.Fatalf("last captured frame differs from the displayed frame")
	}
	return frames
}

func TestCaptureFramesInvariance(t *testing.T) {
	checkCaptureInvariance(t, observeTestROM(), 60)
}

func TestCaptureFramesInvarianceROM(t *testing.T) {
	path := os.Getenv("SNES_OBS_ROM")
	if path == "" {
		t.Skip("SNES_OBS_ROM not set")
	}
	rom, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := checkCaptureInvariance(t, rom, 300)
	distinct := map[string]bool{}
	for _, f := range frames {
		distinct[string(nativeLE(f.Pixels))] = true
	}
	t.Logf("%d frames, %d distinct images", len(frames), len(distinct))
}

func nativeLE(px []uint16) []byte {
	b := make([]byte, 0, 2*len(px))
	for _, v := range px {
		b = append(b, byte(v), byte(v>>8))
	}
	return b
}

func TestCaptureFramesBoundaries(t *testing.T) {
	s := newTestSystem(t, observeTestROM())
	var frames []*Frame
	var bounds []FrameBoundary
	stop, err := s.CaptureFrames(func(b FrameBoundary) bool {
		bounds = append(bounds, b)
		start, vblank := s.PPU.BeamEvents()
		if b.Start != start || b.VBlank != vblank {
			t.Errorf("frame %d: boundary %d,%d; PPU beam events %d,%d", b.Number, b.Start, b.VBlank, start, vblank)
		}
		// The PPU is synchronized to the CPU to within one dot.
		if s.CPU.Cycles+4 <= b.VBlank {
			t.Errorf("frame %d: CPU at %d, before vblank %d", b.Number, s.CPU.Cycles, b.VBlank)
		}
		return b.Number%2 == 0
	}, func(f *Frame) { frames = append(frames, f) })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for range 10 {
		s.Run()
	}
	if len(bounds) != 10 || len(frames) != 5 {
		t.Fatalf("%d boundaries, %d frames; want 10, 5", len(bounds), len(frames))
	}
	for i, b := range bounds {
		if b.Number != i {
			t.Errorf("boundary %d has number %d", i, b.Number)
		}
		if b.VBlank-b.Start != 225*1364 {
			t.Errorf("frame %d: vblank at +%d, want +%d", i, b.VBlank-b.Start, 225*1364)
		}
		if i > 0 {
			// NTSC non-interlaced frames alternate a short line.
			if d := b.Start - bounds[i-1].Start; d != 262*1364 && d != 262*1364-4 {
				t.Errorf("frame %d: length %d", i-1, d)
			}
		}
	}
	for i, f := range frames {
		if f.Number != 2*i || f.Start != bounds[2*i].Start || f.VBlank != bounds[2*i].VBlank || f.Field != f.Number%2 {
			t.Errorf("frame %d: number %d start %d vblank %d field %d", i, f.Number, f.Start, f.VBlank, f.Field)
		}
		if f.Width != 256 || f.Height != 224 || f.Format != PixelFormatBGR555 || len(f.Pixels) != 256*224 || len(f.HiresLines) != 224 {
			t.Errorf("frame %d: %dx%d %s, %d pixels, %d hires flags", f.Number, f.Width, f.Height, f.Format, len(f.Pixels), len(f.HiresLines))
		}
	}
}

func TestCaptureFramesImmutable(t *testing.T) {
	s := newTestSystem(t, observeTestROM())
	var frames []*Frame
	var copies [][]uint16
	stop, err := s.CaptureFrames(func(FrameBoundary) bool { return true }, func(f *Frame) {
		// Make the frame distinguishable, then snapshot it.
		for i := range f.Pixels {
			f.Pixels[i] = uint16(f.Number + i)
		}
		frames = append(frames, f)
		copies = append(copies, slices.Clone(f.Pixels))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for range 5 {
		s.Run()
		for i := range s.PPU.FrontBuffer {
			s.PPU.FrontBuffer[i] = 0x7FFF
		}
	}
	for i, f := range frames {
		if !slices.Equal(f.Pixels, copies[i]) {
			t.Fatalf("frame %d changed after capture", f.Number)
		}
		if &f.Pixels[0] == &s.PPU.FrontBuffer[0] {
			t.Fatalf("frame %d aliases the PPU frame buffer", f.Number)
		}
	}
}

func TestCaptureFramesAttach(t *testing.T) {
	s := newTestSystem(t, observeTestROM())
	keep := func(FrameBoundary) bool { return true }
	n := 0
	sink := func(*Frame) { n++ }
	stop, err := s.CaptureFrames(keep, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureFrames(keep, sink); !errors.Is(err, ErrFrameCaptureActive) {
		t.Fatalf("second CaptureFrames: %v, want ErrFrameCaptureActive", err)
	}
	if _, err := newTestSystem(t, observeTestROM()).CaptureFrames(nil, sink); err == nil {
		t.Fatalf("CaptureFrames(nil, sink) succeeded")
	}
	s.Run()
	stop()
	s.Run()
	if n != 1 {
		t.Fatalf("sink called %d times, want 1", n)
	}
}

func TestCaptureFramesRunAhead(t *testing.T) {
	s := newTestSystem(t, observeTestROM())
	s.SetRunAhead(true)
	var numbers []int
	stop, err := s.CaptureFrames(func(b FrameBoundary) bool {
		numbers = append(numbers, b.Number)
		return false
	}, func(*Frame) {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for range 5 {
		if err := s.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(numbers); i++ {
		if numbers[i] != numbers[i-1]+1 {
			t.Fatalf("frame numbers %v: speculative frames reported", numbers)
		}
	}
	if len(numbers) < 4 {
		t.Fatalf("frame numbers %v", numbers)
	}
}

func TestCaptureFramesNoAllocs(t *testing.T) {
	s := newTestSystem(t, observeTestROM())
	s.Run()
	if n := testing.AllocsPerRun(20, func() { s.Run() }); n != 0 {
		t.Errorf("Run without capture: %v allocs", n)
	}
	stop, err := s.CaptureFrames(func(FrameBoundary) bool { return false }, func(*Frame) {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if n := testing.AllocsPerRun(20, func() { s.Run() }); n != 0 {
		t.Errorf("Run with no frames kept: %v allocs", n)
	}
}
