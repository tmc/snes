package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	rom := make([]byte, 32768)
	rom[0] = 0x60
	rom[0x7fd5] = 0x20
	identity := recovery.ComputeROMIdentity(rom, rom, "none", "lorom")
	doc := recovery.NewDocument(identity)
	doc.Instructions = []recovery.Instruction{{ID: "return", Architecture: "65816", Address: 0x8000, Offset: 0, Bytes: "60", Opcode: 0x60, Mnemonic: "RTS", Context: recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}}}
	if err := writeJSON(filepath.Join(project, "recovery.json"), doc); err != nil {
		t.Fatal(err)
	}
	romPath := filepath.Join(root, "rom.sfc")
	if err := os.WriteFile(romPath, rom, 0600); err != nil {
		t.Fatal(err)
	}
	cases := filepath.Join(root, "cases.jsonl")
	if err := os.WriteFile(cases, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return Config{ProjectDir: project, ROMPath: romPath, CasesPath: cases, CorpusRoot: root, OutDir: filepath.Join(root, "out"), Revision: recovery.ComputeProjectRevision(project, doc), Limit: 5, MaxCases: 1, MaxSteps: 100}
}

func TestRunNoEvidence(t *testing.T) {
	cfg := testConfig(t)
	r, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].Status != "blocked" || r.Candidates[0].Admitted != 0 {
		t.Fatalf("promoted unverified candidate: %+v", r)
	}
	c := r.Candidates[0]
	if _, err := os.Stat(filepath.Join(cfg.OutDir, c.Directory, "generated.c")); !os.IsNotExist(err) {
		t.Fatalf("generated with no admitted context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutDir, "report.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("overwrote published run")
	}
}

func TestRunInputFailures(t *testing.T) {
	tests := []struct {
		name   string
		change func(Config)
	}{
		{"ROM substitution", func(c Config) { b, _ := os.ReadFile(c.ROMPath); b[1]++; os.WriteFile(c.ROMPath, b, 0600) }},
		{"source substitution", func(c Config) {
			b, _ := os.ReadFile(filepath.Join(c.ProjectDir, "recovery.json"))
			os.WriteFile(filepath.Join(c.ProjectDir, "recovery.json"), append(b, ' '), 0600)
		}},
		{"malformed cases", func(c Config) { os.WriteFile(c.CasesPath, []byte("bad"), 0600) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testConfig(t)
			tt.change(c)
			if _, err := Run(context.Background(), c); err == nil {
				t.Fatal("accepted changed inputs")
			}
			if _, err := os.Stat(c.OutDir); !os.IsNotExist(err) {
				t.Fatal("partial output published")
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	c := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, c); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(c.OutDir); !os.IsNotExist(err) {
		t.Fatal("published canceled run")
	}
}

func TestUnverifiedWidthsAndColdFrontier(t *testing.T) {
	for _, width := range []string{"unknown", "clear"} {
		t.Run(width, func(t *testing.T) {
			cfg := testConfig(t)
			b, err := os.ReadFile(filepath.Join(cfg.ProjectDir, "recovery.json"))
			if err != nil {
				t.Fatal(err)
			}
			var d recovery.Document
			if err := json.Unmarshal(b, &d); err != nil {
				t.Fatal(err)
			}
			d.Instructions[0].Context.M = width
			d.Instructions[0].Bytes = "d002"
			d.Instructions[0].Opcode = 0xd0
			d.Instructions[0].Mnemonic = "BNE"
			if err := writeJSON(filepath.Join(cfg.ProjectDir, "recovery.json"), &d); err != nil {
				t.Fatal(err)
			}
			cfg.Revision = recovery.ComputeProjectRevision(cfg.ProjectDir, &d)
			r, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range r.Candidates {
				if candidate.Status != "blocked" || candidate.SourceSHA256 != "" {
					t.Fatal("trusted inferred width or cold frontier")
				}
			}
		})
	}
}

func TestBounds(t *testing.T) {
	for _, change := range []func(*Config){func(c *Config) { c.Limit = 0 }, func(c *Config) { c.MaxSteps = 0 }, func(c *Config) { c.MaxCases = 0 }, func(c *Config) { c.Limit = 101 }} {
		c := testConfig(t)
		change(&c)
		if _, err := Run(context.Background(), c); err == nil {
			t.Fatal("unbounded queue accepted")
		}
	}
}

func ExampleRun() {
	// Supply current project, ROM and case paths and finite bounds in Config.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Config{})
	if err == context.Canceled {
		fmt.Println("canceled")
	}
	// Output: canceled
}

func TestPinnedInputRechecked(t *testing.T) {
	for _, name := range []string{"generated-source", "rom", "cases"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(p, []byte("one"), 0600); err != nil {
				t.Fatal(err)
			}
			var pins []pinnedInput
			if _, err := readInput(p, &pins); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("two"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := checkInputs(pins); err == nil {
				t.Fatal("reused stale input pin")
			}
		})
	}
}

func TestArtifactSubstitution(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "leaf", "identity")
	if err := os.MkdirAll(d, 0700); err != nil {
		t.Fatal(err)
	}
	source := []byte("source")
	if err := os.WriteFile(filepath.Join(d, "generated.c"), source, 0600); err != nil {
		t.Fatal(err)
	}
	report := &Report{Candidates: []Result{{Directory: "artifacts/leaf/identity", SourceSHA256: hash(source)}}}
	if err := checkArtifacts(root, report); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "generated.c"), []byte("mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkArtifacts(root, report); err == nil {
		t.Fatal("accepted changed generated source")
	}
}
