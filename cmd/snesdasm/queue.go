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
	entry := fs.Uint("entry", 0, "mined candidate entry for an explicit named replay (hex accepted)")
	connectedProfile := fs.String("connected-profile", "", "bounded multi-span connected replay profile JSON")
	candidate := fs.String("candidate", "", "consumer-owned bounded candidate JSON for named replay")
	namedSymbols := fs.String("named-symbols", "", "consumer-owned byte symbol JSON for named replay")
	printBinding := fs.Bool("print-named-binding", false, "print bounded named binding digest for policy review")
	format := fs.String("format", "text", "output format: text|json")
	plan := fs.Bool("plan", false, "run frontier planner and output experiment plan")
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
	if *plan {
		var planArgs []string
		for _, a := range args {
			if a != "-plan" && a != "--plan" {
				planArgs = append(planArgs, a)
			}
		}
		return runPlan(planArgs, stdout, stderr)
	}
	if *printBinding {
		if *project == "" || *rom == "" || *cases == "" || *candidate == "" || *namedSymbols == "" || fs.NArg() != 0 {
			return fmt.Errorf("-print-named-binding requires -project, -rom, -cases, -candidate, and -named-symbols")
		}
		digest, err := queue.PreviewNamedBinding(queue.Config{ProjectDir: *project, ROMPath: *rom, CasesPath: *cases, CandidatePath: *candidate, NamedSymbolsPath: *namedSymbols, MaxSteps: *maxSteps})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, digest)
		return err
	}
	if *connectedProfile != "" {
		if *project == "" || *rom == "" || *cases == "" || *corpus == "" || *out == "" || *policy == "" || *policySHA == "" || *candidate != "" || *namedSymbols != "" || *entry != 0 || fs.NArg() != 0 {
			return fmt.Errorf("-connected-profile requires project, ROM, cases, corpus, output, and pinned singular policy; excludes candidate, named symbols, and entry")
		}
		if *maxCases < 1 || *maxCases > 10000 || *format != "text" && *format != "json" {
			return fmt.Errorf("queue budget or format out of range")
		}
		doc, err := loadDoc(*project)
		if err != nil {
			return err
		}
		report, err := queue.RunConnected(context.Background(), queue.Config{ProjectDir: *project, ROMPath: *rom, CasesPath: *cases, CorpusRoot: *corpus, OutDir: *out, Revision: recovery.ComputeProjectRevision(*project, doc), PolicyPath: *policy, PolicySHA256: *policySHA, ConnectedProfilePath: *connectedProfile, MaxCases: *maxCases})
		if err != nil {
			return err
		}
		if *format == "json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		fmt.Fprintf(stdout, "Connected replay $%06X handler $%06X: %s cases=%d admitted=%d matched=%d refused=%d mismatched=%d unexecuted=%d\n", report.Entry, report.HandlerEntry, report.Status, report.Cases, report.Admitted, report.Matched, report.Refused, report.Mismatched, report.Unexecuted)
		fmt.Fprintf(stdout, "  source=%s region=%s policy=%s\n", report.SourceSHA256, report.RegionSHA256, report.PolicySHA256)
		fmt.Fprintf(stdout, "  artifacts: %s\n", *out)
		return nil
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
	if *entry > 0xffffff {
		return fmt.Errorf("-entry out of range")
	}
	if *namedSymbols != "" && ((*entry == 0 && *candidate == "") || *policy == "") {
		return fmt.Errorf("-named-symbols requires -entry or -candidate, and -policy")
	}
	if *candidate != "" && (*namedSymbols == "" || *entry != 0) {
		return fmt.Errorf("-candidate requires -named-symbols and excludes -entry")
	}
	doc, err := loadDoc(*project)
	if err != nil {
		return err
	}
	revision := recovery.ComputeProjectRevision(*project, doc)
	report, err := queue.Run(context.Background(), queue.Config{
		ProjectDir: *project, ROMPath: *rom, CasesPath: *cases, CorpusRoot: *corpus, OutDir: *out,
		PolicyPath: *policy, PolicySHA256: *policySHA, Limit: *limit, MaxCases: *maxCases, MaxSteps: *maxSteps, Revision: revision,
		Entry: uint32(*entry), CandidatePath: *candidate, NamedSymbolsPath: *namedSymbols,
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
