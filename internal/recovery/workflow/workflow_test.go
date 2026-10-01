package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/queue"
)

func testInput(t *testing.T, dir, name string, b []byte) Input {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return Input{p, digest(b)}
}
func setup(t *testing.T) (Options, Config) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	rom := make([]byte, 32768)
	rom[0] = 0x60
	rom[0x7fd5] = 0x20
	doc := recovery.NewDocument(recovery.ComputeROMIdentity(rom, rom, "none", "lorom"))
	if err := artifactJSON(project, "recovery.json", doc); err != nil {
		t.Fatal(err)
	}
	c := Config{Candidate: testInput(t, root, "candidate.json", []byte(`{"id":"leaf-008000","entry":32768,"instruction_count":1,"byte_span":1,"returns":[32768]}`)), CandidateID: "leaf-008000", ROM: testInput(t, root, "rom.sfc", rom), ProjectDir: project, ProjectRevision: recovery.ComputeProjectRevision(project, doc), CorpusRoot: root, Label: "test", Corpus: "test-leaf", MaxFrames: 10, MaxCases: 1, MaxSteps: 100, QueueLimit: 5}
	b, _ := json.Marshal(c)
	return Options{Dir: filepath.Join(root, "task"), Config: testInput(t, root, "config.json", b), MaxTransitions: 5}, c
}
func TestResumeWaiting(t *testing.T) {
	o, _ := setup(t)
	s, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != "await_capture" || s.Sequence != 2 || s.Policy.Path != "" {
		t.Fatalf("state %+v", s)
	}
	again, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if again.Sequence != s.Sequence {
		t.Fatal("resume appended duplicate generation")
	}
	if _, err := os.Stat(filepath.Join(o.Dir, "capture-request.json")); err != nil {
		t.Fatal(err)
	}
}
func TestInputSubstitution(t *testing.T) {
	for _, name := range []string{"config", "candidate", "ROM", "project", "artifact"} {
		t.Run(name, func(t *testing.T) {
			o, c := setup(t)
			if _, err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			path := o.Config.Path
			switch name {
			case "candidate":
				path = c.Candidate.Path
			case "ROM":
				path = c.ROM.Path
			case "project":
				path = filepath.Join(c.ProjectDir, "recovery.json")
			case "artifact":
				path = filepath.Join(o.Dir, "capture-request.json")
			}
			b, _ := os.ReadFile(path)
			if err := os.WriteFile(path, append(b, ' '), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(context.Background(), o); err == nil {
				t.Fatal("accepted substitution")
			}
			files, _ := journalFiles(o.Dir)
			if len(files) != 2 {
				t.Fatal("failed resume mutated journal")
			}
		})
	}
}
func TestBudget(t *testing.T) {
	for _, n := range []int{0, 11} {
		o, _ := setup(t)
		o.MaxTransitions = n
		if _, err := Run(context.Background(), o); err == nil {
			t.Fatal("accepted transition bound")
		}
	}
	for _, field := range []string{"frames", "cases", "steps", "queue"} {
		t.Run(field, func(t *testing.T) {
			o, c := setup(t)
			switch field {
			case "frames":
				c.MaxFrames = 0
			case "cases":
				c.MaxCases = 10001
			case "steps":
				c.MaxSteps = 1000001
			case "queue":
				c.QueueLimit = 101
			}
			b, _ := json.Marshal(c)
			o.Config = testInput(t, filepath.Dir(o.Config.Path), "bad-config.json", b)
			if _, err := Run(context.Background(), o); err == nil {
				t.Fatal("accepted unbounded config")
			}
		})
	}
}
func TestJournalCorruptionAndTemporary(t *testing.T) {
	o, _ := setup(t)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Dir, "journal", ".generation-interrupted"), []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.Dir, "journal", "000002.json")
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), "await_capture", "qualified", 1))
	os.WriteFile(p, b, 0600)
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("accepted journal corruption")
	}
}
func TestExistingDirectory(t *testing.T) {
	o, _ := setup(t)
	os.Mkdir(o.Dir, 0700)
	notes := filepath.Join(o.Dir, "notes")
	os.WriteFile(notes, []byte("mine"), 0600)
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("adopted unrelated directory")
	}
	b, _ := os.ReadFile(notes)
	if string(b) != "mine" {
		t.Fatal("removed unrelated notes")
	}
}
func TestJournalLock(t *testing.T) {
	o, _ := setup(t)
	_, cand, err := loadConfig(o.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, unlock, err := openJournal(o.Dir, o.Config, cand)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("concurrent task accepted")
	}
}
func TestInterruptedPublication(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "extraction"), 0700)
	os.WriteFile(filepath.Join(dir, "extraction", "old"), []byte("unjournaled"), 0600)
	err := runStep(dir, "extraction", func(stage string) error { return os.WriteFile(filepath.Join(stage, "new"), []byte("fresh"), 0600) })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "orphan-extraction-1", "old")); string(b) != "unjournaled" {
		t.Fatal("discarded interrupted output")
	}
	if _, err := os.Stat(filepath.Join(dir, "extraction", "old")); !os.IsNotExist(err) {
		t.Fatal("adopted uncommitted artifact")
	}
}
func TestCancellation(t *testing.T) {
	o, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, o); err != context.Canceled {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(o.Dir, "capture-request.json")); !os.IsNotExist(err) {
		t.Fatal("published canceled capture request")
	}
}
func TestNoPolicyDiscovery(t *testing.T) {
	o, c := setup(t)
	_, cand, err := loadConfig(o.Config)
	if err != nil {
		t.Fatal(err)
	}
	s, unlock, err := openJournal(o.Dir, o.Config, cand)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = "await_policy"
	if err = appendState(o.Dir, &s); err != nil {
		t.Fatal(err)
	}
	unlock()
	os.WriteFile(filepath.Join(c.CorpusRoot, "proposed-trust-root.json"), []byte(`{"Admitted":true}`), 0600)
	s, err = Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != "await_policy" || s.Policy.Path != "" || s.Sequence != 2 {
		t.Fatal("discovered producer policy authority")
	}
}

