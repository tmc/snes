package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/extractor"
	"github.com/tmc/snes/internal/recovery/queue"
)

// BatchTask pins one candidate task and its explicit evidence and policy deliveries.
// Candidate contracts, ROMs, and finite budgets are specified by Config.
type BatchTask struct {
	Config   Input `json:"config"`
	Evidence Input `json:"evidence"`
	Policy   Input `json:"policy"`
}

// BatchConfig pins ten distinct ROM entries and the source inventory used to
// select them. Sources are identities, not admission authority.
type BatchConfig struct {
	Schema  string      `json:"schema"`
	Sources []Input     `json:"sources"`
	Tasks   []BatchTask `json:"tasks"`
}

// BatchRow records an outcome without treating compilation or empty evidence
// as qualification. State and Results point to retained journal and raw receipts.
type BatchRow struct {
	CandidateID string                       `json:"candidate_id"`
	Entry       uint32                       `json:"entry"`
	ROMSHA256   string                       `json:"rom_sha256"`
	Directory   string                       `json:"directory"`
	Status      string                       `json:"status"`
	Stage       string                       `json:"stage"`
	Reason      string                       `json:"reason,omitempty"`
	State       State                        `json:"state"`
	Extraction  *extractor.ExtractionReceipt `json:"extraction,omitempty"`
	Results     []queue.Result               `json:"results,omitempty"`
}

// BatchReport is an immutable readiness manifest. Accepted counts sampled
// captured CPU/RAM qualifications; refused and unexecuted rows are ineligible.
type BatchReport struct {
	Schema        string            `json:"schema"`
	Config        Input             `json:"config"`
	RuntimeSHA256 string            `json:"runtime_sha256"`
	Accepted      int               `json:"accepted"`
	Refused       int               `json:"refused"`
	Unexecuted    int               `json:"unexecuted"`
	Rows          []BatchRow        `json:"rows"`
	Artifacts     map[string]string `json:"artifacts"`
}

