package progress

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/extractor"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/queue"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func fixture() (*recovery.Document, *coverage.Index) {
	sha := strings.Repeat("a", 64)
	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: sha, NormalizedSize: 65536})
	doc.Instructions = []recovery.Instruction{{ID: "one", Address: 0x808000, Offset: 0, Bytes: "ea", Opcode: 0xea}, {ID: "mirror", Address: 0x008000, Offset: 0, Bytes: "ea", Opcode: 0xea}, {ID: "two", Address: 0x808001, Offset: 1, Bytes: "a901", Opcode: 0xa9}}
	idx := coverage.NewIndex(sha)
	idx.Runs["a"] = coverage.RunInfo{ID: "a", ROM_SHA256: sha, StreamSHA: strings.Repeat("b", 64), EventCount: 3}
	idx.Sites = []coverage.Site{{RunID: "a", InstructionID: "one", Address: 0x808000, HasROMOffset: true, Offset: 0, Hits: 1, Frames: []uint64{0}, FrameHits: []uint64{1}}, {RunID: "a", InstructionID: "mirror", Address: 0x008000, HasROMOffset: true, Offset: 0, Hits: 2, Frames: []uint64{0}, FrameHits: []uint64{2}}}
	return doc, idx
}
func pin(t *testing.T, dir, name string, v any) workflow.Input {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, name)
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return workflow.Input{Path: p, SHA256: digest(b)}
}
func TestCountsAndUnavailable(t *testing.T) {
	doc, idx := fixture()
	r, e := summarize(doc, idx)
	if e != nil {
		t.Fatal(e)
	}
	if *r.Metrics[0].Count != "1" || *r.Metrics[1].Count != "3" || r.Metrics[2].Count != nil {
		t.Fatalf("wrong units/counts: %+v", r.Metrics)
	}
	if len(r.Banks) != 2 || r.Banks[0].DecodedBytes != 3 || *r.Banks[0].ReachedStarts != 1 || *r.Banks[1].ReachedStarts != 0 {
		t.Fatal(r.Banks)
	}
	absent, e := summarize(doc, nil)
	if e != nil || absent.Metrics[0].Count != nil || absent.Banks[1].ReachedStarts != nil {
		t.Fatal("missing coverage became zero")
	}
	empty := coverage.NewIndex(doc.ROM.NormalizedSHA256)
	r, e = summarize(doc, empty)
	if e != nil || *r.Metrics[0].Count != "0" {
		t.Fatal("empty recorded index lost zero")
	}
}
func TestInvalidEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*recovery.Document, *coverage.Index)
	}{
		{"wrong rom", func(d *recovery.Document, i *coverage.Index) { i.ROMHash = strings.Repeat("c", 64) }},
		{"offset", func(d *recovery.Document, i *coverage.Index) { i.Sites[0].Offset = 65536 }},
		{"unknown run", func(d *recovery.Document, i *coverage.Index) { i.Sites[0].RunID = "missing" }},
		{"duplicate site", func(d *recovery.Document, i *coverage.Index) { i.Sites = append(i.Sites, i.Sites[0]) }},
		{"frame mismatch", func(d *recovery.Document, i *coverage.Index) { i.Sites[0].FrameHits[0] = 2 }},
		{"run mismatch", func(d *recovery.Document, i *coverage.Index) { r := i.Runs["a"]; r.EventCount = 99; i.Runs["a"] = r }},
		{"conflicting decode", func(d *recovery.Document, i *coverage.Index) {
			d.Instructions[1].Bytes = "eb"
			d.Instructions[1].Opcode = 0xeb
		}},
		{"instruction bounds", func(d *recovery.Document, i *coverage.Index) { d.Instructions[2].Offset = 65535 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, i := fixture()
			tt.change(d, i)
			r, e := summarize(d, i)
			if e == nil || r != nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
}
func TestLoadPinsAndRefuseClaimedQualification(t *testing.T) {
	dir := t.TempDir()
	doc, idx := fixture()
	cfg := Config{Schema: "snes-progress-config-v1", Recovery: pin(t, dir, "recovery.json", doc), Coverage: pin(t, dir, "coverage.json", idx)}
	config := pin(t, dir, "config.json", cfg)
	r, e := Load(context.Background(), config.Path, config.SHA256)
	if e != nil {
		t.Fatal(e)
	}
	b, ok := r.Evidence(1)
	if !ok || digest(b) != cfg.Recovery.SHA256 {
		t.Fatal("evidence pin mismatch")
	}
	b[0] = 0
	original, _ := r.Evidence(1)
	if digest(original) != cfg.Recovery.SHA256 {
		t.Fatal("evidence copy mutated snapshot")
	}
	if e = os.WriteFile(cfg.Coverage.Path, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	if r, e := Load(context.Background(), config.Path, config.SHA256); e == nil || r != nil {
		t.Fatal("stale pin accepted")
	}
	cfg.Coverage = workflow.Input{}
	cfg.Batch = &Batch{Directory: dir, Config: cfg.Recovery, ManifestSHA256: strings.Repeat("a", 64)}
	config = pin(t, dir, "config.json", cfg)
	if r, e := Load(context.Background(), config.Path, config.SHA256); e == nil || r != nil {
		t.Fatal("unverified batch granted qualification")
	}
}
func TestEmittedRefusedAndMissingCapture(t *testing.T) {
	dir := t.TempDir()
	doc, _ := fixture()
	r, _ := summarize(doc, nil)
	artifact := filepath.Join("validation-000006", "queue", "artifacts", "candidate", "generated.c")
	raw := []byte("int recovered(void) { return 1; }\n")
	hash := digest(raw)
	full := filepath.Join(dir, "task", artifact)
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, raw, 0600); e != nil {
		t.Fatal(e)
	}
	row := workflow.BatchRow{CandidateID: "candidate", Entry: 0x8000, ROMSHA256: doc.ROM.NormalizedSHA256, Directory: "task", Stage: "validation", Status: "refused", State: workflow.State{Artifacts: map[string]string{artifact: hash}}, Results: []queue.Result{{Directory: filepath.Join("artifacts", "candidate"), SourceSHA256: hash}}, Extraction: &extractor.ExtractionReceipt{CompleteExecutions: 2}}
	batch := &workflow.BatchReport{Rows: []workflow.BatchRow{row, {CandidateID: "pending", ROMSHA256: doc.ROM.NormalizedSHA256, Entry: 0x9000, Status: "unexecuted"}}, Artifacts: map[string]string{filepath.Join("task", artifact): hash}}
	if e := r.addBatch(dir, batch); e != nil {
		t.Fatal(e)
	}
	if *r.Metrics[2].Count != "2" || !strings.Contains(r.Metrics[2].Scope, "1 of 2") || *r.Metrics[3].Count != "1" || *r.Metrics[4].Count != "0" {
		t.Fatal(r.Metrics)
	}
	if !r.Candidates[0].Emitted || r.Candidates[1].Captured != nil {
		t.Fatal("emission lost or unavailable capture became zero")
	}
	// Repeated validations may retain identical emitted bytes in more than one generation.
	duplicate := filepath.Join("validation-000007", "queue", "artifacts", "candidate", "generated.c")
	other := filepath.Join(dir, "task", duplicate)
	if e := os.MkdirAll(filepath.Dir(other), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(other, raw, 0600); e != nil {
		t.Fatal(e)
	}
	batch.Rows[0].State.Artifacts[duplicate] = hash
	batch.Artifacts[filepath.Join("task", duplicate)] = hash
	retry, _ := summarize(doc, nil)
	if e := retry.addBatch(dir, batch); e != nil || *retry.Metrics[3].Count != "1" {
		t.Fatal("identical retry artifact inflated or refused emission", e)
	}
	// Generated source is bound to both journal and verified batch inventories.
	batch.Artifacts = map[string]string{}
	r, _ = summarize(doc, nil)
	if e := r.addBatch(dir, batch); e == nil {
		t.Fatal("unbound emission accepted")
	}
}
