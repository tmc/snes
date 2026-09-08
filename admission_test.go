package snes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cartridge/chips/updsp"
)

func ExampleSystem_LoadROMWithOptions() {
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20
	rom[0x7fd6] = 0x34 // SA-1 register inspection, without secondary CPU execution.
	sys := NewSystem(nil)
	fmt.Println(sys.LoadROMWithOptions(rom, LoadROMOptions{DiagnosticPassthrough: true}))
	// Output: <nil>
}

func TestSystemCartridgeAdmission(t *testing.T) {
	for _, tt := range []struct {
		name             string
		chipset, subtype byte
	}{
		{"sa1", 0x34, 0}, {"sa1-rom", 0x33, 0}, {"sdd1", 0x43, 0}, {"st010/011", 0xf3, 1}, {"supergameboy", 0xe3, 0}, {"spc7110", 0xf5, 0}, {"st018", 0xf3, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rom := newBootableTestROM()
			rom[0x7fd6], rom[0x7fcf] = tt.chipset, tt.subtype
			sys := NewSystem(nil)
			if err := sys.LoadROM(rom); !errors.Is(err, cartridge.ErrUnsupportedCoprocessor) {
				t.Fatalf("LoadROM = %v, want unsupported", err)
			}
			if sys.Loaded() {
				t.Fatal("rejected cartridge installed")
			}
			if err := sys.LoadROMWithOptions(rom, LoadROMOptions{DiagnosticPassthrough: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("firmware", func(t *testing.T) {
		rom := newBootableTestROM()
		rom[0x7fd5] = 0x23
		sys := NewSystem(nil)
		for _, suffix := range []string{"ROM", "PROGRAM_ROM", "DATA_ROM"} {
			t.Setenv("SNES_DSP1_"+suffix, "")
		}
		if err := sys.LoadROM(rom); err == nil {
			t.Fatal("ambiguous variant accepted")
		}
		opts := LoadROMOptions{DSPVariant: "DSP-1"}
		if err := sys.LoadROMWithOptions(rom, opts); !errors.Is(err, updsp.ErrROMMissing) {
			t.Fatalf("missing firmware: %v", err)
		}
		opts.DiagnosticPassthrough = true
		if err := sys.LoadROMWithOptions(rom, opts); err != nil {
			t.Fatal(err)
		}
		opts.DiagnosticPassthrough = false
		path := filepath.Join(t.TempDir(), "dsp.rom")
		t.Setenv("SNES_DSP1_ROM", path)
		for _, size := range []int{3, 8192} {
			if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
				t.Fatal(err)
			}
			err := sys.LoadROMWithOptions(rom, opts)
			if (err == nil) != (size == 8192) {
				t.Fatalf("size %d: %v", size, err)
			}
		}
		opts.DSPVariant = "DSP-999"
		if err := sys.LoadROMWithOptions(rom, opts); err == nil {
			t.Fatal("invalid variant accepted")
		}
	})
	t.Run("ram", func(t *testing.T) {
		rom := newBootableTestROM()
		rom[0x7fd8] = 63
		sys := NewSystem(nil)
		if err := sys.LoadROM(rom); err == nil || sys.Loaded() {
			t.Fatal("invalid ram header installed")
		}
	})
}
