package machinebranch

import (
	"bufio"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes"
)

func TestRecoveredPins(t *testing.T) {
	good := strings.Repeat("a", 64)
	valid := RecoveredConfig{good, good, good, good, good}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*RecoveredConfig)
	}{
		{"missing_source", func(c *RecoveredConfig) { c.SourceSHA256 = "" }},
		{"missing_edited_ir", func(c *RecoveredConfig) { c.EditedIRSHA256 = "" }},
		{"missing_ir", func(c *RecoveredConfig) { c.IRSHA256 = "" }},
		{"missing_plan", func(c *RecoveredConfig) { c.PlanSHA256 = "" }},
		{"missing_edit", func(c *RecoveredConfig) { c.EditSHA256 = "" }},
		{"uppercase", func(c *RecoveredConfig) { c.SourceSHA256 = strings.ToUpper(c.SourceSHA256) }},
		{"short", func(c *RecoveredConfig) { c.SourceSHA256 = c.SourceSHA256[:62] }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := valid
			tt.change(&bad)
			if err := bad.validate(); err == nil {
				t.Fatal("invalid recovered pin accepted")
			}
		})
	}
}

func TestRecoveredRotationTiming(t *testing.T) {
	rom := make([]byte, 1<<20)
	copy(rom[0x6445b:], rotationBytes)
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	for _, timer := range []uint8{0, 63, 255} {
		t.Run(string(rune('A'+timer%26)), func(t *testing.T) {
			pins, err := PrepareRecovered(rom, 5)
			if err != nil {
				t.Fatal(err)
			}
			session, err := startRecovered(context.Background(), rom, 5, pins)
			if err != nil {
				t.Fatal(err)
			}
			defer session.close()
			makeMachine := func() *snes.System {
				s := snes.NewSystem(nil)
				if err := s.LoadROM(append([]byte(nil), rom...)); err != nil {
					t.Fatal(err)
				}
				s.Power()
				s.CPU.E = false
				s.CPU.P = 0x31
				s.CPU.PB = 0x0c
				s.CPU.DB = 0x0c
				s.CPU.PC = 0xc45b
				s.CPU.S = 0x1fd
				s.CPU.A = 0xaa00
				s.CPU.D = 0
				s.Bus.Write(0x1e01, timer)
				s.Bus.Write(0x1f05, 0x7f)
				s.Bus.Write(0x1f04, 0xff)
				s.Bus.Write(0x1fe, 0x41)
				s.Bus.Write(0x1ff, 0xc4)
				return s
			}
			a, b := makeMachine(), makeMachine()
			b.CPU.ReplaceInstruction = session.selectInstruction
			aj, bj := journal(a), journal(b)
			for n := 0; n < 14; n++ {
				if a.CPU.PC == 0xc442 {
					break
				}
				a.CPU.Step()
				b.CPU.Step()
				if b.CPU.Fault != nil {
					t.Fatal(b.CPU.Fault)
				}
				if a.CPU.Snapshot() != b.CPU.Snapshot() {
					t.Fatalf("step%d: %+v/%+v", n, a.CPU.Snapshot(), b.CPU.Snapshot())
				}
			}
			ah, an := aj.finish()
			bh, bn := bj.finish()
			if ah != bh || an != bn {
				t.Fatal("physical bus differs")
			}
			x, e := a.StateHashes()
			if e != nil {
				t.Fatal(e)
			}
			y, e := b.StateHashes()
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(x, y) {
				t.Fatal("machine component differs")
			}
		})
	}
}

func TestRecoveredSourceIdentities(t *testing.T) {
	rom := make([]byte, 1<<20)
	copy(rom[0x6445b:], rotationBytes)
	original, err := PrepareRecovered(rom, 5)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareRecovered(rom, 5)
	if err != nil || again != original {
		t.Fatal("source identities not deterministic", err)
	}
	edited, err := PrepareRecovered(rom, 6)
	if err != nil {
		t.Fatal(err)
	}
	if edited.IRSHA256 != original.IRSHA256 || edited.PlanSHA256 != original.PlanSHA256 || edited.SourceSHA256 == original.SourceSHA256 || edited.EditSHA256 == original.EditSHA256 || edited.EditedIRSHA256 == original.EditedIRSHA256 {
		t.Fatalf("edit provenance not separated: original=%+v edited=%+v", original, edited)
	}
	for _, tt := range []struct {
		name   string
		change func(*RecoveredConfig)
	}{{"edited IR", func(p *RecoveredConfig) { p.EditedIRSHA256 = strings.Repeat("b", 64) }}, {"source", func(p *RecoveredConfig) { p.SourceSHA256 = strings.Repeat("b", 64) }}, {"IR", func(p *RecoveredConfig) { p.IRSHA256 = strings.Repeat("b", 64) }}, {"plan", func(p *RecoveredConfig) { p.PlanSHA256 = strings.Repeat("b", 64) }}, {"edit", func(p *RecoveredConfig) { p.EditSHA256 = strings.Repeat("b", 64) }}} {
		t.Run(tt.name, func(t *testing.T) {
			bad := original
			tt.change(&bad)
			session, err := startRecovered(context.Background(), rom, 5, bad)
			if session != nil || err == nil {
				t.Fatal("substituted pin accepted")
			}
		})
	}
	if session, err := startRecovered(context.Background(), rom, 6, original); session != nil || err == nil {
		t.Fatal("edit with oldsourcepins accepted")
	}
	rom[0x6446d]++
	if _, err := PrepareRecovered(rom, 5); err == nil {
		t.Fatal("ROM substitution accepted")
	}
}

