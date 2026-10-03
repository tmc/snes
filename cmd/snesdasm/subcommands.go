package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/server"
	"github.com/tmc/snes/internal/recovery/structure"
)

func runCoverage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm coverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm coverage -project dir [flags]",
			"Report execution coverage statistics and hit counts from imported traces.",
			"snesdasm coverage -project game_dasm",
			"snesdasm coverage -project game_dasm -frames 0:1000",
			"snesdasm coverage -project game_dasm -addr 008000 -format json",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		metric     = fs.String("metric", "hits", "coverage metric: hits")
		frames     = fs.String("frames", "", "frame interval A:B (half-open [A,B))")
		format     = fs.String("format", "text", "output format: text|json")
		addrStr    = fs.String("addr", "", "filter by CPU bus address (hex)")
		offsetStr  = fs.String("offset", "", "filter by ROM offset (hex)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help coverage' for usage")
	}

	filter := coverage.Filter{}
	if *frames != "" {
		a, b, err := parseFrames(*frames)
		if err != nil {
			return err
		}
		filter.FrameStart, filter.FrameEnd = &a, &b
	}
	if *addrStr != "" {
		if a, err := parseHex(*addrStr); err == nil {
			filter.Address = &a
		} else {
			return fmt.Errorf("invalid -addr %q: %w", *addrStr, err)
		}
	}
	if *offsetStr != "" {
		if off, err := parseHex(*offsetStr); err == nil {
			filter.Offset = &off
		} else {
			return fmt.Errorf("invalid -offset %q: %w", *offsetStr, err)
		}
	}

	covPath := filepath.Join(*projectDir, "coverage.json")
	cf, err := os.Open(covPath)
	if err != nil {
		return fmt.Errorf("open coverage.json: %w", err)
	}
	defer cf.Close()

	idx, err := coverage.Decode(cf)
	if err != nil {
		return fmt.Errorf("decode coverage.json: %w", err)
	}

	res, err := idx.Query(filter)
	if err != nil {
		return err
	}

	if *metric == "hits" {
		binSize := uint32(32 * 1024)
		bins, _ := idx.QueryBins(filter, binSize, 1024*1024)
		res.Bins = bins
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	fmt.Fprintf(stdout, "Coverage for ROM %s:\n", res.ProjectROMHash)
	fmt.Fprintf(stdout, "  Total hits: %s (Quality: %s)\n", res.TotalHits, res.Quality)
	if res.FrameInterval != "" {
		fmt.Fprintf(stdout, "  Interval:   %s\n", res.FrameInterval)
	}
	if len(res.ByOffset) > 0 {
		fmt.Fprintf(stdout, "  Observed ROM locations: %d\n", len(res.ByOffset))
	}
	return nil
}

func runRoutines(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm routines", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm routines -project dir [flags]",
			"List routine candidates identified during recovery and trace analysis.",
			"snesdasm routines -project game_dasm",
			"snesdasm routines -project game_dasm -format json",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		format     = fs.String("format", "text", "output format: text|json")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help routines' for usage")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	routines := structure.ExtractRoutines(doc)
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(routines)
	}

	fmt.Fprintf(stdout, "Identified %d routine candidate(s):\n", len(routines))
	for _, r := range routines {
		fmt.Fprintf(stdout, "  $%06X: %-16s (%d insns, %d block(s))\n",
			r.EntryAddress, r.Name, r.InstructionCount, len(r.BlockIDs))
	}
	return nil
}

