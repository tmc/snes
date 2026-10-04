package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery/analysis/extractor"
)

func runExtract(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm extract", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		projectDir      = fs.String("project", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project", "path to project directory with recovery.json")
		tracePath       = fs.String("trace", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl", "path to authentic trace.jsonl")
		romPath         = fs.String("rom", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc", "path to authentic ROM file")
		minLength       = fs.Int("min-length", 2, "minimum straight-line span instruction length")
		maxSpans        = fs.Int("max-spans", 20, "maximum number of qualified spans to export")
		candidateBudget = fs.Int("candidate-budget", 150, "candidate span evaluation budget")
		stepBudget      = fs.Int("step-budget", 5000, "total step execution budget")
		outDir          = fs.String("out", "", "optional directory to export qualified cases, generated C, and accounting receipt")
		format          = fs.String("format", "text", "output format: text or json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := extractor.Config{
		TracePath:       *tracePath,
		ROMPath:         *romPath,
		ProjectDir:      *projectDir,
		MinSpanLength:   *minLength,
		MaxSpans:        *maxSpans,
		CandidateBudget: *candidateBudget,
		StepBudget:      *stepBudget,
	}

	ctx := context.Background()
	acct, err := extractor.Run(ctx, cfg)
	if err != nil {
		return fmt.Errorf("extract spans: %w", err)
	}

	// Export artifacts if output directory is requested
	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0755); err != nil {
			return fmt.Errorf("create output directory %s: %w", *outDir, err)
		}

		acctBytes, err := json.MarshalIndent(acct, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal accounting: %w", err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, "accounting.json"), acctBytes, 0644); err != nil {
			return fmt.Errorf("write accounting.json: %w", err)
		}

		spansDir := filepath.Join(*outDir, "spans")
		if err := os.MkdirAll(spansDir, 0755); err != nil {
			return fmt.Errorf("create spans directory: %w", err)
		}

		for _, q := range acct.QualifiedSpans {
			spanSubdir := filepath.Join(spansDir, q.BlockID)
			if err := os.MkdirAll(spanSubdir, 0755); err != nil {
				return fmt.Errorf("create span dir %s: %w", spanSubdir, err)
			}

			// Write generated C
			if err := os.WriteFile(filepath.Join(spanSubdir, "generated.c"), []byte(q.GeneratedC), 0644); err != nil {
				return fmt.Errorf("write generated.c: %w", err)
			}

			// Write replay case
			caseBytes, err := json.MarshalIndent(q.ReplayCase, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal replay case: %w", err)
			}
			if err := os.WriteFile(filepath.Join(spanSubdir, "case.json"), caseBytes, 0644); err != nil {
				return fmt.Errorf("write case.json: %w", err)
			}

			// Write span metadata
			spanMetaBytes, err := json.MarshalIndent(q, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal span metadata: %w", err)
			}
			if err := os.WriteFile(filepath.Join(spanSubdir, "span.json"), spanMetaBytes, 0644); err != nil {
				return fmt.Errorf("write span.json: %w", err)
			}
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(acct)
	}

	// Text format output
	fmt.Fprintf(stdout, "snesdasm extract: straight-line span extraction and qualification receipt\n")
	fmt.Fprintf(stdout, "  Trace Stream SHA256: %s\n", acct.StreamSHA256)
	fmt.Fprintf(stdout, "  ROM SHA256:          %s\n", acct.ROMSHA256)
	fmt.Fprintf(stdout, "  Events Scanned:      %d\n", acct.TotalEventsScanned)
	fmt.Fprintf(stdout, "  Retirements:         %d\n", acct.TotalRetirements)
	fmt.Fprintf(stdout, "  Candidates Found:    %d\n", acct.CandidateSpansFound)
	fmt.Fprintf(stdout, "  Selected / Evaluated:%d\n", acct.SelectedSpans)
	fmt.Fprintf(stdout, "  Qualified Spans:     %d\n", len(acct.QualifiedSpans))
	fmt.Fprintf(stdout, "  Refused Spans:       %d\n", len(acct.RefusedSpans))
	fmt.Fprintf(stdout, "  Mismatched Spans:    %d\n", len(acct.MismatchedSpans))
	fmt.Fprintf(stdout, "  Unique Physical Starts:%d\n", len(acct.UniquePhysicalStarts))
	fmt.Fprintf(stdout, "  Unique ROM Banks:    %v\n\n", acct.UniqueROMBanks)

	fmt.Fprintf(stdout, "Qualified Spans:\n")
	for i, q := range acct.QualifiedSpans {
		fmt.Fprintf(stdout, "  [%2d] %-36s Start:$%06X End:$%06X Bank:%02X Insns:%2d Starts:%2d Seqs:%d..%d\n",
			i+1, q.BlockID, q.StartAddress, q.EndAddress, q.ROMBank, q.InstructionCount, len(q.PhysicalStarts), q.StartSeq, q.EndSeq)
	}

	if len(acct.RefusedSpans) > 0 {
		fmt.Fprintf(stdout, "\nSample Refused Spans:\n")
		limit := 5
		if len(acct.RefusedSpans) < limit {
			limit = len(acct.RefusedSpans)
		}
		for i := 0; i < limit; i++ {
			r := acct.RefusedSpans[i]
			fmt.Fprintf(stdout, "  - Start:$%06X Len:%d Seq:%d: %s\n", r.StartAddress, r.Length, r.StartSeq, r.Reason)
		}
	}

	if len(acct.MismatchedSpans) > 0 {
		fmt.Fprintf(stdout, "\nSample Mismatched Spans:\n")
		limit := 5
		if len(acct.MismatchedSpans) < limit {
			limit = len(acct.MismatchedSpans)
		}
		for i := 0; i < limit; i++ {
			m := acct.MismatchedSpans[i]
			fmt.Fprintf(stdout, "  - Start:$%06X Len:%d Seq:%d: %s\n", m.StartAddress, m.Length, m.StartSeq, m.Reason)
		}
	}

	if *outDir != "" {
		fmt.Fprintf(stdout, "\nArtifacts exported to: %s\n", *outDir)
	}

	return nil
}
