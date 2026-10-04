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

type LookupCaseSpec struct {
	CaseID               string              `json:"case_id"`
	InputWRAM05          uint8               `json:"input_wram_05"`
	Kind                 string              `json:"kind"` // "recorded_baseline" | "unrecorded_prediction"
	Initial              decomp.CPUState     `json:"initial"`
	Memory               []decomp.MemoryCell `json:"memory"`
	ExpectedFullA        string              `json:"expected_full_a"`
	ExpectedFullAHex     uint16              `json:"expected_full_a_hex"`
	ExpectedY            uint16              `json:"expected_y"`
	ExpectedROMAddr      string              `json:"expected_rom_addr"`
	ExpectedROMAddrHex   uint32              `json:"expected_rom_addr_hex"`
	ExpectedROMOffset    string              `json:"expected_rom_offset"`
	ExpectedROMOffsetHex uint32              `json:"expected_rom_offset_hex"`
	ExpectedROMVal       uint8               `json:"expected_rom_val"`
	ExpectedWriteAddr    string              `json:"expected_write_addr"`
	ExpectedWriteVal     uint8               `json:"expected_write_val"`
	WantNextPC           uint32              `json:"want_next_pc"`
}

type LookupCaseResult struct {
	CaseID           string          `json:"case_id"`
	InputWRAM05      uint8           `json:"input_wram_05"`
	Kind             string          `json:"kind"`
	EmuSuccessorPC   string          `json:"emu_successor_pc"`
	CSuccessorPC     string          `json:"c_successor_pc"`
	EmuFullA         string          `json:"emu_full_a"`
	CFullA           string          `json:"c_full_a"`
	EmuY             uint16          `json:"emu_y"`
	CY               uint16          `json:"c_y"`
	EmuWrites        int             `json:"emu_writes"`
	CWrites          int             `json:"c_writes"`
	WriteAddress     string          `json:"write_address"`
	WriteValue       uint8           `json:"write_value"`
	EmuMatchesC      bool            `json:"emu_matches_c"`
	MatchesExpected  bool            `json:"matches_expected"`
	Discrepancy      string          `json:"discrepancy,omitempty"`
	Verified         bool            `json:"verified"`
	EmuState         decomp.CPUState `json:"emu_state"`
	CState           decomp.CPUState `json:"c_state"`
}

type LookupTimelineStep struct {
	StepIndex   int                   `json:"step_index"`
	Address     string                `json:"address"`
	Mnemonic    string                `json:"mnemonic"`
	Recorded    LookupStepRecord      `json:"recorded"`
	Predictions []LookupPredictRecord `json:"predictions"`
}

type LookupStepRecord struct {
	InputVal         uint8                `json:"input_val"`
	OperandEventID   uint64               `json:"operand_event_id,omitempty"`
	EffectiveAddress string               `json:"effective_address,omitempty"`
	ROMOffset        string               `json:"rom_offset,omitempty"`
	BusValue         uint8                `json:"bus_value,omitempty"`
	BusOp            string               `json:"bus_op,omitempty"`
	Reads            []decomp.MemoryWrite `json:"reads,omitempty"`
	Writes           []decomp.MemoryWrite `json:"writes,omitempty"`
	EntryA           string               `json:"entry_a"`
	EntryY           string               `json:"entry_y"`
	EntryP           string               `json:"entry_p"`
	ExitA            string               `json:"exit_a"`
	ExitY            string               `json:"exit_y"`
	ExitP            string               `json:"exit_p"`
	ExitPC           string               `json:"exit_pc"`
	State            decomp.CPUState      `json:"state"`
}

type LookupPredictRecord struct {
	InputVal         uint8                `json:"input_val"`
	EffectiveAddress string               `json:"effective_address,omitempty"`
	ROMOffset        string               `json:"rom_offset,omitempty"`
	BusValue         uint8                `json:"bus_value,omitempty"`
	BusOp            string               `json:"bus_op,omitempty"`
	Reads            []decomp.MemoryWrite `json:"reads,omitempty"`
	Writes           []decomp.MemoryWrite `json:"writes,omitempty"`
	EntryA           string               `json:"entry_a"`
	EntryY           string               `json:"entry_y"`
	EntryP           string               `json:"entry_p"`
	ExitA            string               `json:"exit_a"`
	ExitY            string               `json:"exit_y"`
	ExitP            string               `json:"exit_p"`
	ExitPC           string               `json:"exit_pc"`
	State            decomp.CPUState      `json:"state"`
}

