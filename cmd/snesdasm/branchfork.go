package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

type BranchForkCaseResult struct {
	CaseID              string          `json:"case_id"`
	InputWRAM11         uint8           `json:"input_wram_11"`
	Kind                string          `json:"kind"` // "recorded" | "prediction"
	ExpectedSuccessorPC string          `json:"expected_successor_pc"`
	EmuSuccessorPC      string          `json:"emu_successor_pc"`
	CSuccessorPC        string          `json:"c_successor_pc"`
	EmuMatchesC         bool            `json:"emu_matches_c"`
	MatchesExpected     bool            `json:"matches_expected"`
	EmuWrites           int             `json:"emu_writes"`
	CWrites             int             `json:"c_writes"`
	EmuState            decomp.CPUState `json:"emu_state"`
	CState              decomp.CPUState `json:"c_state"`
}

type TimelineStep struct {
	StepIndex   int             `json:"step_index"`
	Address     string          `json:"address"`
	Mnemonic    string          `json:"mnemonic"`
	Recorded    StepRecord      `json:"recorded"`
	Predictions []PredictRecord `json:"predictions"`
}

type StepRecord struct {
	InputVal uint8           `json:"input_val"`
	EntryA   string          `json:"entry_a"`
	EntryP   string          `json:"entry_p"`
	ExitA    string          `json:"exit_a"`
	ExitP    string          `json:"exit_p"`
	ExitPC   string          `json:"exit_pc"`
	State    decomp.CPUState `json:"state"`
}

type PredictRecord struct {
	InputVal uint8           `json:"input_val"`
	EntryA   string          `json:"entry_a"`
	EntryP   string          `json:"entry_p"`
	ExitA    string          `json:"exit_a"`
	ExitP    string          `json:"exit_p"`
	ExitPC   string          `json:"exit_pc"`
	State    decomp.CPUState `json:"state"`
}

type BranchForkReceipt struct {
	Status              string                 `json:"status"`
	StreamSHA256        string                 `json:"stream_sha256"`
	ROMSHA256           string                 `json:"rom_sha256"`
	BlockAddress        string                 `json:"block_address"`
	DispatchSeq         uint64                 `json:"dispatch_seq"`
	TargetSeq           uint64                 `json:"target_seq"`
	PhysicalReadAddress string                 `json:"physical_read_address"`
	RecordedReadValue   uint8                  `json:"recorded_read_value"`
	DualBackendVerified bool                   `json:"dual_backend_verified"`
	ZeroWritesVerified  bool                   `json:"zero_writes_verified"`
	Results             []BranchForkCaseResult `json:"results"`
}

