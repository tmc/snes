package workflow

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
	"strings"
	"syscall"

	"github.com/tmc/snes/internal/extractor"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/queue"
)

// Input identifies exact serialized file bytes.
type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Config selects one candidate and finite execution budgets.
type Config struct {
	Candidate       Input  `json:"candidate"`
	CandidateID     string `json:"candidate_id"`
	ROM             Input  `json:"rom"`
	ProjectDir      string `json:"project_dir"`
	ProjectRevision string `json:"project_revision"`
	CorpusRoot      string `json:"corpus_root"`
	Label           string `json:"label"`
	Corpus          string `json:"corpus"`
	MaxFrames       int    `json:"max_frames"`
	MaxCases        int    `json:"max_cases"`
	MaxSteps        int    `json:"max_steps"`
	QueueLimit      int    `json:"queue_limit"`
}

// Stream pins one producer stream and its metadata.
type Stream struct {
	Trace   Input `json:"trace"`
	Receipt Input `json:"receipt"`
	Summary Input `json:"summary"`
}

// Evidence delivers retained producer outputs, never policy authority.
type Evidence struct {
	Fixture Stream `json:"fixture"`
	Capture Stream `json:"capture"`
	History Stream `json:"history"`
}

// Options names the durable task and optional explicit deliveries.
type Options struct {
	Dir            string
	Config         Input
	Evidence       Input
	Policy         Input
	MaxTransitions int
}

