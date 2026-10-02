package exploration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/workflow"
)

// Config pins the campaign and its finite bounds. Schedules contains eight
// arrays of thirty port-zero controller states. Policy is optional reviewed
// admission authority; newly extracted proposals never grant that authority.
type Config struct {
	Schema          string           `json:"schema"`
	ROM             workflow.Input   `json:"rom"`
	Discovery       workflow.Input   `json:"discovery"`
	Coverage        workflow.Input   `json:"coverage"`
	Previous        workflow.Input   `json:"previous"`
	PreviousCapture workflow.Input   `json:"previous_capture"`
	Sources         []workflow.Input `json:"sources"`
	TraceTool       workflow.Input   `json:"trace_tool"`
	Target          string           `json:"target"`
	CapturePC       string           `json:"capture_pc"`
	BaselineFrames  int              `json:"baseline_frames"`
	MaxSites        int              `json:"max_sites"`
	MaxInstructions uint64           `json:"max_instructions"`
	MaxTraceEvents  int              `json:"max_trace_events"`
	MaxTraceBytes   int64            `json:"max_trace_bytes"`
	Schedules       [][]uint16       `json:"schedules"`
	Policy          workflow.Input   `json:"policy,omitempty"`
	ProjectDir      string           `json:"project_dir,omitempty"`
	ProjectRevision string           `json:"project_revision"`
}

type target struct {
	ID                  string   `json:"id"`
	Entry               uint32   `json:"entry"`
	Hits                uint64   `json:"discovery_hits"`
	Contexts            []string `json:"contexts"`
	Quality             string   `json:"quality"`
	Status              string   `json:"status"`
	PreviousStatus      string   `json:"previous_status,omitempty"`
	PreviousCaptureHits int      `json:"previous_capture_hits"`
	Instructions        int      `json:"instructions"`
}

