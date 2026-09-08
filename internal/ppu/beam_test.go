package ppu

import (
	"fmt"
	"testing"
)

func TestBeamEventsSETINI(t *testing.T) {
	p := NewPPU()
	for p.GetCycles() < 230*1364 {
		p.Run()
	}
	_, first := p.BeamEvents()
	if first != 225*1364 {
		t.Fatalf("normal edge=%d", first)
	}
	p.ReadRDNMI()
	p.Run()
	_, same := p.BeamEvents()
	if same != first || p.NMIFlag {
		t.Fatal("read-clear replayed beam edge")
	}
	// The reference updates vdisp on SETINI writes; it does not wait for
	// a new field to alter the NMI boundary.
	p.WriteRegister(0x2133, 4)
	p.Run()
	if p.NMIFlag || p.vblankActive {
		t.Fatal("overscan did not lower vblank level")
	}
	state := p.SaveState()
	p.WriteRegister(0x2133, 0)
	p.Run()
	_, event := p.BeamEvents()
	if event != p.GetCycles() || event <= first || !p.NMIFlag {
		t.Fatal("SETINI falling overscan edge did not raise vblank")
	}
	restored := NewPPU()
	restored.LoadState(state)
	restored.WriteRegister(0x2133, 0)
	restored.Run()
	_, got := restored.BeamEvents()
	if got != event {
		t.Fatalf("restored SETINI edge=%d, want %d", got, event)
	}
}

func TestBeamInterlaceLatch(t *testing.T) {
	p := NewPPU()
	for p.GetCycles() < 128*1364 {
		p.Run()
	}
	p.WriteRegister(0x2133, 1) // After this field's counter latch.
	for p.FrameCount == 0 {
		p.Run()
	}
	start, _ := p.BeamEvents()
	if start != 262*1364 {
		t.Fatalf("late interlace write changed current field length: %d", start)
	}
	for p.FrameCount < 2 {
		p.Run()
	}
	start, _ = p.BeamEvents()
	if start != 2*262*1364 {
		t.Fatalf("odd interlace field length: %d", start)
	}
	for p.FrameCount < 3 {
		p.Run()
	}
	start, _ = p.BeamEvents()
	if start != 2*262*1364+263*1364 {
		t.Fatalf("next even interlace field length: %d", start)
	}
}

func ExamplePPU_BeamEvents() {
	p := NewPPU()
	for p.GetCycles() < 225*1364 {
		p.Run()
	}
	fmt.Println(p.BeamEvents())
	// Output: 0 306900
}
