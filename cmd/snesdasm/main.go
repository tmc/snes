// Command snesdasm reconstructs reassemblable assembly projects from SNES ROMs.
package main

import (
	"context"
	"encoding/json"
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

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "coverage":
			return runCoverage(args[1:], stdout, stderr)
		case "routines":
			return runRoutines(args[1:], stdout, stderr)
		case "disasm":
			return runDisasm(args[1:], stdout, stderr)
		case "refs":
			return runRefs(args[1:], stdout, stderr)
		case "graph":
			return runGraph(args[1:], stdout, stderr)
		case "serve":
			return runServe(args[1:], stdout, stderr)
		}
	}
	return runRecovery(args, stdout, stderr)
}

func runRecovery(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm", flag.ContinueOnError)
	fs.SetOutput(stderr)
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
		tracePath         = fs.String("trace", "", "path to runtime observation stream (.jsonl)")
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

	// 3. Perform static analysis starting from reset vector.
	doc := recovery.NewDocument(admitted.Identity)
	if admitted.Identity.Mapper == "lorom" {
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

		traceRes, err := traceimport.Parse(traceFile, receiptReader, admitted.NormalizedROM, admitted.Identity.NormalizedSHA256)
		if err != nil {
			return fmt.Errorf("import trace %q: %w", *tracePath, err)
		}

		mr, err := traceimport.Merge(doc, traceRes)
		if err != nil {
			return fmt.Errorf("merge trace %q: %w", *tracePath, err)
		}

		completenessStr := "complete"
		if !traceRes.IsComplete {
			completenessStr = "incomplete/limited"
		}
		fmt.Fprintf(stdout, "Imported trace (%s): %d records, %d new instructions (%d existing), %d edges\n",
			completenessStr, traceRes.TotalRecords, mr.InstructionsAdded, mr.InstructionsExisting, mr.EdgesAdded)

		// Build and persist coverage index
		covPath := filepath.Join(*outDir, "coverage.json")
		var covIdx *coverage.Index
		if cf, err := os.Open(covPath); err == nil {
			covIdx, _ = coverage.Decode(cf)
			cf.Close()
		}
		if covIdx == nil {
			covIdx = coverage.NewIndex(admitted.Identity.NormalizedSHA256)
		}
		receiptOutcome := "complete"
		if traceRes.Receipt != nil {
			receiptOutcome = traceRes.Receipt.Outcome
		}
		covIdx.AddRun(coverage.RunInfo{
			ID:         traceRes.StreamSHA256,
			ROM_SHA256: admitted.Identity.NormalizedSHA256,
			EngineRev:  traceRes.RunMetadata.EngineRevision,
			Outcome:    receiptOutcome,
			EventCount: uint64(len(traceRes.Events)),
			StreamSHA:  traceRes.StreamSHA256,
			IsComplete: traceRes.IsComplete,
		})
		covIdx.AddEvents(traceRes.Events)
		if cf, err := os.Create(covPath); err == nil {
			_ = covIdx.Encode(cf)
			cf.Close()
		}
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