type LookupReceipt struct {
	Status               string             `json:"status"`
	StreamSHA256         string             `json:"stream_sha256"`
	ROMSHA256            string             `json:"rom_sha256"`
	BlockAddress         string             `json:"block_address"`
	PredecessorEventID   uint64             `json:"predecessor_event_id"`
	PredecessorSeq       uint64             `json:"predecessor_seq"`
	RetirementEventIDs   []uint64           `json:"retirement_event_ids"`
	RetirementSeqs       []uint64           `json:"retirement_seqs"`
	PhysicalWriteAddr    string             `json:"physical_write_addr"`
	DualBackendVerified  bool               `json:"dual_backend_verified"`
	BaselineRawVerified  bool               `json:"baseline_raw_verified"`
	SingleWriteVerified  bool               `json:"single_write_verified"`
	StepAccessesVerified bool               `json:"step_accesses_verified"`
	Results              []LookupCaseResult `json:"results"`
}

func runLookupReplay(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm lookup-replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm lookup-replay -rom rom.sfc -trace trace.jsonl -out dir [flags]",
			"Verify bounded 3-instruction ROM lookup counterfactual replay across Go CPU and compiled C.",
			"snesdasm lookup-replay -rom rom.sfc -trace trace.jsonl -out ./lookup_delivery",
			"snesdasm lookup-replay -rom rom.sfc -trace trace.jsonl -out ./lookup_delivery -format json",
		)
	}

	var (
		romPath   = fs.String("rom", "", "path to admitted ROM file (required)")
		tracePath = fs.String("trace", "", "path to admitted trace JSONL file (required)")
		outDir    = fs.String("out", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-lookup-replay-delivery", "output directory for artifacts")
		format    = fs.String("format", "text", "output format: text|json")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *romPath == "" || *tracePath == "" {
		return fmt.Errorf("-rom and -trace flags are required; run 'snesdasm help lookup-replay' for usage")
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

	// 3. Scan for target events:
	// Predecessor: 52073
	// Instruction 1: read 52076, retirement 52077 ($09F882: LDY $05)
	// Instruction 2: read 52081, retirement 52082 ($09F884: LDA $FB6D,Y)
	// Instruction 3: write 52085, retirement 52086 ($09F887: STA $54)
	dec := json.NewDecoder(bytes.NewReader(traceBytes))
	var (
		ev52073 trace.Event
		ev52076 trace.Event
		ev52077 trace.Event
		ev52081 trace.Event
		ev52082 trace.Event
		ev52085 trace.Event
		ev52086 trace.Event
	)

	for dec.More() {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("decoding trace: %w", err)
		}
		switch e.ID {
		case 52073:
			ev52073 = e
		case 52076:
			ev52076 = e
		case 52077:
			ev52077 = e
		case 52081:
			ev52081 = e
		case 52082:
			ev52082 = e
		case 52085:
			ev52085 = e
		case 52086:
			ev52086 = e
		}
	}

	for _, id := range []uint64{52073, 52076, 52077, 52081, 52082, 52085, 52086} {
		var found bool
		switch id {
		case 52073:
			found = ev52073.ID == 52073
		case 52076:
			found = ev52076.ID == 52076
		case 52077:
			found = ev52077.ID == 52077
		case 52081:
			found = ev52081.ID == 52081
		case 52082:
			found = ev52082.ID == 52082
		case 52085:
			found = ev52085.ID == 52085
		case 52086:
			found = ev52086.ID == 52086
		}
		if !found {
			return fmt.Errorf("missing required event %d", id)
		}
	}

	if ev52073.Insn == nil || ev52077.Insn == nil || ev52082.Insn == nil || ev52086.Insn == nil {
		return errors.New("instruction traces missing from retirement events")
	}

	// Verify sequential retirements: Seq 13198..13201
	if ev52073.Insn.Seq != 13198 || ev52077.Insn.Seq != 13199 || ev52082.Insn.Seq != 13200 || ev52086.Insn.Seq != 13201 {
		return fmt.Errorf("unexpected insn seqs: %d, %d, %d, %d (require 13198..13201)",
			ev52073.Insn.Seq, ev52077.Insn.Seq, ev52082.Insn.Seq, ev52086.Insn.Seq)
	}

	// Verify physical bus operand events
	if ev52076.Addr != 0x001F05 || ev52076.Value != 115 || ev52076.Op != "read" {
		return fmt.Errorf("event 52076 expected read $001F05=115, got %s $%06X=%d", ev52076.Op, ev52076.Addr, ev52076.Value)
	}
	if ev52081.Addr != 0x09FBE0 || ev52081.Value != 20 || ev52081.Op != "read" {
		return fmt.Errorf("event 52081 expected read $09FBE0=20, got %s $%06X=%d", ev52081.Op, ev52081.Addr, ev52081.Value)
	}
	if ev52085.Addr != 0x001F54 || ev52085.Value != 20 || ev52085.Op != "write" {
		return fmt.Errorf("event 52085 expected write $001F54=20, got %s $%06X=%d", ev52085.Op, ev52085.Addr, ev52085.Value)
	}

	// Join instruction fetches to physical ROM
	insnEvents := []trace.Event{ev52077, ev52082, ev52086}
	for _, ie := range insnEvents {
		for _, f := range ie.Insn.Fetches {
			off, ok := analysis.LoROMToOffset(f.Addr, len(romBytes))
			if !ok || int(off) >= len(romBytes) || romBytes[off] != f.Value {
				return fmt.Errorf("fetch $%06X val 0x%02X does not match ROM", f.Addr, f.Value)
			}
		}
	}

	// Initial CPU state from event 52077
	rEntry := ev52077.Insn.Entry
	flagStr := func(b bool) string {
		if b {
			return "set"
		}
		return "clear"
	}
	ctxDerived := recovery.Context{
		E: flagStr(rEntry.E),
		M: flagStr(rEntry.P&0x20 != 0),
		X: flagStr(rEntry.P&0x10 != 0),
		C: flagStr(rEntry.P&0x01 != 0), // Carry is set (P=0xB1)
	}

	if ctxDerived.E != "clear" || ctxDerived.M != "set" || ctxDerived.X != "set" || ctxDerived.C != "set" {
		return fmt.Errorf("unexpected entry context from event 52077: %+v", ctxDerived)
	}

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

	blockInstructions := []recovery.Instruction{
		{
			ID:           "insn-09f882",
			Architecture: "wdc65816",
			Address:      0x09F882,
			Offset:       0x04F882,
			Bytes:        "a405",
			Opcode:       0xA4,
			Mnemonic:     "ldy",
			Mode:         "direct_page",
			Context:      ctxDerived,
		},
		{
			ID:           "insn-09f884",
			Architecture: "wdc65816",
			Address:      0x09F884,
			Offset:       0x04F884,
			Bytes:        "b96dfb",
			Opcode:       0xB9,
			Mnemonic:     "lda",
			Mode:         "absolute_indexed_y",
			Context:      ctxDerived,
		},
		{
			ID:           "insn-09f887",
			Architecture: "wdc65816",
			Address:      0x09F887,
			Offset:       0x04F887,
			Bytes:        "8554",
			Opcode:       0x85,
			Mnemonic:     "sta",
			Mode:         "direct_page",
			Context:      ctxDerived,
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
	}

	block := &structure.BasicBlock{
		ID:           "block-09f882-lookup",
		StartAddress: 0x09F882,
		EndAddress:   0x09F889,
		Instructions: blockInstructions,
		Successors:   []uint32{0x09F889},
	}

	// 4. Lift block to IR and generate compilable C
	ir, err := decomp.LiftBlock(block, ctxDerived)
	if err != nil {
		return fmt.Errorf("lift block: %w", err)
	}

	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		return fmt.Errorf("generate compilable C: %w", err)
	}

	// 5. Source selected ROM bytes by CPU ROM address from admitted ROM
	getROMByte := func(cpuAddr uint32) (uint8, error) {
		off, ok := analysis.LoROMToOffset(cpuAddr, len(romBytes))
		if !ok || int(off) >= len(romBytes) {
			return 0, fmt.Errorf("CPU ROM address $%06X outside mapped ROM", cpuAddr)
		}
		return romBytes[off], nil
	}

	romByteDF, err := getROMByte(0x09FBDF)
	if err != nil {
		return fmt.Errorf("reading ROM byte at $09FBDF: %w", err)
	}
	romByteE0, err := getROMByte(0x09FBE0)
	if err != nil {
		return fmt.Errorf("reading ROM byte at $09FBE0: %w", err)
	}
	romByteE1, err := getROMByte(0x09FBE1)
	if err != nil {
		return fmt.Errorf("reading ROM byte at $09FBE1: %w", err)
	}

	// Verify table bytes match root/known expectations: 22, 20, 19
	if romByteDF != 22 || romByteE0 != 20 || romByteE1 != 19 {
		return fmt.Errorf("unexpected table ROM bytes: DF=%d (want 22), E0=%d (want 20), E1=%d (want 19)",
			romByteDF, romByteE0, romByteE1)
	}

	// 6. Test cases: baseline 115 (recorded), predictions 114, 116
	testSpecs := []LookupCaseSpec{
		{
			CaseID:               "baseline_115",
			InputWRAM05:          115,
			Kind:                 "recorded_baseline",
			Initial:              initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 115},
				{Address: 0x09FBDF, Value: romByteDF},
				{Address: 0x09FBE0, Value: romByteE0},
				{Address: 0x09FBE1, Value: romByteE1},
			},
			ExpectedFullA:        "$FF14",
			ExpectedFullAHex:     0xFF14,
			ExpectedY:            115,
			ExpectedROMAddr:      "$09FBE0",
			ExpectedROMAddrHex:   0x09FBE0,
			ExpectedROMOffset:    "$04FBE0",
			ExpectedROMOffsetHex: 0x04FBE0,
			ExpectedROMVal:       20,
			ExpectedWriteAddr:    "$7E1F54",
			ExpectedWriteVal:     20,
			WantNextPC:           0x09F889,
		},
		{
			CaseID:               "prediction_114",
			InputWRAM05:          114,
			Kind:                 "unrecorded_prediction",
			Initial:              initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 114},
				{Address: 0x09FBDF, Value: romByteDF},
				{Address: 0x09FBE0, Value: romByteE0},
				{Address: 0x09FBE1, Value: romByteE1},
			},
			ExpectedFullA:        "$FF16",
			ExpectedFullAHex:     0xFF16,
			ExpectedY:            114,
			ExpectedROMAddr:      "$09FBDF",
			ExpectedROMAddrHex:   0x09FBDF,
			ExpectedROMOffset:    "$04FBDF",
			ExpectedROMOffsetHex: 0x04FBDF,
			ExpectedROMVal:       22,
			ExpectedWriteAddr:    "$7E1F54",
			ExpectedWriteVal:     22,
			WantNextPC:           0x09F889,
		},
		{
			CaseID:               "prediction_116",
			InputWRAM05:          116,
			Kind:                 "unrecorded_prediction",
			Initial:              initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 116},
				{Address: 0x09FBDF, Value: romByteDF},
				{Address: 0x09FBE0, Value: romByteE0},
				{Address: 0x09FBE1, Value: romByteE1},
			},
			ExpectedFullA:        "$FF13",
			ExpectedFullAHex:     0xFF13,
			ExpectedY:            116,
			ExpectedROMAddr:      "$09FBE1",
			ExpectedROMAddrHex:   0x09FBE1,
			ExpectedROMOffset:    "$04FBE1",
			ExpectedROMOffsetHex: 0x04FBE1,
			ExpectedROMVal:       19,
			ExpectedWriteAddr:    "$7E1F54",
			ExpectedWriteVal:     19,
			WantNextPC:           0x09F889,
		},
	}

	compiledRunner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		return fmt.Errorf("new compiled runner: %w", err)
	}
	defer compiledRunner.Close()

	var cBatchCases []decomp.ReplayCaseInput
	for _, spec := range testSpecs {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  spec.CaseID,
			Initial: spec.Initial,
			Memory:  spec.Memory,
		})
	}

	cResults, err := compiledRunner.RunBatch(ctx, cBatchCases)
	if err != nil {
		return fmt.Errorf("compiled runner batch: %w", err)
	}

	var caseResults []LookupCaseResult
	var timelineSteps []LookupTimelineStep
	allDualMatch := true
	allExpectedMatch := true
	allNoRefusal := true
	allSingleWrite := true
	baselineRawVerified := true

	var emuStepsByCase [][]decomp.StepResult

	for i, spec := range testSpecs {
		mem := map[uint32]uint8{
			0x7E1F05: spec.InputWRAM05,
			0x09FBDF: romByteDF,
			0x09FBE0: romByteE0,
			0x09FBE1: romByteE1,
		}

		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			return fmt.Errorf("emulator run %s: %w", spec.CaseID, err)
		}
		emuStepsByCase = append(emuStepsByCase, emuSteps)

		cRes := cResults[i]

		matched, disc := decomp.CompareExecResults(emuRes, cRes)
		expectedPCMatched := (emuRes.NextPC == spec.WantNextPC) && (cRes.NextPC == spec.WantNextPC)
		expectedAMatched := (emuRes.State.A == spec.ExpectedFullAHex) && (cRes.State.A == spec.ExpectedFullAHex)
		expectedYMatched := (emuRes.State.Y == spec.ExpectedY) && (cRes.State.Y == spec.ExpectedY)
		expectedPMatched := (emuRes.State.P == 0x31) && (cRes.State.P == 0x31)

		noRefusal := !emuRes.MissingRead && !cRes.MissingRead &&
			!emuRes.MMIOAccess && !cRes.MMIOAccess &&
			!emuRes.WriteOverflow && !cRes.WriteOverflow

		singleWrite := (len(emuRes.Writes) == 1 && emuRes.Writes[0].Address == 0x7E1F54 && emuRes.Writes[0].Value == spec.ExpectedWriteVal) &&
			(len(cRes.Writes) == 1 && cRes.Writes[0].Address == 0x7E1F54 && cRes.Writes[0].Value == spec.ExpectedWriteVal)

		caseVerified := matched && expectedPCMatched && expectedAMatched && expectedYMatched && expectedPMatched && noRefusal && singleWrite

		if !matched {
			allDualMatch = false
		}
		if !expectedPCMatched || !expectedAMatched || !expectedYMatched || !expectedPMatched {
			allExpectedMatch = false
		}
		if !noRefusal {
			allNoRefusal = false
		}
		if !singleWrite {
			allSingleWrite = false
		}

		writeAddr := ""
		var writeVal uint8
		if len(emuRes.Writes) > 0 {
			writeAddr = fmt.Sprintf("$%06X", emuRes.Writes[0].Address)
			writeVal = emuRes.Writes[0].Value
		}

		caseResults = append(caseResults, LookupCaseResult{
			CaseID:          spec.CaseID,
			InputWRAM05:     spec.InputWRAM05,
			Kind:            spec.Kind,
			EmuSuccessorPC:  fmt.Sprintf("$%06X", emuRes.NextPC),
			CSuccessorPC:    fmt.Sprintf("$%06X", cRes.NextPC),
			EmuFullA:        fmt.Sprintf("$%04X", emuRes.State.A),
			CFullA:          fmt.Sprintf("$%04X", cRes.State.A),
			EmuY:            emuRes.State.Y,
			CY:              cRes.State.Y,
			EmuWrites:       len(emuRes.Writes),
			CWrites:         len(cRes.Writes),
			WriteAddress:    writeAddr,
			WriteValue:      writeVal,
			EmuMatchesC:     matched,
			MatchesExpected: expectedPCMatched && expectedAMatched && expectedYMatched && expectedPMatched && singleWrite,
			Discrepancy:     disc,
			Verified:        caseVerified,
			EmuState:        emuRes.State,
			CState:          cRes.State,
		})
	}

	// 7. Gate actual per-step reads/writes across all cases and verify baseline raw retirement states
	stepAccessesVerified := true
	for i, spec := range testSpecs {
		steps := emuStepsByCase[i]
		if len(steps) != 3 {
			stepAccessesVerified = false
			fmt.Fprintf(stderr, "case %s: expected 3 step results, got %d\n", spec.CaseID, len(steps))
			continue
		}

		// Step 0: LDY $05 -> reads $7E1F05, zero writes
		if len(steps[0].Reads) != 1 || len(steps[0].Writes) != 0 {
			stepAccessesVerified = false
			fmt.Fprintf(stderr, "case %s step 0: expected 1 read and 0 writes, got %d reads and %d writes\n",
				spec.CaseID, len(steps[0].Reads), len(steps[0].Writes))
		} else {
			r := steps[0].Reads[0]
			if r.Address != 0x7E1F05 || r.Value != spec.InputWRAM05 {
				stepAccessesVerified = false
				fmt.Fprintf(stderr, "case %s step 0: expected read $7E1F05=%d, got $%06X=%d\n",
					spec.CaseID, spec.InputWRAM05, r.Address, r.Value)
			}
		}

		// Step 1: LDA $FB6D,Y -> reads selected CPU ROM address, zero writes
		if len(steps[1].Reads) != 1 || len(steps[1].Writes) != 0 {
			stepAccessesVerified = false
			fmt.Fprintf(stderr, "case %s step 1: expected 1 read and 0 writes, got %d reads and %d writes\n",
				spec.CaseID, len(steps[1].Reads), len(steps[1].Writes))
		} else {
			r := steps[1].Reads[0]
			if r.Address != spec.ExpectedROMAddrHex || r.Value != spec.ExpectedROMVal {
				stepAccessesVerified = false
				fmt.Fprintf(stderr, "case %s step 1: expected read $%06X=%d, got $%06X=%d\n",
					spec.CaseID, spec.ExpectedROMAddrHex, spec.ExpectedROMVal, r.Address, r.Value)
			}
			off, ok := analysis.LoROMToOffset(r.Address, len(romBytes))
			if !ok || off != spec.ExpectedROMOffsetHex {
				stepAccessesVerified = false
				fmt.Fprintf(stderr, "case %s step 1: expected mapped LoROM offset $%06X, got ok=%v $%06X\n",
					spec.CaseID, spec.ExpectedROMOffsetHex, ok, off)
			} else if romBytes[off] != r.Value {
				stepAccessesVerified = false
				fmt.Fprintf(stderr, "case %s step 1: ROM byte at offset $%06X is %d, but read %d\n",
					spec.CaseID, off, romBytes[off], r.Value)
			}
		}

		// Step 2: STA $54 -> zero reads, single write $7E1F54
		if len(steps[2].Reads) != 0 || len(steps[2].Writes) != 1 {
			stepAccessesVerified = false
			fmt.Fprintf(stderr, "case %s step 2: expected 0 reads and 1 write, got %d reads and %d writes\n",
				spec.CaseID, len(steps[2].Reads), len(steps[2].Writes))
		} else {
			w := steps[2].Writes[0]
			if w.Address != 0x7E1F54 || w.Value != spec.ExpectedWriteVal {
				stepAccessesVerified = false
				fmt.Fprintf(stderr, "case %s step 2: expected write $7E1F54=%d, got $%06X=%d\n",
					spec.CaseID, spec.ExpectedWriteVal, w.Address, w.Value)
			}
		}
	}

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

	// Verify baseline execution step accesses match authentic operand events
	if len(baseEmuSteps[0].Reads) != 1 || baseEmuSteps[0].Reads[0].Address != (0x7E0000|(ev52076.Addr&0xFFFF)) || baseEmuSteps[0].Reads[0].Value != uint8(ev52076.Value) {
		baselineRawVerified = false
		fmt.Fprintf(stderr, "baseline step 0 read does not match operand event 52076\n")
	}
	if len(baseEmuSteps[1].Reads) != 1 || baseEmuSteps[1].Reads[0].Address != ev52081.Addr || baseEmuSteps[1].Reads[0].Value != uint8(ev52081.Value) {
		baselineRawVerified = false
		fmt.Fprintf(stderr, "baseline step 1 read does not match operand event 52081\n")
	}
	if len(baseEmuSteps[2].Writes) != 1 || baseEmuSteps[2].Writes[0].Address != (0x7E0000|(ev52085.Addr&0xFFFF)) || baseEmuSteps[2].Writes[0].Value != uint8(ev52085.Value) {
		baselineRawVerified = false
		fmt.Fprintf(stderr, "baseline step 2 write does not match operand event 52085\n")
	}

	allVerified := allDualMatch && allExpectedMatch && allNoRefusal && allSingleWrite && baselineRawVerified && stepAccessesVerified

	// 8. Build 3-row timeline with raw recorded vs predicted
	stepMnemonics := []string{"LDY $05", "LDA $FB6D,Y", "STA $54"}
	stepAddrs := []string{"$09F882", "$09F884", "$09F887"}

	for sIdx := 0; sIdx < 3; sIdx++ {
		rawInsn := insnEvents[sIdx].Insn

		var opEvent trace.Event
		switch sIdx {
		case 0:
			opEvent = ev52076
		case 1:
			opEvent = ev52081
		case 2:
			opEvent = ev52085
		}

		recBusOp := opEvent.Op
		recBusVal := uint8(opEvent.Value)
		recEffAddr := ""
		recROMOff := ""
		if opEvent.Addr >= 0x080000 {
			recEffAddr = fmt.Sprintf("$%06X", opEvent.Addr)
			if off, ok := analysis.LoROMToOffset(opEvent.Addr, len(romBytes)); ok {
				recROMOff = fmt.Sprintf("$%06X", off)
			}
		} else {
			recEffAddr = fmt.Sprintf("$%06X", 0x7E0000|(opEvent.Addr&0xFFFF))
		}

		recStep := LookupStepRecord{
			InputVal:         115,
			OperandEventID:   opEvent.ID,
			EffectiveAddress: recEffAddr,
			ROMOffset:        recROMOff,
			BusValue:         recBusVal,
			BusOp:            recBusOp,
			Reads:            baseEmuSteps[sIdx].Reads,
			Writes:           baseEmuSteps[sIdx].Writes,
			EntryA:           fmt.Sprintf("$%04X", rawInsn.Entry.A),
			EntryY:           fmt.Sprintf("$%04X", rawInsn.Entry.Y),
			EntryP:           fmt.Sprintf("$%02X", rawInsn.Entry.P),
			ExitA:            fmt.Sprintf("$%04X", rawInsn.Exit.A),
			ExitY:            fmt.Sprintf("$%04X", rawInsn.Exit.Y),
			ExitP:            fmt.Sprintf("$%02X", rawInsn.Exit.P),
			ExitPC:           fmt.Sprintf("$%04X", rawInsn.Exit.PC),
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

		var predSteps []LookupPredictRecord
		for cIdx := 1; cIdx < 3; cIdx++ {
			pStep := emuStepsByCase[cIdx][sIdx]
			inVal := testSpecs[cIdx].InputWRAM05

			predBusOp := ""
			predEffAddr := ""
			predROMOff := ""
			var predBusVal uint8

			if len(pStep.Reads) > 0 {
				predBusOp = "read"
				r := pStep.Reads[0]
				predBusVal = r.Value
				predEffAddr = fmt.Sprintf("$%06X", r.Address)
				if off, ok := analysis.LoROMToOffset(r.Address, len(romBytes)); ok {
					predROMOff = fmt.Sprintf("$%06X", off)
				}
			} else if len(pStep.Writes) > 0 {
				predBusOp = "write"
				w := pStep.Writes[0]
				predBusVal = w.Value
				predEffAddr = fmt.Sprintf("$%06X", w.Address)
			}

			predSteps = append(predSteps, LookupPredictRecord{
				InputVal:         inVal,
				EffectiveAddress: predEffAddr,
				ROMOffset:        predROMOff,
				BusValue:         predBusVal,
				BusOp:            predBusOp,
				Reads:            pStep.Reads,
				Writes:           pStep.Writes,
				EntryA:           fmt.Sprintf("$%04X", pStep.EntryState.A),
				EntryY:           fmt.Sprintf("$%04X", pStep.EntryState.Y),
				EntryP:           fmt.Sprintf("$%02X", pStep.EntryState.P),
				ExitA:            fmt.Sprintf("$%04X", pStep.ExitState.A),
				ExitY:            fmt.Sprintf("$%04X", pStep.ExitState.Y),
				ExitP:            fmt.Sprintf("$%02X", pStep.ExitState.P),
				ExitPC:           fmt.Sprintf("$%04X", pStep.ExitState.PC),
				State:            pStep.ExitState,
			})
		}

		timelineSteps = append(timelineSteps, LookupTimelineStep{
			StepIndex:   sIdx + 1,
			Address:     stepAddrs[sIdx],
			Mnemonic:    stepMnemonics[sIdx],
			Recorded:    recStep,
			Predictions: predSteps,
		})
	}

	// 9. Write output artifacts
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create out dir %s: %w", *outDir, err)
	}

	caseData := map[string]any{
		"case_id":               "lookup-replay-09f882",
		"stream_sha256":         streamSHA,
		"rom_sha256":            romSHA,
		"block_address":         "$09F882",
		"predecessor_event_id":  52073,
		"retirement_event_ids":  []uint64{52077, 52082, 52086},
		"physical_read_address": "$7E1F05",
		"physical_write_addr":   "$7E1F54",
		"instructions":          blockInstructions,
		"initial_cpu_state":     initState,
		"replay_cases":          cBatchCases,
		"expected_specs":        testSpecs,
		"cases":                 testSpecs,
	}
	if err := writeJSON(filepath.Join(*outDir, "case.json"), caseData); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(*outDir, "generated.c"), []byte(cCode), 0644); err != nil {
		return fmt.Errorf("write generated.c: %w", err)
	}

	if err := writeJSON(filepath.Join(*outDir, "timeline.json"), timelineSteps); err != nil {
		return err
	}

	statusStr := "success"
	if !allVerified {
		statusStr = "verification_failure"
	}

	receipt := LookupReceipt{
		Status:               statusStr,
		StreamSHA256:         streamSHA,
		ROMSHA256:            romSHA,
		BlockAddress:         "$09F882",
		PredecessorEventID:   52073,
		PredecessorSeq:       13198,
		RetirementEventIDs:   []uint64{52077, 52082, 52086},
		RetirementSeqs:       []uint64{13199, 13200, 13201},
		PhysicalWriteAddr:    "$7E1F54",
		DualBackendVerified:  allDualMatch,
		BaselineRawVerified:  baselineRawVerified,
		SingleWriteVerified:  allSingleWrite,
		StepAccessesVerified: stepAccessesVerified,
		Results:              caseResults,
	}

	if err := writeJSON(filepath.Join(*outDir, "receipt.json"), receipt); err != nil {
		return err
	}

	if !allVerified {
		return fmt.Errorf("lookup-replay verification failed: dual match=%v, expected match=%v, no refusal=%v, single write=%v, baseline raw=%v, step accesses=%v",
			allDualMatch, allExpectedMatch, allNoRefusal, allSingleWrite, baselineRawVerified, stepAccessesVerified)
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(receipt)
	}

	fmt.Fprintf(stdout, "ROM Lookup Counterfactual Replay Verification ($09F882):\n")
	fmt.Fprintf(stdout, "  Stream SHA-256:         %s\n", streamSHA)
	fmt.Fprintf(stdout, "  ROM SHA-256:            %s\n", romSHA)
	fmt.Fprintf(stdout, "  Dual-Backend Verified:  %v (Go CPU vs Compiled C)\n", allDualMatch)
	fmt.Fprintf(stdout, "  Single Write Verified:  %v ($7E1F54)\n", allSingleWrite)
	fmt.Fprintf(stdout, "  Baseline Raw Verified:  %v\n", baselineRawVerified)
	fmt.Fprintf(stdout, "  Results:\n")
	for _, res := range caseResults {
		fmt.Fprintf(stdout, "    [%s] input $1F05=%d -> A=%s Y=%d Write %s=%d (Emu=%s C=%s match=%v expected=%v)\n",
			res.Kind, res.InputWRAM05, res.EmuFullA, res.EmuY, res.WriteAddress, res.WriteValue, res.EmuSuccessorPC, res.CSuccessorPC, res.EmuMatchesC, res.MatchesExpected)
	}
	fmt.Fprintf(stdout, "  Artifacts saved to:     %s\n", *outDir)

	return nil
}
