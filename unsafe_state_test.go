package snes

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"strings"
	"testing"
)

func encodeTestState(t *testing.T, state any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSystemUnserializeRejectsUnsafeChipSelectors(t *testing.T) {
	for _, chip := range []string{"gsu", "updsp"} {
		t.Run(chip, func(t *testing.T) {
			sys := NewSystem(nil)
			rom := newGSUTestROM()
			opts := LoadROMOptions{}
			if chip == "updsp" {
				rom = newBootableTestROM()
				rom[0x7fd5] = 0x23
				opts = LoadROMOptions{DSPVariant: "DSP-1", DiagnosticPassthrough: true}
				t.Setenv("SNES_DSP1_ROM", "")
				t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
				t.Setenv("SNES_DSP1_DATA_ROM", "")
			}
			copy(rom, []byte{0xa9, 0x5a, 0x8d, 0x10, 0, 0xdb})
			if err := sys.LoadROMWithOptions(rom, opts); err != nil {
				t.Fatal(err)
			}
			sys.Power()
			before := mustSystemState(t, sys)
			bad := mustSystemState(t, sys)
			var cart struct {
				Mode          int
				RAM           []byte
				CoprocessorID string
				Coprocessor   []byte
			}
			if err := gob.NewDecoder(bytes.NewReader(bad.CartState)).Decode(&cart); err != nil {
				t.Fatal(err)
			}
			if chip == "gsu" {
				// GETB ALT2 at its ROM-read wait, with the GSU running.
				type frame struct {
					Active                  bool
					Op, Phase, Mode, SrcReg uint8
				}
				cart.Coprocessor = encodeTestState(t, struct {
					RAM       []byte
					SFR       uint16
					StepSlice frame
				}{RAM: cart.RAM, SFR: 1 << 5, StepSlice: frame{true, 0xef, 6, 2, 16}})
			} else {
				var mapper struct {
					Core    []byte
					HasCore bool
					MapType uint8
				}
				if err := gob.NewDecoder(bytes.NewReader(cart.Coprocessor)).Decode(&mapper); err != nil {
					t.Fatal(err)
				}
				var core struct {
					PRG  []uint32
					DROM []uint16
					SP   uint8
				}
				if err := gob.NewDecoder(bytes.NewReader(mapper.Core)).Decode(&core); err != nil {
					t.Fatal(err)
				}
				core.SP = 4
				core.PRG[0] = 2<<22 | 0x140<<13 // LCALL uses the saved stack pointer
				mapper.Core = encodeTestState(t, core)
				cart.Coprocessor = encodeTestState(t, mapper)
			}
			bad.CartState = encodeTestState(t, cart)
			bad.WRAM[0x20] = 0xff
			bad.WRIO = 0x12
			err := sys.Unserialize(encodeTestState(t, bad))
			wantError := "invalid stack pointer"
			if chip == "gsu" {
				wantError = "invalid partial-instruction register selector"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("Unserialize = %v, want %q", err, wantError)
			}
			if !reflect.DeepEqual(before, mustSystemState(t, sys)) {
				t.Fatal("failed validation changed machine")
			}
			sys.CPU.Step()
			sys.CPU.Step()
			if sys.Bus.Read(0x10) != 0x5a {
				t.Fatal("machine stopped executing after rejected state")
			}
		})
	}
}
