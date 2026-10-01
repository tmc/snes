package machinebranch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes"
)

func fixture(t *testing.T) (Config, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	copy(rom, []byte{0x80, 0xfe})
	s := snes.NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	s.Power()
	state, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	rp, sp := filepath.Join(dir, "rom.sfc"), filepath.Join(dir, "checkpoint.state")
	for p, b := range map[string][]byte{rp: rom, sp: state} {
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return Config{ROMPath: rp, ROMSHA256: digest(rom), StatePath: sp, StateSHA256: digest(state), Frames: 2, Inputs: []Input{{Frame: 0, Port: 0, Buttons: 0x1000}, {Frame: 1, Port: 0, Buttons: 0}}}, rom, state
}

func TestRunOriginalMatch(t *testing.T) {
	c, rom, state := fixture(t)
	r, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OriginalMatch || r.ReplacementExecuted || r.CapturedProofEligible {
		t.Fatalf("incorrect status %+v", r)
	}
	if r.Baseline.InitialStateSHA256 != c.StateSHA256 || r.Replica.InitialStateSHA256 != c.StateSHA256 {
		t.Fatal("different initial states")
	}
	for i, f := range r.Baseline.Frames {
		g := r.Replica.Frames[i]
		if f.StateSHA256 != g.StateSHA256 || !reflect.DeepEqual(f.Pixels, g.Pixels) {
			t.Fatal("branch divergence")
		}
		if f.VBlankCycle <= f.StartCycle || f.EndCycle < f.VBlankCycle {
			t.Fatal("invalid frame boundary")
		}
	}
	again, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, again) {
		t.Fatal("repeated result differs")
	}
	gotROM, _ := os.ReadFile(c.ROMPath)
	gotState, _ := os.ReadFile(c.StatePath)
	if !reflect.DeepEqual(rom, gotROM) || !reflect.DeepEqual(state, gotState) {
		t.Fatal("original inputs changed")
	}
}

func TestCompleteStateRoundtrip(t *testing.T) {
	_, rom, state := fixture(t)
	s, h, err := restore(rom, state)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if h != digest(state) || !reflect.DeepEqual(b, state) {
		t.Fatal("state roundtrip differs")
	}
}

func TestRunRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config, []byte, []byte)
		want   string
	}{
		{"ROMsubstitution", func(c *Config, r, s []byte) { r[10] ^= 1; os.WriteFile(c.ROMPath, r, 0600) }, "SHA-256 mismatch"},
		{"state substitution", func(c *Config, r, s []byte) { s[len(s)-1] ^= 1; os.WriteFile(c.StatePath, s, 0600) }, "SHA-256 mismatch"},
		{"wrong ROM in state", func(c *Config, r, s []byte) { r[10] ^= 1; os.WriteFile(c.ROMPath, r, 0600); c.ROMSHA256 = digest(r) }, "cartridge does not match state"},
		{"truncated state", func(c *Config, r, s []byte) {
			s = s[:len(s)/2]
			os.WriteFile(c.StatePath, s, 0600)
			c.StateSHA256 = digest(s)
		}, "restore baseline"},
		{"unordered input", func(c *Config, r, s []byte) { c.Inputs = []Input{{Frame: 1}, {Frame: 0}} }, "ordered"},
		{"duplicate input", func(c *Config, r, s []byte) { c.Inputs = []Input{{Frame: 0}, {Frame: 0}} }, "unique"},
		{"mode", func(c *Config, r, s []byte) { c.Mode = "rom_patch" }, "unsupported execution mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r, s := fixture(t)
			tt.change(&c, r, s)
			got, err := Run(context.Background(), c)
			if got != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("result=%v err=%v", got, err)
			}
		})
	}
}

func TestGeneratedCModeRefuses(t *testing.T) {
	r, err := Run(context.Background(), Config{Mode: "generated_c"})
	if r != nil || !errors.Is(err, ErrGeneratedCBridge) {
		t.Fatalf("%v %v", r, err)
	}
}

func TestCancellation(t *testing.T) {
	c, _, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := Run(ctx, c)
	if r != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v %v", r, err)
	}
}
