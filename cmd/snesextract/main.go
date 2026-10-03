// snesextract extracts deterministic snes-routine-case-v1 replay cases from emulator traces.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/extractor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "snesextract: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("snesextract", flag.ContinueOnError)

	candidatePath := fs.String("candidate", "", "path to candidate proposal JSON")
	candidateID := fs.String("candidate-id", "", "candidate ID if proposal file contains multiple candidates")
	fixturePath := fs.String("fixture", "", "path to fixture trace.jsonl.gz")
	fixtureReceipt := fs.String("fixture-receipt", "", "path to fixture trace.receipt.json")
	fixtureSummary := fs.String("fixture-summary", "", "path to fixture summary.json")
	capturePath := fs.String("capture", "", "path to capture.jsonl")
	captureReceipt := fs.String("capture-receipt", "", "path to capture.receipt.json")
	captureSummary := fs.String("capture-summary", "", "path to capture.summary.json")
	historyPath := fs.String("history", "", "path to history.jsonl")
	historyReceipt := fs.String("history-receipt", "", "path to history.receipt.json")
	historySummary := fs.String("history-summary", "", "path to history.summary.json")
	checkpointPath := fs.String("checkpoint", "", "path to checkpoint state file (optional)")
	checkpointFrame := fs.Int("checkpoint-frame", 0, "checkpoint absolute frame number")
	romPath := fs.String("rom", "", "path to SNES ROM")
	romSHA256 := fs.String("rom-sha256", "", "expected ROM SHA-256 digest (optional verification)")
	inputsPath := fs.String("inputs", "", "path to input schedule JSON (optional)")
	startBoundary := fs.String("start-boundary", "", "start boundary (e.g. restored_state, power_on)")
	minFrame := fs.Int("min-frame", 0, "minimum frame number to consider")
	prefix := fs.String("prefix", "", "case ID prefix (optional)")
	label := fs.String("label", "capture", "corpus label")
	corpus := fs.String("corpus", "", "corpus name")
	outDir := fs.String("out", "", "output directory for extracted artifacts")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *candidatePath == "" || *fixturePath == "" || *capturePath == "" || *historyPath == "" || *romPath == "" || *outDir == "" {
		return fmt.Errorf("missing required flags (-candidate, -fixture, -capture, -history, -rom, -out)")
	}

	candBytes, err := os.ReadFile(*candidatePath)
	if err != nil {
		return fmt.Errorf("read candidate file: %w", err)
	}
	cand, err := extractor.LoadCandidate(candBytes, *candidateID)
	if err != nil {
		return fmt.Errorf("load candidate: %w", err)
	}

	corpusName := *corpus
	if corpusName == "" {
		corpusName = fmt.Sprintf("%s-%s", *label, cand.ID)
	}

	casePrefix := *prefix
	if casePrefix == "" {
		casePrefix = fmt.Sprintf("%s_", *label)
	}

	cfg := extractor.Config{
		Candidate:               cand,
		ExpectedROMSHA256:       *romSHA256,
		FixturePath:             *fixturePath,
		FixtureReceiptPath:      *fixtureReceipt,
		FixtureSummaryPath:      *fixtureSummary,
		CapturePath:             *capturePath,
		CaptureReceiptPath:      *captureReceipt,
		CaptureSummaryPath:      *captureSummary,
		HistoryPath:             *historyPath,
		HistoryReceiptPath:      *historyReceipt,
		HistorySummaryPath:      *historySummary,
		CheckpointPath:          *checkpointPath,
		CheckpointAbsoluteFrame: *checkpointFrame,
		ROMPath:                 *romPath,
		InputsPath:              *inputsPath,
		StartBoundary:           *startBoundary,
		MinFrame:                *minFrame,
		CasePrefix:              casePrefix,
		CorpusLabel:             *label,
		CorpusName:              corpusName,
	}

	result, err := extractor.Extract(cfg)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Reserve output directory exclusively before staging.
	if _, err := os.Stat(*outDir); err == nil {
		return fmt.Errorf("output directory %q already exists; refusing to overwrite", *outDir)
	}

	// Staged atomic publication: write completely to an exclusive temporary directory first.
	stageParent := filepath.Dir(*outDir)
	if err := os.MkdirAll(stageParent, 0755); err != nil {
		return fmt.Errorf("create output parent directory: %w", err)
	}
	stageDir, err := os.MkdirTemp(stageParent, ".snesextract-stage-*")
	if err != nil {
		return fmt.Errorf("create stage directory: %w", err)
	}
	defer os.RemoveAll(stageDir)

	// 1. Write cases.jsonl
	casesFile, err := os.Create(filepath.Join(stageDir, "cases.jsonl"))
	if err != nil {
		return fmt.Errorf("create cases.jsonl: %w", err)
	}
	for _, c := range result.Cases {
		b, err := json.Marshal(c)
		if err != nil {
			casesFile.Close()
			return fmt.Errorf("marshal case %s: %w", c.CaseID, err)
		}
		if _, err := fmt.Fprintf(casesFile, "%s\n", b); err != nil {
			casesFile.Close()
			return fmt.Errorf("write case %s: %w", c.CaseID, err)
		}
	}
	if err := casesFile.Close(); err != nil {
		return fmt.Errorf("close cases.jsonl: %w", err)
	}

	// 2. Write proposed-trust-root.json
	trustRootBytes, err := json.MarshalIndent(result.TrustRoot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal trust root: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "proposed-trust-root.json"), trustRootBytes, 0644); err != nil {
		return fmt.Errorf("write proposed-trust-root.json: %w", err)
	}

	// 3. Write negative-controls.jsonl
	negFile, err := os.Create(filepath.Join(stageDir, "negative-controls.jsonl"))
	if err != nil {
		return fmt.Errorf("create negative-controls.jsonl: %w", err)
	}
	for _, c := range result.NegativeControls {
		b, err := json.Marshal(c)
		if err != nil {
			negFile.Close()
			return fmt.Errorf("marshal negative control %s: %w", c.CaseID, err)
		}
		if _, err := fmt.Fprintf(negFile, "%s\n", b); err != nil {
			negFile.Close()
			return fmt.Errorf("write negative control %s: %w", c.CaseID, err)
		}
	}
	if err := negFile.Close(); err != nil {
		return fmt.Errorf("close negative-controls.jsonl: %w", err)
	}

	// 4. Write swap-controls.jsonl
	swapFile, err := os.Create(filepath.Join(stageDir, "swap-controls.jsonl"))
	if err != nil {
		return fmt.Errorf("create swap-controls.jsonl: %w", err)
	}
	for _, s := range result.SwapControls {
		b, err := json.Marshal(s)
		if err != nil {
			swapFile.Close()
			return fmt.Errorf("marshal swap control for %s: %w", s.AdmitCaseID, err)
		}
		if _, err := fmt.Fprintf(swapFile, "%s\n", b); err != nil {
			swapFile.Close()
			return fmt.Errorf("write swap control for %s: %w", s.AdmitCaseID, err)
		}
	}
	if err := swapFile.Close(); err != nil {
		return fmt.Errorf("close swap-controls.jsonl: %w", err)
	}

	// 5. Write receipt.json last as readiness marker.
	receiptBytes, err := json.MarshalIndent(result.Receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "receipt.json"), receiptBytes, 0644); err != nil {
		return fmt.Errorf("write receipt.json: %w", err)
	}

	// Atomic publication: rename stageDir to *outDir exclusively.
	// Since stageDir was created in stageParent = filepath.Dir(*outDir),
	// this is guaranteed to be an atomic same-filesystem directory rename.
	if err := os.Rename(stageDir, *outDir); err != nil {
		return fmt.Errorf("publish output directory: %w", err)
	}

	fmt.Printf("snesextract: extracted %d cases (from %d hits, %d rejected), %d negative controls, %d swap controls to %s\n",
		result.Receipt.CompleteExecutions, result.Receipt.TotalEntryHits, result.Receipt.RejectedExecutions,
		result.Receipt.NegativeControls, result.Receipt.SwapControls, *outDir)
	return nil
}
