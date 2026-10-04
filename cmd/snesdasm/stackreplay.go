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
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

type StackCaseSpec struct {
	CaseID              string              `json:"case_id"`
	InputVal            uint8               `json:"input_val"`
	Kind                string              `json:"kind"` // "recorded" | "prediction"
	InitialMemory       []decomp.MemoryCell `json:"initial_memory"`
	StackMemoryNote     string              `json:"stack_memory_note"`
	ExpectedOutput      uint8               `json:"expected_output"`
	ExpectedSuccessorPC string              `json:"expected_successor_pc"`
	ExpectedP           string              `json:"expected_p"`
	WantNextPC          uint32              `json:"want_next_pc"`
	WantP               uint8               `json:"want_p"`
}

type StackCaseResult struct {
	CaseID              string               `json:"case_id"`
	InputVal            uint8                `json:"input_val"`
	OutputVal           uint8                `json:"output_val"`
	Kind                string               `json:"kind"` // "recorded" | "prediction"
	ExpectedSuccessorPC string               `json:"expected_successor_pc"`
	EmuSuccessorPC      string               `json:"emu_successor_pc"`
	CSuccessorPC        string               `json:"c_successor_pc"`
	EmuP                string               `json:"emu_p"`
	CP                  string               `json:"c_p"`
	EmuMatchesC         bool                 `json:"emu_matches_c"`
	MatchesExpected     bool                 `json:"matches_expected"`
	EmuWrites           int                  `json:"emu_writes"`
	CWrites             int                  `json:"c_writes"`
	Writes              []decomp.MemoryWrite `json:"writes"`
	TotalWrites         uint32               `json:"total_writes"`
	WriteOverflow       bool                 `json:"write_overflow"`
	MissingRead         bool                 `json:"missing_read"`
	MissingAddr         uint32               `json:"missing_addr,omitempty"`
	MMIOAccess          bool                 `json:"mmio_access"`
	MMIOAddr            uint32               `json:"mmio_addr,omitempty"`
	Discrepancy         string               `json:"discrepancy,omitempty"`
	Verified            bool                 `json:"verified"`
	EmuState            decomp.CPUState      `json:"emu_state"`
	CState              decomp.CPUState      `json:"c_state"`
}

type StackTimelineStep struct {
	StepIndex   int                  `json:"step_index"`
	Address     string               `json:"address"`
	ROMOffset   string               `json:"rom_offset"`
	Mnemonic    string               `json:"mnemonic"`
	Recorded    StackStepRecord      `json:"recorded"`
	Predictions []StackPredictRecord `json:"predictions"`
}

type StackStepRecord struct {
	InputVal uint8                `json:"input_val"`
	EntryA   string               `json:"entry_a"`
	EntryS   string               `json:"entry_s"`
	EntryDB  string               `json:"entry_db"`
	EntryP   string               `json:"entry_p"`
	ExitA    string               `json:"exit_a"`
	ExitS    string               `json:"exit_s"`
	ExitDB   string               `json:"exit_db"`
	ExitP    string               `json:"exit_p"`
	ExitPC   string               `json:"exit_pc"`
	// Reads holds Go emulator data memory reads observed during this step (derived from StepResult.Reads).
	Reads    []decomp.MemoryWrite `json:"reads,omitempty"`
	Writes   []decomp.MemoryWrite `json:"writes,omitempty"`
	State    decomp.CPUState      `json:"state"`
	CState   decomp.CPUState      `json:"c_state"`
}

type StackPredictRecord struct {
	InputVal uint8                `json:"input_val"`
	EntryA   string               `json:"entry_a"`
	EntryS   string               `json:"entry_s"`
	EntryDB  string               `json:"entry_db"`
	EntryP   string               `json:"entry_p"`
	ExitA    string               `json:"exit_a"`
	ExitS    string               `json:"exit_s"`
	ExitDB   string               `json:"exit_db"`
	ExitP    string               `json:"exit_p"`
	ExitPC   string               `json:"exit_pc"`
	// Reads holds Go emulator data memory reads observed during this step (derived from StepResult.Reads).
	Reads    []decomp.MemoryWrite `json:"reads,omitempty"`
	Writes   []decomp.MemoryWrite `json:"writes,omitempty"`
	State    decomp.CPUState      `json:"state"`
	CState   decomp.CPUState      `json:"c_state"`
}

