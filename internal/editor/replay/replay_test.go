package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/decomp"
)

func TestPatch(t *testing.T) {
	tests := []struct {
		name, source, find, replace, want string
		bad                               bool
	}{
		{"unique", "return 1;", "1", "2", "return 2;", false},
		{"missing", "return 1;", "3", "2", "", true},
		{"ambiguous", "1+1", "1", "2", "", true},
		{"empty", "return 1;", "", "2", "", true},
		{"unchanged", "return 1;", "1", "1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := []byte(tt.source)
			got, err := applyPatch(b, Patch{Find: tt.find, Replace: tt.replace})
			if tt.bad {
				if err == nil {
					t.Fatal("bad patch accepted")
				}
				return
			}
			if err != nil || string(got) != tt.want {
				t.Fatalf("patch=%q err=%v", got, err)
			}
			if string(b) != tt.source {
				t.Fatal("original mutated")
			}
		})
	}
}
func TestComparisonSensitivity(t *testing.T) {
	base := decomp.ExecResult{State: decomp.CPUState{A: 1, X: 2, Y: 3, S: 0x1ff, D: 4, DB: 5, PB: 6, PC: 0x8000, P: 0x30}, NextPC: 0x68000, TotalWrites: 2, Writes: []decomp.MemoryWrite{{Address: 0x7e0000, Value: 1}, {Address: 0x7e0001, Value: 2}}}
	if status, _ := compare(base, base); status != "same_sample" {
		t.Fatal(status)
	}
	changes := []func(*decomp.ExecResult){func(r *decomp.ExecResult) { r.State.A++ }, func(r *decomp.ExecResult) { r.State.X++ }, func(r *decomp.ExecResult) { r.State.Y++ }, func(r *decomp.ExecResult) { r.State.S++ }, func(r *decomp.ExecResult) { r.State.D++ }, func(r *decomp.ExecResult) { r.State.DB++ }, func(r *decomp.ExecResult) { r.State.PB++ }, func(r *decomp.ExecResult) { r.State.PC++ }, func(r *decomp.ExecResult) { r.State.P++ }, func(r *decomp.ExecResult) { r.State.E = true }, func(r *decomp.ExecResult) { r.NextPC++ }, func(r *decomp.ExecResult) { r.TotalWrites++ }, func(r *decomp.ExecResult) { r.Writes[0], r.Writes[1] = r.Writes[1], r.Writes[0] }, func(r *decomp.ExecResult) { r.Writes[0].Value++ }}
	for i, change := range changes {
		r := base
		r.Writes = append([]decomp.MemoryWrite(nil), base.Writes...)
		change(&r)
		if status, _ := compare(base, r); status != "diverged" {
			t.Fatalf("mutation %d status=%s", i, status)
		}
	}
	for _, change := range []func(*decomp.ExecResult){func(r *decomp.ExecResult) { r.MissingRead = true }, func(r *decomp.ExecResult) { r.MMIOAccess = true }, func(r *decomp.ExecResult) { r.WriteOverflow = true }} {
		r := base
		change(&r)
		if status, _ := compare(base, r); status != "refused" {
			t.Fatal("unsupported edited result not refused")
		}
	}
	r := base
	r.State.Cycles = 10
	if status, _ := compare(base, r); status != "same_sample" {
		t.Fatal("unqualified timing included")
	}
}
func TestPinnedAndConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	raw := []byte("pinned")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readPinned(path, digest(raw)); err != nil || !bytes.Equal(got, raw) {
		t.Fatal(err)
	}
	for _, sha := range []string{"", strings.Repeat("a", 64), strings.Repeat("z", 64)} {
		if _, err := readPinned(path, sha); err == nil {
			t.Fatal("bad identity accepted")
		}
	}
	cfg, err := LoadConfig(strings.NewReader(`{"max_steps":7,"start":32768,"end":32769}`))
	if err != nil || cfg.MaxSteps != 7 || cfg.Start != 32768 {
		t.Fatalf("decoded config lost: %+v %v", cfg, err)
	}
	for _, input := range []string{`{"unexpected":1}`, `{} {}`, `{`, strings.Repeat(" ", 1<<20+1)} {
		if _, err := LoadConfig(strings.NewReader(input)); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
func TestRunPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Config{ProjectDir: "project", CorpusRoot: "corpus", OutDir: dir, Revision: "revision", Start: 0x8000, End: 0x8001, MaxSteps: 1})
	if err == nil || !strings.Contains(err.Error(), "output already exists") {
		t.Fatalf("error=%v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatal("existing output changed")
	}
}
func TestCapturedEdit(t *testing.T) {
	path := os.Getenv("SNES_EXPERIMENT_CONFIG")
	if path == "" {
		t.Skip("set SNES_EXPERIMENT_CONFIG to run a retained captured baseline and actual compiled edit")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutDir = filepath.Join(t.TempDir(), "experiment")
	original, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Baseline.CapturedProofEligible || !result.Baseline.Matched || result.Baseline.Metadata.IsStale {
		t.Fatal("original baseline not fresh qualified")
	}
	if result.EditedCapturedProofEligible || result.EditedStatus != "diverged" {
		t.Fatalf("edited result wrongly qualified or undetected: %+v", result.EditedStatus)
	}
	after, err := os.ReadFile(cfg.SourcePath)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("original source changed")
	}
	secondCfg := cfg
	secondCfg.OutDir = filepath.Join(t.TempDir(), "repeat")
	second, err := Run(context.Background(), secondCfg)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := compare(result.Baseline.CompiledC, second.Baseline.CompiledC); status != "same_sample" {
		t.Fatal("baseline nondeterministic")
	}
	if status, _ := compare(result.Edited, second.Edited); status != "same_sample" {
		t.Fatal("edited experiment nondeterministic")
	}
	if second.EditedCapturedProofEligible {
		t.Fatal("edited result inherited proof")
	}
	if _, err := os.Stat(filepath.Join(cfg.OutDir, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestExternalScheduleRefused(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ProjectDir: "project", CorpusRoot: "corpus", OutDir: filepath.Join(dir, "out"), Revision: "revision", Start: 0x8000, End: 0x8001, MaxSteps: 1}
	values := [][]byte{[]byte("rom"), []byte("{}"), []byte("source"), []byte(`{"find":"source","replace":"edit"}`), []byte(`[{}]`)}
	paths := []*string{&cfg.ROMPath, &cfg.CasePath, &cfg.SourcePath, &cfg.PatchPath, &cfg.InputsPath}
	hashes := []*string{&cfg.ROMSHA256, &cfg.CaseSHA256, &cfg.SourceSHA256, &cfg.PatchSHA256, &cfg.InputsSHA256}
	for i, v := range values {
		p := filepath.Join(dir, string(rune('a'+i)))
		if err := os.WriteFile(p, v, 0600); err != nil {
			t.Fatal(err)
		}
		*paths[i] = p
		*hashes[i] = digest(v)
	}
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "external input schedule unsupported") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(cfg.OutDir); !os.IsNotExist(err) {
		t.Fatal("unsupported schedule published output")
	}
}

func TestStructuredChanges(t *testing.T) {
	a := decomp.ExecResult{State: decomp.CPUState{A: 7}, TotalWrites: 1, Writes: []decomp.MemoryWrite{{Address: 0x7e0022, Value: 1}}}
	b := a
	b.State.A = 8
	b.Writes = []decomp.MemoryWrite{{Address: 0x7e0022, Value: 2}}
	cpu, writes := changes(a, b)
	if len(cpu) != 1 || cpu[0] != (StateChange{Field: "a", Original: 7, Edited: 8}) || len(writes) != 1 || writes[0].Index != 0 || writes[0].Original.Value != 1 || writes[0].Edited.Value != 2 {
		t.Fatalf("delta=%+v %+v", cpu, writes)
	}
	b.Writes = nil
	b.TotalWrites = 0
	_, writes = changes(a, b)
	if len(writes) != 1 || writes[0].Edited != nil {
		t.Fatal("removed write not represented")
	}
}

func TestRegionAddressBounds(t *testing.T) {
	for _, tt := range []struct {
		name       string
		start, end uint32
	}{
		{"start", 0x1008000, 0x1008001},
		{"end", 0xff8000, 0x1000001},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{ProjectDir: "project", CorpusRoot: "corpus", OutDir: filepath.Join(t.TempDir(), "out"), Revision: "revision", Start: tt.start, End: tt.end, MaxSteps: 1}
			if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "unsupported region") {
				t.Fatalf("Run: %v", err)
			}
			if _, err := os.Stat(cfg.OutDir); !os.IsNotExist(err) {
				t.Fatalf("output published: %v", err)
			}
		})
	}
}

