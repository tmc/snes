package machinebranch

import (
	"bufio"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes"
)

func syntheticProfile() ([]byte, RegionConfig) {
	// Authored CPU fixture: load one RAM byte, add an immediate, store, return.
	code := []byte{0xad, 0x40, 0, 0x18, 0x69, 5, 0x8d, 0x40, 0, 0x60}
	rom := make([]byte, 32768)
	copy(rom, code)
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	return rom, RegionConfig{Start: 0x8000, Bytes: len(code), CodeSHA256: digest(code), DataBank: 0, Cells: []uint32{0x40}, EditAddress: 0x8004, Original: 5, Replacements: []uint8{5, 6}}
}

func TestExplicitProfileTiming(t *testing.T) {
	rom, profile := syntheticProfile()
	for _, value := range []uint8{0, 0x7f, 0xff} {
		t.Run(fmt.Sprintf("%02x", value), func(t *testing.T) {
			pins, err := PrepareRecovered(rom, 5, profile)
			if err != nil {
				t.Fatal(err)
			}
			session, err := startRecovered(context.Background(), rom, 5, pins, profile)
			if err != nil {
				t.Fatal(err)
			}
			defer session.close()
			makeMachine := func() *snes.System {
				s := snes.NewSystem(nil)
				if err := s.LoadROM(rom); err != nil {
					t.Fatal(err)
				}
				s.Power()
				s.CPU.E = false
				s.CPU.P = 0x31
				s.CPU.PB = 0
				s.CPU.DB = 0
				s.CPU.PC = 0x8000
				s.CPU.S = 0x1fd
				s.CPU.A = 0xaa00
				s.CPU.D = 0
				s.Bus.Write(0x40, value)
				s.Bus.Write(0x1fe, 0x10)
				s.Bus.Write(0x1ff, 0x80)
				return s
			}
			a, b := makeMachine(), makeMachine()
			b.CPU.ReplaceInstruction = session.selectInstruction
			aj, bj := journal(a), journal(b)
			for n := 0; n < 5; n++ {
				a.CPU.Step()
				b.CPU.Step()
				if b.CPU.Fault != nil {
					t.Fatalf("step %d: %v", n, b.CPU.Fault)
				}
				if a.CPU.Snapshot() != b.CPU.Snapshot() {
					t.Fatalf("step %d differs", n)
				}
			}
			ah, an := aj.finish()
			bh, bn := bj.finish()
			if ah != bh || an != bn {
				t.Fatal("physical bus differs")
			}
			x, err := a.StateHashes()
			if err != nil {
				t.Fatal(err)
			}
			y, err := b.StateHashes()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(x, y) {
				t.Fatal("component state differs")
			}
		})
	}
}

func TestProfileRefusals(t *testing.T) {
	rom, p := syntheticProfile()
	if _, err := PrepareRecovered(rom, 5); err == nil {
		t.Fatal("ambient profile accepted")
	}
	if _, err := PrepareRecovered(rom, 7, p); err == nil {
		t.Fatal("unlisted replacement accepted")
	}
	for _, tt := range []struct {
		name string
		edit func(*RegionConfig)
	}{
		{"bad pin", func(p *RegionConfig) { p.CodeSHA256 = "wrong" }},
		{"operand entry", func(p *RegionConfig) { p.EditAddress++ }},
		{"wrong original", func(p *RegionConfig) { p.Original = 6 }},
		{"MMIO cell", func(p *RegionConfig) { p.Cells = []uint32{0x2100} }},
		{"cross bank", func(p *RegionConfig) { p.Start = 0xffff }},
		{"no original", func(p *RegionConfig) { p.Replacements = []uint8{6} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := p
			tt.edit(&q)
			if _, err := PrepareRecovered(rom, 5, q); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	rom[5]++
	if _, err := PrepareRecovered(rom, 5, p); err == nil {
		t.Fatal("ROM substitution accepted")
	}
}

func ExampleRegionConfig_Allows() {
	profile := RegionConfig{Replacements: []uint8{2, 3}}
	fmt.Println(profile.Allows(2), profile.Allows(4))
	// Output: true false
}

type discardInput struct{}

func (discardInput) Write(p []byte) (int, error) { return len(p), nil }
func (discardInput) Close() error                { return nil }

func TestProfileMalformedProtocol(t *testing.T) {
	for _, line := range []string{
		"F 0 0 extra\n", "F +0 0\n", "F 1 0\n", "F 0 1\n",
		"R 64 0\n", "W 64 1\n", "I 6 0\n", "D 0\n",
		"D 0 0 0 509 0 32771 0 0 48\n",
	} {
		t.Run(line, func(t *testing.T) {
			rom, profile := syntheticProfile()
			s := snes.NewSystem(nil)
			if err := s.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			s.Power()
			s.CPU.E = false
			s.CPU.P = 0x30
			s.CPU.PB = 0
			s.CPU.DB = 0
			s.CPU.PC = 0x8000
			s.CPU.S = 0x1fd
			s.CPU.D = 0
			session := &compiledSession{profile: profile, code: append([]byte(nil), rom[:10]...), input: discardInput{}, lines: bufio.NewScanner(strings.NewReader(line)), report: &Compiled{SemanticsOrigin: "generic_machine_ir"}}
			s.CPU.ReplaceInstruction = session.selectInstruction
			s.CPU.Step()
			if s.CPU.Fault == nil || session.report.Instructions != 0 {
				t.Fatal("malformed protocol accepted or counted")
			}
		})
	}
}
