// Package queue generates and qualifies bounded recovery candidates.
// Ranking and inventory metadata never grant evidence admission.
package queue

import (
	"bufio"
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

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// Config identifies current inputs and finite execution bounds.
// OutDir must not exist. Revision is the current project content identity.
type Config struct {
	ProjectDir, ROMPath, CasesPath, CorpusRoot, OutDir, Revision string
	Limit, MaxCases, MaxSteps                                    int
}

// Result records one candidate's status and its bounded comparison scope.
// Qualified means captured CPU and ordered-write agreement, not timing proof.
type Result struct {
	Candidate    candidates.Candidate `json:"candidate"`
	Status       string               `json:"status"`
	ReasonCode   string               `json:"reason_code,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Directory    string               `json:"directory"`
	SourceSHA256 string               `json:"source_sha256,omitempty"`
	IRSHA256     string               `json:"ir_sha256,omitempty"`
	Cases        int                  `json:"cases"`
	Admitted     int                  `json:"admitted"`
	Matched      int                  `json:"matched"`
	Refused      int                  `json:"refused"`
	Mismatched   int                  `json:"mismatched"`
	Unexecuted   int                  `json:"unexecuted"`
}

// Report identifies all queue inputs and published candidate artifacts.
type Report struct {
	Schema      string              `json:"schema"`
	Revision    string              `json:"revision"`
	ROMSHA256   string              `json:"rom_sha256"`
	Sources     []candidates.Source `json:"sources"`
	Candidates  []Result            `json:"candidates"`
	Limitations []string            `json:"limitations"`
}

type pinnedInput struct{ path, hash string }

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func readInput(path string, pins *[]pinnedInput) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 256<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 256<<20 {
		return nil, fmt.Errorf("input exceeds 256 MiB: %s", path)
	}
	*pins = append(*pins, pinnedInput{path, hash(b)})
	return b, nil
}
func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

// Run executes candidates serially and publishes a complete staged payload.
// An exclusive output directory reservation prevents overwriting other runs.
// The root report.json is published last and is the readiness marker.
// Cancellation, changed inputs, and publication errors leave OutDir absent.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.ProjectDir == "" || cfg.ROMPath == "" || cfg.CasesPath == "" || cfg.CorpusRoot == "" || cfg.OutDir == "" || cfg.Revision == "" {
		return nil, fmt.Errorf("queue: missing input or output identity")
	}
	if cfg.Limit < 1 || cfg.Limit > 100 || cfg.MaxCases < 1 || cfg.MaxCases > 10000 || cfg.MaxSteps < 1 || cfg.MaxSteps > 1000000 {
		return nil, fmt.Errorf("queue: invalid finite bounds")
	}
	if _, err := os.Lstat(cfg.OutDir); err == nil {
		return nil, fmt.Errorf("queue: output already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var pins []pinnedInput
	docBytes, err := readInput(filepath.Join(cfg.ProjectDir, "recovery.json"), &pins)
	if err != nil {
		return nil, err
	}
	doc, err := recovery.Decode(bytes.NewReader(docBytes))
	if err != nil {
		return nil, err
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("queue: project revision mismatch")
	}
	romBytes, err := readInput(cfg.ROMPath, &pins)
	if err != nil {
		return nil, err
	}
	rom, err := recovery.AdmitROM(bytes.NewReader(romBytes), recovery.AdmissionOptions{})
	if err != nil {
		return nil, err
	}
	if rom.Identity.NormalizedSHA256 != doc.ROM.NormalizedSHA256 || doc.ROM.Mapper != "lorom" {
		return nil, fmt.Errorf("queue: ROM identity or mapper differs from recovery document")
	}
	sources := []candidates.Source{{ID: "recovery.json", SHA256: hash(docBytes), Kind: "recovery_document"}, {ID: cfg.ROMPath, SHA256: hash(romBytes), Kind: "rom"}}
	var idx *coverage.Index
	coverageBytes, err := readInput(filepath.Join(cfg.ProjectDir, "coverage.json"), &pins)
	if err == nil {
		idx, err = coverage.Decode(bytes.NewReader(coverageBytes))
		if err != nil {
			return nil, err
		}
		sources = append(sources, candidates.Source{ID: "coverage.json", SHA256: hash(coverageBytes), Kind: "coverage_index"})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	caseBytes, err := readInput(cfg.CasesPath, &pins)
	if err != nil {
		return nil, err
	}
	inventory, source, err := candidates.ReadInventory(bytes.NewReader(caseBytes), cfg.CasesPath)
	if err != nil {
		return nil, err
	}
	sources = append(sources, source)
	cases, err := readCases(caseBytes)
	if err != nil {
		return nil, err
	}
	mined, err := candidates.Mine(doc, idx, inventory, sources, candidates.Options{})
	if err != nil {
		return nil, err
	}
	if len(mined.Candidates) > cfg.Limit {
		mined.Candidates = mined.Candidates[:cfg.Limit]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.OutDir), 0755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(cfg.OutDir), ".queue-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	report := &Report{Schema: "snes-recovery-queue-v1", Revision: cfg.Revision, ROMSHA256: rom.Identity.NormalizedSHA256, Sources: sources, Limitations: []string{"serial bounded execution; qualified scope is sampled captured CPU and ordered writes, not timing or whole-game equivalence", "new routine IDs remain blocked by current evidence admission contracts; inventory contexts are not trusted", "per-candidate selected-case limit may omit occurrences; qualification applies only to persisted receipts"}}
	verifier := decomp.NewEvidenceVerifier(cfg.CorpusRoot)
	for _, candidate := range mined.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, err := json.Marshal(struct {
			Candidate            candidates.Candidate
			Revision, ROM, Cases string
			MaxSteps, MaxCases   int
		}{candidate, cfg.Revision, report.ROMSHA256, hash(caseBytes), cfg.MaxSteps, cfg.MaxCases})
		if err != nil {
			return nil, err
		}
		directory := filepath.Join(candidate.ID, hash(identity))
		result, err := execute(ctx, cfg, rom.NormalizedROM, candidate, cases, verifier, filepath.Join(stage, directory))
		if err != nil {
			return nil, err
		}
		result.Directory = filepath.Join("artifacts", directory)
		if err := writeJSON(filepath.Join(stage, directory, "result.json"), result); err != nil {
			return nil, err
		}
		report.Candidates = append(report.Candidates, result)
	}
	if err := checkArtifacts(stage, report); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(stage, "report.json"), report); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkInputs(pins); err != nil {
		return nil, err
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("queue: project changed during execution")
	}
	if _, err := os.Lstat(cfg.OutDir); err == nil {
		return nil, fmt.Errorf("queue: output appeared during execution")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Mkdir(cfg.OutDir, 0700); err != nil {
		return nil, fmt.Errorf("reserve queue output: %w", err)
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(cfg.OutDir)
		}
	}()
	if err := os.Rename(stage, filepath.Join(cfg.OutDir, "artifacts")); err != nil {
		return nil, fmt.Errorf("publish queue: %w", err)
	}
	if err := os.Rename(filepath.Join(cfg.OutDir, "artifacts", "report.json"), filepath.Join(cfg.OutDir, "report.json")); err != nil {
		return nil, fmt.Errorf("publish report: %w", err)
	}
	published = true
	return report, nil
}

func readCases(b []byte) ([]decomp.ReplayCase, error) {
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(make([]byte, 65536), 4<<20)
	var out []decomp.ReplayCase
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		var c decomp.ReplayCase
		d := json.NewDecoder(bytes.NewReader(s.Bytes()))
		if err := d.Decode(&c); err != nil {
			return nil, fmt.Errorf("queue case: %w", err)
		}
		out = append(out, c)
	}
	return out, s.Err()
}

func entryContext(s decomp.CPUState) recovery.Context {
	bit := func(b bool) string {
		if b {
			return "set"
		}
		return "clear"
	}
	return recovery.Context{E: bit(s.E), M: bit(s.P&0x20 != 0), X: bit(s.P&0x10 != 0), C: "unknown"}
}

func execute(ctx context.Context, cfg Config, rom []byte, candidate candidates.Candidate, input []decomp.ReplayCase, v *decomp.EvidenceVerifier, dir string) (Result, error) {
	result := Result{Candidate: candidate, Status: "blocked", ReasonCode: "no_evidence"}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	if err := writeJSON(filepath.Join(dir, "proposal.json"), candidate); err != nil {
		return result, err
	}
	var selected []decomp.ReplayCase
	var admissions []decomp.AdmissionRecord
	for _, original := range input {
		if uint32(original.InitialState.PB)<<16|uint32(original.InitialState.PC) != candidate.Entry {
			continue
		}
		if result.Cases >= cfg.MaxCases {
			break
		}
		result.Cases++
		if err := ctx.Err(); err != nil {
			return result, err
		}
		c := original
		if c.ROMSHA256 != hash(rom) {
			result.Refused++
			result.Reason = "case ROM differs from queue ROM"
			continue
		}
		admission, err := v.Admit(&c, nil, nil)
		admissions = append(admissions, admission)
		if err != nil {
			result.Refused++
			result.Reason = "evidence admission: " + err.Error()
			continue
		}
		selected = append(selected, c)
		result.Admitted++
		result.Unexecuted++
	}
	if err := writeJSON(filepath.Join(dir, "admissions.json"), admissions); err != nil {
		return result, err
	}
	if len(selected) == 0 {
		if result.Reason == "" {
			result.Reason = "no admitted entry context or captured cases"
		}
		return result, nil
	}
	context := entryContext(selected[0].InitialState)
	for _, c := range selected {
		if entryContext(c.InitialState) != context {
			result.ReasonCode = "entry_context"
			result.Reason = "conflicting admitted entry widths"
			return result, nil
		}
	}
	p := candidate.Proposal
	if p.Start != candidate.Entry || p.End <= p.Start || p.End-p.Start > 32768 || p.Start&0xffff < 0x8000 || p.Start&0xff0000 != (p.End-1)&0xff0000 {
		result.Reason = "unsupported bounded LoROM region"
		return result, nil
	}
	off := int((p.Start>>16&0x7f)*0x8000 + (p.Start & 0x7fff))
	length := int(p.End - p.Start)
	if off < 0 || off+length > len(rom) {
		result.Reason = "region outside supplied ROM"
		return result, nil
	}
	frontiers := map[uint32]string{}
	for _, f := range p.RefusalFrontiers {
		frontiers[f.Target] = f.Reason
	}
	region, err := decomp.DecodeRegionWithConfig(decomp.DecodeRegionConfig{CodeBytes: append([]byte(nil), rom[off:off+length]...), EntryAddr: p.Start, EntryCtx: context, PinnedROM: rom, ROMBaseAddr: p.Start, MaxSteps: cfg.MaxSteps, AllowInternalJSR: true, RefusalTargets: frontiers})
	if err != nil {
		result.ReasonCode = "decode"
		result.Reason = "decode: " + err.Error()
		return result, nil
	}
	// Data reads use the explicitly bound runtime ROM; never embed ROM assets.
	region.ROMBytes = nil
	code, err := decomp.GenerateRegionC(region)
	if err != nil {
		result.ReasonCode = "generate"
		result.Reason = "generate: " + err.Error()
		return result, nil
	}
	result.SourceSHA256 = hash([]byte(code))
	if err := writeJSON(filepath.Join(dir, "region.json"), region); err != nil {
		return result, err
	}
	irBytes, err := os.ReadFile(filepath.Join(dir, "region.json"))
	if err != nil {
		return result, err
	}
	result.IRSHA256 = hash(irBytes)
	sourcePath := filepath.Join(dir, "generated.c")
	if err := os.WriteFile(sourcePath, []byte(code), 0600); err != nil {
		return result, err
	}
	runner, err := decomp.NewCompiledRegionRunnerWithROM(ctx, sourcePath, "execute_"+region.Name, rom)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.ReasonCode = "compile"
		result.Reason = "compile: " + err.Error()
		return result, nil
	}
	defer runner.Close()
	if err := runner.BindRegion(region, cfg.Revision); err != nil {
		result.Reason = "bind: " + err.Error()
		return result, nil
	}
	result.Status = "compiled"
	result.ReasonCode = "selected_cases_incomplete"
	var receipts []decomp.ReplayReceipt
	for _, c := range selected {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		receipt, err := decomp.ExecuteThreeWayRoutineReplay(ctx, runner, c)
		if err != nil {
			return result, err
		}
		decomp.ValidateRoutineReplayReceiptFreshness(&receipt, &c, region, code, hash(rom), cfg.Revision, runner)
		receipts = append(receipts, receipt)
		result.Unexecuted--
		if receipt.CapturedProofEligible && receipt.Eligible && receipt.Matched && receipt.EffectsMatch && !receipt.Metadata.IsStale {
			result.Matched++
		} else {
			if (receipt.Matched && receipt.Metadata.IsStale) || receipt.CompiledC.MissingRead || receipt.CompiledC.MMIOAccess || receipt.CompiledC.WriteOverflow || !strings.Contains(strings.ToLower(receipt.Discrepancy), "mismatch") {
				result.Refused++
			} else {
				result.Mismatched++
			}
			result.Reason = receipt.Discrepancy
			if result.Reason == "" {
				result.Reason = receipt.Metadata.StaleReason
			}
		}
	}
	if err := writeJSON(filepath.Join(dir, "cases.json"), selected); err != nil {
		return result, err
	}
	if err := writeJSON(filepath.Join(dir, "receipts.json"), receipts); err != nil {
		return result, err
	}
	if result.Mismatched > 0 {
		result.Status = "mismatch"
		result.ReasonCode = "replay_mismatch"
	} else if result.Refused == 0 && result.Matched == result.Cases && result.Cases > 0 {
		result.Status = "qualified"
		result.ReasonCode = "captured_sample_match"
		result.Reason = "all selected admitted cases match captured CPU and ordered writes; no timing claim"
	}
	return result, nil
}

func checkInputs(pins []pinnedInput) error {
	for _, p := range pins {
		b, err := os.ReadFile(p.path)
		if err != nil {
			return err
		}
		if hash(b) != p.hash {
			return fmt.Errorf("queue: input changed during execution: %s", p.path)
		}
	}
	return nil
}

func checkArtifacts(stage string, report *Report) error {
	for _, c := range report.Candidates {
		directory := strings.TrimPrefix(c.Directory, "artifacts"+string(filepath.Separator))
		for _, artifact := range []struct{ name, pin string }{{"generated.c", c.SourceSHA256}, {"region.json", c.IRSHA256}} {
			if artifact.pin == "" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(stage, directory, artifact.name))
			if err != nil {
				return err
			}
			if hash(b) != artifact.pin {
				return fmt.Errorf("queue: generated artifact changed: %s/%s", directory, artifact.name)
			}
		}
	}
	return nil
}
