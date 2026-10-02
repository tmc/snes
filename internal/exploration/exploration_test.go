package exploration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/extractor"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func testDir(t *testing.T) string {
	t.Helper()
	home, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	d, e := os.MkdirTemp(filepath.Join(home, "tmp"), "exploration-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}
func testPin(t *testing.T, dir, name string, v any) workflow.Input {
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
func fixture(t *testing.T) (string, Config, workflow.Input) {
	d := testDir(t)
	rom := testPin(t, d, "rom", 0)
	idx := coverage.NewIndex(rom.SHA256)
	idx.Runs["run"] = coverage.RunInfo{ID: "run", ROM_SHA256: rom.SHA256, IsComplete: true, Outcome: "complete", StreamSHA: "stream"}
	idx.Sites = []coverage.Site{{RunID: "run", Address: 0x9347, Hits: 652}}
	cov := testPin(t, d, "coverage", idx)
	dis := testPin(t, d, "discovery", candidates.Report{ROMSHA256: rom.SHA256, Sources: []candidates.Source{{Kind: "coverage_index", SHA256: cov.SHA256}}, Candidates: []candidates.Candidate{{ID: "hit", Entry: 0x9347, InstructionCount: 20}, {ID: "missing", Entry: 0x8000, InstructionCount: 30}}})
	old := testPin(t, d, "old", workflow.BatchReport{Rows: []workflow.BatchRow{{CandidateID: "hit", Entry: 0x9347, ROMSHA256: rom.SHA256, Stage: "extraction", Status: "refused", Extraction: &extractor.ExtractionReceipt{CaptureSHA256: "oldstream"}}}})
	summary := testPin(t, d, "summary", map[string]any{"rom_hash": rom.SHA256, "trace_hash": "oldstream", "pc_ranges": []map[string]any{{"space": "cpu", "start": 0x1000, "end": 0x1001}}})
	c := Config{Schema: "snes-exploration-config-v1", ROM: rom, Discovery: dis, Coverage: cov, Previous: old, PreviousCapture: summary, TraceTool: rom, Sources: []workflow.Input{dis}, Target: "hit", CapturePC: "cpu:00:9347-cpu:00:937a", BaselineFrames: 240, MaxSites: 10000, MaxInstructions: 1000000, MaxTraceEvents: 1000000, MaxTraceBytes: 1000000, ProjectRevision: "test"}
	for i := 0; i < 8; i++ {
		a := make([]uint16, 30)
		a[0] = uint16(i)
		c.Schedules = append(c.Schedules, a)
	}
	return d, c, testPin(t, d, "config", c)
}
func TestPlanClassification(t *testing.T) {
	_, c, _ := fixture(t)
	rows, e := plan(c)
	if e != nil {
		t.Fatal(e)
	}
	if rows[0].Status != "reached_but_filtered" || rows[0].Hits != 652 || rows[1].Status != "not_reached_in_pinned_runs" {
		t.Fatalf("rows=%+v", rows)
	}
}
func TestPinnedPublication(t *testing.T) {
	d, _, pin := fixture(t)
	out := filepath.Join(d, "output")
	r, e := Run(context.Background(), out, pin, false)
	if e != nil {
		t.Fatal(e)
	}
	if r.Stage != "discovery" {
		t.Fatal(r.Stage)
	}
	if _, e = Run(context.Background(), out, pin, false); e == nil {
		t.Fatal("existing output accepted")
	}
	if e = os.WriteFile(pin.Path, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Run(context.Background(), filepath.Join(d, "other"), pin, false); e == nil {
		t.Fatal("changed config accepted")
	}
	if _, e = os.Stat(filepath.Join(d, "other")); !os.IsNotExist(e) {
		t.Fatal("invalid config published")
	}
}
func TestBounds(t *testing.T) {
	d, c, _ := fixture(t)
	for _, tt := range []struct {
		name   string
		change func(*Config)
	}{{"frames", func(c *Config) { c.BaselineFrames = 241 }}, {"schedules", func(c *Config) { c.Schedules = c.Schedules[:7] }}, {"sites", func(c *Config) { c.MaxSites = 100001 }}, {"instructions", func(c *Config) { c.MaxInstructions = 10000001 }}} {
		t.Run(tt.name, func(t *testing.T) {
			v := c
			tt.change(&v)
			p := testPin(t, d, tt.name, v)
			if _, e := load(p); e == nil {
				t.Fatal("invalid bound accepted")
			}
		})
	}
}
func TestCounterContextsAndBudgets(t *testing.T) {
	o := counter{sites: map[uint64]uint64{}, max: 4, maxSites: 2}
	in := cpu.Observation{Entry: cpu.Snapshot{PB: 0, PC: 0x9347, P: 0x30}}
	o.ObserveInstruction(in)
	in.Entry.P |= 1
	o.ObserveInstruction(in)
	if len(o.sites) != 2 {
		t.Fatal(o.sites)
	}
	in.Entry.PC++
	o.ObserveInstruction(in)
	if o.fault == nil {
		t.Fatal("site budget ignored")
	}
}
func TestCancelledPublication(t *testing.T) {
	d, _, pin := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(d, "cancel")
	if _, e := Run(ctx, out, pin, false); e == nil {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("cancel published")
	}
}