func TestRecoveredModeRefusals(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  Config
	}{
		{"missingpins", Config{Mode: "recovered_c", Addend: 5, Frames: 1}},
		{"invalidaddend", Config{Mode: "recovered_c", Addend: 7, Frames: 1, Recovered: &RecoveredConfig{}}},
		{"othermodepins", Config{Mode: "original_interpreter", Frames: 1, Recovered: &RecoveredConfig{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Run(context.Background(), tt.cfg)
			if r != nil || err == nil {
				t.Fatal("invalid recovery config accepted")
			}
		})
	}
}

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

func TestRecoveredProtocolRefusals(t *testing.T) {
	for _, tt := range []struct {
		name     string
		at       uint16
		p        byte
		requests string
		done     string
		want     bool
	}{
		{"clc_valid", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 0 50284 12 12 48\n", true},
		{"clc_wrong_pc", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 0 1 12 12 48\n", false},
		{"clc_wrong_stack", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 510 0 50284 12 12 48\n", false},
		{"clc_wrong_index", 0xc46b, 0x31, "I 6 0\n", "D 43520 8 9 509 0 50284 12 12 48\n", false},
		{"clc_wrong_bank", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 0 50284 12 13 48\n", false},
		{"clc_decimal", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 0 50284 12 12 56\n", false},
		{"missing_response", 0xc46b, 0x31, "", "", false},
		{"clc_wrong_dp", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 1 50284 12 12 48\n", false},
		{"clc_wrong_db", 0xc46b, 0x31, "I 6 0\n", "D 43520 7 9 509 0 50284 13 12 48\n", false},
		{"clc_out_of_range", 0xc46b, 0x31, "I 6 0\n", "D 65536 7 9 509 0 50284 12 12 48\n", false},
		{"malformed_done", 0xc46b, 0x31, "I 6 0\n", "D 0\n", false},
		{"missing_operation", 0xc46b, 0x31, "", "D 43520 7 9 509 0 50284 12 12 48\n", false},
		{"wrong_operation", 0xc46b, 0x31, "I 7 0\n", "D 43520 7 9 509 0 50284 12 12 48\n", false},
		{"branch_valid", 0xc463, 0x31, "F 0 0\nI 6 0\n", "D 43520 7 9 509 0 50280 12 12 49\n", true},
		{"branch_wrong_pc", 0xc463, 0x31, "F 0 0\nI 6 0\n", "D 43520 7 9 509 0 50277 12 12 49\n", false},
		{"branch_fallthrough", 0xc463, 0x33, "F 0 0\n", "D 43520 7 9 509 0 50277 12 12 51\n", true},
		{"return_valid", 0xc47a, 0x31, "I 12 0\nR 510 0\nR 511 0\nI 6 0\n", "D 43520 7 9 511 0 50242 12 12 49\n", true},
		{"return_wrong_pc", 0xc47a, 0x31, "I 12 0\nR 510 0\nR 511 0\nI 6 0\n", "D 43520 7 9 511 0 1 12 12 49\n", false},
		{"return_wrong_stack", 0xc47a, 0x31, "I 12 0\nR 510 0\nR 511 0\nI 6 0\n", "D 43520 7 9 509 0 50242 12 12 49\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rom := make([]byte, 1<<20)
			copy(rom[0x6445b:], rotationBytes)
			rom[0x7fd5] = 0x20
			s := snes.NewSystem(nil)
			if err := s.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			s.Power()
			s.CPU.E = false
			s.CPU.P = tt.p
			s.CPU.PB = 12
			s.CPU.DB = 12
			s.CPU.PC = tt.at
			s.CPU.S = 509
			s.CPU.A = 43520
			s.CPU.X = 7
			s.CPU.Y = 9
			s.Bus.Write(510, 0x41)
			s.Bus.Write(511, 0xc4)
			c := &compiledSession{input: discardCloser{io.Discard}, lines: bufio.NewScanner(strings.NewReader(tt.requests + tt.done)), report: &Compiled{SemanticsOrigin: "generic_machine_ir"}}
			s.CPU.ReplaceInstruction = c.selectInstruction
			s.CPU.Step()
			if (s.CPU.Fault == nil) != tt.want {
				t.Fatalf("fault=%v want accepted=%v", s.CPU.Fault, tt.want)
			}
			if !tt.want && c.report.Instructions != 0 {
				t.Fatal("refused instruction counted executed")
			}
		})
	}
}

func TestFrameTimingIdentity(t *testing.T) {
	a := Frame{RelativeFrame: 1, PPUFrame: 3, StartCycle: 10, VBlankCycle: 20, EndCycle: 30, Width: 256, Height: 224, FirstRenderedLine: 1, BusSHA256: "bus", BusEvents: 1, StateSHA256: "state", FramebufferSHA256: "pixels"}
	for _, tt := range []struct {
		name   string
		change func(*Frame)
	}{
		{"ppu_frame", func(f *Frame) { f.PPUFrame++ }},
		{"start", func(f *Frame) { f.StartCycle++ }},
		{"vblank", func(f *Frame) { f.VBlankCycle++ }},
		{"end", func(f *Frame) { f.EndCycle++ }},
		{"width", func(f *Frame) { f.Width++ }},
		{"height", func(f *Frame) { f.Height++ }},
		{"first_line", func(f *Frame) { f.FirstRenderedLine++ }},
		{"hires", func(f *Frame) { f.Hires = true }},
		{"pseudo_hires", func(f *Frame) { f.PseudoHires = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := a
			tt.change(&b)
			if sameFrame(a, b) {
				t.Fatal("changed frame identity matched")
			}
		})
	}
	if !sameFrame(a, a) {
		t.Fatal("identical frame differs")
	}
}
