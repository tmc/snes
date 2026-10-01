package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestRunRecoveredArtifacts(t *testing.T) {
	dir := t.TempDir()
	rom := make([]byte, 1<<20)
	code := []byte{0xee, 1, 0x1e, 0xad, 1, 0x1e, 0xc9, 0x40, 0xd0, 3, 0xee, 0, 0x1e, 0xad, 5, 0x1f, 0x18, 0x69, 5, 0x8d, 5, 0x1f, 0xad, 4, 0x1f, 0x18, 0x69, 3, 0x8d, 4, 0x1f, 0x60}
	copy(rom[0x6445b:], code)
	copy(rom[0x64480:], []byte{0x20, 0x5b, 0xc4, 0x80, 0xfb})
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	s := snes.NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	s.Power()
	s.CPU.E = false
	s.CPU.P = 0x31
	s.CPU.PB = 12
	s.CPU.DB = 12
	s.CPU.PC = 0xc480
	s.CPU.S = 0x1fd
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
	pins, err := machinebranch.PrepareRecovered(rom, 5)
	if err != nil {
		t.Fatal(err)
	}
	c := machinebranch.Config{ROMPath: rp, ROMSHA256: hash(rom), StatePath: sp, StateSHA256: hash(state), Frames: 1, Mode: "recovered_c", Addend: 5, Recovered: &pins}
	for _, bad := range []bool{true, false} {
		cc := c
		pinCopy := pins
		cc.Recovered = &pinCopy
		if bad {
			cc.Recovered.EditedIRSHA256 = strings.Repeat("0", 64)
		}
		b, err := json.Marshal(cc)
		if err != nil {
			t.Fatal(err)
		}
		cp := filepath.Join(dir, "config.json")
		if err := os.WriteFile(cp, b, 0600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, fmt.Sprintf("out-%v", bad))
		err = run([]string{"-config", cp, "-config-sha256", hash(b), "-out", out}, new(bytes.Buffer))
		if bad {
			if err == nil {
				t.Fatal("substituted pin accepted")
			}
			if _, e := os.Stat(out); !os.IsNotExist(e) {
				t.Fatal("partial output published")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join(out, "recovered.c"))
		if err != nil || hash(source) != pins.SourceSHA256 {
			t.Fatal("executed source identity", err)
		}

		for name, pin := range map[string]string{"original-ir.json": pins.IRSHA256, "edited-ir.json": pins.EditedIRSHA256} {
			b, err := os.ReadFile(filepath.Join(out, name))
			if err != nil || hash(b) != pin {
				t.Fatal("IR material identity", name, err)
			}
		}
		ids, err := os.ReadFile(filepath.Join(out, "recovered-identities.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got machinebranch.RecoveredConfig
		if err := json.Unmarshal(ids, &got); err != nil || got != pins {
			t.Fatal("published identities", err)
		}
		result, err := os.ReadFile(filepath.Join(out, "result.json"))
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Result machinebranch.Result `json:"result"`
		}
		if err := json.Unmarshal(result, &envelope); err != nil {
			t.Fatal(err)
		}
		r := envelope.Result
		if !r.OriginalMatch || !r.ReplacementExecuted || r.CapturedProofEligible || r.Compiled.SemanticsOrigin != "generic_machine_ir" {
			t.Fatal("incorrect source/qualification")
		}
	}
}