func runBranchFork(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm branch-fork", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm branch-fork -rom rom.sfc -trace trace.jsonl -out dir [flags]",
			"Execute bounded three-instruction branch fork replay at $0CC120 across Go CPU and compiled C.",
			"snesdasm branch-fork -rom rom.sfc -trace trace.jsonl -out ./branch_fork_out",
			"snesdasm branch-fork -rom rom.sfc -trace trace.jsonl -out ./branch_fork_out -format json",
		)
	}

	var (
		romPath   = fs.String("rom", "", "path to admitted ROM file (required)")
		tracePath = fs.String("trace", "", "path to admitted trace JSONL file (required)")
		outDir    = fs.String("out", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-branch-fork-delivery", "output directory for artifacts")
		format    = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *romPath == "" || *tracePath == "" {
		return fmt.Errorf("-rom and -trace flags are required; run 'snesdasm help branch-fork' for usage")
	}

	ctx := context.Background()

	// 1. Validate ROM bytes and hash
	romBytes, err := os.ReadFile(*romPath)
	if err != nil {
		return fmt.Errorf("read ROM %s: %w", *romPath, err)
	}
	romSHA := fmt.Sprintf("%x", sha256.Sum256(romBytes))
	if romSHA != analysis.AdmittedROMSHA256 {
		return fmt.Errorf("ROM SHA-256 %s does not match admitted %s", romSHA, analysis.AdmittedROMSHA256)
	}

	// 2. Validate trace bytes and hash
	traceBytes, err := os.ReadFile(*tracePath)
	if err != nil {
		return fmt.Errorf("read trace %s: %w", *tracePath, err)
	}
	streamSHA := fmt.Sprintf("%x", sha256.Sum256(traceBytes))
	if streamSHA != analysis.AdmittedStreamSHA256 {
		return fmt.Errorf("stream SHA-256 %s does not match admitted %s", streamSHA, analysis.AdmittedStreamSHA256)
	}

	// 3. Scan for target retirements (29897, 29900, 29903) and read event 29896
	dec := json.NewDecoder(bytes.NewReader(traceBytes))
	var (
		ev29896 trace.Event
		ev29897 trace.Event
		ev29900 trace.Event
		ev29903 trace.Event
	)
	for dec.More() {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("decoding trace: %w", err)
		}
		switch e.ID {
		case 29896:
			ev29896 = e
		case 29897:
			ev29897 = e
		case 29900:
			ev29900 = e
		case 29903:
			ev29903 = e
		}
	}

	if ev29896.ID != 29896 || ev29897.ID != 29897 || ev29900.ID != 29900 || ev29903.ID != 29903 {
		return errors.New("missing one or more required events (29896, 29897, 29900, 29903)")
	}
	if ev29897.Insn == nil || ev29900.Insn == nil || ev29903.Insn == nil {
		return errors.New("instruction traces missing from retirement events")
	}

	// Verify physical WRAM read at 0x000011
	if ev29896.Addr != 0x000011 || ev29896.Value != 3 {
		return fmt.Errorf("read event 29896 expected addr $000011 val 3, got addr $%06X val %d", ev29896.Addr, ev29896.Value)
	}

	// Verify sequential retirements: Seq 8487..8489
	if ev29897.Insn.Seq != 8487 || ev29900.Insn.Seq != 8488 || ev29903.Insn.Seq != 8489 {
		return fmt.Errorf("unexpected insn seqs: %d, %d, %d (require 8487, 8488, 8489)",
			ev29897.Insn.Seq, ev29900.Insn.Seq, ev29903.Insn.Seq)
	}

	// Join fetches to physical ROM
	insnEvents := []trace.Event{ev29897, ev29900, ev29903}
	for _, ie := range insnEvents {
		for _, f := range ie.Insn.Fetches {
			off, ok := analysis.LoROMToOffset(f.Addr, len(romBytes))
			if !ok || int(off) >= len(romBytes) || romBytes[off] != f.Value {
				return fmt.Errorf("fetch $%06X val 0x%02X does not match ROM", f.Addr, f.Value)
			}
		}
	}

	ctxClear := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}

	blockInstructions := []recovery.Instruction{
		{
			ID:           "insn-0cc120",
			Architecture: "wdc65816",
			Address:      0x0CC120,
			Offset:       0x064120,
			Bytes:        "a511",
			Opcode:       0xA5,
			Mnemonic:     "lda",
			Mode:         "direct_page",
			Context:      ctxClear,
		},
		{
			ID:           "insn-0cc122",
			Architecture: "wdc65816",
			Address:      0x0CC122,
			Offset:       0x064122,
			Bytes:        "c908",
			Opcode:       0xC9,
			Mnemonic:     "cmp",
			Mode:         "immediate_memory",
			Context:      ctxClear,
		},
		{
			ID:           "insn-0cc124",
			Architecture: "wdc65816",
			Address:      0x0CC124,
			Offset:       0x064124,
			Bytes:        "900d",
			Opcode:       0x90,
			Mnemonic:     "bcc",
			Mode:         "program_counter_relative",
			Context:      ctxClear,
		},
	}

	block := &structure.BasicBlock{
		ID:           "block-0cc120-branchfork",
		StartAddress: 0x0CC120,
		EndAddress:   0x0CC126,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC133, 0x0CC126},
	}

	// 4. Lift block to IR and generate compilable C
	ir, err := decomp.LiftBlock(block, ctxClear)
	if err != nil {
		return fmt.Errorf("lift block: %w", err)
	}

	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		return fmt.Errorf("generate compilable C: %w", err)
	}

	// Initial CPU state from event 29897
	rEntry := ev29897.Insn.Entry
	initState := decomp.CPUState{
		A:  rEntry.A,
		X:  rEntry.X,
		Y:  rEntry.Y,
		S:  rEntry.S,
		D:  rEntry.D,
		DB: rEntry.DB,
		PB: rEntry.PB,
		PC: rEntry.PC,
		P:  rEntry.P,
		E:  rEntry.E,
	}

	// 5. Setup test cases: baseline 3 (recorded), predictions 7, 8, 9
	testSpecs := []struct {
		caseID     string
		inputVal   uint8
		kind       string
		wantNextPC uint32
	}{
		{caseID: "baseline_3", inputVal: 3, kind: "recorded", wantNextPC: 0x0CC133},
		{caseID: "prediction_7", inputVal: 7, kind: "prediction", wantNextPC: 0x0CC133},
		{caseID: "prediction_8", inputVal: 8, kind: "prediction", wantNextPC: 0x0CC126},
		{caseID: "prediction_9", inputVal: 9, kind: "prediction", wantNextPC: 0x0CC126},
	}

	// Build C runner batch
	compiledRunner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		return fmt.Errorf("new compiled runner: %w", err)
	}
	defer compiledRunner.Close()

	var cBatchCases []decomp.ReplayCaseInput
	for _, spec := range testSpecs {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  spec.caseID,
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x000011, Value: spec.inputVal},
			},
		})
	}

	cResults, err := compiledRunner.RunBatch(ctx, cBatchCases)
	if err != nil {
		return fmt.Errorf("compiled runner batch: %w", err)
	}

	var caseResults []BranchForkCaseResult
	var timelineSteps []TimelineStep
	allDualMatch := true
	allZeroWrites := true

	// Also record per-step results for the timeline
	var emuStepsByCase [][]decomp.StepResult

	for i, spec := range testSpecs {
		mem := map[uint32]uint8{
			0x000011: spec.inputVal,
		}
		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			return fmt.Errorf("emulator run %s: %w", spec.caseID, err)
		}
		emuStepsByCase = append(emuStepsByCase, emuSteps)

		cRes := cResults[i]

		emuMatchesC := (emuRes.NextPC == cRes.NextPC) &&
			(emuRes.State.A == cRes.State.A) &&
			(emuRes.State.P == cRes.State.P) &&
			(emuRes.State.X == cRes.State.X) &&
			(emuRes.State.Y == cRes.State.Y) &&
			(emuRes.State.S == cRes.State.S) &&
			(emuRes.State.PB == cRes.State.PB) &&
			(emuRes.State.DB == cRes.State.DB) &&
			(emuRes.State.D == cRes.State.D)

		matchesExpected := (emuRes.NextPC == spec.wantNextPC) && (cRes.NextPC == spec.wantNextPC)
		if !emuMatchesC {
			allDualMatch = false
		}
		if len(emuRes.Writes) != 0 || len(cRes.Writes) != 0 {
			allZeroWrites = false
		}

		caseResults = append(caseResults, BranchForkCaseResult{
			CaseID:              spec.caseID,
			InputWRAM11:         spec.inputVal,
			Kind:                spec.kind,
			ExpectedSuccessorPC: fmt.Sprintf("$%06X", spec.wantNextPC),
			EmuSuccessorPC:      fmt.Sprintf("$%06X", emuRes.NextPC),
			CSuccessorPC:        fmt.Sprintf("$%06X", cRes.NextPC),
			EmuMatchesC:         emuMatchesC,
			MatchesExpected:     matchesExpected,
			EmuWrites:           len(emuRes.Writes),
			CWrites:             len(cRes.Writes),
			EmuState:            emuRes.State,
			CState:              cRes.State,
		})
	}

	// Build three-row timeline
	stepMnemonics := []string{"LDA $11", "CMP #$08", "BCC $C133"}
	stepAddrs := []string{"$0CC120", "$0CC122", "$0CC124"}

	for sIdx := 0; sIdx < 3; sIdx++ {
		baseStep := emuStepsByCase[0][sIdx]
		recStep := StepRecord{
			InputVal: 3,
			EntryA:   fmt.Sprintf("$%04X", baseStep.EntryState.A),
			EntryP:   fmt.Sprintf("$%02X", baseStep.EntryState.P),
			ExitA:    fmt.Sprintf("$%04X", baseStep.ExitState.A),
			ExitP:    fmt.Sprintf("$%02X", baseStep.ExitState.P),
			ExitPC:   fmt.Sprintf("$%04X", baseStep.ExitState.PC),
			State:    baseStep.ExitState,
		}

		var predSteps []PredictRecord
		for cIdx := 1; cIdx < 4; cIdx++ {
			pStep := emuStepsByCase[cIdx][sIdx]
			predSteps = append(predSteps, PredictRecord{
				InputVal: testSpecs[cIdx].inputVal,
				EntryA:   fmt.Sprintf("$%04X", pStep.EntryState.A),
				EntryP:   fmt.Sprintf("$%02X", pStep.EntryState.P),
				ExitA:    fmt.Sprintf("$%04X", pStep.ExitState.A),
				ExitP:    fmt.Sprintf("$%02X", pStep.ExitState.P),
				ExitPC:   fmt.Sprintf("$%04X", pStep.ExitState.PC),
				State:    pStep.ExitState,
			})
		}

		timelineSteps = append(timelineSteps, TimelineStep{
			StepIndex:   sIdx + 1,
			Address:     stepAddrs[sIdx],
			Mnemonic:    stepMnemonics[sIdx],
			Recorded:    recStep,
			Predictions: predSteps,
		})
	}

	// 6. Write output artifacts
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create out dir %s: %w", *outDir, err)
	}

	// case.json
	caseData := map[string]any{
		"case_id":               "branch-fork-0cc120",
		"stream_sha256":         streamSHA,
		"rom_sha256":            romSHA,
		"block_address":         "$0CC120",
		"dispatch_event":        29893,
		"target_event":          29897,
		"physical_read_address": "$000011",
		"recorded_read_value":   3,
		"instructions":          blockInstructions,
		"initial_cpu_state":     initState,
		"cases":                 testSpecs,
	}
	if err := writeJSON(filepath.Join(*outDir, "case.json"), caseData); err != nil {
		return err
	}

	// generated.c
	if err := os.WriteFile(filepath.Join(*outDir, "generated.c"), []byte(cCode), 0644); err != nil {
		return fmt.Errorf("write generated.c: %w", err)
	}

	// timeline.json
	if err := writeJSON(filepath.Join(*outDir, "timeline.json"), timelineSteps); err != nil {
		return err
	}

	// receipt.json
	receipt := BranchForkReceipt{
		Status:              "success",
		StreamSHA256:        streamSHA,
		ROMSHA256:           romSHA,
		BlockAddress:        "$0CC120",
		DispatchSeq:         8486,
		TargetSeq:           8487,
		PhysicalReadAddress: "$000011",
		RecordedReadValue:   3,
		DualBackendVerified: allDualMatch,
		ZeroWritesVerified:  allZeroWrites,
		Results:             caseResults,
	}
	if err := writeJSON(filepath.Join(*outDir, "receipt.json"), receipt); err != nil {
		return err
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(receipt)
	}

	fmt.Fprintf(stdout, "Branch Fork Replay Verification ($0CC120):\n")
	fmt.Fprintf(stdout, "  Stream SHA-256:         %s\n", streamSHA)
	fmt.Fprintf(stdout, "  ROM SHA-256:            %s\n", romSHA)
	fmt.Fprintf(stdout, "  Dual-Backend Verified:  %v (Go CPU vs Compiled C)\n", allDualMatch)
	fmt.Fprintf(stdout, "  Zero Writes Verified:   %v\n", allZeroWrites)
	fmt.Fprintf(stdout, "  Results:\n")
	for _, res := range caseResults {
		fmt.Fprintf(stdout, "    [%s] input $0011=%d -> Successor: %s (Emu=%s C=%s match=%v expected=%v)\n",
			res.Kind, res.InputWRAM11, res.ExpectedSuccessorPC, res.EmuSuccessorPC, res.CSuccessorPC, res.EmuMatchesC, res.MatchesExpected)
	}
	fmt.Fprintf(stdout, "  Artifacts saved to:     %s\n", *outDir)

	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json %s: %w", path, err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("write json %s: %w", path, err)
	}
	return nil
}
