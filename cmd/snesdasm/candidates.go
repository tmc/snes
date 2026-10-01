package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/coverage"
)

func runCandidates(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project directory (required)")
	format := fs.String("format", "text", "output format: text|json")
	cases := fs.String("cases", "", "optional routine-case JSONL inventory (unverified)")
	limit := fs.Int("limit", 20, "maximum candidates, 0 for all")
	maxInstructions := fs.Int("max-instructions", 4096, "maximum recovered instructions per candidate")
	maxBytes := fs.Uint("max-bytes", 32768, "maximum candidate byte span")
	maxDepth := fs.Int("max-depth", 2, "maximum callee traversal depth")
	fs.Usage = func() {
		subcommandUsage(fs, "snesdasm candidates -project dir [flags]", "Rank recovery proposals without granting generation or proof eligibility.", "snesdasm candidates -project game_dasm -format json")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" {
		return fmt.Errorf("-project flag is required")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid format %q", *format)
	}
	if *limit < 0 || *maxInstructions <= 0 || *maxDepth <= 0 || *maxBytes == 0 || uint64(*maxBytes) > 0xFFFFFF {
		return fmt.Errorf("invalid candidate limit or bound")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	recoveryBytes, err := os.ReadFile(filepath.Join(*project, "recovery.json"))
	if err != nil {
		return err
	}
	doc, err := recovery.Decode(bytes.NewReader(recoveryBytes))
	if err != nil {
		return err
	}
	sha := sha256.Sum256(recoveryBytes)
	sources := []candidates.Source{{ID: "recovery.json", SHA256: hex.EncodeToString(sha[:]), Kind: "recovery_document"}}
	var idx *coverage.Index
	f, err := os.Open(filepath.Join(*project, "coverage.json"))
	if err == nil {
		h := sha256.New()
		idx, err = coverage.Decode(io.TeeReader(f, h))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		sources = append(sources, candidates.Source{ID: "coverage.json", SHA256: hex.EncodeToString(h.Sum(nil)), Kind: "coverage_index"})
	} else if !os.IsNotExist(err) {
		return err
	}
	var inventory []candidates.Occurrence
	if *cases != "" {
		f, err := os.Open(*cases)
		if err != nil {
			return err
		}
		var src candidates.Source
		inventory, src, err = candidates.ReadInventory(f, filepath.Base(*cases))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		sources = append(sources, src)
	}
	report, err := candidates.Mine(doc, idx, inventory, sources, candidates.Options{MaxInstructions: *maxInstructions, MaxBytes: uint32(*maxBytes), MaxCallDepth: *maxDepth})
	if err != nil {
		return err
	}
	if *limit > 0 && len(report.Candidates) > *limit {
		report.Candidates = report.Candidates[:*limit]
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(stdout, "Candidate proposals (unqualified; inventory counts are not capture proof):")
	for _, c := range report.Candidates {
		fmt.Fprintf(stdout, "$%06X %-13s %4d insns (%d liftable), %d bytes, entry hits %s (%s), reported complete %d, interrupted %d\n", c.Entry, c.Kind, c.InstructionCount, c.SupportedInstructions, c.ByteSpan, c.ObservedEntryHits, c.EntryHitQuality, c.ReportedCompleteExecutions, c.ReportedInterruptedExecutions)
		if len(c.Flags) > 0 {
			fmt.Fprintf(stdout, "  flags: %v\n", c.Flags)
		}
		for _, f := range c.Proposal.RefusalFrontiers {
			fmt.Fprintf(stdout, "  frontier $%06X -> $%06X: %s\n", f.From, f.Target, f.Reason)
		}
	}
	return nil
}