func testEvidence(t *testing.T, root string) Input {
	t.Helper()
	trace := testInput(t, root, "stream.jsonl", nil)
	receipt := testInput(t, root, "receipt.json", []byte(`{}`))
	summary := testInput(t, root, "summary.json", []byte(`{"frames":1}`))
	stream := Stream{Trace: trace, Receipt: receipt, Summary: summary}
	b, _ := json.Marshal(Evidence{Fixture: stream, Capture: stream, History: stream})
	return testInput(t, root, "evidence.json", b)
}
func TestDeliverySubstitution(t *testing.T) {
	for _, name := range []string{"evidence file", "stream", "policy file", "different delivery"} {
		t.Run(name, func(t *testing.T) {
			o, c := setup(t)
			_, cand, err := loadConfig(o.Config)
			if err != nil {
				t.Fatal(err)
			}
			s, unlock, err := openJournal(o.Dir, o.Config, cand)
			if err != nil {
				t.Fatal(err)
			}
			s.Phase = "blocked"
			s.Evidence = testEvidence(t, c.CorpusRoot)
			s.Policy = testInput(t, c.CorpusRoot, "policy.json", []byte(`{}`))
			if err = appendState(o.Dir, &s); err != nil {
				t.Fatal(err)
			}
			unlock()
			switch name {
			case "evidence file":
				os.WriteFile(s.Evidence.Path, []byte("bad"), 0600)
			case "stream":
				os.WriteFile(filepath.Join(c.CorpusRoot, "stream.jsonl"), []byte("bad"), 0600)
			case "policy file":
				os.WriteFile(s.Policy.Path, []byte("bad"), 0600)
			case "different delivery":
				o.Evidence = testInput(t, c.CorpusRoot, "other.json", []byte(`{}`))
			}
			if _, err := Run(context.Background(), o); err == nil {
				t.Fatal("accepted delivery substitution")
			}
		})
	}
}
func TestArtifactPathEscape(t *testing.T) {
	if err := verifyArtifacts(t.TempDir(), State{Artifacts: map[string]string{"../secret": "hash"}}); err == nil {
		t.Fatal("accepted path escape")
	}
}
func TestRequestPublicationRecovery(t *testing.T) {
	dir := t.TempDir()
	if err := artifactJSON(dir, "request.json", map[string]int{"limit": 1}); err != nil {
		t.Fatal(err)
	}
	if err := artifactJSON(dir, "request.json", map[string]int{"limit": 1}); err != nil {
		t.Fatal(err)
	}
	if err := artifactJSON(dir, "request.json", map[string]int{"limit": 2}); err == nil {
		t.Fatal("overwrote pending request")
	}
}

func TestNonRegularAndOversizedInput(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pinned(Input{fifo, digest(nil)}, 0); err == nil {
		t.Fatal("accepted FIFO")
	}
	p := filepath.Join(root, "sparse")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(2<<30 + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = pinned(Input{p, digest(nil)}, 0); err == nil {
		t.Fatal("accepted stream size above bound")
	}
}

func TestSelectedQualification(t *testing.T) {
	valid := queue.Result{Candidate: candidates.Candidate{Entry: 0x8000}, Status: "qualified", Matched: 1, Admitted: 1}
	if selectedCases(&queue.Report{Candidates: []queue.Result{valid}}, 0x8000) != 1 {
		t.Fatal("lost selected qualified case")
	}
	for _, name := range []string{"other entry", "refusal", "mismatch", "unexecuted", "no cases"} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			switch name {
			case "other entry":
				bad.Candidate.Entry++
			case "refusal":
				bad.Refused = 1
			case "mismatch":
				bad.Mismatched = 1
			case "unexecuted":
				bad.Unexecuted = 1
			case "no cases":
				bad.Matched = 0
			}
			if selectedCases(&queue.Report{Candidates: []queue.Result{bad}}, 0x8000) != 0 {
				t.Fatal("promoted unqualified selected task")
			}
		})
	}
}
