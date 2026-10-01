package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/queue"
)

func TestQueueFlagValidation(t *testing.T) {
	valid := []string{"-project", "project", "-rom", "rom", "-cases", "cases", "-corpus", "corpus", "-out", "out"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing project", nil, "-project flag is required"},
		{"missing ROM", valid[:2], "-rom flag is required"},
		{"missing cases", valid[:4], "-cases flag is required"},
		{"missing corpus", valid[:6], "-corpus flag is required"},
		{"missing output", valid[:8], "-out flag is required"},
	}
	for _, tt := range []struct{ name, flag, value string }{{"zero candidates", "-limit", "0"}, {"negative candidates", "-limit", "-1"}, {"large candidates", "-limit", "101"}, {"zero cases", "-maxcases", "0"}, {"large cases", "-maxcases", "10001"}, {"zero steps", "-maxsteps", "0"}, {"large steps", "-maxsteps", "1000001"}} {
		args := append(append([]string(nil), valid...), tt.flag, tt.value)
		tests = append(tests, struct {
			name string
			args []string
			want string
		}{tt.name, args, "queue budget out of range"})
	}
	tests = append(tests, struct {
		name string
		args []string
		want string
	}{"format", append(append([]string(nil), valid...), "-format", "xml"), "invalid format"})
	tests = append(tests, struct {
		name string
		args []string
		want string
	}{"positional", append(append([]string(nil), valid...), "arbitrary-command"), "unexpected arguments"})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errout bytes.Buffer
			err := runQueue(tt.args, &out, &errout)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
			if out.Len() != 0 {
				t.Fatal("invalid flags published output")
			}
		})
	}
}

func queueFixture(t *testing.T) (args []string, project, out string) {
	t.Helper()
	dir := t.TempDir()
	project = filepath.Join(dir, "project")
	out = filepath.Join(dir, "out")
	rom := makeSyntheticLoROM(64)
	copy(rom[:3], []byte{0xa9, 0x01, 0x60})
	romPath := filepath.Join(dir, "rom.sfc")
	if err := os.WriteFile(romPath, rom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	d := recovery.NewDocument(recovery.ComputeROMIdentity(rom, rom, "none", "lorom"))
	ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	d.Instructions = []recovery.Instruction{{ID: "load", Architecture: "wdc65816", Address: 0x8000, Offset: 0, Bytes: "a901", Opcode: 0xa9, Mnemonic: "LDA", Mode: "immediate", Context: ctx}, {ID: "return", Architecture: "wdc65816", Address: 0x8002, Offset: 2, Bytes: "60", Opcode: 0x60, Mnemonic: "RTS", Mode: "implied", Context: ctx}}
	d.Edges = []recovery.Edge{{ID: "next", Kind: "fallthrough", Source: "load", Destination: 0x8002}}
	f, err := os.Create(filepath.Join(project, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.Encode(f, d); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	cases := filepath.Join(dir, "cases.jsonl")
	if err := os.WriteFile(cases, nil, 0600); err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(dir, "corpus")
	if err := os.Mkdir(corpus, 0700); err != nil {
		t.Fatal(err)
	}
	args = []string{"queue", "-project", project, "-rom", romPath, "-cases", cases, "-corpus", corpus, "-out", out, "-limit", "1", "-maxcases", "2"}
	return args, project, out
}

func TestQueueNewCandidateBlocked(t *testing.T) {
	args, project, outdir := queueFixture(t)
	before, err := os.ReadFile(filepath.Join(project, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(append(args, "-format", "json"), &stdout, &stderr); err != nil {
		t.Fatalf("queue: %v (%s)", err, stderr.String())
	}
	var report queue.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema == "" || report.Revision == "" || len(report.Candidates) != 1 {
		t.Fatalf("missing durable candidate result: %+v", report)
	}
	r := report.Candidates[0]
	if r.Status != "blocked" || r.Reason == "" || r.ReasonCode == "" || r.Admitted != 0 || r.Matched != 0 {
		t.Fatalf("case-free candidate promoted: %+v", r)
	}
	after, err := os.ReadFile(filepath.Join(project, "recovery.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("queue mutated project")
	}
	entries, err := os.ReadDir(outdir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no queue artifacts: %v", err)
	}
}

func TestQueuePreservesExistingOutput(t *testing.T) {
	args, _, out := queueFixture(t)
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "notes.txt")
	want := []byte("unrelated work\n")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// The core may refuse an occupied root or publish into an owned subdirectory.
	// Neither policy can remove or alter the unrelated file.
	_ = run(args, &stdout, &stderr)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("queue changed unrelated output")
	}
}

func TestQueueHelp(t *testing.T) {
	var out, errout bytes.Buffer
	if err := run([]string{"help", "queue"}, &out, &errout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "compilation do not grant qualification") || !strings.Contains(out.String(), "-maxcases") {
		t.Fatalf("help omits scope/budget: %s", out.String())
	}
}

// TestQueueCapturedCorpus is an opt-in CLI integration gate with retained captures.
// It does not manufacture admission from a synthetic fixture.
func TestQueueCapturedCorpus(t *testing.T) {
	project := os.Getenv("SNES_QUEUE_PROJECT")
	if project == "" {
		t.Skip("set SNES_QUEUE_PROJECT/ROM/CASES/CORPUS to run retained-capture integration")
	}
	paths := []struct{ flag, env string }{{"project", "SNES_QUEUE_PROJECT"}, {"rom", "SNES_QUEUE_ROM"}, {"cases", "SNES_QUEUE_CASES"}, {"corpus", "SNES_QUEUE_CORPUS"}}
	args := []string{"queue"}
	for _, p := range paths {
		value := os.Getenv(p.env)
		if value == "" {
			t.Fatalf("%s is required", p.env)
		}
		args = append(args, "-"+p.flag, value)
	}
	args = append(args, "-out", filepath.Join(t.TempDir(), "queue"), "-limit", "100", "-maxcases", "1", "-format", "json")
	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("captured queue: %v (%s)", err, stderr.String())
	}
	var report queue.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	qualified := 0
	for _, r := range report.Candidates {
		if r.Status == "qualified" {
			qualified++
			if r.Admitted < 1 || r.Matched != r.Admitted || r.Refused != 0 || r.Mismatched != 0 || r.Unexecuted != 0 || r.SourceSHA256 == "" || r.IRSHA256 == "" {
				t.Fatalf("unsubstantiated qualification: %+v", r)
			}
		}
	}
	if qualified == 0 {
		for _, r := range report.Candidates {
			t.Logf("%s status=%s code=%s cases=%d admitted=%d matched=%d refused=%d mismatched=%d reason=%s", r.Candidate.ID, r.Status, r.ReasonCode, r.Cases, r.Admitted, r.Matched, r.Refused, r.Mismatched, r.Reason)
		}
		t.Fatal("no admitted fresh captured candidate qualified")
	}
	t.Logf("%d candidates, %d qualified on bounded admitted captures", len(report.Candidates), qualified)
}

func TestQueuePolicyPair(t *testing.T) {
	var stdout, stderr bytes.Buffer
	for _, flag := range []string{"-policy", "-policy-sha256"} {
		err := runQueue([]string{"-project", "p", "-rom", "r", "-cases", "c", "-corpus", "root", "-out", "out", flag, "value"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "supplied together") {
			t.Fatalf("%s: %v", flag, err)
		}
	}
}
