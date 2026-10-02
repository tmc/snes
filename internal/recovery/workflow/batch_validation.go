package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tmc/snes/internal/recovery"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/queue"
)

// ResumeBatch verifies a recorded batch against an externally measured readiness
// manifest SHA-256. The expected digest must come from the original completion,
// not from the output being inspected. Sources and deliveries are checked again;
// the result describes execution under its recorded RuntimeSHA256, without
// executing C again or claiming qualification under the current executable.
// The retained directory and manifest are never modified.
func ResumeBatch(ctx context.Context, dir string, config Input, expectedManifestSHA256 string) (*BatchReport, error) {
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
	manifest := Input{Path: filepath.Join(dir, "manifest.json"), SHA256: expectedManifestSHA256}
	b, err := pinned(manifest, 16<<20)
	if err != nil {
		return nil, fmt.Errorf("batch readiness pin: %w", err)
	}
	var r BatchReport
	if err := strict(b, &r); err != nil {
		return nil, err
	}
	if r.Schema != "snes-recovery-batch-v1" || r.Config != config || len(r.Rows) != 10 {
		return nil, fmt.Errorf("batch identity changed")
	}
	if err := verifyArtifacts(dir, State{Artifacts: r.Artifacts}); err != nil {
		return nil, err
	}
	actual, err := treePins(dir, ".")
	if err != nil {
		return nil, err
	}
	delete(actual, "manifest.json")
	if !reflect.DeepEqual(actual, r.Artifacts) {
		return nil, fmt.Errorf("batch artifact inventory changed")
	}
	accepted, refused, unexecuted := 0, 0, 0
	for i, t := range c.Tasks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg, cand, err := loadConfig(t.Config)
		if err != nil {
			return nil, err
		}
		row := r.Rows[i]
		directory := fmt.Sprintf("task-%02d-%06x", i+1, cand.Entry)
		if row.Directory != directory {
			return nil, fmt.Errorf("batch task directory changed")
		}
		taskDir := filepath.Join(dir, directory)
		if err := validateBatchRow(taskDir, row, t, r.RuntimeSHA256); err != nil {
			return nil, err
		}
		b, err := pinned(Input{Path: filepath.Join(dir, fmt.Sprintf("row-%02d.json", i+1)), SHA256: r.Artifacts[fmt.Sprintf("row-%02d.json", i+1)]}, 16<<20)
		if err != nil {
			return nil, err
		}
		var retained BatchRow
		if err := strict(b, &retained); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(retained, row) {
			return nil, fmt.Errorf("batch manifest row changed")
		}
		state, _, err := readJournal(taskDir)
		if err != nil {
			return nil, err
		}
		expected, err := reconstructBatchRow(taskDir, directory, cfg, cand, state)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(expected, row) {
			return nil, fmt.Errorf("batch row does not match journal and execution artifacts")
		}
		switch expected.Status {
		case "accepted":
			accepted++
		case "refused":
			refused++
		case "unexecuted":
			unexecuted++
		default:
			return nil, fmt.Errorf("invalid batch status")
		}
	}
	if accepted != r.Accepted || refused != r.Refused || unexecuted != r.Unexecuted || accepted+refused+unexecuted != 10 {
		return nil, fmt.Errorf("batch accounting changed")
	}
	if _, err := loadBatch(config); err != nil {
		return nil, err
	}
	if err := verifyArtifacts(dir, State{Artifacts: r.Artifacts}); err != nil {
		return nil, err
	}
	if _, err := pinned(manifest, 16<<20); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &r, nil
}

func stateJSON(dir, name string, s State, v any) error {
	h, ok := s.Artifacts[name]
	if !ok {
		return fmt.Errorf("artifact is not committed in task journal: %s", name)
	}
	b, err := pinned(Input{Path: filepath.Join(dir, name), SHA256: h}, 256<<20)
	if err != nil {
		return err
	}
	return strict(b, v)
}

