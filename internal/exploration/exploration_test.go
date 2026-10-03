package exploration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestPrivateToolPinsExecutedBytes(t *testing.T) {
	d := testDir(t)
	source := filepath.Join(d, "source")
	original := []byte("#!/bin/sh\nprintf original\\n\n")
	if e := os.WriteFile(source, original, 0700); e != nil {
		t.Fatal(e)
	}
	stage := filepath.Join(d, "private")
	if e := os.Mkdir(stage, 0700); e != nil {
		t.Fatal(e)
	}
	pin := workflow.Input{Path: source, SHA256: digest(original)}
	copy, e := copyTool(stage, pin)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(source, []byte("#!/bin/sh\nprintf substituted\\n\n"), 0700); e != nil {
		t.Fatal(e)
	}
	got, e := exec.Command(copy).Output()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.HasPrefix(got, []byte("original")) {
		t.Fatalf("executed substituted tool: %q", got)
	}
	if _, e = copyTool(stage, pin); e == nil {
		t.Fatal("changed original accepted")
	}
	if _, e = read(workflow.Input{Path: copy, SHA256: pin.SHA256}, 128<<20); e != nil {
		t.Fatal(e)
	}
}

func createValidWinnerBaseline(t *testing.T, dir, romSHA string) (expectedState, expectedInput, expectedSite string) {
	framesDir := filepath.Join(dir, "frames")
	if err := os.MkdirAll(framesDir, 0755); err != nil {
		t.Fatal(err)
	}

	chkState := []byte("checkpoint-state-bytes")
	expectedState = digest(chkState)
	expectedInput = digest([]byte("[0]"))

	tracePath := filepath.Join(dir, "trace.jsonl")
	insnRecord := `{"id":1,"schema":2,"kind":"cpu_insn","frame":0,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true}}}`
	runHeader := fmt.Sprintf(`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":%q,"initial_state_sha256":%q,"replay_input_sha256":%q,"start":"checkpoint","engine_revision":"rev1","engine_dirty":false,"mapper":"lorom"}}`, romSHA, expectedState, expectedInput)
	traceContent := runHeader + "\n" + insnRecord + "\n"
	if err := os.WriteFile(tracePath, []byte(traceContent), 0600); err != nil {
		t.Fatal(err)
	}
	tsha := digest([]byte(traceContent))

	receiptPath := filepath.Join(dir, "trace.receipt.json")
	trJSON := fmt.Sprintf(`{"schema":2,"outcome":"complete","stream_sha256":%q,"event_count":2}`+"\n", tsha)
	if err := os.WriteFile(receiptPath, []byte(trJSON), 0600); err != nil {
		t.Fatal(err)
	}

	a := uint32(0x8000)
	c := uint8(14)
	sitesJSON, _ := json.Marshal([]site{{Address: a, Context: c, Hits: 1}})
	expectedSite = digest(sitesJSON)

	manifestPath := filepath.Join(framesDir, "frames.jsonl")
	frameHeader := fmt.Sprintf(`{"schema":1,"kind":"frame_run","run":{"rom_sha256":%q,"initial_state_sha256":%q,"replay_input_sha256":%q,"start":"checkpoint","engine_revision":"rev1","engine_dirty":false,"mapper":"lorom"}}`, romSHA, expectedState, expectedInput)
	frameRecord := `{"kind":"frame","index":0,"number":0,"start":0,"vblank":306900,"stored":true,"width":256,"height":224}`
	manifestContent := frameHeader + "\n" + frameRecord + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0600); err != nil {
		t.Fatal(err)
	}
	msha := digest([]byte(manifestContent))

	frameReceiptPath := filepath.Join(framesDir, "frames.receipt.json")
	frJSON := fmt.Sprintf(`{"schema":1,"outcome":"complete","frames":1,"stored":1,"manifest_sha256":%q}`+"\n", msha)
	if err := os.WriteFile(frameReceiptPath, []byte(frJSON), 0600); err != nil {
		t.Fatal(err)
	}

	summaryPath := filepath.Join(dir, "summary.json")
	sumJSON := fmt.Sprintf(`{"frame_summary":[{"frame":0,"state_hash":%q}]}`+"\n", expectedState)
	if err := os.WriteFile(summaryPath, []byte(sumJSON), 0600); err != nil {
		t.Fatal(err)
	}

	return expectedState, expectedInput, expectedSite
}

