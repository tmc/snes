package snes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemAttachedChipReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "Power"
		if reset {
			name = "Reset"
		}
		t.Run(name, func(t *testing.T) {
			restart := func(s *System) {
				if reset {
					s.Reset()
				} else {
					s.Power()
				}
			}
			t.Run("GSU", func(t *testing.T) {
				s := NewSystem(nil)
				if err := s.LoadROM(newGSUTestROM()); err != nil {
					t.Fatal(err)
				}
				chip := s.gsu
				ram := &s.cart.RAM[0]
				s.cart.RAM[17] = 0xa5
				chip.COLR = 0x33
				chip.R[1] = 3
				chip.SetPC(0)
				chip.Go()
				chip.Run(2) // Leave an unflushed PLOT pending.
				restart(s)
				if s.gsu != chip || &s.cart.RAM[0] != ram || s.cart.RAM[17] != 0xa5 {
					t.Fatal("reset replaced chip or battery RAM")
				}
				if chip.Running() || chip.R[1] != 0 || chip.Cycles() != 0 {
					t.Fatal("GSU execution survived reset")
				}
				chip.COLR = 0x7b
				chip.R[1] = 4
				chip.SetPC(0)
				chip.Go()
				chip.Run(3)
				if s.PPU.VRAM[4] != 0x7b || s.PPU.VRAM[3] != 0 {
					t.Fatalf("post-reset plot = %x, stale plot = %x", s.PPU.VRAM[4], s.PPU.VRAM[3])
				}
			})
			t.Run("Cx4", func(t *testing.T) {
				rom := newBootableTestROM()
				rom[0x7fd6], rom[0x7fcf] = 0xf3, 0x10
				s := NewSystem(nil)
				if err := s.LoadROM(rom); err != nil {
					t.Fatal(err)
				}
				s.Bus.Write(0x6000, 0xa5)
				s.Bus.Write(0x7f80, 0x91)
				restart(s)
				if s.Bus.Read(0x6000) != 0 || s.Bus.Read(0x7f80) != 0 {
					t.Fatal("volatile Cx4 state survived reset")
				}
				s.Bus.Write(0x7f80, 7)
				s.Bus.Write(0x7f4f, 0x54)
				if got := s.Bus.Read(0x7f83); got != 49 {
					t.Fatalf("post-reset square = %d, want 49", got)
				}
			})
			t.Run("OBC1", func(t *testing.T) {
				rom := newBootableTestROM()
				rom[0x7fd6] = 0x25
				s := NewSystem(nil)
				if err := s.LoadROM(rom); err != nil {
					t.Fatal(err)
				}
				s.Bus.Write(0x7ff5, 0)
				s.Bus.Write(0x7ff6, 1)
				// Battery memory can be loaded directly while the previous latches remain.
				s.cart.RAM[0x1ff5], s.cart.RAM[0x1ff6] = 1, 3
				s.cart.RAM[0x180c] = 0x71
				restart(s)
				if got := s.Bus.Read(0x7ff0); got != 0x71 {
					t.Fatalf("relatched OAM = %x, want 71", got)
				}
				s.Bus.Write(0x7ff1, 0x92)
				if s.cart.RAM[0x180d] != 0x92 {
					t.Fatal("post-reset write used stale OAM latches")
				}
			})
			t.Run("SRTC", func(t *testing.T) {
				rom := newBootableTestROM()
				rom[0x7fd6] = 0x55
				s := NewSystem(nil)
				if err := s.LoadROM(rom); err != nil {
					t.Fatal(err)
				}
				s.Bus.Write(0x2801, 0xd)
				want := make([]byte, 14)
				for i := range want {
					want[i] = s.Bus.Read(0x2800)
				}
				s.Bus.Write(0x2801, 0xe) // Leave the chip in command mode.
				restart(s)
				for i, v := range want {
					if got := s.Bus.Read(0x2800); got != v {
						t.Fatalf("RTC cell %d = %x, want %x", i, got, v)
					}
				}
			})
			t.Run("DSP", func(t *testing.T) {
				firmware := make([]byte, 8192)
				for i := 0; i < 2048; i++ {
					op := uint32(3<<22 | 0x1234<<6 | 8) // LD $1234,DR
					firmware[i*3], firmware[i*3+1], firmware[i*3+2] = byte(op>>16), byte(op>>8), byte(op)
				}
				path := filepath.Join(t.TempDir(), "dsp.rom")
				if err := os.WriteFile(path, firmware, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("SNES_DSP1_ROM", path)
				rom := newBootableTestROM()
				rom[0x7fd5] = 0x23
				s := NewSystem(nil)
				if err := s.LoadROMWithOptions(rom, LoadROMOptions{DSPVariant: "DSP-1"}); err != nil {
					t.Fatal(err)
				}
				s.cart.Step(3)
				if got := s.Bus.Read(0x206000); got != 0x34 {
					t.Fatalf("firmware result before reset = %x", got)
				}
				s.cart.Step(2) // Leave enough fractional debt to distinguish a missed reset.
				restart(s)
				if got := s.Bus.Read(0x206001); got != 0 {
					t.Fatalf("reset SR = %x, want 0", got)
				}
				s.cart.Step(2)
				if got := s.Bus.Read(0x206001); got != 0 {
					t.Fatalf("pre-reset fractional cycle survived: SR=%x", got)
				}
				s.cart.Step(1)
				if got := s.Bus.Read(0x206001); got != 0x80 {
					t.Fatalf("firmware did not resume: SR=%x", got)
				}
				if lo, hi := s.Bus.Read(0x206000), s.Bus.Read(0x206000); lo != 0x34 || hi != 0x12 {
					t.Fatalf("post-reset DR = %02x%02x", hi, lo)
				}
			})
		})
	}
}
