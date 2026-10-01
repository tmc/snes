package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/queue"
)

func runQueue(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "recovery project directory (required)")
	rom := fs.String("rom", "", "explicit original ROM path (required)")
	cases := fs.String("cases", "", "routine case JSONL path (required; may be empty)")
	corpus := fs.String("corpus", "", "retained evidence corpus root (required)")
	policy := fs.String("policy", "", "explicit operator-reviewed admission policy JSON (optional)")
	policySHA := fs.String("policy-sha256", "", "required SHA-256 of explicit policy file")
	out := fs.String("out", "", "durable queue artifact directory (required)")
	limit := fs.Int("limit", 5, "maximum candidates attempted (1..100)")
	maxCases := fs.Int("maxcases", 100, "maximum cases per candidate (1..10000)")
	maxSteps := fs.Int("maxsteps", 50000, "maximum machine instructions per case (1..1000000)")
	format := fs.String("format", "text", "output format: text|json")
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm queue -project dir -rom file -cases file -corpus dir -out dir [flags]",
			"Attempt bounded C recovery sequentially. Generation and compilation do not grant qualification. Only fresh admitted cases can establish the reported CPU/RAM scope; timing and device equivalence remain excluded.",
			"snesdasm queue -project game_dasm -rom game.sfc -cases cases.jsonl -corpus captures -out recovered_c -limit 5 -maxcases 100",
		)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, required := range []struct{ name, value string }{{"project", *project}, {"rom", *rom}, {"cases", *cases}, {"corpus", *corpus}, {"out", *out}} {
		if required.value == "" {
			return fmt.Errorf("-%s flag is required", required.name)
		}
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid format %q", *format)
	}
	if *limit < 1 || *limit > 100 || *maxCases < 1 || *maxCases > 10000 || *maxSteps < 1 || *maxSteps > 1000000 {
		return fmt.Errorf("queue budget out of range")
	}
	if (*policy == "") != (*policySHA == "") {
		return fmt.Errorf("-policy and -policy-sha256 must be supplied together")
	}
	doc, err := loadDoc(*project)
	if err != nil {
		return err
	}
	revision := recovery.ComputeProjectRevision(*project, doc)
	report, err := queue.Run(context.Background(), queue.Config{
		ProjectDir: *project, ROMPath: *rom, CasesPath: *cases, CorpusRoot: *corpus, OutDir: *out,
		PolicyPath: *policy, PolicySHA256: *policySHA, Limit: *limit, MaxCases: *maxCases, MaxSteps: *maxSteps, Revision: revision,
	})
	if err != nil {
		return err
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(stdout, "Recovery queue (CPU/RAM scope only; compilation is not qualification):")
	fmt.Fprintf(stdout, "  Project revision: %s\n", report.Revision)
	fmt.Fprintf(stdout, "  ROM SHA-256: %s\n", report.ROMSHA256)
	fmt.Fprintf(stdout, "  Candidates attempted: %d\n", len(report.Candidates))
	for _, r := range report.Candidates {
		fmt.Fprintf(stdout, "$%06X %-12s cases=%d admitted=%d matched=%d refused=%d mismatched=%d unexecuted=%d\n", r.Candidate.Entry, r.Status, r.Cases, r.Admitted, r.Matched, r.Refused, r.Mismatched, r.Unexecuted)
		if r.Reason != "" {
			fmt.Fprintf(stdout, "  reason: %s: %s\n", r.ReasonCode, r.Reason)
		}
		if r.Directory != "" {
			fmt.Fprintf(stdout, "  artifacts: %s\n", filepath.Join(*out, r.Directory))
		}
	}
	return nil
}