func runDisasm(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm disasm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm disasm -project dir [flags]",
			"Print recovered disassembly instructions from the recovery document.",
			"snesdasm disasm -project game_dasm",
			"snesdasm disasm -project game_dasm -addr 008000 -limit 50",
			"snesdasm disasm -project game_dasm -format json",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		addrStr    = fs.String("addr", "", "start CPU bus address (hex)")
		format     = fs.String("format", "text", "output format: text|json")
		limit      = fs.Int("limit", 100, "maximum instructions to display")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help disasm' for usage")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	var targetAddr uint32
	if *addrStr != "" {
		a, err := parseHex(*addrStr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %w", *addrStr, err)
		}
		targetAddr = a
	}

	var matched []recovery.Instruction
	for _, inst := range doc.Instructions {
		if targetAddr == 0 || inst.Address >= targetAddr {
			matched = append(matched, inst)
			if len(matched) >= *limit {
				break
			}
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(matched)
	}

	for _, inst := range matched {
		fmt.Fprintf(stdout, "$%06X  %-8s  %-12s  [E:%s M:%s X:%s C:%s]\n",
			inst.Address, inst.Bytes, inst.Mnemonic,
			inst.Context.E, inst.Context.M, inst.Context.X, inst.Context.C)
	}
	return nil
}

func runRefs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm refs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm refs -project dir [flags]",
			"List memory references and hardware MMIO registers accessed by recovered code.",
			"snesdasm refs -project game_dasm",
			"snesdasm refs -project game_dasm -addr 2100",
			"snesdasm refs -project game_dasm -format json",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		addrStr    = fs.String("addr", "", "filter by instruction or target address (hex)")
		format     = fs.String("format", "text", "output format: text|json")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help refs' for usage")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	refs := structure.ExtractMemoryReferences(doc)
	if *addrStr != "" {
		target, err := parseHex(*addrStr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %w", *addrStr, err)
		}
		var filtered []structure.MemoryReference
		for _, r := range refs {
			if r.EncodedAddress == target || r.InstructionAddress == target {
				filtered = append(filtered, r)
			}
		}
		refs = filtered
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(refs)
	}

	fmt.Fprintf(stdout, "Found %d memory reference(s):\n", len(refs))
	for _, r := range refs {
		name := r.HardwareName
		if name == "" {
			name = fmt.Sprintf("$%06X", r.EncodedAddress)
		}
		fmt.Fprintf(stdout, "  $%06X: %-10s -> %-12s (%-5s, %s)\n",
			r.InstructionAddress, r.Mnemonic, name, r.Direction, r.AddressSpace)
	}
	return nil
}

func runGraph(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm graph -project dir [flags]",
			"Print the control-flow graph (CFG) for recovered basic blocks.",
			"snesdasm graph -project game_dasm",
			"snesdasm graph -project game_dasm -addr 008000 -format dot",
			"snesdasm graph -project game_dasm -format text",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		addrStr    = fs.String("addr", "", "routine entry address (hex)")
		format     = fs.String("format", "dot", "output format: dot|json|text")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help graph' for usage")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	var entryAddr uint32
	if *addrStr != "" {
		a, err := parseHex(*addrStr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %w", *addrStr, err)
		}
		entryAddr = a
	}

	cfg := structure.BuildCFG(doc, entryAddr)

	switch *format {
	case "dot":
		fmt.Fprint(stdout, cfg.ToDOT())
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	default:
		fmt.Fprintf(stdout, "CFG has %d node(s) and %d edge(s)\n", len(cfg.Nodes), len(cfg.Edges))
		for _, e := range cfg.Edges {
			fmt.Fprintf(stdout, "  %s -> %s (%s, %s)\n", e.From, e.To, e.Kind, e.Provenance)
		}
	}
	return nil
}

func runServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm serve -project dir [flags]",
			"Start an HTTP server providing an interactive inspection UI for the project.",
			"snesdasm serve -project game_dasm",
			"snesdasm serve -project game_dasm -http 127.0.0.1:8081",
		)
	}
	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		httpAddr   = fs.String("http", "localhost:8080", "HTTP listen address")
		obsWindow  = fs.String("observation-window", "", "path to observation window JSON file")
		obsWinSHA  = fs.String("observation-window-sha256", "", "expected SHA-256 of observation window JSON file")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help serve' for usage")
	}

	var opts []server.ServerOption
	if *obsWindow != "" || *obsWinSHA != "" {
		opts = append(opts, server.WithObservationWindow(*obsWindow, *obsWinSHA))
	}

	srv, err := server.NewServer(*projectDir, opts...)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "snesdasm serving project %q at http://%s\n", *projectDir, *httpAddr)
	return http.ListenAndServe(*httpAddr, srv)
}

func loadDoc(projectDir string) (*recovery.Document, error) {
	docPath := filepath.Join(projectDir, "recovery.json")
	f, err := os.Open(docPath)
	if err != nil {
		return nil, fmt.Errorf("open recovery.json: %w", err)
	}
	defer f.Close()
	return recovery.Decode(f)
}

// parseFrames parses a half-open frame interval "A:B" with A <= B.
func parseFrames(s string) (start, end uint64, err error) {
	a, b, ok := strings.Cut(s, ":")
	if ok {
		start, err1 := strconv.ParseUint(a, 10, 64)
		end, err2 := strconv.ParseUint(b, 10, 64)
		if err1 == nil && err2 == nil && start <= end {
			return start, end, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid -frames %q: want A:B with unsigned integers A <= B", s)
}

func parseHex(s string) (uint32, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "$")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}
