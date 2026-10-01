package machinebranch

import (
	"bufio"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes"
)

func TestCompiledRotationTiming(t *testing.T) {
	rom := make([]byte, 1<<20)
	copy(rom[0x6445b:], rotationBytes)
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	for _, timer := range []uint8{0, 63, 255} {
		t.Run(string(rune('A'+timer%26)), func(t *testing.T) {
			session, err := startCompiled(context.Background(), rom, 5)
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
func TestCompiledROMSubstitution(t *testing.T) {
	rom := make([]byte, 1<<20)
	copy(rom[0x6445b:], rotationBytes)
	rom[0x6446d]++
	if _, err := startCompiled(context.Background(), rom, 5); err == nil {
		t.Fatal("substituted ROM accepted")
	}
}

type discardInput struct{}

func (discardInput) Write(p []byte) (int, error) { return len(p), nil }
func (discardInput) Close() error                { return nil }
func TestCompiledMalformedProtocol(t *testing.T) {
	for _, tt := range []struct{ name, line string }{
		{"trailing_request", "I 6 0 extra\n"},
		{"trailing_done", "D 0 0 0 509 0 50270 12 12 48 extra\n"},
		{"premature_done", "D 0 0 0 509 0 50270 12 12 48\n"},
		{"read_before_idle", "R 794113 0\n"},
		{"write_before_idle", "W 794113 1\n"},
		{"fetch_before_idle", "F 0 0\n"},
		{"wrong_idle", "I 12 0\n"},
		{"idle_payload", "I 6 1\n"},
		{"fetch_payload", "I 6 0\nF 0 1\n"},
		{"signed_value", "I +6 0\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rom := make([]byte, 1<<20)
			copy(rom[0x6445b:], rotationBytes)
			rom[0x7fd5] = 0x20
			rom[0x7ffd] = 0x80
			s := snes.NewSystem(nil)
			if err := s.LoadROM(rom); err != nil {
				t.Fatal(err)
			}
			s.Power()
			s.CPU.E = false
			s.CPU.P = 0x30
			s.CPU.PB = 0x0c
			s.CPU.DB = 0x0c
			s.CPU.PC = 0xc45b
			s.CPU.S = 0x1fd
			s.CPU.D = 0
			session := &compiledSession{input: discardInput{}, lines: bufio.NewScanner(strings.NewReader(tt.line)), report: new(Compiled)}
			s.CPU.ReplaceInstruction = session.selectInstruction
			s.CPU.Step()
			if s.CPU.Fault == nil {
				t.Fatal("malformed protocol accepted")
			}
			if session.report.Instructions != 0 {
				t.Fatal("failed instruction counted")
			}
		})
	}
}