func TestCaptureWinnerValidationControls(t *testing.T) {
	d := testDir(t)
	romSHA := strings.Repeat("a", 64)

	for _, tt := range []struct {
		name       string
		tamper     func(dir string, expectedSite *string)
		wantStatus string
		wantReason string
	}{
		{
			name:       "authentic_baseline",
			tamper:     nil,
			wantStatus: "complete",
			wantReason: "",
		},
		{
			name: "missing_frame_receipt",
			tamper: func(dir string, _ *string) {
				os.Remove(filepath.Join(dir, "frames", "frames.receipt.json"))
			},
			wantStatus: "unqualified",
			wantReason: "frame receipt missing",
		},
		{
			name: "missing_summary",
			tamper: func(dir string, _ *string) {
				os.Remove(filepath.Join(dir, "summary.json"))
			},
			wantStatus: "unqualified",
			wantReason: "summary missing",
		},
		{
			name: "frame_outcome_limit",
			tamper: func(dir string, _ *string) {
				rcPath := filepath.Join(dir, "frames", "frames.receipt.json")
				data, _ := os.ReadFile(rcPath)
				var m map[string]any
				json.Unmarshal(data, &m)
				m["outcome"] = "limit"
				b, _ := json.MarshalIndent(m, "", "  ")
				os.WriteFile(rcPath, b, 0600)
			},
			wantStatus: "unqualified",
			wantReason: `frame capture outcome "limit"`,
		},
		{
			name: "wrong_final_state",
			tamper: func(dir string, _ *string) {
				smPath := filepath.Join(dir, "summary.json")
				data, _ := os.ReadFile(smPath)
				var m map[string]any
				json.Unmarshal(data, &m)
				fsList := m["frame_summary"].([]any)
				fs0 := fsList[len(fsList)-1].(map[string]any)
				fs0["state_hash"] = "0000000000000000000000000000000000000000000000000000000000000000"
				b, _ := json.MarshalIndent(m, "", "  ")
				os.WriteFile(smPath, b, 0600)
			},
			wantStatus: "unqualified",
			wantReason: "final state mismatch",
		},
		{
			name: "false_stream_digest",
			tamper: func(dir string, _ *string) {
				rcPath := filepath.Join(dir, "trace.receipt.json")
				data, _ := os.ReadFile(rcPath)
				var m map[string]any
				json.Unmarshal(data, &m)
				m["stream_sha256"] = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				b, _ := json.MarshalIndent(m, "", "  ")
				os.WriteFile(rcPath, b, 0600)
			},
			wantStatus: "unqualified",
			wantReason: "trace stream hash mismatch",
		},
		{
			name: "wrong_event_count",
			tamper: func(dir string, _ *string) {
				rcPath := filepath.Join(dir, "trace.receipt.json")
				data, _ := os.ReadFile(rcPath)
				var m map[string]any
				json.Unmarshal(data, &m)
				m["event_count"] = 1
				b, _ := json.MarshalIndent(m, "", "  ")
				os.WriteFile(rcPath, b, 0600)
			},
			wantStatus: "unqualified",
			wantReason: "trace event count",
		},
		{
			name: "wrong_trace_run_header",
			tamper: func(dir string, _ *string) {
				tPath := filepath.Join(dir, "trace.jsonl")
				tBytes, _ := os.ReadFile(tPath)
				lines := strings.Split(string(tBytes), "\n")
				var h map[string]any
				json.Unmarshal([]byte(lines[0]), &h)
				runMap := h["run"].(map[string]any)
				runMap["rom_sha256"] = strings.Repeat("0", 64)
				newH, _ := json.Marshal(h)
				lines[0] = string(newH)
				newContent := strings.Join(lines, "\n")
				os.WriteFile(tPath, []byte(newContent), 0600)
				newSHA := digest([]byte(newContent))
				rcPath := filepath.Join(dir, "trace.receipt.json")
				data, _ := os.ReadFile(rcPath)
				var m map[string]any
				json.Unmarshal(data, &m)
				m["stream_sha256"] = newSHA
				b, _ := json.MarshalIndent(m, "", "  ")
				os.WriteFile(rcPath, b, 0600)
			},
			wantStatus: "unqualified",
			wantReason: "run identity",
		},
		{
			name: "site_census_divergence",
			tamper: func(dir string, expectedSite *string) {
				*expectedSite = strings.Repeat("0", 64)
			},
			wantStatus: "unqualified",
			wantReason: "site census",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stage := filepath.Join(d, tt.name)
			expState, expInput, expSite := createValidWinnerBaseline(t, stage, romSHA)
			if tt.tamper != nil {
				tt.tamper(stage, &expSite)
			}
			wc := WinnerCapture{
				Status:              "unqualified",
				PrefixExpectedState: expState,
				InputSHA256:         expInput,
				PrefixExpectedSite:  expSite,
				Frames:              1,
			}
			err := validateWinnerArtifacts(stage, 500000, 250000000, romSHA, expState, expState, expInput, expSite, 1, &wc)
			if err != nil {
				wc.Reason = err.Error()
			} else {
				wc.Status = "complete"
			}
			if wc.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (reason: %q)", wc.Status, tt.wantStatus, wc.Reason)
			}
			if tt.wantReason != "" && !strings.Contains(wc.Reason, tt.wantReason) {
				t.Fatalf("reason = %q, want containing %q", wc.Reason, tt.wantReason)
			}
		})
	}
}

