package ppu

import "testing"

// runFrames runs p until FrameHook has reported n frames.
func runFrames(t *testing.T, p *PPU, n int, hook func(*FrameInfo)) {
	t.Helper()
	got := 0
	p.FrameHook = func(f *FrameInfo) {
		got++
		hook(f)
	}
	defer func() { p.FrameHook = nil }()
	for i := 0; got < n; i++ {
		if i > n*400_000 {
			t.Fatalf("only %d of %d frames completed", got, n)
		}
		p.Run()
	}
}

func TestFrameHookBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name   string
		setini uint8
		height int
	}{
		{"normal", 0x00, 224},
		{"overscan", 0x04, 240},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPPU()
			p.SETINI = tt.setini
			var frames []FrameInfo
			runFrames(t, p, 4, func(f *FrameInfo) {
				if f.VBlank != p.cycles {
					t.Errorf("frame %d: VBlank %d, PPU cycles %d", f.Number, f.VBlank, p.cycles)
				}
				frames = append(frames, *f)
			})
			for i, f := range frames {
				if f.Number != i || f.Height != tt.height || f.Overscan != (tt.height == 240) || f.FirstLine != 0 {
					t.Errorf("frame %d: %+v", i, f)
				}
				// Line vdisp begins vblank; each line is 1364 master clocks.
				vdisp := uint64(tt.height + 1)
				if tt.height == 240 {
					vdisp = 240
				}
				if got := f.VBlank - f.Start; got != vdisp*1364 {
					t.Errorf("frame %d: VBlank-Start = %d, want %d", i, got, vdisp*1364)
				}
				if i > 0 && f.Start <= frames[i-1].VBlank {
					t.Errorf("frame %d starts at %d, before frame %d vblank %d", i, f.Start, i-1, frames[i-1].VBlank)
				}
				if f.Field != (i%2 == 1) {
					t.Errorf("frame %d: field %v", i, f.Field)
				}
			}
		})
	}
}

func TestFrameHookOncePerFrame(t *testing.T) {
	p := NewPPU()
	var numbers []int
	runFrames(t, p, 2, func(f *FrameInfo) {
		numbers = append(numbers, f.Number)
		if f.Number == 0 {
			// Enabling overscan at line 225 leaves vblank until
			// line 240, which re-enters it within the same frame.
			p.SETINI = 0x04
		}
	})
	if numbers[0] != 0 || numbers[1] != 1 {
		t.Fatalf("frames reported %v, want [0 1]", numbers)
	}
}

func TestCopyFrameHires(t *testing.T) {
	p := NewPPU()
	f := &FrameInfo{Height: 2}
	for i := range p.FrontBuffer[:512] {
		p.FrontBuffer[i] = uint16(i)
	}
	for i := range p.hiresFrontBuffer[:1024] {
		p.hiresFrontBuffer[i] = uint16(0x4000 | i)
	}
	if got := p.CopyFrame(nil, f); len(got) != 512 || got[300] != 300 {
		t.Fatalf("lores copy: len %d", len(got))
	}
	f.Hires[1] = true
	got := p.CopyFrame(nil, f)
	if len(got) != 1024 {
		t.Fatalf("hires copy: len %d, want 1024", len(got))
	}
	// Line 0 is lores and doubled; line 1 is hires.
	if got[0] != 0 || got[1] != 0 || got[2] != 1 || got[511] != 255 {
		t.Errorf("doubled lores line: %v", got[:4])
	}
	if got[512] != 0x4000|512 || got[1023] != 0x4000|1023 {
		t.Errorf("hires line: %04x %04x", got[512], got[1023])
	}
}

func TestFrameLineModes(t *testing.T) {
	p := NewPPU()
	p.INIDISP = 0x0F
	p.BGMode = 5
	p.SETINI = 0x08
	var f FrameInfo
	runFrames(t, p, 1, func(fi *FrameInfo) { f = *fi })
	if !f.AnyHires() || !f.Hires[0] || !f.Hires[223] || !f.PseudoHires {
		t.Fatalf("hires %v %v pseudo %v", f.Hires[0], f.Hires[223], f.PseudoHires)
	}
	p.INIDISP = 0x80 // force blank: no hires output
	runFrames(t, p, 1, func(fi *FrameInfo) { f = *fi })
	if f.AnyHires() || f.PseudoHires {
		t.Fatalf("force-blank frame reports hires %v pseudo %v", f.AnyHires(), f.PseudoHires)
	}
}

func TestFrameAfterLoadState(t *testing.T) {
	p := NewPPU()
	for p.vCounter < 100 {
		p.Run()
	}
	st := p.SaveState()
	q := NewPPU()
	q.LoadState(st)
	var f FrameInfo
	runFrames(t, q, 1, func(fi *FrameInfo) { f = *fi })
	if f.Number != 0 || f.FirstLine < 99 || f.FirstLine > 100 {
		t.Fatalf("frame %d first line %d, want 0 and 99-100", f.Number, f.FirstLine)
	}
}