// State is the last committed journal generation. Qualified scope is sampled CPU/RAM.
type State struct {
	RuntimeSHA256  string            `json:"runtime_sha256"`
	Sequence       int               `json:"sequence"`
	Phase          string            `json:"phase"`
	Config         Input             `json:"config"`
	Evidence       Input             `json:"evidence,omitempty"`
	Policy         Input             `json:"policy,omitempty"`
	CandidateID    string            `json:"candidate_id"`
	Entry          uint32            `json:"entry"`
	Artifacts      map[string]string `json:"artifacts"`
	Reason         string            `json:"reason,omitempty"`
	QualifiedCases int               `json:"qualified_cases,omitempty"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func pinned(in Input, limit int64) ([]byte, error) {
	if !filepath.IsAbs(in.Path) {
		return nil, fmt.Errorf("input path must be absolute")
	}
	decoded, err := hex.DecodeString(in.SHA256)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != in.SHA256 || in.Path == "" {
		return nil, fmt.Errorf("missing or malformed input identity")
	}
	f, err := os.OpenFile(in.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input must be a regular file")
	}
	bound := limit
	if bound == 0 {
		bound = 2 << 30
	}
	if info.Size() > bound {
		return nil, fmt.Errorf("input exceeds limit")
	}
	var b []byte
	if limit > 0 {
		b, err = io.ReadAll(io.LimitReader(f, limit+1))
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("input exceeds limit")
		}
	} else {
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			return nil, err
		}
		if hex.EncodeToString(h.Sum(nil)) != in.SHA256 {
			return nil, fmt.Errorf("input digest mismatch: %s", in.Path)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if digest(b) != in.SHA256 {
		return nil, fmt.Errorf("input digest mismatch: %s", in.Path)
	}
	return b, nil
}
func sameInput(a, b Input) bool { return a == b }
func inputs(e Evidence) []Input {
	return []Input{e.Fixture.Trace, e.Fixture.Receipt, e.Fixture.Summary, e.Capture.Trace, e.Capture.Receipt, e.Capture.Summary, e.History.Trace, e.History.Receipt, e.History.Summary}
}
func loadEvidence(in Input, maxFrames int) (Evidence, error) {
	var e Evidence
	b, err := pinned(in, 1<<20)
	if err != nil {
		return e, err
	}
	if err = strict(b, &e); err != nil {
		return e, err
	}
	for _, x := range inputs(e) {
		if _, err = pinned(x, 0); err != nil {
			return e, err
		}
	}
	for _, stream := range []Stream{e.Fixture, e.Capture, e.History} {
		b, err = pinned(stream.Summary, 16<<20)
		if err != nil {
			return e, err
		}
		var summary struct {
			Frames int `json:"frames"`
		}
		if err = json.Unmarshal(b, &summary); err != nil {
			return e, err
		}
		if summary.Frames < 1 || summary.Frames > maxFrames {
			return e, fmt.Errorf("producer frame count outside budget")
		}
	}
	return e, nil
}
func loadConfig(in Input) (Config, extractor.Candidate, error) {
	var c Config
	var cand extractor.Candidate
	b, err := pinned(in, 1<<20)
	if err != nil {
		return c, cand, err
	}
	if err = strict(b, &c); err != nil {
		return c, cand, err
	}
	if c.MaxFrames < 1 || c.MaxFrames > 10000 || c.MaxCases < 1 || c.MaxCases > 10000 || c.MaxSteps < 1 || c.MaxSteps > 1000000 || c.QueueLimit < 1 || c.QueueLimit > 100 || !filepath.IsAbs(c.ProjectDir) || !filepath.IsAbs(c.CorpusRoot) || c.ProjectRevision == "" || c.Label == "" || c.Corpus == "" {
		return c, cand, fmt.Errorf("missing identity or budget outside bounds")
	}
	b, err = pinned(c.Candidate, 4<<20)
	if err != nil {
		return c, cand, err
	}
	cand, err = extractor.LoadCandidate(b, c.CandidateID)
	if err != nil {
		return c, cand, err
	}
	if cand.ID == "" || cand.Entry >= 1<<24 || cand.InstructionCount < 1 || cand.InstructionCount > 256 || cand.ByteSpan < 1 || cand.ByteSpan > 32768 {
		return c, cand, fmt.Errorf("unsupported candidate bounds")
	}
	if _, err = pinned(c.ROM, 16<<20); err != nil {
		return c, cand, err
	}
	docBytes, err := os.ReadFile(filepath.Join(c.ProjectDir, "recovery.json"))
	if err != nil {
		return c, cand, err
	}
	var doc recovery.Document
	if err = json.Unmarshal(docBytes, &doc); err != nil {
		return c, cand, err
	}
	if recovery.ComputeProjectRevision(c.ProjectDir, &doc) != c.ProjectRevision {
		return c, cand, fmt.Errorf("project revision changed")
	}
	return c, cand, nil
}
func verifyArtifacts(dir string, s State) error {
	for name, h := range s.Artifacts {
		if filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || len(name) > 4096 {
			return fmt.Errorf("invalid artifact path")
		}
		if _, err := pinned(Input{filepath.Join(dir, name), h}, 0); err != nil {
			return err
		}
	}
	return nil
}
func treePins(dir, relative string) (map[string]string, error) {
	m := make(map[string]string)
	err := filepath.WalkDir(filepath.Join(dir, relative), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink artifact refused")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		m[name] = digest(b)
		return nil
	})
	return m, err
}
func artifactJSON(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(dir, name)
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, b) {
			return nil
		}
		return fmt.Errorf("existing artifact differs: %s", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".json-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(temp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
func extractOutput(dir string, c Config, cand extractor.Candidate, e Evidence) error {
	r, err := extractor.Extract(extractor.Config{Candidate: cand, ExpectedROMSHA256: c.ROM.SHA256, ROMPath: c.ROM.Path, FixturePath: e.Fixture.Trace.Path, FixtureReceiptPath: e.Fixture.Receipt.Path, FixtureSummaryPath: e.Fixture.Summary.Path, CapturePath: e.Capture.Trace.Path, CaptureReceiptPath: e.Capture.Receipt.Path, CaptureSummaryPath: e.Capture.Summary.Path, HistoryPath: e.History.Trace.Path, HistoryReceiptPath: e.History.Receipt.Path, HistorySummaryPath: e.History.Summary.Path, CorpusName: c.Corpus, CorpusLabel: c.Label, CasePrefix: c.Label + "_"})
	if err != nil {
		return err
	}
	if len(r.Cases) > 10000 {
		return fmt.Errorf("extracted case count outside bound")
	}
	var b bytes.Buffer
	for _, x := range r.Cases {
		encoded, err := json.Marshal(x)
		if err != nil {
			return err
		}
		b.Write(encoded)
		b.WriteByte('\n')
	}
	if err = os.WriteFile(filepath.Join(dir, "cases.jsonl"), b.Bytes(), 0600); err != nil {
		return err
	}
	if err = artifactJSON(dir, "proposed-trust-root.json", r.TrustRoot); err != nil {
		return err
	}
	return artifactJSON(dir, "receipt.json", r.Receipt)
}

// Run advances at most MaxTransitions journal generations. Missing evidence or
// explicit policy returns a waiting state, not implicit authority. Resume pins
// all previously committed inputs and artifacts. Cancellation is checked between
// extraction phases; the existing extractor has no in-process cancellation API.
func Run(ctx context.Context, o Options) (State, error) {
	var zero State
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !filepath.IsAbs(o.Dir) || o.MaxTransitions < 1 || o.MaxTransitions > 10 {
		return zero, fmt.Errorf("missing task directory or transition budget outside bounds")
	}
	c, cand, err := loadConfig(o.Config)
	if err != nil {
		return zero, err
	}
	s, unlock, err := openJournal(o.Dir, o.Config, cand)
	if err != nil {
		return zero, err
	}
	defer unlock()
	runtimeSHA, err := runtimeIdentity()
	if err != nil {
		return s, err
	}
	if s.RuntimeSHA256 != runtimeSHA {
		return s, fmt.Errorf("workflow executable changed")
	}
	if !sameInput(s.Config, o.Config) {
		return s, fmt.Errorf("task config changed")
	}
	if err = verifyArtifacts(o.Dir, s); err != nil {
		return s, err
	}
	if s.Evidence.Path != "" {
		if o.Evidence.Path != "" && !sameInput(s.Evidence, o.Evidence) {
			return s, fmt.Errorf("evidence delivery changed")
		}
		if _, err = loadEvidence(s.Evidence, c.MaxFrames); err != nil {
			return s, err
		}
	}
	if s.Policy.Path != "" {
		if o.Policy.Path != "" && !sameInput(s.Policy, o.Policy) {
			return s, fmt.Errorf("operator policy changed")
		}
		if _, err = pinned(s.Policy, 1<<20); err != nil {
			return s, err
		}
	}
	for step := 0; step < o.MaxTransitions; step++ {
		if err = ctx.Err(); err != nil {
			return s, err
		}
		next := s
		next.Artifacts = make(map[string]string)
		for k, v := range s.Artifacts {
			next.Artifacts[k] = v
		}
		switch s.Phase {
		case "selected":
			path := filepath.Join(o.Dir, "capture-request.json")
			if err = artifactJSON(o.Dir, "capture-request.json", map[string]any{"candidate": cand, "max_frames": c.MaxFrames, "ROM": c.ROM, "status": "request_only_no_producer_execution", "required_events": []string{"cpu_insn", "bus", "cpu_transition", "dma", "hdma", "mmio"}, "history": "complete supported CPU WRAM write history from a pinned initial boundary"}); err != nil {
				return s, err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return s, err
			}
			next.Artifacts["capture-request.json"] = digest(b)
			next.Phase = "await_capture"
		case "await_capture":
			if o.Evidence.Path == "" {
				return s, nil
			}
			if _, err := loadEvidence(o.Evidence, c.MaxFrames); err != nil {
				return s, err
			}
			next.Evidence = o.Evidence
			next.Phase = "extracting"
		case "extracting":
			e, err := loadEvidence(s.Evidence, c.MaxFrames)
			if err != nil {
				return s, err
			}
			if err = runStep(o.Dir, "extraction", func(stage string) error { return extractOutput(stage, c, cand, e) }); err != nil {
				next.Phase = "blocked"
				next.Reason = "extract: " + err.Error()
			} else {
				next.Phase = "await_policy"
				pins, err := treePins(o.Dir, "extraction")
				if err != nil {
					return s, err
				}
				for k, v := range pins {
					next.Artifacts[k] = v
				}
			}
		case "await_policy":
			if o.Policy.Path == "" {
				return s, nil
			}
			if _, err = pinned(o.Policy, 1<<20); err != nil {
				return s, err
			}
			next.Policy = o.Policy
			next.Phase = "validation"
		case "validation":
			var report *queue.Report
			err = runStep(o.Dir, "validation", func(stage string) error {
				out := filepath.Join(stage, "queue")
				var err error
				report, err = queue.Run(ctx, queue.Config{ProjectDir: c.ProjectDir, ROMPath: c.ROM.Path, CasesPath: filepath.Join(o.Dir, "extraction", "cases.jsonl"), CorpusRoot: c.CorpusRoot, PolicyPath: s.Policy.Path, PolicySHA256: s.Policy.SHA256, OutDir: out, Revision: c.ProjectRevision, Limit: c.QueueLimit, MaxCases: c.MaxCases, MaxSteps: c.MaxSteps})
				return err
			})
			if ctx.Err() != nil {
				return s, ctx.Err()
			}
			next.Phase = "blocked"
			next.Reason = "selected candidate was not freshly qualified"
			if err != nil {
				next.Reason = "validate: " + err.Error()
			} else {
				pins, err := treePins(o.Dir, "validation")
				if err != nil {
					return s, err
				}
				for k, v := range pins {
					next.Artifacts[k] = v
				}
				if matched := selectedCases(report, s.Entry); matched > 0 {
					next.Phase = "qualified"
					next.Reason = "sampled captured CPU and ordered WRAM effects; not timing or frame equivalence"
					next.QualifiedCases = matched
				}
			}
		case "qualified", "blocked":
			return s, nil
		default:
			return s, fmt.Errorf("unknown journal phase")
		}
		if _, _, err = loadConfig(o.Config); err != nil {
			return s, err
		}
		if next.Evidence.Path != "" {
			if _, err = loadEvidence(next.Evidence, c.MaxFrames); err != nil {
				return s, err
			}
		}
		if next.Policy.Path != "" {
			if _, err = pinned(next.Policy, 1<<20); err != nil {
				return s, err
			}
		}
		if err = ctx.Err(); err != nil {
			return s, err
		}
		if err = appendState(o.Dir, &next); err != nil {
			return s, err
		}
		s = next
	}
	return s, nil
}

func selectedCases(report *queue.Report, entry uint32) int {
	n := 0
	for _, r := range report.Candidates {
		if r.Candidate.Entry == entry && r.Status == "qualified" && r.Matched > 0 && r.Matched == r.Admitted && r.Refused == 0 && r.Mismatched == 0 && r.Unexecuted == 0 {
			n = max(n, r.Matched)
		}
	}
	return n
}