// Report retains explicit discovery, capture and search outcomes. A completed
// campaign does not imply that any captured case passed replay qualification.
type Report struct {
	Schema        string            `json:"schema"`
	Config        workflow.Input    `json:"config"`
	RuntimeSHA256 string            `json:"runtime_sha256"`
	Targets       []target          `json:"targets"`
	Stage         string            `json:"stage"`
	Reason        string            `json:"reason,omitempty"`
	Baseline      *branch           `json:"baseline,omitempty"`
	Branches      []branch          `json:"branches,omitempty"`
	Repeats       []branch          `json:"repeats,omitempty"`
	Winner        int               `json:"winner"`
	Repeatable    bool              `json:"repeatable"`
	Capture       *captureResult    `json:"capture,omitempty"`
	Artifacts     map[string]string `json:"artifacts"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func read(in workflow.Input, limit int64) ([]byte, error) {
	if !filepath.IsAbs(in.Path) || len(in.SHA256) != 64 {
		return nil, fmt.Errorf("invalid pin for %q", in.Path)
	}
	f, e := os.Open(in.Path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit || digest(b) != in.SHA256 {
		return nil, fmt.Errorf("size or digest mismatch: %s", in.Path)
	}
	return b, nil
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing json")
	}
	return nil
}
func write(dir, name string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0600)
}
func load(pin workflow.Input) (Config, error) {
	var c Config
	b, e := read(pin, 1<<20)
	if e != nil {
		return c, e
	}
	if e = decode(b, &c); e != nil {
		return c, e
	}
	if c.Schema != "snes-exploration-config-v1" || c.BaselineFrames < 1 || c.BaselineFrames > 240 || c.MaxSites < 1 || c.MaxSites > 100000 || c.MaxInstructions < 1 || c.MaxInstructions > 10000000 || c.MaxTraceEvents < 1 || c.MaxTraceEvents > 20000000 || c.MaxTraceBytes < 1 || c.MaxTraceBytes > 1<<30 || len(c.Schedules) != 8 || c.Target == "" || c.CapturePC == "" || c.ProjectRevision == "" || len(c.Sources) == 0 {
		return c, fmt.Errorf("invalid campaign bounds or identity")
	}
	seen := map[string]bool{}
	for _, s := range c.Schedules {
		if len(s) != 30 {
			return c, fmt.Errorf("schedules must contain thirty frames")
		}
		b, _ := json.Marshal(s)
		key := digest(b)
		if seen[key] {
			return c, fmt.Errorf("duplicate controller schedule")
		}
		seen[key] = true
	}
	for _, p := range append([]workflow.Input{c.ROM, c.Discovery, c.Coverage, c.Previous, c.PreviousCapture, c.TraceTool}, c.Sources...) {
		if _, e = read(p, 128<<20); e != nil {
			return c, e
		}
	}
	if c.Policy.Path != "" {
		if _, e = read(c.Policy, 1<<20); e != nil {
			return c, e
		}
	}
	return c, nil
}
func plan(c Config) ([]target, error) {
	b, e := read(c.Discovery, 32<<20)
	if e != nil {
		return nil, e
	}
	var d candidates.Report
	if e = json.Unmarshal(b, &d); e != nil {
		return nil, e
	}
	b, e = read(c.Coverage, 64<<20)
	if e != nil {
		return nil, e
	}
	idx, e := coverage.Decode(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	if d.ROMSHA256 != c.ROM.SHA256 || idx.ROMHash != c.ROM.SHA256 {
		return nil, fmt.Errorf("discovery coverage rom mismatch")
	}
	for id, run := range idx.Runs {
		if run.ID != id || run.ROM_SHA256 != c.ROM.SHA256 || run.StreamSHA == "" {
			return nil, fmt.Errorf("coverage run identity mismatch")
		}
	}
	declared := false
	for _, p := range d.Sources {
		if p.Kind == "coverage_index" && p.SHA256 == c.Coverage.SHA256 {
			declared = true
		}
	}
	if !declared {
		return nil, fmt.Errorf("coverage absent from discovery source pins")
	}
	b, e = read(c.Previous, 64<<20)
	if e != nil {
		return nil, e
	}
	var old workflow.BatchReport
	if e = json.Unmarshal(b, &old); e != nil {
		return nil, e
	}
	var capture struct {
		ROM    string `json:"rom_hash"`
		SHA    string `json:"trace_hash"`
		Ranges []struct {
			Space      string `json:"space"`
			Start, End uint32
		} `json:"pc_ranges"`
	}
	b, e = read(c.PreviousCapture, 32<<20)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &capture); e != nil {
		return nil, e
	}
	if capture.ROM != c.ROM.SHA256 {
		return nil, fmt.Errorf("previous capture rom mismatch")
	}
	rows := map[uint32]workflow.BatchRow{}
	for _, r := range old.Rows {
		if r.ROMSHA256 != c.ROM.SHA256 {
			return nil, fmt.Errorf("previous batch rom mismatch")
		}
		rows[r.Entry] = r
	}
	var out []target
	for _, cand := range d.Candidates {
		t := target{ID: cand.ID, Entry: cand.Entry, Instructions: cand.InstructionCount, Status: "unobserved", Quality: "unavailable"}
		ctx := map[string]bool{}
		for _, s := range idx.Sites {
			if s.Address != cand.Entry {
				continue
			}
			r, ok := idx.Runs[s.RunID]
			if !ok || r.ROM_SHA256 != c.ROM.SHA256 {
				return nil, fmt.Errorf("coverage site lacks matching run")
			}
			if ^uint64(0)-t.Hits < s.Hits {
				return nil, fmt.Errorf("coverage hits overflow")
			}
			t.Hits += s.Hits
			ctx[fmt.Sprintf("e=%s,m=%s,x=%s,c=%s", s.Context.E, s.Context.M, s.Context.X, s.Context.C)] = true
			q := "complete"
			if !r.IsComplete || r.Outcome != "complete" || len(r.Gaps) > 0 {
				q = "incomplete"
			}
			if t.Quality != "incomplete" {
				t.Quality = q
			}
		}
		for k := range ctx {
			t.Contexts = append(t.Contexts, k)
		}
		sort.Strings(t.Contexts)
		if t.Hits > 0 {
			t.Status = "already_reached"
		} else if len(idx.Runs) > 0 {
			complete := true
			for _, r := range idx.Runs {
				if !r.IsComplete || r.Outcome != "complete" || len(r.Gaps) > 0 {
					complete = false
				}
			}
			if complete {
				t.Status = "not_reached_in_pinned_runs"
			}
		}
		if r, ok := rows[t.Entry]; ok {
			t.PreviousStatus = r.Status
			if r.Extraction != nil {
				t.PreviousCaptureHits = r.Extraction.TotalEntryHits
			}
			if t.Hits > 0 && r.Stage == "extraction" && r.Extraction != nil && r.Extraction.TotalEntryHits == 0 {
				t.Status = "reached_capture_gap"
				if r.Extraction.CaptureSHA256 != capture.SHA {
					return nil, fmt.Errorf("previous capture identity mismatch")
				}
				covered := false
				for _, window := range capture.Ranges {
					if window.Space == "cpu" && t.Entry >= window.Start && t.Entry <= window.End {
						covered = true
					}
				}
				if len(capture.Ranges) > 0 && !covered {
					t.Status = "reached_but_filtered"
				}

			}
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Status == "reached_capture_gap" || a.Status == "reached_but_filtered") != (b.Status == "reached_capture_gap" || b.Status == "reached_but_filtered") {
			return a.Status == "reached_capture_gap" || a.Status == "reached_but_filtered"
		}
		if a.Instructions != b.Instructions {
			return a.Instructions < b.Instructions
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return a.Entry < b.Entry
	})
	found := false
	for _, t := range out {
		if t.ID == c.Target {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("target absent from discovery")
	}
	return out, nil
}

// Run publishes one new campaign directory. execute=false records only pinned
// discovery classification. Runtime errors are explicit nonqualifying stage
// outcomes; invalid or changed pins prevent publication altogether.
func Run(ctx context.Context, out string, pin workflow.Input, execute bool) (*Report, error) {
	if !filepath.IsAbs(out) {
		return nil, fmt.Errorf("output must be absolute")
	}
	if _, e := os.Lstat(out); !os.IsNotExist(e) {
		return nil, fmt.Errorf("output already exists or inaccessible")
	}
	if e := os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(out+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, fmt.Errorf("reserve output: %w", e)
	}
	lock.Close()
	defer os.Remove(out + ".lock")
	if _, e := os.Lstat(out); !os.IsNotExist(e) {
		return nil, fmt.Errorf("output already exists or inaccessible")
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	c, e := load(pin)
	if e != nil {
		return nil, e
	}
	targets, e := plan(c)
	if e != nil {
		return nil, e
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(exe)
	if e != nil {
		return nil, e
	}
	r := &Report{Schema: "snes-exploration-v1", Config: pin, RuntimeSHA256: digest(b), Targets: targets, Stage: "discovery", Winner: -1, Artifacts: map[string]string{}}
	if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return nil, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(out), ".exploration-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(stage)
	if execute {
		if e = run(ctx, stage, c, r); e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			r.Reason = e.Error()
		}
	}
	if _, e = load(pin); e != nil {
		return nil, fmt.Errorf("recheck pins: %w", e)
	}
	e = filepath.WalkDir(stage, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(stage, p)
		r.Artifacts[rel] = digest(b)
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = write(stage, "manifest.json", r); e != nil {
		return nil, e
	}
	if e = os.Rename(stage, out); e != nil {
		return nil, e
	}
	return r, nil
}