type StackReplayReceipt struct {
	Status               string            `json:"status"`
	StreamSHA256         string            `json:"stream_sha256"`
	ROMSHA256            string            `json:"rom_sha256"`
	BlockAddress         string            `json:"block_address"`
	DispatchSeq          uint64            `json:"dispatch_seq"`
	TargetSeq            uint64            `json:"target_seq"`
	PhysicalReadAddress  string            `json:"physical_read_address"`
	PhysicalWriteAddress string            `json:"physical_write_address"`
	RecordedReadValue    uint8             `json:"recorded_read_value"`
	RecordedWriteValue   uint8             `json:"recorded_write_value"`
	BaselineRawVerified   bool              `json:"baseline_raw_verified"`
	DualBackendVerified   bool              `json:"dual_backend_verified"`
	ExpectedMatchVerified bool              `json:"expected_match_verified"`
	ThreeWritesVerified   bool              `json:"three_writes_verified"`
	Results               []StackCaseResult `json:"results"`
}

type StackBundleManifest struct {
	BundleID                string            `json:"bundle_id"`
	BlockAddress            string            `json:"block_address"`
	Qualification           string            `json:"qualification"`
	StreamSHA256            string            `json:"stream_sha256"`
	ROMSHA256               string            `json:"rom_sha256"`
	DocumentSHA256          string            `json:"document_sha256"`
	CanonicalInstructionIDs []string          `json:"canonical_instruction_ids"`
	RetirementEventIDs      []uint64          `json:"retirement_event_ids"`
	RetirementSeqs          []uint64          `json:"retirement_seqs"`
	OperandBusIDs           []uint64          `json:"operand_bus_ids,omitempty"`
	ArtifactDigests         map[string]string `json:"artifact_digests"`
}

