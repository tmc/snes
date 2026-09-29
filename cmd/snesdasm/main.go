// Command snesdasm reconstructs reassemblable assembly projects from SNES ROMs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
	"github.com/tmc/snes/internal/recovery/asmexport"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/traceimport"
	"github.com/tmc/snes/internal/recovery/verify"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "snesdasm: %v\n", err)
		os.Exit(1)
	}
}

// A command is a snesdasm subcommand.
type command struct {
	name    string
	summary string
	run     func(args []string, stdout, stderr io.Writer) error
}

var commands = []command{
	{"coverage", "report execution coverage from a project's trace imports", runCoverage},
	{"routines", "list routine candidates", runRoutines},
	{"disasm", "print recovered instructions", runDisasm},
	{"refs", "list memory references", runRefs},
	{"graph", "print the control-flow graph", runGraph},
	{"watches", "list watch definitions", runWatches},
	{"watch", "show the value history of one watch", runWatch},
	{"serve", "serve the project inspection UI over HTTP", runServe},
}

func lookupCommand(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

// run runs snesdasm with args. A help request is not an error.
func run(args []string, stdout, stderr io.Writer) error {
	err := dispatch(args, stdout, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func dispatch(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		if args[0] == "help" {
			if len(args) > 1 {
				if c := lookupCommand(args[1]); c != nil {
					return c.run([]string{"-h"}, stdout, stdout)
				}
				return fmt.Errorf("unknown help topic %q; run 'snesdasm help'", args[1])
			}
			return runRecovery([]string{"-h"}, stdout, stdout)
		}
		if c := lookupCommand(args[0]); c != nil {
			return c.run(args[1:], stdout, stderr)
		}
	}
	return runRecovery(args, stdout, stderr)
}

func usage(fs *flag.FlagSet) {
	w := fs.Output()
	fmt.Fprint(w, `usage: snesdasm -rom file [-out dir] [flags]
       snesdasm <command> -project dir [flags]

With -rom, snesdasm recovers an assembly project from a ROM into -out,
optionally importing a runtime trace (-trace).

Commands:
`)
	for _, c := range commands {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, `
Run 'snesdasm help <command>' for command flags.

Recovery flags:
`)
	fs.PrintDefaults()
}

func runRecovery(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(fs) }
	var (
		romPath           = fs.String("rom", "", "path to SNES ROM file")
		outDir            = fs.String("out", "", "target output directory")
		allowCopierHeader = fs.Bool("allow-copier-header", false, "allow stripping 512-byte copier header")
		projectName       = fs.String("project-name", "recovery", "project name for Futaba manifest")
		verifyRebuilt     = fs.String("verify-rebuilt", "", "path to existing rebuilt ROM to verify against normalized input")
		assemble          = fs.Bool("assemble", false, "run assembler after export and verify byte-identical reproduction")
		assemblerBin      = fs.String("assembler", "", "path to assembler binary (defaults to 'snesasm')")
		timeout           = fs.Duration("timeout", 30*time.Second, "execution timeout for assembler")
		overwrite         = fs.Bool("overwrite", false, "overwrite existing export and verification directories")
		tracePath         = fs.String("trace", "", "path to runtime observation stream (.jsonl or .jsonl.gz)")
		traceReceipt      = fs.String("trace-receipt", "", "path to trace receipt.json (defaults to receipt.json next to trace)")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *romPath == "" {
		return fmt.Errorf("-rom flag is required")
	}

	if *outDir == "" {
		base := filepath.Base(*romPath)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)
		*outDir = name + "_dasm"
	}

	// 1. Pre-validate output ownership before ANY file mutation.
	if entries, err := os.ReadDir(*outDir); err == nil && len(entries) > 0 {
		hasNonAnnotation := false
		for _, e := range entries {
			if e.Name() != "annotations.json" {
				hasNonAnnotation = true
				break
			}
		}
		if hasNonAnnotation && !*overwrite {
			return fmt.Errorf("output directory %q already exists and is not empty; use -overwrite to replace", *outDir)
		}
	}

	// 2. Admit ROM.
	f, err := os.Open(*romPath)
	if err != nil {
		return fmt.Errorf("open rom: %w", err)
	}
	defer f.Close()

	admitted, err := recovery.AdmitROM(f, recovery.AdmissionOptions{
		AllowCopierHeader: *allowCopierHeader,
	})
	if err != nil {
		return fmt.Errorf("admit rom: %w", err)
	}

	// Ensure target directory exists.
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	exportDir := filepath.Join(*outDir, "export")
	verDir := filepath.Join(*outDir, "verification")

	// If overwriting, clear previous export and verification directories while preserving annotations.json.
	if *overwrite {
		_ = os.RemoveAll(exportDir)
		_ = os.RemoveAll(verDir)
	}

	// 3. Perform static analysis starting from reset vector or load existing recovery.
	doc := recovery.NewDocument(admitted.Identity)
	existingDocPath := filepath.Join(*outDir, "recovery.json")
	if *tracePath == "" && fileExists(existingDocPath) {
		if df, err := os.Open(existingDocPath); err == nil {
			if loaded, err := recovery.Decode(df); err == nil {
				doc = loaded
				fmt.Fprintf(stdout, "Loaded existing recovery document: %d instructions, %d edges\n",
					len(doc.Instructions), len(doc.Edges))
			}
			df.Close()
		}
	} else if admitted.Identity.Mapper == "lorom" {
		if res, err := analysis.AnalyzeLoROM(admitted.NormalizedROM, doc, analysis.Config{MaxInstructions: 5000}); err != nil {
			fmt.Fprintf(stderr, "warning: analysis failed: %v\n", err)
		} else {
			fmt.Fprintf(stdout, "Analyzed reset routine at $%06X: %d instructions, %d edges, %d issues\n",
				res.ResetAddress, len(res.Instructions), len(res.Edges), len(res.Issues))
		}
	}

	// 4. Ingest runtime observation trace if provided.
	if *tracePath != "" {
		traceFile, err := os.Open(*tracePath)
		if err != nil {
			return fmt.Errorf("open trace file %q: %w", *tracePath, err)
		}
		defer traceFile.Close()

		var receiptReader io.Reader
		rcPath := *traceReceipt
		if rcPath == "" {
			dir := filepath.Dir(*tracePath)
			base := filepath.Base(*tracePath)
			stem := strings.TrimSuffix(base, ".gz")
			stem = strings.TrimSuffix(stem, filepath.Ext(stem))
			candidate1 := filepath.Join(dir, stem+".receipt.json")
			candidate2 := filepath.Join(dir, "receipt.json")
			if _, err := os.Stat(candidate1); err == nil {
				rcPath = candidate1
			} else if _, err := os.Stat(candidate2); err == nil {
				rcPath = candidate2
			}
		}
		if rcPath != "" {
			rf, err := os.Open(rcPath)
			if err != nil {
				return fmt.Errorf("open trace receipt %q: %w", rcPath, err)
			}
			defer rf.Close()
			receiptReader = rf
		}

		covPath := filepath.Join(*outDir, "coverage.json")
		covIdx, err := loadCoverage(covPath, admitted.Identity.NormalizedSHA256, stderr)
		if err != nil {
			return err
		}

		traceRes, err := traceimport.Parse(traceFile, receiptReader, admitted.NormalizedROM, admitted.Identity.NormalizedSHA256)
		if err != nil {
			return fmt.Errorf("import trace %q: %w", *tracePath, err)
		}

		mr, err := traceimport.Merge(doc, traceRes)
		if err != nil {
			return fmt.Errorf("merge trace %q: %w", *tracePath, err)
		}

		receiptOutcome := "complete"
		if traceRes.Receipt != nil {
			receiptOutcome = traceRes.Receipt.Outcome
		}
		runID := traceRes.LogicalRunID
		if runID == "" {
			runID = traceRes.StreamSHA256
		}
		if err := covIdx.AddRun(coverage.RunInfo{
			ID:         runID,
			ROM_SHA256: admitted.Identity.NormalizedSHA256,
			EngineRev:  traceRes.RunMetadata.EngineRevision,
			Outcome:    receiptOutcome,
			StreamSHA:  traceRes.StreamSHA256,
			IsComplete: traceRes.IsComplete,
			Gaps:       traceRes.Gaps,
		}, traceRes.Sites); err != nil {
			return fmt.Errorf("record coverage run: %w", err)
		}
		if err := writeCoverage(covPath, covIdx); err != nil {
			return err
		}

		completenessStr := "complete"
		if !traceRes.IsComplete {
			completenessStr = "incomplete/limited"
		}
		ri := covIdx.Runs[runID]
		fmt.Fprintf(stdout, "Imported trace (%s): %d records, %d executions at %d sites over frames %d-%d, %d new instructions (%d existing), %d edges\n",
			completenessStr, traceRes.TotalRecords, ri.EventCount, len(traceRes.Sites), ri.MinFrame, ri.MaxFrame,
			mr.InstructionsAdded, mr.InstructionsExisting, mr.EdgesAdded)
	}

	docPath := filepath.Join(*outDir, "recovery.json")
	docFile, err := os.Create(docPath)
	if err != nil {
		return fmt.Errorf("create recovery.json: %w", err)
	}
	if err := recovery.Encode(docFile, doc); err != nil {
		docFile.Close()
		return fmt.Errorf("encode recovery.json: %w", err)
	}
	docFile.Close()

	// 4. Export assembly project.
	expRes, err := asmexport.Export(exportDir, doc, admitted.NormalizedROM, asmexport.Config{
		ProjectName: *projectName,
		EntryAsm:    "main.asm",
		Overwrite:   *overwrite,
	})
	if err != nil {
		return fmt.Errorf("export assembly: %w", err)
	}

	fmt.Fprintf(stdout, "Admitted ROM: %s (%d bytes, normalized %d bytes, mapper %s)\n",
		*romPath, admitted.Identity.OriginalSize, admitted.Identity.NormalizedSize, admitted.Identity.Mapper)
	fmt.Fprintf(stdout, "Exported: %d bytes into %s (manifest: %s)\n",
		expRes.BytesExported, exportDir, filepath.Base(expRes.ManifestPath))

	// 5. Toolchain assembly and verification if requested.
	asmCmd := *assemblerBin
	if *assemble && asmCmd == "" {
		asmCmd = "snesasm"
	}

	if asmCmd != "" {
		if _, err := exec.LookPath(asmCmd); err != nil {
			return fmt.Errorf("assembler executable %q not found: %w", asmCmd, err)
		}

		buildDir := filepath.Join(verDir, "build")
		if err := os.MkdirAll(buildDir, 0755); err != nil {
			return fmt.Errorf("create verification build dir: %w", err)
		}

		rebuiltPath := filepath.Join(buildDir, "rebuilt.sfc")
		logPath := filepath.Join(verDir, "assembler.log")
		logFile, err := os.Create(logPath)
		if err != nil {
			return fmt.Errorf("create assembler log: %w", err)
		}
		defer logFile.Close()

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()

		cfg := verify.Config{
			AssemblerPath: asmCmd,
			AssemblerArgs: []string{"-o", rebuiltPath, "main.asm"},
			WorkingDir:    exportDir,
			LogWriter:     logFile,
			SourceHashes:  expRes.SourceHashes,
		}

		receipt, err := verify.Verify(ctx, rebuiltPath, admitted.NormalizedROM, cfg)
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}

		receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal receipt: %w", err)
		}
		if err := os.WriteFile(filepath.Join(verDir, "receipt.json"), append(receiptJSON, '\n'), 0644); err != nil {
			return fmt.Errorf("write receipt.json: %w", err)
		}

		fmt.Fprintf(stdout, "Assembler: %s (took %dms)\n", receipt.AssemblerPath, receipt.DurationMs)
		fmt.Fprintf(stdout, "Verification: %s (mismatches: %d)\n", receipt.Outcome, receipt.MismatchCount)

		if receipt.Outcome != verify.OutcomeMatched {
			if receipt.Error != "" {
				return fmt.Errorf("verification failed (%s): %s", receipt.Outcome, receipt.Error)
			}
			return fmt.Errorf("verification failed with outcome %q and %d mismatches", receipt.Outcome, receipt.MismatchCount)
		}
		return nil
	}

	// 6. Optional verification of pre-existing rebuilt ROM.
	if *verifyRebuilt != "" {
		receipt, err := verify.Verify(context.Background(), *verifyRebuilt, admitted.NormalizedROM, verify.Config{
			SourceHashes: expRes.SourceHashes,
		})
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}

		if err := os.MkdirAll(verDir, 0755); err != nil {
			return fmt.Errorf("create verification dir: %w", err)
		}
		receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal receipt: %w", err)
		}
		if err := os.WriteFile(filepath.Join(verDir, "receipt.json"), append(receiptJSON, '\n'), 0644); err != nil {
			return fmt.Errorf("write receipt.json: %w", err)
		}

		fmt.Fprintf(stdout, "Verification: %s (mismatches: %d)\n", receipt.Outcome, receipt.MismatchCount)
		if receipt.Outcome != verify.OutcomeMatched {
			return fmt.Errorf("verification outcome: %s", receipt.Outcome)
		}
	}

	return nil
}

// loadCoverage reads the coverage index at path, or returns a new index for
// romHash if none exists. An index that cannot be decoded, such as one in an
// older format, is replaced with a warning.
func loadCoverage(path, romHash string, stderr io.Writer) (*coverage.Index, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return coverage.NewIndex(romHash), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open coverage.json: %w", err)
	}
	defer f.Close()
	idx, err := coverage.Decode(f)
	if err != nil {
		fmt.Fprintf(stderr, "warning: replacing %s: %v\n", path, err)
		return coverage.NewIndex(romHash), nil
	}
	return idx, nil
}

func writeCoverage(path string, idx *coverage.Index) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create coverage.json: %w", err)
	}
	if err := idx.Encode(f); err != nil {
		f.Close()
		return fmt.Errorf("write coverage.json: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write coverage.json: %w", err)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