func loadBatch(in Input) (BatchConfig, error) {
	var c BatchConfig
	b, err := pinned(in, 1<<20)
	if err != nil {
		return c, err
	}
	if err = strict(b, &c); err != nil {
		return c, err
	}
	if c.Schema != "snes-recovery-batch-config-v1" || len(c.Tasks) != 10 || len(c.Sources) == 0 {
		return c, fmt.Errorf("batch requires ten tasks and pinned sources")
	}
	for _, source := range c.Sources {
		if _, err := pinned(source, 0); err != nil {
			return c, fmt.Errorf("batch source: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, t := range c.Tasks {
		cfg, cand, err := loadConfig(t.Config)
		if err != nil {
			return c, fmt.Errorf("batch config: %w", err)
		}
		key := fmt.Sprintf("%s:%06x", cfg.ROM.SHA256, cand.Entry)
		if seen[key] {
			return c, fmt.Errorf("duplicate batch ROM entry")
		}
		seen[key] = true
		if t.Evidence.Path != "" {
			if _, err := loadEvidence(t.Evidence, cfg.MaxFrames); err != nil {
				return c, fmt.Errorf("batch evidence: %w", err)
			}
		}
		if t.Policy.Path != "" {
			if _, err := pinned(t.Policy, 1<<20); err != nil {
				return c, fmt.Errorf("batch policy: %w", err)
			}
		}
	}
	return c, nil
}

// RunBatch executes ten existing workflows serially in private staging and
// publishes manifest.json last. Identical reruns verify source, executable,
// project, evidence, policy, journals and artifacts and return the retained
// result without executing again. Use a new directory for fresh qualification.
// A failed or cancelled publication leaves the output directory absent.
func RunBatch(ctx context.Context, dir string, config Input) (*BatchReport, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("batch output must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := loadBatch(config)
	if err != nil {
		return nil, err
	}
	runtime, err := runtimeIdentity()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dir); err == nil {
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return nil, fmt.Errorf("existing batch has no readiness manifest: %w", err)
		}
		var r BatchReport
		if err := strict(b, &r); err != nil {
			return nil, err
		}
		if r.Schema != "snes-recovery-batch-v1" || r.Config != config || r.RuntimeSHA256 != runtime || len(r.Rows) != 10 || r.Accepted+r.Refused+r.Unexecuted != 10 {
			return nil, fmt.Errorf("batch identity changed")
		}
		if err := verifyArtifacts(dir, State{Artifacts: r.Artifacts}); err != nil {
			return nil, err
		}
		accepted, refused, unexecuted := 0, 0, 0
		for i, row := range r.Rows {
			b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("row-%02d.json", i+1)))
			if err != nil {
				return nil, err
			}
			var retained BatchRow
			if err := strict(b, &retained); err != nil {
				return nil, err
			}
			x, _ := json.Marshal(retained)
			y, _ := json.Marshal(row)
			if string(x) != string(y) {
				return nil, fmt.Errorf("batch manifest row changed")
			}
			switch row.Status {
			case "accepted":
				accepted++
			case "refused":
				refused++
			case "unexecuted":
				unexecuted++
			default:
				return nil, fmt.Errorf("invalid batch status")
			}
			s, _, err := readJournal(filepath.Join(dir, row.Directory))
			if err != nil {
				return nil, err
			}
			if s.Sequence != row.State.Sequence || s.Phase != row.State.Phase {
				return nil, fmt.Errorf("batch journal changed")
			}
		}
		if accepted != r.Accepted || refused != r.Refused || unexecuted != r.Unexecuted {
			return nil, fmt.Errorf("batch accounting changed")
		}
		return &r, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".batch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	r := &BatchReport{Schema: "snes-recovery-batch-v1", Config: config, RuntimeSHA256: runtime}
	for i, t := range c.Tasks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg, cand, err := loadConfig(t.Config)
		if err != nil {
			return nil, err
		}
		relative := fmt.Sprintf("task-%02d-%06x", i+1, cand.Entry)
		taskDir := filepath.Join(stage, relative)
		s, runErr := Run(ctx, Options{Dir: taskDir, Config: t.Config, Evidence: t.Evidence, Policy: t.Policy, MaxTransitions: 6})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := BatchRow{CandidateID: cand.ID, Entry: cand.Entry, ROMSHA256: cfg.ROM.SHA256, Directory: relative, State: s, Stage: s.Phase, Reason: s.Reason}
		if runErr != nil {
			row.Status = "refused"
			row.Reason = runErr.Error()
		} else {
			switch s.Phase {
			case "qualified":
				row.Status = "accepted"
			case "blocked":
				row.Status = "refused"
			default:
				row.Status = "unexecuted"
			}
		}
		// Preserve detailed admission, compile and replay counts for the selected entry.
		if s.ValidationDir != "" {
			b, err := os.ReadFile(filepath.Join(taskDir, s.ValidationDir, "queue", "report.json"))
			if err != nil {
				return nil, err
			}
			var q queue.Report
			if err := json.Unmarshal(b, &q); err != nil {
				return nil, err
			}
			row.Results = q.Candidates
		} else {
			matches, err := filepath.Glob(filepath.Join(taskDir, "validation-*", "queue", "report.json"))
			if err != nil {
				return nil, err
			}
			if len(matches) > 0 {
				b, err := os.ReadFile(matches[len(matches)-1])
				if err != nil {
					return nil, err
				}
				var q queue.Report
				if err := json.Unmarshal(b, &q); err != nil {
					return nil, err
				}
				row.Results = q.Candidates
			}
		}

		receiptPath := filepath.Join(taskDir, "extraction", "receipt.json")
		if b, err := os.ReadFile(receiptPath); err == nil {
			var extraction extractor.ExtractionReceipt
			if err := json.Unmarshal(b, &extraction); err != nil {
				return nil, err
			}
			row.Extraction = &extraction
			if extraction.CompleteExecutions == 0 {
				row.Status = "refused"
				row.Stage = "extraction"
				row.Reason = fmt.Sprintf("no complete captured executions: entry_hits=%d rejected=%d", extraction.TotalEntryHits, extraction.RejectedExecutions)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if row.Status == "accepted" {
			row.Stage = "qualification"
		}
		if row.Status == "refused" && row.Stage != "extraction" && len(row.Results) > 0 {
			for _, result := range row.Results {
				if result.Status == "qualified" {
					continue
				}
				row.Stage = result.ReasonCode
				if row.Stage == "no_evidence" {
					row.Stage = "admission"
				}
				row.Reason = result.Reason
				break
			}
		}
		switch row.Status {
		case "accepted":
			r.Accepted++
		case "refused":
			r.Refused++
		default:
			r.Unexecuted++
		}
		if err := artifactJSON(stage, fmt.Sprintf("row-%02d.json", i+1), row); err != nil {
			return nil, err
		}
		r.Rows = append(r.Rows, row)
	}
	if _, err := loadBatch(config); err != nil {
		return nil, err
	}
	r.Artifacts, err = treePins(stage, ".")
	if err != nil {
		return nil, err
	}
	// Paths from treePins are already relative to stage.
	if err := artifactJSON(stage, "manifest.json", r); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Exclusive reservation prevents a concurrent invocation from replacing output.
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("reserve batch output: %w", err)
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(dir)
		}
	}()
	if err := os.Rename(stage, filepath.Join(dir, "payload")); err != nil {
		return nil, err
	}
	// Keep journals under the payload directory and paths in the manifest consistent.
	// Move each completed artifact into the reserved root; manifest is the final marker.
	entries, err := os.ReadDir(filepath.Join(dir, "payload"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == "manifest.json" {
			continue
		}
		if err := os.Rename(filepath.Join(dir, "payload", entry.Name()), filepath.Join(dir, entry.Name())); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(filepath.Join(dir, "payload", "manifest.json"), filepath.Join(dir, "manifest.json")); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(dir, "payload")); err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	published = true
	return r, nil
}