func validateBatchRow(taskDir string, row BatchRow, t BatchTask, runtime string) error {
	cfg, cand, err := loadConfig(t.Config)
	if err != nil {
		return err
	}
	if row.CandidateID != cand.ID || row.Entry != cand.Entry || row.ROMSHA256 != cfg.ROM.SHA256 {
		return fmt.Errorf("batch task identity changed")
	}
	info, err := os.Lstat(taskDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("batch task must be a real directory")
	}
	s, _, err := readJournal(taskDir)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(s, row.State) || s.Config != t.Config || s.Entry != cand.Entry || s.CandidateID != cand.ID || s.RuntimeSHA256 != runtime {
		return fmt.Errorf("batch full journal binding changed")
	}
	if s.Evidence.Path != "" && s.Evidence != t.Evidence {
		return fmt.Errorf("batch evidence delivery changed")
	}
	if s.Policy.Path != "" && s.Policy != t.Policy {
		return fmt.Errorf("batch policy delivery changed")
	}
	if s.Evidence.Path == "" && s.Phase != "selected" && s.Phase != "await_capture" {
		return fmt.Errorf("batch journal is missing evidence delivery")
	}
	if s.Policy.Path == "" && (s.Phase == "validation" || s.Phase == "qualified") {
		return fmt.Errorf("batch journal is missing policy delivery")
	}
	if err := verifyArtifacts(taskDir, s); err != nil {
		return err
	}
	var delivery Evidence
	if row.Extraction != nil {
		var receipt = *row.Extraction
		if err := stateJSON(taskDir, "extraction/receipt.json", s, &receipt); err != nil {
			return err
		}
		if !reflect.DeepEqual(receipt, *row.Extraction) || receipt.RoutineID != cand.ID || receipt.ROMSHA256 != cfg.ROM.SHA256 || receipt.CompleteExecutions < 0 || receipt.RejectedExecutions < 0 || receipt.TotalEntryHits != receipt.CompleteExecutions+receipt.RejectedExecutions {
			return fmt.Errorf("batch extraction accounting or identity changed")
		}
		e, err := loadEvidence(s.Evidence, cfg.MaxFrames)
		if err != nil {
			return err
		}
		delivery = e
		if receipt.FixtureSHA256 != e.Fixture.Trace.SHA256 || receipt.CaptureSHA256 != e.Capture.Trace.SHA256 || receipt.HistorySHA256 != e.History.Trace.SHA256 {
			return fmt.Errorf("batch extraction evidence differs from delivery")
		}
	}
	if s.Phase != "qualified" {
		if row.Status == "accepted" {
			return fmt.Errorf("unqualified journal cannot grant batch acceptance")
		}
		return nil
	}
	if row.Status != "accepted" || row.Extraction == nil || row.Extraction.CompleteExecutions < 1 || s.QualifiedCases < 1 || s.ValidationDir != fmt.Sprintf("validation-%06d", s.Sequence) {
		return fmt.Errorf("qualified batch row lacks execution binding")
	}
	queueDir := filepath.Join(s.ValidationDir, "queue")
	var q queue.Report
	if err := stateJSON(taskDir, filepath.Join(queueDir, "report.json"), s, &q); err != nil {
		return err
	}
	if q.Schema != "snes-recovery-queue-v1" || q.Revision != cfg.ProjectRevision || q.ROMSHA256 != cfg.ROM.SHA256 || q.PolicyFileSHA256 != t.Policy.SHA256 || !reflect.DeepEqual(q.Candidates, row.Results) || len(q.Candidates) < 1 || len(q.Candidates) > cfg.QueueLimit {
		return fmt.Errorf("batch queue identity changed")
	}
	rom, err := pinned(cfg.ROM, 16<<20)
	if err != nil {
		return err
	}
	policyBytes, err := pinned(t.Policy, 1<<20)
	if err != nil {
		return err
	}
	var policy decomp.AdmissionPolicy
	if err := strict(policyBytes, &policy); err != nil {
		return err
	}
	verifier, err := decomp.NewEvidenceVerifierWithPolicy(cfg.CorpusRoot, policy, rom)
	if err != nil {
		return err
	}
	root, ok := policy.Corpora[cfg.Corpus]
	if !ok || root.Label != cfg.Label || root.ROMSHA256 != cfg.ROM.SHA256 || root.DecompressedSHA != row.Extraction.FixtureDecSHA256 {
		return fmt.Errorf("batch extraction differs from reviewed corpus")
	}
	if q.PolicySHA256 != verifier.PolicySHA256() {
		return fmt.Errorf("batch canonical policy changed")
	}
	extractionBytes, err := pinned(Input{Path: filepath.Join(taskDir, "extraction/cases.jsonl"), SHA256: s.Artifacts["extraction/cases.jsonl"]}, 256<<20)
	if err != nil {
		return err
	}
	original, err := batchCases(extractionBytes)
	if err != nil {
		return err
	}
	if len(original) != row.Extraction.CompleteExecutions {
		return fmt.Errorf("batch extraction case count differs from receipt")
	}
	originalCases := make(map[string]bool, len(original))
	for _, c := range original {
		// Admission fills these fixture/effect fields before hashing. Bind the
		// producer payload after that documented normalization; raw replay
		// effects and policy identities are validated separately below.
		decomp.NormalizeProducerCase(&c)
		c.ObservedEffectsCapture = true
		if c.DecompressedStreamSHA256 == "" {
			c.DecompressedStreamSHA256 = row.Extraction.FixtureDecSHA256
		}
		originalCases[decomp.ComputeCaseHash(c)] = true
	}
	sources := map[string]string{}
	for _, source := range q.Sources {
		sources[source.Kind] = source.SHA256
	}
	doc, err := os.ReadFile(filepath.Join(cfg.ProjectDir, "recovery.json"))
	if err != nil {
		return err
	}
	if sources["recovery_document"] != digest(doc) || sources["rom"] != cfg.ROM.SHA256 || sources["reported_routine_case_inventory"] != digest(extractionBytes) {
		return fmt.Errorf("batch queue input identities changed")
	}
	qualified := 0
	for _, result := range q.Candidates {
		if result.Candidate.Entry != cand.Entry {
			return fmt.Errorf("batch queue selected another entry")
		}
		if result.Status != "qualified" {
			continue
		}
		if result.Candidate.ID != cand.ID || result.Candidate.Proposal.Start != cand.Entry || result.Candidate.Proposal.End-result.Candidate.Proposal.Start != uint32(cand.ByteSpan) || result.Candidate.InstructionCount != cand.InstructionCount {
			return fmt.Errorf("batch qualified candidate contract changed")
		}
		if result.Cases < 1 || result.Cases > cfg.MaxCases || result.Admitted != result.Cases || result.Matched != result.Cases || result.Refused != 0 || result.Mismatched != 0 || result.Unexecuted != 0 {
			return fmt.Errorf("batch qualified counts are inconsistent")
		}
		if filepath.IsAbs(result.Directory) || filepath.Clean(result.Directory) != result.Directory || result.Directory == ".." || strings.HasPrefix(result.Directory, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid queue artifact directory")
		}
		directory := filepath.Join(queueDir, result.Directory)
		var selected []decomp.ReplayCase
		var admissions []decomp.AdmissionRecord
		var receipts []decomp.ReplayReceipt
		for _, item := range []struct {
			name  string
			value any
		}{{"cases.json", &selected}, {"admissions.json", &admissions}, {"receipts.json", &receipts}} {
			if err := stateJSON(taskDir, filepath.Join(directory, item.name), s, item.value); err != nil {
				return err
			}
		}
		if len(selected) != result.Cases || len(admissions) != len(selected) || len(receipts) != len(selected) {
			return fmt.Errorf("batch qualified counts differ from raw receipts")
		}
		for _, artifact := range []struct{ name, hash string }{{"generated.c", result.SourceSHA256}, {"region.json", result.IRSHA256}} {
			name := filepath.Join(directory, artifact.name)
			if artifact.hash == "" || s.Artifacts[name] != artifact.hash {
				return fmt.Errorf("batch generated artifact binding changed")
			}
		}
		seen := map[string]bool{}
		for i, c := range selected {
			caseHash := decomp.ComputeCaseHash(c)
			if seen[caseHash] {
				return fmt.Errorf("duplicate selected captured case")
			}
			seen[caseHash] = true
			if !originalCases[caseHash] {
				return fmt.Errorf("batch selected case is absent from extraction")
			}
			if uint32(c.InitialState.PB)<<16|uint32(c.InitialState.PC) != cand.Entry || c.ROMSHA256 != cfg.ROM.SHA256 {
				return fmt.Errorf("batch selected case identity changed")
			}
			if c.CaseHash != caseHash || c.Evidence == nil || c.Evidence.Corpus != cfg.Corpus || c.Evidence.Label != cfg.Label || c.EngineRevision != root.EngineRevision {
				return fmt.Errorf("batch selected case provenance changed")
			}
			if !batchEvidenceMatches(cfg.CorpusRoot, c.Evidence.Fixture, delivery.Fixture) || !batchEvidenceMatches(cfg.CorpusRoot, c.Evidence.Capture, delivery.Capture) || !batchEvidenceMatches(cfg.CorpusRoot, c.Evidence.History, delivery.History) {
				return fmt.Errorf("batch selected case delivery changed")
			}
			a, r := admissions[i], receipts[i]
			if !a.Admitted || a.CaseID != c.CaseID || a.AdmissionDigest == "" || a.PolicySHA256 != q.PolicySHA256 || a.ROMSHA256 != cfg.ROM.SHA256 || r.AdmissionDigest != a.AdmissionDigest || c.AdmissionDigest != a.AdmissionDigest || !a.ObservedEffectsCapture {
				return fmt.Errorf("batch admission receipt binding changed")
			}
			if r.CaseID != c.CaseID || r.CaseHash != caseHash || !reflect.DeepEqual(r.CaseIdentity, c.Identity()) || !r.CapturedProofEligible || !r.Eligible || !r.Matched || !r.EffectsMatch || !r.ObservedMatch || !r.EmulatorMatch || !r.CMatch || r.Discrepancy != "" || r.EffectsStatus != "effects_matched" || !c.ObservedEffectsCapture {
				return fmt.Errorf("batch replay receipt is not a qualified case")
			}
			m := r.Metadata
			memory := map[uint32]uint8{}
			for _, cell := range c.InitialMemory {
				if _, ok := memory[cell.Address]; ok {
					return fmt.Errorf("duplicate initial memory cell")
				}
				memory[cell.Address] = cell.Value
			}
			memoryHash, _ := decomp.ComputeInitialMemory(memory)
			if m.IsStale || m.StaleReason != "" || m.ProjectRevision != cfg.ProjectRevision || m.ROMSHA256 != cfg.ROM.SHA256 || m.StartAddress != cand.Entry || m.GeneratedCHash != result.SourceSHA256 || m.Compiler == "" || m.CompilerFlags == "" || m.RunnerHash == "" || m.CodeHash == "" || m.MemoryPolicy != "snes_wram_mirror_v1" || m.Context != batchContext(c.InitialState) || m.InitialCPUStateHash != decomp.ComputeCPUStateHash(c.InitialState) || m.InitialMemHash != memoryHash {
				return fmt.Errorf("batch replay provenance changed")
			}
			for _, execution := range []decomp.ExecResult{r.TraceObserved, r.ReferenceEmu, r.CompiledC} {
				cpu, _ := decomp.CompareCPUStates(c.ObservedExit, execution.State)
				writes, _ := decomp.CompareWrites(c.ObservedWrites, execution.Writes)
				if !cpu || !writes || execution.NextPC != c.ObservedNextPC || execution.MissingRead || execution.MMIOAccess || execution.WriteOverflow {
					return fmt.Errorf("batch raw replay effects disagree with captured case")
				}
			}
		}
		qualified = max(qualified, len(receipts))
	}
	if qualified != s.QualifiedCases || qualified != selectedCases(&q, cand.Entry) {
		return fmt.Errorf("batch journal qualification differs from raw replay accounting")
	}
	return nil
}

func batchCases(b []byte) ([]decomp.ReplayCase, error) {
	decoder := json.NewDecoder(bytes.NewReader(b))
	var cases []decomp.ReplayCase
	for {
		var c decomp.ReplayCase
		err := decoder.Decode(&c)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func batchContext(s decomp.CPUState) recovery.Context {
	flag := func(v bool) string {
		if v {
			return "set"
		}
		return "clear"
	}
	return recovery.Context{E: flag(s.E), M: flag(s.P&0x20 != 0), X: flag(s.P&0x10 != 0), C: flag(s.P&1 != 0)}
}

func batchEvidenceMatches(root string, ref *decomp.EvidenceFileRef, stream Stream) bool {
	match := func(ref *decomp.EvidenceFileRef, input Input) bool {
		if ref == nil || ref.SHA256 != input.SHA256 {
			return false
		}
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.Clean(path) == filepath.Clean(input.Path)
	}
	return ref != nil && match(ref, stream.Trace) && match(ref.Receipt, stream.Receipt) && match(ref.Summary, stream.Summary)
}
