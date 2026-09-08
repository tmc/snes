package snes

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSystemResetResumesExecution(t *testing.T) {
	for _, name := range []string{"stp", "wai", "fault", "interrupt"} {
		t.Run(name, func(t *testing.T) {
			sys := NewSystem(nil)
			rom := newBootableTestROM()
			copy(rom, []byte{0xa9, 0x5a, 0x8d, 0x10, 0x00, 0xdb})
			rom[0x100] = 0xdb
			rom[0x101] = 0xcb
			if err := sys.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			sys.Power()
			switch name {
			case "stp":
				sys.CPU.PC = 0x8100
				sys.CPU.Step()
			case "wai":
				sys.CPU.PC = 0x8101
				sys.CPU.Step()
			case "fault":
				sys.CPU.Fault = errors.New("test fault")
			case "interrupt":
				sys.CPU.TriggerNMI()
				sys.CPU.TriggerIRQ()
			}
			hookCalls := 0
			sys.CPU.InterruptHook = func(string) { hookCalls++ }
			sys.Bus.Write(0x4200, 1)
			cpu := sys.CPU
			sys.DMA.Request(0xff)
			sys.Reset()
			if sys.CPU != cpu || cpu.Stopped || cpu.Waiting || cpu.Fault != nil || cpu.NMIPending || cpu.IRQPending || sys.DMA.SaveState().Execution.Pending || sys.DMA.Enable != 0 || sys.autoJoypadEnabled || sys.PPU.AutoJoypad {
				t.Fatal("reset retained execution state or replaced CPU")
			}
			sys.CPU.Step()
			sys.CPU.Step()
			if got := sys.Bus.Read(0x10); got != 0x5a {
				t.Fatalf("reset program wrote %02x, want 5a", got)
			}
			cpu.TriggerNMI()
			cpu.Step()
			if hookCalls != 1 {
				t.Fatal("reset lost interrupt hook")
			}
		})
	}
}

func TestSystemStateWRIO(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x4201, 0x35)
	data, err := sys.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*System{sys, NewSystem(nil)} {
		target.Bus.Write(0x4201, 0xc2)
		if err := target.Unserialize(data); err != nil {
			t.Fatal(err)
		}
		if got := target.Bus.Read(0x4213); got != 0x35 {
			t.Fatalf("WRIO = %02x, want 35", got)
		}
	}
}

