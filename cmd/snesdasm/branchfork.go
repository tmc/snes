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

type BranchForkCaseSpec struct {
	CaseID              string `json:"case_id"`
	InputVal            uint8  `json:"input_val"`
	Kind                string `json:"kind"` // "recorded" | "prediction"
	ExpectedSuccessorPC string `json:"expected_successor_pc"`
	WantNextPC          uint32 `json:"want_next_pc"`
}

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
	Discrepancy         string          `json:"discrepancy,omitempty"`
	Verified            bool            `json:"verified"`
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
	BaselineRawVerified bool                   `json:"baseline_raw_verified"`
	DualBackendVerified bool                   `json:"dual_backend_verified"`
	ZeroWritesVerified  bool                   `json:"zero_writes_verified"`
	Results             []BranchForkCaseResult `json:"results"`
}

func compareNoncycleState(raw trace.Registers, emu decomp.CPUState) (bool, string) {
	if raw.A != emu.A {
		return false, fmt.Sprintf("A mismatch: raw=0x%04X emu=0x%04X", raw.A, emu.A)
	}
	if raw.X != emu.X {
		return false, fmt.Sprintf("X mismatch: raw=0x%04X emu=0x%04X", raw.X, emu.X)
	}
	if raw.Y != emu.Y {
		return false, fmt.Sprintf("Y mismatch: raw=0x%04X emu=0x%04X", raw.Y, emu.Y)
	}
	if raw.S != emu.S {
		return false, fmt.Sprintf("S mismatch: raw=0x%04X emu=0x%04X", raw.S, emu.S)
	}
	if raw.D != emu.D {
		return false, fmt.Sprintf("D mismatch: raw=0x%04X emu=0x%04X", raw.D, emu.D)
	}
	if raw.DB != emu.DB {
		return false, fmt.Sprintf("DB mismatch: raw=0x%02X emu=0x%02X", raw.DB, emu.DB)
	}
	if raw.PB != emu.PB {
		return false, fmt.Sprintf("PB mismatch: raw=0x%02X emu=0x%02X", raw.PB, emu.PB)
	}
	if raw.PC != emu.PC {
		return false, fmt.Sprintf("PC mismatch: raw=0x%04X emu=0x%04X", raw.PC, emu.PC)
	}
	if raw.P != emu.P {
		return false, fmt.Sprintf("P mismatch: raw=0x%02X emu=0x%02X", raw.P, emu.P)
	}
	if raw.E != emu.E {
		return false, fmt.Sprintf("E mismatch: raw=%v emu=%v", raw.E, emu.E)
	}
	return true, ""
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

	// Verify block instructions and context against pinned retirements and fetches
	for i, ie := range insnEvents {
		rawInsn := ie.Insn
		expectedAddr := (uint32(rawInsn.Entry.PB) << 16) | uint32(rawInsn.Entry.PC)
		if blockInstructions[i].Address != expectedAddr {
			return fmt.Errorf("instruction %d address mismatch: expected $%06X, got $%06X", i, expectedAddr, blockInstructions[i].Address)
		}
		if blockInstructions[i].Opcode != rawInsn.Fetches[0].Value {
			return fmt.Errorf("instruction %d opcode mismatch: expected 0x%02X, got 0x%02X", i, rawInsn.Fetches[0].Value, blockInstructions[i].Opcode)
		}
		var hexBytes string
		for _, f := range rawInsn.Fetches {
			hexBytes += fmt.Sprintf("%02x", f.Value)
		}
		if blockInstructions[i].Bytes != hexBytes {
			return fmt.Errorf("instruction %d bytes mismatch: expected %s, got %s", i, hexBytes, blockInstructions[i].Bytes)
		}
		rawE := "clear"
		if rawInsn.Entry.E {
			rawE = "set"
		}
		rawM := "clear"
		if rawInsn.Entry.P&0x20 != 0 {
			rawM = "set"
		}
		rawX := "clear"
		if rawInsn.Entry.P&0x10 != 0 {
			rawX = "set"
		}
		rawC := "clear"
		if rawInsn.Entry.P&0x01 != 0 {
			rawC = "set"
		}
		if blockInstructions[i].Context.E != rawE || blockInstructions[i].Context.M != rawM ||
			blockInstructions[i].Context.X != rawX || blockInstructions[i].Context.C != rawC {
			return fmt.Errorf("instruction %d context mismatch: expected E=%s M=%s X=%s C=%s, got %v",
				i, rawE, rawM, rawX, rawC, blockInstructions[i].Context)
		}
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
	testSpecs := []BranchForkCaseSpec{
		{CaseID: "baseline_3", InputVal: 3, Kind: "recorded", ExpectedSuccessorPC: "$0CC133", WantNextPC: 0x0CC133},
		{CaseID: "prediction_7", InputVal: 7, Kind: "prediction", ExpectedSuccessorPC: "$0CC133", WantNextPC: 0x0CC133},
		{CaseID: "prediction_8", InputVal: 8, Kind: "prediction", ExpectedSuccessorPC: "$0CC126", WantNextPC: 0x0CC126},
		{CaseID: "prediction_9", InputVal: 9, Kind: "prediction", ExpectedSuccessorPC: "$0CC126", WantNextPC: 0x0CC126},
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
			CaseID:  spec.CaseID,
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x000011, Value: spec.InputVal},
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
	allExpectedMatch := true
	allNoRefusal := true
	allZeroWrites := true
	baselineRawVerified := true

	// Also record per-step results for the timeline
	var emuStepsByCase [][]decomp.StepResult

	for i, spec := range testSpecs {
		mem := map[uint32]uint8{
			0x000011: spec.InputVal,
		}
		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			return fmt.Errorf("emulator run %s: %w", spec.CaseID, err)
		}
		emuStepsByCase = append(emuStepsByCase, emuSteps)

		cRes := cResults[i]

		matched, disc := decomp.CompareExecResults(emuRes, cRes)
		expectedPCMatched := (emuRes.NextPC == spec.WantNextPC) && (cRes.NextPC == spec.WantNextPC)
		noRefusal := !emuRes.MissingRead && !cRes.MissingRead &&
			!emuRes.MMIOAccess && !cRes.MMIOAccess &&
			!emuRes.WriteOverflow && !cRes.WriteOverflow
		zeroWrites := emuRes.TotalWrites == 0 && cRes.TotalWrites == 0 &&
			len(emuRes.Writes) == 0 && len(cRes.Writes) == 0

		caseVerified := matched && expectedPCMatched && noRefusal && zeroWrites
		if !matched {
			allDualMatch = false
		}
		if !expectedPCMatched {
			allExpectedMatch = false
		}
		if !noRefusal {
			allNoRefusal = false
		}
		if !zeroWrites {
			allZeroWrites = false
		}

		caseResults = append(caseResults, BranchForkCaseResult{
			CaseID:              spec.CaseID,
			InputWRAM11:         spec.InputVal,
			Kind:                spec.Kind,
			ExpectedSuccessorPC: spec.ExpectedSuccessorPC,
			EmuSuccessorPC:      fmt.Sprintf("$%06X", emuRes.NextPC),
			CSuccessorPC:        fmt.Sprintf("$%06X", cRes.NextPC),
			EmuMatchesC:         matched,
			MatchesExpected:     expectedPCMatched,
			EmuWrites:           len(emuRes.Writes),
			CWrites:             len(cRes.Writes),
			Discrepancy:         disc,
			Verified:            caseVerified,
			EmuState:            emuRes.State,
			CState:              cRes.State,
		})
	}

	// Compare baseline emulator execution against raw recorded retirement states for all 3 instructions
	baseEmuSteps := emuStepsByCase[0]
	for sIdx := 0; sIdx < 3; sIdx++ {
		rawInsn := insnEvents[sIdx].Insn
		entryMatch, entryDisc := compareNoncycleState(rawInsn.Entry, baseEmuSteps[sIdx].EntryState)
		if !entryMatch {
			baselineRawVerified = false
			fmt.Fprintf(stderr, "baseline step %d entry raw mismatch: %s\n", sIdx, entryDisc)
		}
		exitMatch, exitDisc := compareNoncycleState(rawInsn.Exit, baseEmuSteps[sIdx].ExitState)
		if !exitMatch {
			baselineRawVerified = false
			fmt.Fprintf(stderr, "baseline step %d exit raw mismatch: %s\n", sIdx, exitDisc)
		}
	}

	allVerified := allDualMatch && allExpectedMatch && allNoRefusal && allZeroWrites && baselineRawVerified

	// Build three-row timeline with actual raw recorded retirement states
	stepMnemonics := []string{"LDA $11", "CMP #$08", "BCC $C133"}
	stepAddrs := []string{"$0CC120", "$0CC122", "$0CC124"}

	for sIdx := 0; sIdx < 3; sIdx++ {
		rawInsn := insnEvents[sIdx].Insn
		recStep := StepRecord{
			InputVal: 3,
			EntryA:   fmt.Sprintf("$%04X", rawInsn.Entry.A),
			EntryP:   fmt.Sprintf("$%02X", rawInsn.Entry.P),
			ExitA:    fmt.Sprintf("$%04X", rawInsn.Exit.A),
			ExitP:    fmt.Sprintf("$%02X", rawInsn.Exit.P),
			ExitPC:   fmt.Sprintf("$%04X", rawInsn.Exit.PC),
			State: decomp.CPUState{
				A:  rawInsn.Exit.A,
				X:  rawInsn.Exit.X,
				Y:  rawInsn.Exit.Y,
				S:  rawInsn.Exit.S,
				D:  rawInsn.Exit.D,
				DB: rawInsn.Exit.DB,
				PB: rawInsn.Exit.PB,
				PC: rawInsn.Exit.PC,
				P:  rawInsn.Exit.P,
				E:  rawInsn.Exit.E,
			},
		}

		var predSteps []PredictRecord
		for cIdx := 1; cIdx < 4; cIdx++ {
			pStep := emuStepsByCase[cIdx][sIdx]
			predSteps = append(predSteps, PredictRecord{
				InputVal: testSpecs[cIdx].InputVal,
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

	statusStr := "success"
	if !allVerified {
		statusStr = "verification_failure"
	}

	// receipt.json
	receipt := BranchForkReceipt{
		Status:              statusStr,
		StreamSHA256:        streamSHA,
		ROMSHA256:           romSHA,
		BlockAddress:        "$0CC120",
		DispatchSeq:         8486,
		TargetSeq:           8487,
		PhysicalReadAddress: "$000011",
		RecordedReadValue:   3,
		BaselineRawVerified: baselineRawVerified,
		DualBackendVerified: allDualMatch,
		ZeroWritesVerified:  allZeroWrites,
		Results:             caseResults,
	}
	if err := writeJSON(filepath.Join(*outDir, "receipt.json"), receipt); err != nil {
		return err
	}

	if !allVerified {
		return fmt.Errorf("branch-fork verification failed: dual match=%v, expected match=%v, no refusal=%v, zero writes=%v, baseline raw=%v",
			allDualMatch, allExpectedMatch, allNoRefusal, allZeroWrites, baselineRawVerified)
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
