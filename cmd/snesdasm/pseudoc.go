package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
)

// PseudocOutput represents the JSON response for the pseudoc command.
type PseudocOutput struct {
	BlockID          string                    `json:"block_id"`
	StartAddress     uint32                    `json:"start_address"`
	EndAddress       uint32                    `json:"end_address"`
	EntryContext     any                       `json:"entry_context"`
	PseudoC          string                    `json:"pseudoc,omitempty"`
	CompilableC      string                    `json:"compilable_c,omitempty"`
	SourceMap        []decomp.SourceMapEntry   `json:"source_map,omitempty"`
	LoweredCount     int                       `json:"lowered_count"`
	TotalCount       int                       `json:"total_count"`
	UnsupportedCount int                       `json:"unsupported_count"`
	Assumptions      []string                  `json:"assumptions,omitempty"`
	Validation       *decomp.ComparisonReceipt `json:"validation,omitempty"`
	ReplayCases      []decomp.ReplayCase       `json:"replay_cases,omitempty"`
	ReplayReceipt    *decomp.ReplayReceipt     `json:"replay_receipt,omitempty"`
}

func runPseudoc(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm pseudoc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm pseudoc -project dir [flags]",
			"Generate machine-semantic pseudo-C and compilable C for a recovered basic block.",
			"snesdasm pseudoc -project game_dasm",
			"snesdasm pseudoc -project game_dasm -addr 0086DF",
			"snesdasm pseudoc -project game_dasm -addr 0086DF -compilable",
			"snesdasm pseudoc -project game_dasm -addr 0086DF -validate -format json",
			"snesdasm pseudoc -project game_dasm -addr 0086DF -replay -format json",
		)
	}

	var (
		projectDir = fs.String("project", "", "path to project directory (required)")
		addrStr    = fs.String("addr", "", "basic block start address (hex)")
		compilable = fs.Bool("compilable", false, "output compilable C code instead of pseudo-C")
		validate   = fs.Bool("validate", false, "run differential validation against emulator and output receipt")
		receipt    = fs.Bool("receipt", false, "display saved verification receipt for the target block if available")
		replay     = fs.Bool("replay", false, "run captured replay verification against observed trace execution")
		casesFlag  = fs.Bool("cases", false, "list stored captured replay cases for the block")
		format     = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help pseudoc' for usage")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	blocks := structure.ExtractBasicBlocks(doc)
	if len(blocks) == 0 {
		return fmt.Errorf("no basic blocks found in project %s", *projectDir)
	}

	var targetBlock *structure.BasicBlock
	if *addrStr != "" {
		addr, err := parseHex(*addrStr)
		if err != nil {
			return fmt.Errorf("invalid -addr %q: %w", *addrStr, err)
		}
		for _, b := range blocks {
			if b.StartAddress == addr || (addr >= b.StartAddress && addr < b.EndAddress) {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil {
			return fmt.Errorf("no basic block found covering address $%06X", addr)
		}
	} else {
		// Default to first block or block 0086DF if present
		for _, b := range blocks {
			if b.StartAddress == 0x0086DF {
				targetBlock = b
				break
			}
		}
		if targetBlock == nil {
			targetBlock = blocks[0]
		}
	}

	entryCtx := doc.Instructions[0].Context
	if len(targetBlock.Instructions) > 0 && targetBlock.Instructions[0].Context.M != "" {
		entryCtx = targetBlock.Instructions[0].Context
	}

	ir, err := decomp.LiftBlock(targetBlock, entryCtx)
	if err != nil {
		return fmt.Errorf("lift block: %w", err)
	}

	pseudoCText, sourceMap := decomp.GeneratePseudoC(ir)
	compilableCText, compErr := decomp.GenerateCompilableC(ir)
	if *compilable && compErr != nil {
		return fmt.Errorf("generate compilable C: %w", compErr)
	}

	var validationReceipt *decomp.ComparisonReceipt
	if *validate && compilableCText != "" {
		pb := uint8(targetBlock.StartAddress >> 16)
		pc := uint16(targetBlock.StartAddress & 0xFFFF)
		initState := decomp.CPUState{
			A:  0x0000,
			X:  0x0000,
			Y:  0x0000,
			S:  0x01FF,
			D:  0x0000,
			DB: pb,
			PB: pb,
			PC: pc,
			P:  0x00,
			E:  false,
		}
		if entryCtx.M == "set" || entryCtx.M == "1" {
			initState.P |= 0x20
		}
		if entryCtx.X == "set" || entryCtx.X == "1" {
			initState.P |= 0x10
		}
		if entryCtx.E == "set" || entryCtx.E == "1" {
			initState.E = true
		}
		if entryCtx.C == "set" || entryCtx.C == "1" {
			initState.P |= 0x01
		}
		rev := recovery.ComputeProjectRevision(*projectDir, doc)
		cfg := decomp.DefaultVerifyConfig()
		cfg.ROMSHA256 = doc.ROM.NormalizedSHA256
		cfg.ProjectRevision = rev
		mem := make(map[uint32]uint8)
		r := decomp.Compare(context.Background(), ir, "default_verification", initState, mem, cfg)
		validationReceipt = &r
		if err := decomp.SaveReceipt(decomp.ReceiptPath(*projectDir, targetBlock.ID), r); err != nil {
			return fmt.Errorf("save receipt: %w", err)
		}
	} else if *receipt {
		rev := recovery.ComputeProjectRevision(*projectDir, doc)
		if r, err := decomp.LoadReceipt(decomp.ReceiptPath(*projectDir, targetBlock.ID)); err == nil {
			decomp.ValidateReceiptFreshness(&r, ir, compilableCText, nil, doc.ROM.NormalizedSHA256, rev)
			validationReceipt = &r
		}
	}

	var replayCases []decomp.ReplayCase
	var replayReceipt *decomp.ReplayReceipt

	if *casesFlag {
		cs, _ := decomp.ListCases(*projectDir, targetBlock.ID)
		replayCases = cs
	}

	if *replay {
		cs, _ := decomp.ListCases(*projectDir, targetBlock.ID)
		if len(cs) == 0 {
			return fmt.Errorf("no captured replay cases found for block %s", targetBlock.ID)
		}
		targetCase := cs[0]

		runner, err := decomp.NewCompiledRunner(context.Background(), ir)
		if err != nil {
			return fmt.Errorf("create compiled runner: %w", err)
		}
		defer runner.Close()

		rev := recovery.ComputeProjectRevision(*projectDir, doc)
		cfg := decomp.DefaultVerifyConfig()
		cfg.ROMSHA256 = doc.ROM.NormalizedSHA256
		cfg.ProjectRevision = rev

		r := decomp.ExecuteThreeWayReplay(context.Background(), ir, targetCase, runner, cfg)
		replayReceipt = &r
		if err := decomp.SaveReplayReceipt(decomp.ReplayReceiptPath(*projectDir, targetBlock.ID), r); err != nil {
			return fmt.Errorf("save replay receipt: %w", err)
		}
	}

	out := PseudocOutput{
		BlockID:          targetBlock.ID,
		StartAddress:     targetBlock.StartAddress,
		EndAddress:       targetBlock.EndAddress,
		EntryContext:     entryCtx,
		PseudoC:          pseudoCText,
		CompilableC:      compilableCText,
		SourceMap:        sourceMap,
		LoweredCount:     ir.LoweredCount,
		TotalCount:       ir.TotalCount,
		UnsupportedCount: ir.UnsupportedCount,
		Assumptions:      ir.Assumptions,
		Validation:       validationReceipt,
		ReplayCases:      replayCases,
		ReplayReceipt:    replayReceipt,
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if *compilable {
		fmt.Fprint(stdout, compilableCText)
		return nil
	}

	fmt.Fprint(stdout, pseudoCText)
	if *validate && validationReceipt != nil {
		fmt.Fprintf(stdout, "\n// Validation against emulator: matched=%v (NextPC: 0x%06X)\n",
			validationReceipt.Matched, validationReceipt.ActualC.NextPC)
	}
	if *replay && replayReceipt != nil {
		fmt.Fprintf(stdout, "\n// Captured Replay: matched=%v, eligible=%v (Observed: %v, Emu: %v, C: %v, NextPC: 0x%06X)\n",
			replayReceipt.Matched, replayReceipt.Eligible,
			replayReceipt.ObservedMatch, replayReceipt.EmulatorMatch, replayReceipt.CMatch,
			replayReceipt.CompiledC.NextPC)
	}
	return nil
}