func reviewRewriteRunHeader(t *testing.T, dir string, frame bool, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, "trace.jsonl")
	receipt := filepath.Join(dir, "trace.receipt.json")
	digestField := "stream_sha256"
	if frame {
		path = filepath.Join(dir, "frames", "frames.jsonl")
		receipt = filepath.Join(dir, "frames", "frames.receipt.json")
		digestField = "manifest_sha256"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var h map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		t.Fatal(err)
	}
	edit(h["run"].(map[string]any))
	b, err = json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	lines[0] = string(b)
	data := []byte(strings.Join(lines, "\n"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	r[digestField] = digest(data)
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRequiredWinnerRunIdentity(t *testing.T) {
	for _, tt := range []struct {
		name      string
		frame     bool
		edit      func(map[string]any)
		wantError bool
	}{
		{name: "fully pinned baseline"},
		{name: "wrong trace initial rejects", edit: func(r map[string]any) { r["initial_state_sha256"] = strings.Repeat("0", 64) }, wantError: true},
		{name: "missing trace initial", edit: func(r map[string]any) { delete(r, "initial_state_sha256") }, wantError: true},
		{name: "missing trace input", edit: func(r map[string]any) { delete(r, "replay_input_sha256") }, wantError: true},
		{name: "missing frame initial", frame: true, edit: func(r map[string]any) { delete(r, "initial_state_sha256") }, wantError: true},
		{name: "missing frame input", frame: true, edit: func(r map[string]any) { delete(r, "replay_input_sha256") }, wantError: true},
		{name: "frame engine identity differs", frame: true, edit: func(r map[string]any) { r["engine_revision"] = strings.Repeat("b", 40) }, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			rom := strings.Repeat("a", 64)
			state, input, site := createValidWinnerBaseline(t, dir, rom)
			for _, frame := range []bool{false, true} {
				reviewRewriteRunHeader(t, dir, frame, func(r map[string]any) {
					r["engine_revision"] = "f360f7b5bcc2392f3f21024d3429bad39ec0056e"
					r["engine_dirty"] = false
					r["start"] = "checkpoint"
					r["mapper"] = "lorom"
					r["rom_provenance"] = "lorom"
					r["events"] = []string{"cpu_insn", "cpu_transition", "bus", "mmio", "dma", "ppu"}
					r["limits"] = map[string]any{"events": 500000, "bytes": 250000000, "frames": 1}
				})
			}
			if tt.edit != nil {
				reviewRewriteRunHeader(t, dir, tt.frame, tt.edit)
			}
			var wc WinnerCapture
			err := validateWinnerArtifacts(dir, 500000, 250000000, rom, state, state, input, site, 1, &wc)
			t.Logf("expected_initial=%s expected_input=%s err=%v", state, input, err)
			if tt.wantError && err == nil {
				t.Errorf("incomplete or inconsistent run identity accepted")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("valid positive baseline rejected: %v", err)
			}
		})
	}
}

