package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/editor/machinebranch"
)

func testConfig(t *testing.T) (string, string) {
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
	c := machinebranch.Config{ROMPath: rp, ROMSHA256: hash(rom), StatePath: sp, StateSHA256: hash(state), Frames: 1}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p, hash(b)
}

func TestRunPublishesFrames(t *testing.T) {
	p, pin := testConfig(t)
	out := filepath.Join(t.TempDir(), "run")
	var stdout bytes.Buffer
	if err := run([]string{"-config", p, "-config-sha256", pin, "-out", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result machinebranch.Result `json:"result"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope.Result
	if !r.OriginalMatch || r.ReplacementExecuted || r.CapturedProofEligible {
		t.Fatal("wrong qualification")
	}
	for _, branch := range []machinebranch.Branch{r.Baseline, r.Replica} {
		for _, frame := range branch.Frames {
			b, err := os.ReadFile(filepath.Join(out, frame.PNGPath))
			if err != nil {
				t.Fatal(err)
			}
			if hash(b) != frame.PNGSHA256 {
				t.Fatal("wrong PNG identity")
			}
		}
	}
	cp, err := os.ReadFile(filepath.Join(out, "checkpoint.state"))
	if err != nil {
		t.Fatal(err)
	}
	if hash(cp) != r.Config.StateSHA256 {
		t.Fatal("wrong checkpoint identity")
	}
	before := append([]byte(nil), b...)
	if err := run([]string{"-config", p, "-config-sha256", pin, "-out", out}, &stdout); err == nil {
		t.Fatal("existing output accepted")
	}
	after, _ := os.ReadFile(filepath.Join(out, "result.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("existing output changed")
	}
}

func TestRunConfigRefusals(t *testing.T) {
	p, pin := testConfig(t)
	for _, kind := range []string{"wrong hash", "unknown field", "trailing data"} {
		t.Run(kind, func(t *testing.T) {
			b, _ := os.ReadFile(p)
			q := filepath.Join(t.TempDir(), "config.json")
			want := pin
			switch kind {
			case "wrong hash":
				want = strings.Repeat("0", 64)
			case "unknown field":
				b = append([]byte(`{"unknown":1,`), b[1:]...)
				want = hash(b)
			case "trailing data":
				b = append(b, []byte(" {}")...)
				want = hash(b)
			}
			if err := os.WriteFile(q, b, 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out")
			if err := run([]string{"-config", q, "-config-sha256", want, "-out", out}, new(bytes.Buffer)); err == nil {
				t.Fatal("invalid config accepted")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("partial output published")
			}
		})
	}
}
