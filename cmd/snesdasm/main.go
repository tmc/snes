// Command snesdasm reconstructs reassemblable assembly projects from SNES ROMs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/asmexport"
	"github.com/tmc/snes/internal/recovery/verify"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "snesdasm: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		romPath           = fs.String("rom", "", "path to SNES ROM file")
		outDir            = fs.String("out", "", "target output directory")
		allowCopierHeader = fs.Bool("allow-copier-header", false, "allow stripping 512-byte copier header")
		projectName       = fs.String("project-name", "recovery", "project name for Futaba manifest")
		verifyRebuilt     = fs.String("verify-rebuilt", "", "path to rebuilt ROM to verify against normalized input")
		overwrite         = fs.Bool("overwrite", false, "overwrite existing export directory")
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

	// 1. Admit ROM.
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

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// 2. Initialize and write recovery.json.
	doc := recovery.NewDocument(admitted.Identity)
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

	// 3. Export assembly project.
	exportDir := filepath.Join(*outDir, "export")
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

	// 4. Optional verification.
	if *verifyRebuilt != "" {
		receipt, err := verify.Verify(context.Background(), *verifyRebuilt, admitted.NormalizedROM, verify.Config{})
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}

		verDir := filepath.Join(*outDir, "verification")
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
