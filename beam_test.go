package snes

import "testing"

func newBeamTestSystem(t *testing.T, pal bool, setini uint8) *System {
	t.Helper()
	rom := newBootableTestROM()
	rom[0], rom[1] = 0x80, 0xfe                           // BRA to itself.
	rom[0x100], rom[0x101], rom[0x102] = 0xe6, 0x10, 0x40 // INC $10; RTI.
	rom[0x7ffa], rom[0x7ffb] = 0x00, 0x81
	if pal {
		rom[0x7fd9] = 0x02
	} else {
		rom[0x7fd9] = 0x01
	}
	sys := NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()
	sys.PPU.WriteRegister(0x2133, setini)
	sys.Scheduler.SetNMI(true)
	return sys
}

func TestSystemFrameBoundaryMatrix(t *testing.T) {
	// bsnes 9144b5a: ppu/io.cpp updateVideoMode and
	// ppu/counter/counter-inline.hpp tickScanline. These constants are
	// independent of scheduler's historical average frame periods.
	for _, tc := range []struct {
		name    string
		pal     bool
		setini  uint8
		line    uint64
		periods [2]uint64
	}{
		{"NTSC", false, 0, 225, [2]uint64{262 * 1364, 262*1364 - 4}},
		{"NTSC overscan", false, 4, 240, [2]uint64{262 * 1364, 262*1364 - 4}},
		{"NTSC interlace", false, 1, 225, [2]uint64{263 * 1364, 262 * 1364}},
		{"NTSC overscan interlace", false, 5, 240, [2]uint64{263 * 1364, 262 * 1364}},
		{"PAL", true, 0, 225, [2]uint64{312 * 1364, 312 * 1364}},
		{"PAL overscan", true, 4, 240, [2]uint64{312 * 1364, 312 * 1364}},
		{"PAL interlace", true, 1, 225, [2]uint64{313 * 1364, 312*1364 + 4}},
		{"PAL overscan interlace", true, 5, 240, [2]uint64{313 * 1364, 312*1364 + 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newBeamTestSystem(t, tc.pal, tc.setini)
			frameStart := uint64(0)
			for frame := 0; frame < 3; frame++ {
				if err := sys.Run(); err != nil {
					t.Fatal(err)
				}
				want := frameStart + tc.line*1364
				start, event := sys.PPU.BeamEvents()
				if start != frameStart || event != want {
					t.Fatalf("frame %d beam events = %d/%d, want %d/%d", frame, start, event, frameStart, want)
				}
				if sys.CPU.Cycles < want || sys.CPU.Cycles >= want+32 {
					t.Fatalf("frame %d CPU returned at %d, want [%d,%d)", frame, sys.CPU.Cycles, want, want+32)
				}
				if !sys.PPU.NMIFlag || !sys.CPU.NMIPending || !sys.Scheduler.NMITriggered() {
					t.Fatalf("frame %d missing vblank/NMI flag or delivery", frame)
				}
				if got := sys.Bus.Read(0x7e0010); got != uint8(frame) {
					t.Fatalf("frame %d NMI handler count=%d", frame, got)
				}
				if got := sys.PPU.SaveState().PPUField; got != (frame&1 != 0) {
					t.Fatalf("frame %d field=%v", frame, got)
				}
				frameStart += tc.periods[frame&1]
			}
		})
	}
}

func TestSystemOverscanNoEarlyNMI(t *testing.T) {
	sys := newBeamTestSystem(t, false, 4)
	// Position the real CPU clock at the normal-mode boundary and synchronize
	// through the production PPU path without executing a pending interrupt.
	sys.CPU.Cycles = 225 * 1364
	sys.Scheduler.Sync(sys.PPU)
	if sys.PPU.NMIFlag || sys.CPU.NMIPending || sys.Scheduler.NMITriggered() {
		t.Fatal("overscan NMI arrived at normal-mode boundary")
	}
	sys.CPU.Cycles = 240 * 1364
	sys.Scheduler.Sync(sys.PPU)
	if !sys.PPU.NMIFlag || !sys.CPU.NMIPending {
		t.Fatal("overscan NMI missing at line 240")
	}
}

