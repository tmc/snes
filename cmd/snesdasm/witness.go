package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		edge      = fs.Int("edge", 2, "witness edge: 1 ($0080C6->$0CC120) or 2 (cumulative $0087BD->$0CC404)")
		outDir    = fs.String("out", "", "optional output directory to write receipt.json and frontier.json")
		format    = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *romPath == "" || *tracePath == "" {
		return fmt.Errorf("-rom and -trace flags are required; run 'snesdasm help witness' for usage")
	}
	if *edge < 1 || *edge > 2 {
		return fmt.Errorf("invalid -edge %d: must be 1 or 2", *edge)
	}

	seedVal, err := strconv.ParseUint(*seedStr, 16, 32)
	if err != nil {
		return fmt.Errorf("invalid seed address %q: %w", *seedStr, err)
	}
	if seedVal != 0x008056 {
		return fmt.Errorf("unsupported seed address $%06X; only independently recorded seed 008056 is admitted", seedVal)
	}

	rom, err := os.ReadFile(*romPath)
	if err != nil {
		return fmt.Errorf("reading ROM file: %w", err)
	}

	traceBytes, err := os.ReadFile(*tracePath)
	if err != nil {
		return fmt.Errorf("reading trace file: %w", err)
	}

	witnesses, err := analysis.DeriveWitnessesFromTrace(traceBytes, rom)
	if err != nil {
		return fmt.Errorf("deriving witnesses from trace: %w", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: witnesses[0].ROMSHA256,
	})

	cfg := analysis.Config{
		MaxInstructions: *budget,
		SeedAddress:     uint32(seedVal),
		SeedContext:     recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
	}

	selectedWitnesses := witnesses[:*edge]
	report, err := analysis.RunCumulativeWitnessRecovery(rom, doc, selectedWitnesses, cfg)
	if err != nil {
		return fmt.Errorf("running cumulative witness recovery: %w", err)
	}

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0755); err != nil {
			return fmt.Errorf("creating output dir %s: %w", *outDir, err)
		}
		receiptBytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling receipt: %w", err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, "receipt.json"), receiptBytes, 0644); err != nil {
			return fmt.Errorf("writing receipt.json: %w", err)
		}

		frontierData := map[string]any{
			"edge_index":         report.EdgeIndex,
			"source":             report.SourceAddress,
			"target":             report.TargetAddress,
			"observed_context":   report.ObservedContext,
			"static_context":     report.StaticContext,
			"stream_sha256":      report.StreamSHA256,
			"rom_sha256":         report.ROMSHA256,
			"baseline_starts":    report.BaselinePhysicalStarts,
			"with_second_starts": report.WitnessPhysicalStarts,
			"delta_starts":       report.DeltaPhysicalStarts,
			"added_offsets":      report.AddedPhysicalOffsets,
			"issues":             report.UnresolvedIssues,
		}
		frontierBytes, err := json.MarshalIndent(frontierData, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling frontier: %w", err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, "frontier.json"), frontierBytes, 0644); err != nil {
			return fmt.Errorf("writing frontier.json: %w", err)
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(stdout, "Witness Recovery Report:\n")
	fmt.Fprintf(stdout, "  Mode:                   %s (seed: %s, budget: %d)\n", report.Mode, report.SeedAddress, report.Budget)
	fmt.Fprintf(stdout, "  Edge Index:             %d\n", report.EdgeIndex)
	fmt.Fprintf(stdout, "  Stream SHA-256:         %s\n", report.StreamSHA256)
	fmt.Fprintf(stdout, "  ROM SHA-256:            %s\n", report.ROMSHA256)
	fmt.Fprintf(stdout, "  Dispatch Event:         %d\n", report.DispatchEventID)
	fmt.Fprintf(stdout, "  Pointer Events:         %v\n", report.PointerEventIDs)
	fmt.Fprintf(stdout, "  Target Event:           %d\n", report.TargetEventID)
	fmt.Fprintf(stdout, "  Indirect Dispatch Edge: %s -> %s\n", report.SourceAddress, report.TargetAddress)
	fmt.Fprintf(stdout, "  Observed Target Ctx:    E=%s M=%s X=%s C=%s\n", report.ObservedContext.E, report.ObservedContext.M, report.ObservedContext.X, report.ObservedContext.C)
	fmt.Fprintf(stdout, "  Static Dispatch Ctx:    E=%s M=%s X=%s C=%s\n", report.StaticContext.E, report.StaticContext.M, report.StaticContext.X, report.StaticContext.C)
	fmt.Fprintf(stdout, "  Baseline Starts:        %d (instructions: %d, edges: %d)\n", report.BaselinePhysicalStarts, report.BaselineInstructions, report.BaselineEdges)
	fmt.Fprintf(stdout, "  Witness Starts:         %d (instructions: %d, edges: %d)\n", report.WitnessPhysicalStarts, report.WitnessInstructions, report.WitnessEdges)
	fmt.Fprintf(stdout, "  Net Delta:              +%d physical starts (+%d instructions, +%d edges)\n", report.DeltaPhysicalStarts, report.DeltaInstructions, report.DeltaEdges)
	fmt.Fprintf(stdout, "  Added Physical Offsets: %d offsets\n", len(report.AddedPhysicalOffsets))
	fmt.Fprintf(stdout, "  Unresolved Issues:      %d issues remaining\n", len(report.UnresolvedIssues))

	return nil
}