func TestExplicitPolicyInputErrors(t *testing.T) {
	for _, tt := range []struct {
		name, content, want string
		missing, unpaired   bool
	}{
		{name: "unpaired", unpaired: true, want: "supplied together"},
		{name: "missing", missing: true, want: "no such file"},
		{name: "wrong hash", content: "{}", want: "digest mismatch"},
		{name: "malformed", content: "{", want: "decode policy"},
		{name: "unknown", content: `{"unknown":true}`, want: "decode policy"},
		{name: "trailing", content: "{} {}", want: "decode policy"},
		{name: "oversize", content: strings.Repeat(" ", 1<<20+1), want: "policy exceeds"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := Config{ProjectDir: "project", CorpusRoot: "corpus", OutDir: filepath.Join(dir, "out"), Revision: "revision", Start: 0x8000, End: 0x8001, MaxSteps: 1}
			rom := []byte("rom")
			c := decomp.ReplayCase{ROMSHA256: digest(rom), InitialState: decomp.CPUState{PC: 0x8000}}
			caseBytes, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			values := [][]byte{rom, caseBytes, []byte("source"), []byte(`{"find":"source","replace":"edit"}`), []byte(`[]`)}
			paths := []*string{&cfg.ROMPath, &cfg.CasePath, &cfg.SourcePath, &cfg.PatchPath, &cfg.InputsPath}
			hashes := []*string{&cfg.ROMSHA256, &cfg.CaseSHA256, &cfg.SourceSHA256, &cfg.PatchSHA256, &cfg.InputsSHA256}
			for i, v := range values {
				p := filepath.Join(dir, string(rune('a'+i)))
				if err := os.WriteFile(p, v, 0600); err != nil {
					t.Fatal(err)
				}
				*paths[i] = p
				*hashes[i] = digest(v)
			}
			cfg.PolicyPath = filepath.Join(dir, "policy.json")
			cfg.PolicySHA256 = digest([]byte(tt.content))
			if !tt.missing {
				if err := os.WriteFile(cfg.PolicyPath, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.unpaired {
				cfg.PolicySHA256 = ""
			}
			if tt.name == "wrong hash" {
				cfg.PolicySHA256 = digest([]byte("different"))
			}
			if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(cfg.OutDir); !os.IsNotExist(err) {
				t.Fatalf("published output: %v", err)
			}
		})
	}
}