func TestSystemLoadROMFailurePreservesState(t *testing.T) {
	firmware := filepath.Join(t.TempDir(), "dsp1.rom")
	if err := os.WriteFile(firmware, []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SNES_DSP1_ROM", firmware)
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")
	for _, name := range []string{"empty", "st018", "spc7110", "firmware"} {
		t.Run(name, func(t *testing.T) {
			bad := newBootableTestROM()
			switch name {
			case "empty":
				bad = nil
			case "st018":
				bad[0x7fd6] = 0xf3
				bad[0x7fcf] = 2
			case "spc7110":
				bad[0x7fd6] = 0xf5
			case "firmware":
				bad[0x7fd5] = 0x23
			}
			load := func(sys *System) error {
				if name == "firmware" {
					return sys.LoadROMWithOptions(bad, LoadROMOptions{DSPVariant: "DSP-1"})
				}
				return sys.LoadROM(bad)
			}
			fresh := NewSystem(nil)
			if err := load(fresh); err == nil || fresh.Loaded() {
				t.Fatal("failed load installed cartridge")
			}
			sys := NewSystem(nil)
			rom := newGSUTestROM()
			copy(rom, []byte{0xa9, 0x5a, 0x8d, 0x10, 0, 0xdb})
			if err := sys.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			sys.Power()
			cart, gsu, hash := sys.cart, sys.gsu, sys.romHash
			before := mustSystemState(t, sys)
			if err := load(sys); err == nil {
				t.Fatal("load succeeded")
			}
			if sys.cart != cart || sys.gsu != gsu || sys.romHash != hash || !reflect.DeepEqual(before, mustSystemState(t, sys)) {
				t.Fatal("failed load changed machine")
			}
			sys.CPU.Step()
			sys.CPU.Step()
			if sys.Bus.Read(0x10) != 0x5a {
				t.Fatal("previous ROM stopped executing")
			}
		})
	}
}

func mustSystemState(t *testing.T, sys *System) systemState {
	t.Helper()
	data, err := sys.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	var state systemState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSystemUnserializeFailurePreservesState(t *testing.T) {
	tests := []struct {
		name   string
		change func(*systemState)
	}{
		{"version 1", func(s *systemState) { s.Version = 1 }},
		{"version 2", func(s *systemState) { s.Version = 2 }},
		{"zero hash", func(s *systemState) { s.ROMHash = [32]byte{} }},
		{"horizontal counter", func(s *systemState) { s.PPU.HCounter = s.PPU.HPeriod / 4 }},
		{"cart", func(s *systemState) { s.CartState = []byte{1, 2, 3} }},
		{"cheat", func(s *systemState) { s.Cheats = []Cheat{{Address: 0x1000000}} }},
		{"wram", func(s *systemState) { s.WRAM = s.WRAM[:1] }},
		{"vram", func(s *systemState) { s.PPU.VRAM = nil }},
		{"apu ram", func(s *systemState) { s.APU.RAM = nil }},
		{"dsp sample phase", func(s *systemState) { s.APU.DSPCycles = 64 }},
		{"brr index", func(s *systemState) { s.APU.DSP.Voices[0].BRRNibblePos = -1 }},
		{"pitch phase", func(s *systemState) { s.APU.DSP.Voices[0].Phase = 0x1000 }},
		{"apu phase", func(s *systemState) { s.APU.MicroOp.Active = true; s.APU.MicroOp.Opcode = 0xff }},
		{"device", func(s *systemState) { s.Connected[0] = 99 }},
		{"board", func(s *systemState) {
			var b bytes.Buffer
			_ = gob.NewEncoder(&b).Encode(struct{ CoprocessorID string }{"gsu"})
			s.CartState = b.Bytes()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := NewSystem(nil)
			rom := newBootableTestROM()
			copy(rom, []byte{0xa9, 0x5a, 0x8d, 0x10, 0, 0xdb})
			if err := sys.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			sys.Power()
			before := mustSystemState(t, sys)
			state := mustSystemState(t, sys)
			state.WRAM[0x20] = 0x99
			state.WRIO = 0x12
			tt.change(&state)
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(state); err != nil {
				t.Fatal(err)
			}
			if err := sys.Unserialize(buf.Bytes()); err == nil {
				t.Fatal("malformed state accepted")
			}
			if !reflect.DeepEqual(before, mustSystemState(t, sys)) {
				t.Fatal("failed restore changed machine")
			}
			if err := sys.RunFrame(); err != nil {
				t.Fatal(err)
			}
			if sys.Bus.Read(0x10) != 0x5a {
				t.Fatal("machine did not continue after rejected state")
			}
		})
	}
}

func TestSystemStateReplay(t *testing.T) {
	for _, chip := range []string{"plain", "gsu", "obc1", "srtc"} {
		for _, runAhead := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/runAhead=%v", chip, runAhead), func(t *testing.T) {
				rom := newBootableTestROM()
				switch chip {
				case "gsu":
					rom = newGSUTestROM()
				case "obc1":
					rom[0x7fd6] = 0x25
				case "srtc":
					rom[0x7fd6] = 0x55
				}
				copy(rom, []byte{0xe6, 0x20, 0x80, 0xfc}) // INC $20; BRA back
				sys := NewSystem(nil)
				if err := sys.LoadROM(rom); err != nil {
					t.Fatal(err)
				}
				sys.Power()
				sys.SetRunAhead(runAhead)
				sys.Bus.Write(0x4201, 0x35)
				sys.Controller1.Buttons = 0xa500
				sys.Bus.Write(0x4016, 1)
				sys.Bus.Write(0x4016, 0)
				sys.Bus.Read(0x4016)
				snapshot, err := sys.Serialize()
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err := sys.RunFrame(); err != nil {
						t.Fatal(err)
					}
				}
				want := mustSystemState(t, sys)
				if want.CPU.PC == 0x8000 && want.CPU.Cycles == 0 {
					t.Fatal("replay did not execute")
				}
				sys.Bus.Write(0x4201, 0xff)
				sys.Controller1.Buttons = 0
				if err := sys.Unserialize(snapshot); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err := sys.RunFrame(); err != nil {
						t.Fatal(err)
					}
				}
				got := mustSystemState(t, sys)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("restore changed future machine, frame, input or audio state")
				}
			})
		}
	}
}

func TestSystemStateMEMSEL(t *testing.T) {
	sys := NewSystem(nil)
	sys.Bus.Write(0x420d, 0xff)
	data, err := sys.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	sys.Bus.Write(0x420d, 0)
	if err := sys.Unserialize(data); err != nil {
		t.Fatal(err)
	}
	if sys.Bus.MEMSEL != 0xff {
		t.Fatalf("MEMSEL = %02x, want ff", sys.Bus.MEMSEL)
	}
}
