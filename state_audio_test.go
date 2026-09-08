package snes

import (
	"testing"
)

func TestSystemStateResumesActiveDSPVoice(t *testing.T) {
	sys := NewSystem(nil)
	sys.APU.Processor.Stopped = true
	ram := &sys.APU.RAM
	ram[0x2000], ram[0x2001] = 0, 0x30
	ram[0x2002], ram[0x2003] = 0, 0x30
	ram[0x3000] = 0x97 // looping BRR block, range 9, filter 1
	copy(ram[0x3001:], []byte{0x71, 0x8f, 0x23, 0xa4, 0x56, 0x7c, 0xde, 0x90})
	d := sys.APU.DSP
	for _, reg := range []struct{ address, value byte }{
		{0x5d, 0x20}, {0x00, 0x7f}, {0x01, 0x7f}, {0x02, 0x37}, {0x03, 0x12},
		{0x05, 0}, {0x07, 0x7f}, {0x0c, 0x7f}, {0x1c, 0x7f}, {0x6c, 0x20}, {0x4c, 1},
	} {
		d.Write(reg.address, reg.value)
	}
	for i := 0; i < 64*19+7; i++ {
		sys.APU.Run()
	}
	snapshot, err := sys.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	// Discard the already generated audio: only future samples qualify replay.
	sys.DrainAudio(make([]int16, 100))
	for i := 0; i < 64*80; i++ {
		sys.APU.Run()
	}
	want := make([]int16, 200)
	want = want[:sys.DrainAudio(want)]
	nonzero := false
	varied := false
	for _, sample := range want {
		nonzero = nonzero || sample != 0
		varied = varied || sample != want[0]
	}
	if !nonzero || !varied || len(want) != 160 {
		t.Fatalf("voice witness is silent or incomplete: %d samples", len(want))
	}
	for _, target := range []*System{sys, NewSystem(nil)} {
		target.APU.DSP.Write(0x6c, 0xe0)
		for i := 0; i < 64; i++ {
			target.APU.Run()
		}
		if err := target.Unserialize(snapshot); err != nil {
			t.Fatal(err)
		}
		target.DrainAudio(make([]int16, 100))
		for i := 0; i < 64*80; i++ {
			target.APU.Run()
		}
		got := make([]int16, 200)
		got = got[:target.DrainAudio(got)]
		if len(got) != len(want) {
			t.Fatalf("resumed samples = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("future sample %d = %d, want %d", i, got[i], want[i])
			}
		}
	}
}
