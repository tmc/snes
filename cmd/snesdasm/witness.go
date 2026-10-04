package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
)

func runWitness(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm witness", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm witness -rom rom.sfc -trace trace.jsonl [flags]",
			"Derive dispatch witness from authentic trace events and execute bounded recovery.",
			"snesdasm witness -rom rom.sfc -trace trace.jsonl",
			"snesdasm witness -rom rom.sfc -trace trace.jsonl -seed 008056 -format json",
		)
	}

	var (
		romPath   = fs.String("rom", "", "path to admitted ROM file (required)")
		tracePath = fs.String("trace", "", "path to admitted trace JSONL file (required)")
		seedStr   = fs.String("seed", "008056", "seed address in hex (default: 008056)")
		budget    = fs.Int("budget", 5000, "maximum instruction budget (default: 5000)")
		format    = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *romPath == "" || *tracePath == "" {
		return fmt.Errorf("-rom and -trace flags are required; run 'snesdasm help witness' for usage")
	}

	seedVal, err := strconv.ParseUint(*seedStr, 16, 32)
	if err != nil {
		return fmt.Errorf("invalid seed address %q: %w", *seedStr, err)
	}

	rom, err := os.ReadFile(*romPath)
	if err != nil {
		return fmt.Errorf("reading ROM file: %w", err)
	}

	traceBytes, err := os.ReadFile(*tracePath)
	if err != nil {
		return fmt.Errorf("reading trace file: %w", err)
	}

	derived, err := analysis.DeriveWitnessFromTrace(traceBytes, rom)
	if err != nil {
		return fmt.Errorf("deriving witness from trace: %w", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: derived.ROMSHA256,
	})

	cfg := analysis.Config{
		MaxInstructions: *budget,
		SeedAddress:     uint32(seedVal),
		SeedContext:     recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
	}

	report, err := analysis.RunWitnessRecovery(rom, doc, derived, cfg)
	if err != nil {
		return fmt.Errorf("running witness recovery: %w", err)
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(stdout, "Witness Recovery Report:\n")
	fmt.Fprintf(stdout, "  Mode:                   %s\n", report.Mode)
	fmt.Fprintf(stdout, "  Start Address:          %s\n", report.StartAddress)
	fmt.Fprintf(stdout, "  Stream SHA-256:         %s\n", report.StreamSHA256)
	fmt.Fprintf(stdout, "  ROM SHA-256:            %s\n", report.ROMSHA256)
	fmt.Fprintf(stdout, "  Dispatch Event:         %d\n", report.DispatchEventID)
	fmt.Fprintf(stdout, "  Pointer Events:         %v\n", report.PointerEventIDs)
	fmt.Fprintf(stdout, "  Target Event:           %d\n", report.TargetEventID)
	fmt.Fprintf(stdout, "  Indirect Dispatch Edge: %s -> %s\n", report.SourceAddress, report.TargetAddress)
	fmt.Fprintf(stdout, "  Observed Context:       E=%s M=%s X=%s C=%s\n", report.ObservedContext.E, report.ObservedContext.M, report.ObservedContext.X, report.ObservedContext.C)
	fmt.Fprintf(stdout, "  Static Context:         E=%s M=%s X=%s C=%s\n", report.StaticContext.E, report.StaticContext.M, report.StaticContext.X, report.StaticContext.C)
	fmt.Fprintf(stdout, "  Baseline Starts:        %d (instructions: %d, edges: %d)\n", report.BaselinePhysicalStarts, report.BaselineInstructions, report.BaselineEdges)
	fmt.Fprintf(stdout, "  Witness Starts:         %d (instructions: %d, edges: %d)\n", report.WitnessPhysicalStarts, report.WitnessInstructions, report.WitnessEdges)
	fmt.Fprintf(stdout, "  Net Delta:              +%d physical starts (+%d instructions, +%d edges)\n", report.DeltaPhysicalStarts, report.DeltaInstructions, report.DeltaEdges)
	fmt.Fprintf(stdout, "  Added Physical Offsets: %d offsets\n", len(report.AddedPhysicalOffsets))
	fmt.Fprintf(stdout, "  Unresolved Issues:      %d issues remaining\n", len(report.UnresolvedIssues))

	return nil
}
