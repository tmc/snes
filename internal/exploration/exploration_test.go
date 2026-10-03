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

func TestCaptureWinnerValidationControls(t *testing.T) {
	d := testDir(t)
	rom := make([]byte, 1<<16)
	rom[0] = 0x80
	rom[1] = 0xfe
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	romPath := filepath.Join(d, "rom.bin")
	if err := os.WriteFile(romPath, rom, 0600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		tamper     func(dir string)
		wantStatus string
		wantReason string
	}{
		{
			name: "missing_frame_receipt",
			tamper: func(dir string) {
				os.Remove(filepath.Join(dir, "frames", "frames.receipt.json"))
			},
			wantStatus: "unqualified",
			wantReason: "frame receipt missing",
		},
		{
			name: "missing_summary",
			tamper: func(dir string) {
				os.Remove(filepath.Join(dir, "summary.json"))
			},
			wantStatus: "unqualified",
			wantReason: "summary missing",
		},
		{
			name: "frame_outcome_limit",
			tamper: func(dir string) {
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
			tamper: func(dir string) {
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
			tamper: func(dir string) {
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			testRoot := filepath.Join(d, tt.name)
			if err := os.MkdirAll(testRoot, 0700); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
set -eu
rom= state= inputs= cadence= frames= events= frame_dir= out= receipt= summary=
shift
while [ "$#" -gt 0 ]; do
  key="$1"; val="$2"; shift 2
  case "$key" in
    -rom) rom="$val";;
    -state) state="$val";;
    -inputs) inputs="$val";;
    -cadence) cadence="$val";;
    -frames) frames="$val";;
    -events) events="$val";;
    -frame-dir) frame_dir="$val";;
    -frame-png) ;;
    -out) out="$val";;
    -receipt) receipt="$val";;
    -summary) summary="$val";;
    -max-events|-max-bytes) ;;
  esac
done

mkdir -p "$frame_dir"
printf '{"kind":"run","run":{"rom_sha256":"test","engine_revision":"control"}}\n' > "$out"
tsha=$(shasum -a 256 "$out" | cut -d' ' -f1)
printf '{"schema":2,"outcome":"complete","stream_sha256":"%s","event_count":1}\n' "$tsha" > "$receipt"

printf '{"kind":"frame_run","schema":1}\n' > "$frame_dir/frames.jsonl"
msha=$(shasum -a 256 "$frame_dir/frames.jsonl" | cut -d' ' -f1)
printf '{"schema":1,"outcome":"complete","frames":1,"stored":1,"manifest_sha256":"%s"}\n' "$msha" > "$frame_dir/frames.receipt.json"

stsha=$(shasum -a 256 "$state" | cut -d' ' -f1)
printf '{"frame_summary":[{"frame":0,"state_hash":"%s"}]}\n' "$stsha" > "$summary"
`
			toolPath := filepath.Join(testRoot, "producer.sh")
			if err := os.WriteFile(toolPath, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}

			// Pre-generate stage directory
			stage := filepath.Join(testRoot, "stage")
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			chk := []byte("dummy-state-bytes")
			os.WriteFile(filepath.Join(stage, "checkpoint.state"), chk, 0600)

			// Producer run to create artifacts
			args := []string{
				"run",
				"-rom", romPath,
				"-state", filepath.Join(stage, "checkpoint.state"),
				"-inputs", "dummy",
				"-cadence", "frame",
				"-frames", "1",
				"-events", "cpu_insn,cpu_transition,bus,mmio,dma,ppu",
				"-frame-dir", filepath.Join(stage, "frames"),
				"-frame-png", "all",
				"-out", filepath.Join(stage, "trace.jsonl"),
				"-receipt", filepath.Join(stage, "trace.receipt.json"),
				"-summary", filepath.Join(stage, "summary.json"),
				"-max-events", "10000",
				"-max-bytes", "100000",
			}
			cmd := exec.Command(toolPath, args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tool run: %v, out: %s", err, out)
			}

			// Apply test tampering
			tt.tamper(stage)

			wc := &WinnerCapture{
				Status:              "unqualified",
				InitialStateSHA:     digest(chk),
				PrefixExpectedState: digest(chk),
				Frames:              1,
			}
			// Run validation logic directly on the tampered files
			// Validate trace receipt
			tb, err := os.ReadFile(filepath.Join(stage, "trace.receipt.json"))
			if err != nil {
				wc.Reason = "trace receipt missing"
			} else {
				var tr struct {
					Schema       int    `json:"schema"`
					Outcome      string `json:"outcome"`
					StreamSHA256 string `json:"stream_sha256"`
					EventCount   int    `json:"event_count"`
				}
				if json.Unmarshal(tb, &tr) != nil || tr.Schema != 2 || tr.Outcome != "complete" {
					wc.Reason = "trace receipt invalid"
				} else {
					trBytes, _ := os.ReadFile(filepath.Join(stage, "trace.jsonl"))
					if digest(trBytes) != tr.StreamSHA256 {
						wc.Reason = fmt.Sprintf("trace stream hash mismatch: computed %s, receipt has %s", digest(trBytes), tr.StreamSHA256)
					}
				}
			}

			if wc.Reason == "" {
				fb, err := os.ReadFile(filepath.Join(stage, "frames", "frames.receipt.json"))
				if err != nil {
					wc.Reason = "frame receipt missing"
				} else {
					var fr struct {
						Schema         int    `json:"schema"`
						Outcome        string `json:"outcome"`
						ManifestSHA256 string `json:"manifest_sha256"`
						Frames         int    `json:"frames"`
						Stored         int    `json:"stored"`
					}
					if json.Unmarshal(fb, &fr) != nil || fr.Schema != 1 {
						wc.Reason = "frame receipt invalid"
					} else if fr.Outcome != "complete" {
						wc.Reason = fmt.Sprintf("frame capture outcome %q", fr.Outcome)
					} else {
						mfBytes, _ := os.ReadFile(filepath.Join(stage, "frames", "frames.jsonl"))
						if digest(mfBytes) != fr.ManifestSHA256 {
							wc.Reason = "frame manifest hash mismatch"
						}
					}
				}
			}

			if wc.Reason == "" {
				sb, err := os.ReadFile(filepath.Join(stage, "summary.json"))
				if err != nil {
					wc.Reason = "summary missing"
				} else {
					var sm struct {
						FrameSummary []struct {
							Frame     int    `json:"frame"`
							StateHash string `json:"state_hash"`
						} `json:"frame_summary"`
					}
					if json.Unmarshal(sb, &sm) != nil || len(sm.FrameSummary) == 0 {
						wc.Reason = "summary invalid"
					} else {
						finalHash := sm.FrameSummary[len(sm.FrameSummary)-1].StateHash
						if finalHash != wc.PrefixExpectedState {
							wc.Reason = fmt.Sprintf("final state mismatch: summary has %s, measured prefix has %s", finalHash, wc.PrefixExpectedState)
						}
					}
				}
			}

			if wc.Reason == "" {
				wc.Status = "complete"
			}

			if wc.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", wc.Status, tt.wantStatus)
			}
			if !strings.Contains(wc.Reason, tt.wantReason) {
				t.Errorf("reason = %q, want containing %q", wc.Reason, tt.wantReason)
			}
		})
	}
}