func runStackReplay(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm stack-replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm stack-replay -rom rom.sfc -trace trace.jsonl -out dir [flags]",
			"Execute bounded 4-instruction stack-bank replay at $0CC404 across Go CPU and compiled C.",
			"snesdasm stack-replay -rom rom.sfc -trace trace.jsonl -out ./stack_replay_out",
			"snesdasm stack-replay -rom rom.sfc -trace trace.jsonl -out ./stack_replay_out -format json",
		)
	}

	var (
		romPath   = fs.String("rom", "", "path to admitted ROM file (required)")
		tracePath = fs.String("trace", "", "path to admitted trace JSONL file (required)")
		outDir    = fs.String("out", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-stack-replay-delivery", "output directory for artifacts")
		format    = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *romPath == "" || *tracePath == "" {
		return fmt.Errorf("-rom and -trace flags are required; run 'snesdasm help stack-replay' for usage")
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

	// 3. Scan for target retirements (30003, 30006, 30009, 30015) and bus events (30000, 30002, 30005, 30008, 30013, 30014)
	dec := json.NewDecoder(bytes.NewReader(traceBytes))
	var (
		ev30000 trace.Event
		ev30002 trace.Event
		ev30003 trace.Event
		ev30005 trace.Event
		ev30006 trace.Event
		ev30008 trace.Event
		ev30009 trace.Event
		ev30013 trace.Event
		ev30014 trace.Event
		ev30015 trace.Event
	)
	for dec.More() {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("decoding trace: %w", err)
		}
		switch e.ID {
		case 30000:
			ev30000 = e
		case 30002:
			ev30002 = e
		case 30003:
			ev30003 = e
		case 30005:
			ev30005 = e
		case 30006:
			ev30006 = e
		case 30008:
			ev30008 = e
		case 30009:
			ev30009 = e
		case 30013:
			ev30013 = e
		case 30014:
			ev30014 = e
		case 30015:
			ev30015 = e
		}
	}

	if ev30003.ID != 30003 || ev30006.ID != 30006 || ev30009.ID != 30009 || ev30015.ID != 30015 {
		return errors.New("missing one or more required retirement events (30003, 30006, 30009, 30015)")
	}
	if ev30002.ID != 30002 || ev30005.ID != 30005 || ev30008.ID != 30008 || ev30013.ID != 30013 || ev30014.ID != 30014 {
		return errors.New("missing one or more required operand bus events (30002, 30005, 30008, 30013, 30014)")
	}

	// Verify sequential retirements: Seq 8512..8515
	if ev30003.Insn.Seq != 8512 || ev30006.Insn.Seq != 8513 || ev30009.Insn.Seq != 8514 || ev30015.Insn.Seq != 8515 {
		return fmt.Errorf("unexpected insn seqs: %d, %d, %d, %d (require 8512..8515)",
			ev30003.Insn.Seq, ev30006.Insn.Seq, ev30009.Insn.Seq, ev30015.Insn.Seq)
	}

	// Verify preceding retirement 30000: Seq 8511, successor_pc bank 12, addr 50180 ($0CC404)
	if ev30000.Insn.Seq != 8511 || ev30000.Insn.SuccessorPC.Bank != 12 || ev30000.Insn.SuccessorPC.Addr != 50180 {
		return fmt.Errorf("preceding retirement 30000 mismatch: seq=%d target=%02X:%04X",
			ev30000.Insn.Seq, ev30000.Insn.SuccessorPC.Bank, ev30000.Insn.SuccessorPC.Addr)
	}

	// Verify ordered operand accesses
	if ev30002.Addr != 508 || ev30002.After == nil || *ev30002.After != 0 {
		var afterVal uint64
		if ev30002.After != nil {
			afterVal = *ev30002.After
		}
		return fmt.Errorf("event 30002 expected write 01FC=00, got addr=%04X after=%02X", ev30002.Addr, afterVal)
	}
	if ev30005.Addr != 507 || ev30005.Value != 12 {
		return fmt.Errorf("event 30005 expected write 01FB=0C, got addr=%04X val=%02X", ev30005.Addr, ev30005.Value)
	}
	if ev30008.Addr != 507 || ev30008.Value != 12 {
		return fmt.Errorf("event 30008 expected read 01FB=0C, got addr=%04X val=%02X", ev30008.Addr, ev30008.Value)
	}
	if ev30013.Addr != 7690 {
		return fmt.Errorf("event 30013 expected read 1E0A, got addr=%04X", ev30013.Addr)
	}
	if ev30014.Addr != 7690 {
		return fmt.Errorf("event 30014 expected write 1E0A, got addr=%04X", ev30014.Addr)
	}

	// Verify fetches match ROM
	insnEvents := []trace.Event{ev30003, ev30006, ev30009, ev30015}
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
			ID:           "insn-0cc404",
			Architecture: "wdc65816",
			Address:      0x0CC404,
			Offset:       0x064404,
			Bytes:        "8b",
			Opcode:       0x8B,
			Mnemonic:     "phb",
			Mode:         "implied",
			Context:      ctxClear,
		},
		{
			ID:           "insn-0cc405",
			Architecture: "wdc65816",
			Address:      0x0CC405,
			Offset:       0x064405,
			Bytes:        "4b",
			Opcode:       0x4B,
			Mnemonic:     "phk",
			Mode:         "implied",
			Context:      ctxClear,
		},
		{
			ID:           "insn-0cc406",
			Architecture: "wdc65816",
			Address:      0x0CC406,
			Offset:       0x064406,
			Bytes:        "ab",
			Opcode:       0xAB,
			Mnemonic:     "plb",
			Mode:         "implied",
			Context:      ctxClear,
		},
		{
			ID:           "insn-0cc407",
			Architecture: "wdc65816",
			Address:      0x0CC407,
			Offset:       0x064407,
			Bytes:        "ee0a1e",
			Opcode:       0xEE,
			Mnemonic:     "inc",
			Mode:         "absolute",
			Context:      ctxClear,
		},
	}

	block := &structure.BasicBlock{
		ID:           "block-0cc404-prefix-4",
		StartAddress: 0x0CC404,
		EndAddress:   0x0CC40A,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC40A},
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

	// Initial CPU state from event 30003
	rEntry := ev30003.Insn.Entry
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

	// Recorded raw values from capture
	rawReadVal := uint8(ev30013.Value)
	rawWriteVal := uint8(ev30014.Value)

	// 5. Setup test cases: baseline (recorded), prediction 127, prediction 255
	testSpecs := []StackCaseSpec{
		{
			CaseID:   "baseline_54",
			InputVal: rawReadVal,
			Kind:     "recorded",
			InitialMemory: []decomp.MemoryCell{
				{Address: 0x7E1E0A, Value: rawReadVal},
			},
			StackMemoryNote:     "stack memory is uninitialized; overwritten by PHB ($7E01FC) and PHK ($7E01FB) before read",
			ExpectedOutput:      rawWriteVal,
			ExpectedSuccessorPC: "$0CC40A",
			ExpectedP:           "$30",
			WantNextPC:          0x0CC40A,
			WantP:               0x30,
		},
		{
			CaseID:   "prediction_127",
			InputVal: 127,
			Kind:     "prediction",
			InitialMemory: []decomp.MemoryCell{
				{Address: 0x7E1E0A, Value: 127},
			},
			StackMemoryNote:     "stack memory is uninitialized; overwritten by PHB ($7E01FC) and PHK ($7E01FB) before read",
			ExpectedOutput:      128,
			ExpectedSuccessorPC: "$0CC40A",
			ExpectedP:           "$B0",
			WantNextPC:          0x0CC40A,
			WantP:               0xB0,
		},
		{
			CaseID:   "prediction_255",
			InputVal: 255,
			Kind:     "prediction",
			InitialMemory: []decomp.MemoryCell{
				{Address: 0x7E1E0A, Value: 255},
			},
			StackMemoryNote:     "stack memory is uninitialized; overwritten by PHB ($7E01FC) and PHK ($7E01FB) before read",
			ExpectedOutput:      0,
			ExpectedSuccessorPC: "$0CC40A",
			ExpectedP:           "$32",
			WantNextPC:          0x0CC40A,
			WantP:               0x32,
		},
	}

	var cBatchCases []decomp.ReplayCaseInput
	for _, spec := range testSpecs {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  spec.CaseID,
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1E0A, Value: spec.InputVal},
			},
		})
	}

	// Build compiled runners for prefixes 1, 2, 3, 4 to capture intermediate C step evidence
	var cPrefixRunners []*decomp.CompiledRunner
	for k := 1; k <= 4; k++ {
		prefixEnd := blockInstructions[k-1].Address + uint32(len(blockInstructions[k-1].Bytes)/2)
		prefixBlock := &structure.BasicBlock{
			ID:           fmt.Sprintf("block-0cc404-prefix-%d", k),
			StartAddress: 0x0CC404,
			EndAddress:   prefixEnd,
			Instructions: blockInstructions[:k],
			Successors:   []uint32{prefixEnd},
		}
		prefixIR, err := decomp.LiftBlock(prefixBlock, ctxClear)
		if err != nil {
			return fmt.Errorf("lift prefix block %d: %w", k, err)
		}
		runner, err := decomp.NewCompiledRunner(ctx, prefixIR)
		if err != nil {
			return fmt.Errorf("new compiled runner prefix %d: %w", k, err)
		}
		defer runner.Close()
		cPrefixRunners = append(cPrefixRunners, runner)
	}

	var cPrefixResults [][]decomp.ExecResult
	for k := 0; k < 4; k++ {
		res, err := cPrefixRunners[k].RunBatch(ctx, cBatchCases)
		if err != nil {
			return fmt.Errorf("compiled runner prefix %d batch: %w", k+1, err)
		}
		cPrefixResults = append(cPrefixResults, res)
	}
	cResults := cPrefixResults[3]

	var caseResults []StackCaseResult
	var timelineSteps []StackTimelineStep
	allDualMatch := true
	allExpectedMatch := true
	allThreeWrites := true
	baselineRawVerified := true
	allCasesVerified := true

	var emuStepsByCase [][]decomp.StepResult

	for i, spec := range testSpecs {
		mem := map[uint32]uint8{
			0x7E1E0A: spec.InputVal,
		}

		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			return fmt.Errorf("emulator run case %s: %w", spec.CaseID, err)
		}
		emuStepsByCase = append(emuStepsByCase, emuSteps)

		cRes := cResults[i]

		matched, disc := decomp.CompareExecResults(emuRes, cRes)
		if !matched {
			allDualMatch = false
		}

		// Verify intermediate C step states match emulator step exit states
		intermediateCMatched := true
		for stepIdx := 0; stepIdx < 4; stepIdx++ {
			cStepRes := cPrefixResults[stepIdx][i]
			cStepMatched, cStepDisc := decomp.CompareCPUStates(emuSteps[stepIdx].ExitState, cStepRes.State)
			if !cStepMatched {
				intermediateCMatched = false
				allDualMatch = false
				if disc == "" {
					disc = fmt.Sprintf("step %d intermediate C state mismatch: %s", stepIdx+1, cStepDisc)
				}
			}
			if cStepRes.WriteOverflow || cStepRes.MissingRead || cStepRes.MMIOAccess {
				intermediateCMatched = false
				allDualMatch = false
				if disc == "" {
					disc = fmt.Sprintf("step %d intermediate C refusal: overflow=%v missing_read=%v mmio=%v",
						stepIdx+1, cStepRes.WriteOverflow, cStepRes.MissingRead, cStepRes.MMIOAccess)
				}
			}
		}

		matchesExpected := emuRes.NextPC == spec.WantNextPC && emuRes.State.P == spec.WantP
		if !matchesExpected {
			allExpectedMatch = false
		}

		if len(emuRes.Writes) != 3 || len(cRes.Writes) != 3 {
			allThreeWrites = false
		}

		// Verify 3 ordered writes: stack 0x01FC=00, stack 0x01FB=0C, counter 0x1E0A=expected_output
		writesOk := len(emuRes.Writes) == 3 && len(cRes.Writes) == 3 &&
			emuRes.Writes[0].Address == 0x7E01FC && emuRes.Writes[0].Value == 0x00 &&
			emuRes.Writes[1].Address == 0x7E01FB && emuRes.Writes[1].Value == 0x0C &&
			emuRes.Writes[2].Address == 0x7E1E0A && emuRes.Writes[2].Value == spec.ExpectedOutput &&
			cRes.Writes[0].Address == 0x7E01FC && cRes.Writes[0].Value == 0x00 &&
			cRes.Writes[1].Address == 0x7E01FB && cRes.Writes[1].Value == 0x0C &&
			cRes.Writes[2].Address == 0x7E1E0A && cRes.Writes[2].Value == spec.ExpectedOutput

		if !writesOk {
			allThreeWrites = false
		}

		if spec.Kind == "recorded" {
			// Compare baseline states/accesses against admitted raw capture
			rawEvents := []trace.Event{ev30003, ev30006, ev30009, ev30015}
			for sIdx, rev := range rawEvents {
				st := emuSteps[sIdx]
				rEntry := rev.Insn.Entry
				rExit := rev.Insn.Exit
				if st.EntryState.A != rEntry.A ||
					st.EntryState.X != rEntry.X ||
					st.EntryState.Y != rEntry.Y ||
					st.EntryState.S != rEntry.S ||
					st.EntryState.D != rEntry.D ||
					st.EntryState.DB != rEntry.DB ||
					st.EntryState.PB != rEntry.PB ||
					st.EntryState.PC != rEntry.PC ||
					st.EntryState.P != rEntry.P ||
					st.EntryState.E != rEntry.E {
					baselineRawVerified = false
				}
				if st.ExitState.A != rExit.A ||
					st.ExitState.X != rExit.X ||
					st.ExitState.Y != rExit.Y ||
					st.ExitState.S != rExit.S ||
					st.ExitState.D != rExit.D ||
					st.ExitState.DB != rExit.DB ||
					st.ExitState.PB != rExit.PB ||
					st.ExitState.PC != rExit.PC ||
					st.ExitState.P != rExit.P ||
					st.ExitState.E != rExit.E {
					baselineRawVerified = false
				}
			}

			// Raw reads: event 30008 val 12, event 30013 val 54
			if len(emuSteps[2].Reads) != 1 || emuSteps[2].Reads[0].Address != 0x7E01FB || emuSteps[2].Reads[0].Value != 12 || emuSteps[2].Reads[0].Value != uint8(ev30008.Value) {
				baselineRawVerified = false
			}
			if len(emuSteps[3].Reads) != 1 || emuSteps[3].Reads[0].Address != 0x7E1E0A || emuSteps[3].Reads[0].Value != 54 || emuSteps[3].Reads[0].Value != uint8(ev30013.Value) {
				baselineRawVerified = false
			}

			// Raw writes:
			if len(emuSteps[0].Writes) != 1 || emuSteps[0].Writes[0].Address != 0x7E01FC || emuSteps[0].Writes[0].Value != 0x00 ||
				ev30002.After == nil || emuSteps[0].Writes[0].Value != byte(*ev30002.After) {
				baselineRawVerified = false
			}
			if len(emuSteps[1].Writes) != 1 || emuSteps[1].Writes[0].Address != 0x7E01FB || emuSteps[1].Writes[0].Value != 0x0C ||
				emuSteps[1].Writes[0].Value != byte(ev30005.Value) {
				baselineRawVerified = false
			}
			if len(emuSteps[3].Writes) != 1 || emuSteps[3].Writes[0].Address != 0x7E1E0A || emuSteps[3].Writes[0].Value != 55 ||
				emuSteps[3].Writes[0].Value != byte(ev30014.Value) {
				baselineRawVerified = false
			}
		}

		noRefusal := !emuRes.MissingRead && !emuRes.WriteOverflow && !emuRes.MMIOAccess &&
			!cRes.MissingRead && !cRes.WriteOverflow && !cRes.MMIOAccess

		caseVerified := matched && intermediateCMatched && matchesExpected && writesOk && noRefusal
		if spec.Kind == "recorded" && !baselineRawVerified {
			caseVerified = false
		}
		if !caseVerified {
			allCasesVerified = false
		}

		res := StackCaseResult{
			CaseID:              spec.CaseID,
			InputVal:            spec.InputVal,
			OutputVal:           spec.ExpectedOutput,
			Kind:                spec.Kind,
			ExpectedSuccessorPC: spec.ExpectedSuccessorPC,
			EmuSuccessorPC:      fmt.Sprintf("$%06X", emuRes.NextPC),
			CSuccessorPC:        fmt.Sprintf("$%06X", cRes.NextPC),
			EmuP:                fmt.Sprintf("$%02X", emuRes.State.P),
			CP:                  fmt.Sprintf("$%02X", cRes.State.P),
			EmuMatchesC:         matched,
			MatchesExpected:     matchesExpected,
			EmuWrites:           len(emuRes.Writes),
			CWrites:             len(cRes.Writes),
			Writes:              emuRes.Writes,
			TotalWrites:         emuRes.TotalWrites,
			WriteOverflow:       emuRes.WriteOverflow,
			MissingRead:         emuRes.MissingRead,
			MissingAddr:         emuRes.MissingAddr,
			MMIOAccess:          emuRes.MMIOAccess,
			MMIOAddr:            emuRes.MMIOAddr,
			Discrepancy:         disc,
			Verified:            caseVerified,
			EmuState:            emuRes.State,
			CState:              cRes.State,
		}
		caseResults = append(caseResults, res)
	}

	// 6. Assemble Timeline Steps
	for stepIdx := 0; stepIdx < 4; stepIdx++ {
		inst := blockInstructions[stepIdx]
		baseStep := emuStepsByCase[0][stepIdx]

		step := StackTimelineStep{
			StepIndex: stepIdx + 1,
			Address:   fmt.Sprintf("$%06X", inst.Address),
			ROMOffset: fmt.Sprintf("$%06X", inst.Offset),
			Mnemonic:  fmt.Sprintf("%s %s", strings.ToUpper(inst.Mnemonic), inst.Bytes),
			Recorded: StackStepRecord{
				InputVal: testSpecs[0].InputVal,
				EntryA:   fmt.Sprintf("$%04X", baseStep.EntryState.A),
				EntryS:   fmt.Sprintf("$%04X", baseStep.EntryState.S),
				EntryDB:  fmt.Sprintf("$%02X", baseStep.EntryState.DB),
				EntryP:   fmt.Sprintf("$%02X", baseStep.EntryState.P),
				ExitA:    fmt.Sprintf("$%04X", baseStep.ExitState.A),
				ExitS:    fmt.Sprintf("$%04X", baseStep.ExitState.S),
				ExitDB:   fmt.Sprintf("$%02X", baseStep.ExitState.DB),
				ExitP:    fmt.Sprintf("$%02X", baseStep.ExitState.P),
				ExitPC:   fmt.Sprintf("$%04X", baseStep.ExitState.PC),
				Reads:    baseStep.Reads, // Saved Go emulator reads (derived from StepResult.Reads)
				Writes:   baseStep.Writes,
				State:    baseStep.ExitState,
				CState:   cPrefixResults[stepIdx][0].State,
			},
		}
		if inst.Mnemonic == "phb" {
			step.Mnemonic = "PHB"
		} else if inst.Mnemonic == "phk" {
			step.Mnemonic = "PHK"
		} else if inst.Mnemonic == "plb" {
			step.Mnemonic = "PLB"
		} else if inst.Mnemonic == "inc" {
			step.Mnemonic = "INC $1E0A"
		}

		for caseIdx := 1; caseIdx < len(testSpecs); caseIdx++ {
			pStep := emuStepsByCase[caseIdx][stepIdx]
			step.Predictions = append(step.Predictions, StackPredictRecord{
				InputVal: testSpecs[caseIdx].InputVal,
				EntryA:   fmt.Sprintf("$%04X", pStep.EntryState.A),
				EntryS:   fmt.Sprintf("$%04X", pStep.EntryState.S),
				EntryDB:  fmt.Sprintf("$%02X", pStep.EntryState.DB),
				EntryP:   fmt.Sprintf("$%02X", pStep.EntryState.P),
				ExitA:    fmt.Sprintf("$%04X", pStep.ExitState.A),
				ExitS:    fmt.Sprintf("$%04X", pStep.ExitState.S),
				ExitDB:   fmt.Sprintf("$%02X", pStep.ExitState.DB),
				ExitP:    fmt.Sprintf("$%02X", pStep.ExitState.P),
				ExitPC:   fmt.Sprintf("$%04X", pStep.ExitState.PC),
				Reads:    pStep.Reads, // Saved Go emulator reads (derived from StepResult.Reads)
				Writes:   pStep.Writes,
				State:    pStep.ExitState,
				CState:   cPrefixResults[stepIdx][caseIdx].State,
			})
		}
		timelineSteps = append(timelineSteps, step)
	}

	// 7. Write output artifacts
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}

	caseDoc := map[string]interface{}{
		"case_id":                "stack-replay-0cc404",
		"block_address":          "$0CC404",
		"physical_read_address":  "$7E1E0A",
		"physical_write_address": "$7E1E0A",
		"recorded_read_value":    rawReadVal,
		"recorded_write_value":   rawWriteVal,
		"stream_sha256":          streamSHA,
		"rom_sha256":             romSHA,
		"dispatch_event":         30000,
		"target_event":           30003,
		"initial_cpu_state":      initState,
		"stack_policy":           "stack memory is uninitialized; overwritten by PHB ($7E01FC) and PHK ($7E01FB) before read",
		"instructions":           blockInstructions,
		"cases":                  testSpecs,
	}
	caseBytes, err := json.MarshalIndent(caseDoc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal case.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "case.json"), caseBytes, 0644); err != nil {
		return fmt.Errorf("write case.json: %w", err)
	}

	if err := os.WriteFile(filepath.Join(*outDir, "generated.c"), []byte(cCode), 0644); err != nil {
		return fmt.Errorf("write generated.c: %w", err)
	}

	timelineBytes, err := json.MarshalIndent(timelineSteps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal timeline.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "timeline.json"), timelineBytes, 0644); err != nil {
		return fmt.Errorf("write timeline.json: %w", err)
	}

	overallStatus := "success"
	if !baselineRawVerified || !allDualMatch || !allExpectedMatch || !allThreeWrites || !allCasesVerified {
		overallStatus = "failed"
	}

	receipt := StackReplayReceipt{
		Status:                overallStatus,
		StreamSHA256:          streamSHA,
		ROMSHA256:             romSHA,
		BlockAddress:          "$0CC404",
		DispatchSeq:           8511,
		TargetSeq:             8512,
		PhysicalReadAddress:   "$7E1E0A",
		PhysicalWriteAddress:  "$7E1E0A",
		RecordedReadValue:     rawReadVal,
		RecordedWriteValue:    rawWriteVal,
		BaselineRawVerified:   baselineRawVerified,
		DualBackendVerified:   allDualMatch,
		ExpectedMatchVerified: allExpectedMatch,
		ThreeWritesVerified:   allThreeWrites,
		Results:               caseResults,
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "receipt.json"), receiptBytes, 0644); err != nil {
		return fmt.Errorf("write receipt.json: %w", err)
	}

	caseDigest := fmt.Sprintf("%x", sha256.Sum256(caseBytes))
	generatedCDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(cCode)))
	timelineDigest := fmt.Sprintf("%x", sha256.Sum256(timelineBytes))
	receiptDigest := fmt.Sprintf("%x", sha256.Sum256(receiptBytes))

	manifest := StackBundleManifest{
		BundleID:       "stack_0cc404",
		BlockAddress:   "$0CC404",
		Qualification:  "Saved replay: Go CPU and compiled C agree",
		StreamSHA256:   streamSHA,
		ROMSHA256:      romSHA,
		DocumentSHA256: "cb6f4a1af5d6ec5f2d906596ea7e3d61f05d17f6709477133afc551837f51836",
		CanonicalInstructionIDs: []string{
			"b8ada5111a6770c7c31706b7132feddf82d73cbd35f025527632f370332d24fb",
			"4822592d95b274cfbb0423cb73f8a30fb1a9b53bbf414aa814b47eb80f8db550",
			"6d78c881fcf4aa1526d4c7c0666bf1beb03a2a1ba7833b134368da3447481769",
			"9e4b50e1dead1c3aba9f2604e59629481035d51df50a885bca792a2780cb9b77",
		},
		RetirementEventIDs: []uint64{30003, 30006, 30009, 30015},
		RetirementSeqs:     []uint64{8512, 8513, 8514, 8515},
		OperandBusIDs:      []uint64{30002, 30005, 30008, 30013, 30014},
		ArtifactDigests: map[string]string{
			"case_sha256":        caseDigest,
			"generated_c_sha256": generatedCDigest,
			"receipt_sha256":     receiptDigest,
			"timeline_sha256":    timelineDigest,
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "manifest.json"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write manifest.json: %w", err)
	}

	if *format == "json" {
		stdout.Write(receiptBytes)
		stdout.Write([]byte("\n"))
	} else {
		fmt.Fprintf(stdout, "Stack replay executed successfully:\n")
		fmt.Fprintf(stdout, "  Block: $0CC404..$0CC407 stopping at $0CC40A\n")
		fmt.Fprintf(stdout, "  Baseline counter: %d ($%02X) -> %d ($%02X)\n", rawReadVal, rawReadVal, rawWriteVal, rawWriteVal)
		fmt.Fprintf(stdout, "  Dual-backend verified: %v\n", allDualMatch)
		fmt.Fprintf(stdout, "  Baseline raw verified: %v\n", baselineRawVerified)
		fmt.Fprintf(stdout, "  Three writes verified: %v\n", allThreeWrites)
		fmt.Fprintf(stdout, "  Artifacts written to: %s\n", *outDir)
	}

	if overallStatus != "success" {
		return fmt.Errorf("stack replay verification gates failed: baseline_raw=%v dual_backend=%v expected_match=%v three_writes=%v all_cases=%v",
			baselineRawVerified, allDualMatch, allExpectedMatch, allThreeWrites, allCasesVerified)
	}

	return nil
}
