package snes

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSystemStateFrameSkipBounds(t *testing.T) {
	for _, skip := range []uint{0, 9, 10, ^uint(0)} {
		t.Run(fmt.Sprint(skip), func(t *testing.T) {
			sys := NewSystem(nil)
			rom := newBootableTestROM()
			copy(rom, []byte{0xa9, 0x5a, 0x8d, 0x10, 0, 0xdb})
			if err := sys.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			sys.Power()
			before := mustSystemState(t, sys)
			state := mustSystemState(t, sys)
			state.FrameSkip = skip
			state.WRAM[0x20] = 0xff
			err := sys.Unserialize(encodeTestState(t, state))
			if skip > 9 {
				if err == nil || !strings.Contains(err.Error(), "frame skip") {
					t.Fatalf("Unserialize = %v, want frame-skip rejection", err)
				}
				if !reflect.DeepEqual(before, mustSystemState(t, sys)) {
					t.Fatal("rejected frame skip changed machine")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if sys.FrameSkip() != skip {
					t.Fatalf("frame skip = %d, want %d", sys.FrameSkip(), skip)
				}
			}
			// Rejected maxuint must not wrap RunFrame's steps to zero.
			if err := sys.RunFrame(); err != nil {
				t.Fatal(err)
			}
			if sys.Bus.Read(0x10) != 0x5a {
				t.Fatal("reset-vector program did not execute")
			}
		})
	}
}
