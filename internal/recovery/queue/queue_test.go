package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/decomp"
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

func TestExplicitPolicyInputErrors(t *testing.T) {
	for _, tt := range []struct {
		name, content, expected string
		missing, unpaired       bool
	}{
		{name: "unpaired", unpaired: true, expected: "supplied together"},
		{name: "missing", missing: true, expected: "no such file"},
		{name: "wrong hash", content: "{}", expected: "SHA-256 mismatch"},
		{name: "malformed", content: "{", expected: "decode policy"},
		{name: "unknown", content: `{"unknown":true}`, expected: "decode policy"},
		{name: "trailing", content: "{} {}", expected: "trailing policy JSON"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.PolicyPath = filepath.Join(t.TempDir(), "policy.json")
			cfg.PolicySHA256 = hash([]byte(tt.content))
			if tt.unpaired {
				cfg.PolicySHA256 = ""
			}
			if !tt.missing {
				if err := os.WriteFile(cfg.PolicyPath, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "wrong hash" {
				cfg.PolicySHA256 = hash([]byte("different"))
			}
			_, err := Run(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tt.expected) {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(cfg.OutDir); !os.IsNotExist(err) {
				t.Fatalf("published output: %v", err)
			}
		})
	}
}

func TestPolicyArtifactSubstitution(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{"corpora":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "admission-policy.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	report := &Report{PolicyFileSHA256: hash(original)}
	if err := checkArtifacts(dir, report); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "admission-policy.json"), []byte("substituted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkArtifacts(dir, report); err == nil {
		t.Fatal("substituted policy accepted")
	}
}

func TestSelectedEntryBeforeLimit(t *testing.T) {
	cfg := testConfig(t)
	b, err := os.ReadFile(filepath.Join(cfg.ProjectDir, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc recovery.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	base := doc.Instructions[0]
	call := base
	call.ID = "call"
	call.Bytes = "200081"
	call.Opcode = 0x20
	call.Mnemonic = "JSR"
	ret := base
	ret.ID = "caller-return"
	ret.Address = 0x8003
	ret.Offset = 3
	target := base
	target.ID = "target-return"
	target.Address = 0x8100
	target.Offset = 0x100
	doc.Instructions = []recovery.Instruction{call, ret, target}
	if err := writeJSON(filepath.Join(cfg.ProjectDir, "recovery.json"), &doc); err != nil {
		t.Fatal(err)
	}
	cfg.Revision = recovery.ComputeProjectRevision(cfg.ProjectDir, &doc)
	cfg.Limit = 1
	cfg.Entry = 0x8100
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Candidates) != 1 || report.Candidates[0].Candidate.Entry != 0x8100 {
		t.Fatalf("selected wrong entry: %+v", report.Candidates)
	}
}

func TestPrefetchFailureIsBoundedRefusal(t *testing.T) {
	cfg := testConfig(t)
	rom, err := os.ReadFile(cfg.ROMPath)
	if err != nil {
		t.Fatal(err)
	}
	corpus := "queue-prefetch-test"
	fixtureHash := hash([]byte("fixture"))
	root := decomp.CorpusTrustRoot{Label: "test", EngineRevision: "synthetic", ROMSHA256: hash(rom), FixtureSHA256: fixtureHash, DecompressedSHA: fixtureHash, FixtureReceiptSHA256: fixtureHash, FixtureSummarySHA256: fixtureHash, CaptureSHA256: fixtureHash, CaptureReceiptSHA256: fixtureHash, CaptureSummarySHA256: fixtureHash, HistorySHA256: fixtureHash, HistorySummarySHA256: fixtureHash}
	verifier, err := decomp.NewEvidenceVerifierWithPolicy(cfg.CorpusRoot, decomp.AdmissionPolicy{Corpora: map[string]decomp.CorpusTrustRoot{corpus: root}}, rom)
	if err != nil {
		t.Fatal(err)
	}
	c := decomp.ReplayCase{SchemaVersion: "snes-routine-case-v1", CaseID: "missing-fixture", RoutineID: "leaf-008000", EntrySeq: 2, ExitSeq: 2, CallSeq: 1, ReturnSeq: 3, InitialState: decomp.CPUState{PC: 0x8000}, Evidence: &decomp.CaseEvidence{Corpus: corpus, Fixture: &decomp.EvidenceFileRef{Path: "missing.jsonl", SHA256: fixtureHash}}}
	result, err := execute(context.Background(), cfg, rom, candidates.Candidate{ID: "leaf-008000", Entry: 0x8000}, []decomp.ReplayCase{c, c}, verifier, decomp.AdmissionPolicy{}, nil, filepath.Join(filepath.Dir(cfg.OutDir), "prefetch"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasonCode != "fixture_prefetch" || result.Cases != 1 || result.Refused != 1 || result.Admitted != 0 || result.SourceSHA256 != "" {
		t.Fatalf("prefetch promoted or exceeded bounds: %+v", result)
	}
}

func TestReadCandidateProfile(t *testing.T) {
	good := []byte(`{"id":"sub_0cc45b","kind":"dispatch_handler","entry":836699,"start":836699,"end":836731,"returns":[836730]}`)
	c, err := readCandidateProfile(good)
	if err != nil || c.ID != "sub_0cc45b" || c.Proposal.End != 836731 || c.Entry != 836699 {
		t.Fatalf("profile=%+v error=%v", c, err)
	}
	for _, b := range [][]byte{
		[]byte(`{"id":"../escape","entry":836699,"start":836699,"end":836731}`),
		[]byte(`{"id":"sub_0cc45b","entry":836699,"start":836700,"end":836731}`),
		[]byte(`{"id":"sub_0cc45b","entry":836699,"start":836699,"end":836731,"returns":[836732]}`),
	} {
		if _, err := readCandidateProfile(b); err == nil {
			t.Fatalf("accepted invalid profile %s", b)
		}
	}
}