func TestSystemFrameBoundaryRestore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pal    bool
		setini uint8
		edge   uint64
	}{
		{"normal", false, 0, 225 * 1364}, {"overscan", false, 4, 240 * 1364},
		{"PAL interlace", true, 5, 240 * 1364},
	} {
		for _, offset := range []int64{-4, 0, 4} {
			t.Run(tc.name+"/"+map[int64]string{-4: "before", 0: "at", 4: "after"}[offset], func(t *testing.T) {
				sys := newBeamTestSystem(t, tc.pal, tc.setini)
				sys.CPU.Cycles = uint64(int64(tc.edge) + offset)
				sys.Scheduler.Sync(sys.PPU)
				state, err := sys.Serialize()
				if err != nil {
					t.Fatal(err)
				}
				restored := newBeamTestSystem(t, tc.pal, tc.setini)
				if err := restored.Unserialize(state); err != nil {
					t.Fatal(err)
				}
				for frame := 0; frame < 2; frame++ {
					if err := sys.Run(); err != nil {
						t.Fatal(err)
					}
					if err := restored.Run(); err != nil {
						t.Fatal(err)
					}
					a, b := sys.PPU.BeamEvents()
					c, d := restored.PPU.BeamEvents()
					if a != c || b != d || sys.CPU.Cycles != restored.CPU.Cycles || sys.CPU.NMIPending != restored.CPU.NMIPending {
						t.Fatalf("frame %d diverged after restore: beam %d/%d vs %d/%d", frame, a, b, c, d)
					}
					if sys.Scheduler.SaveState() != restored.Scheduler.SaveState() {
						t.Fatal("scheduler event phase changed after restore")
					}
					if sys.Bus.Read(0x7e0010) != restored.Bus.Read(0x7e0010) {
						t.Fatal("NMI handler effects diverged")
					}
				}
			})
		}
	}
}

func TestSystemBeamDoesNotDeliverAheadOfCPU(t *testing.T) {
	sys := newBeamTestSystem(t, false, 0)
	sys.CPU.Cycles = 225*1364 - 2
	sys.Scheduler.Sync(sys.PPU)
	_, event := sys.PPU.BeamEvents()
	if event != 225*1364 {
		t.Fatalf("PPU rounded event = %d", event)
	}
	if sys.CPU.NMIPending || sys.Scheduler.NMITriggered() {
		t.Fatal("PPU synchronization delivered future NMI")
	}
	sys.CPU.Cycles += 2
	sys.Scheduler.Sync(sys.PPU)
	if !sys.CPU.NMIPending || !sys.Scheduler.NMITriggered() {
		t.Fatal("NMI missing when CPU reached beam edge")
	}
}

func TestSystemMixedFrameCalls(t *testing.T) {
	for _, mode := range []string{"full frame", "sync past edge", "sync before edge"} {
		t.Run(mode, func(t *testing.T) {
			sys := newBeamTestSystem(t, false, 0)
			want := uint64(262*1364 + 225*1364)
			switch mode {
			case "full frame":
				if err := sys.RunFrame(); err != nil {
					t.Fatal(err)
				}
			case "sync past edge":
				sys.CPU.Cycles = 225*1364 + 8
				sys.Scheduler.Sync(sys.PPU)
			case "sync before edge":
				sys.CPU.Cycles = 225*1364 - 2
				sys.Scheduler.Sync(sys.PPU)
				want = 225 * 1364
			}
			entry := sys.CPU.Cycles
			if err := sys.Run(); err != nil {
				t.Fatal(err)
			}
			_, event := sys.PPU.BeamEvents()
			if event != want || event <= entry {
				t.Fatalf("display after %s returned event %d from entry %d, want %d", mode, event, entry, want)
			}
		})
	}
}
