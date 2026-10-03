package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

const (
	pinnedStreamSHA256 = "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421"
	pinnedROMSHA256    = "66871d66be19ad2c34c927d6b14cd8eb6fc3181965b6e517cb361f7316009cfb"
)

func runReplaySlice(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm replay-slice", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		projectDir  = fs.String("project", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project", "path to project directory with recovery.json")
		tracePath   = fs.String("trace", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl", "path to trace.jsonl")
		eventsRange = fs.String("events", "139205:139220", "trace event interval A:B")
		probePath   = fs.String("probe", "", "path to probe json fallback (only used if -trace is empty)")
		romPath     = fs.String("rom", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc", "path to ROM file")
		outDir      = fs.String("out", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-139220-genuine-delivery", "output directory for case, manifest, generated C, and receipt")
	)

	if err := fs.Parse(args); err != nil {
		return err
	}

	startEvent, endEvent, err := parseEventRange(*eventsRange)
	if err != nil {
		return fmt.Errorf("invalid -events %q: %w", *eventsRange, err)
	}

	ctx := context.Background()

	// 1. Read and validate actual ROM
	if *romPath == "" {
		return fmt.Errorf("missing required -rom flag")
	}
	romBytes, err := os.ReadFile(*romPath)
	if err != nil {
		return fmt.Errorf("read ROM %s: %w", *romPath, err)
	}
	romSum := sha256.Sum256(romBytes)
	actualROMSHA := hex.EncodeToString(romSum[:])
	if actualROMSHA != pinnedROMSHA256 {
		return fmt.Errorf("rom sha256 %s does not match expected pinned %s", actualROMSHA, pinnedROMSHA256)
	}

	// 2. Load and validate active project document
	if *projectDir == "" {
		return fmt.Errorf("missing required -project flag")
	}
	doc, err := loadDoc(*projectDir)
	if err != nil {
		return fmt.Errorf("load project document %s: %w", *projectDir, err)
	}
	if doc.ROM.NormalizedSHA256 != "" && doc.ROM.NormalizedSHA256 != actualROMSHA {
		return fmt.Errorf("project doc ROM SHA256 %s differs from actual ROM %s", doc.ROM.NormalizedSHA256, actualROMSHA)
	}

	// Index document instructions by 24-bit address
	docInstByAddr := make(map[uint32]recovery.Instruction, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		docInstByAddr[inst.Address] = inst
	}

	// 3. Load and hash events from trace or explicit probe fallback
	events, streamSHA, err := loadAndHashEvents(*tracePath, *probePath, startEvent, endEvent)
	if err != nil {
		return fmt.Errorf("load trace events: %w", err)
	}

	// 4. Validate raw bus and retirement events
	var (
		insnEvents []trace.Event
		busReads   []trace.Event
		busWrites  []trace.Event
	)
	for _, e := range events {
		switch e.Kind {
		case "cpu_insn":
			insnEvents = append(insnEvents, e)
		case "bus":
			if e.Space == "wram" || e.Space == "ram" || e.Op == "write" {
				if e.Op == "read" {
					busReads = append(busReads, e)
				} else if e.Op == "write" {
					busWrites = append(busWrites, e)
				}
			}
		}
	}

	if len(insnEvents) == 0 {
		return fmt.Errorf("no cpu_insn retirement events found in event range %d..%d", startEvent, endEvent)
	}

	firstInsn := insnEvents[0]
	lastInsn := insnEvents[len(insnEvents)-1]

	// Find and validate the memory input read (event 139209)
	if len(busReads) == 0 {
		return fmt.Errorf("no data bus read observed in event range %d..%d", startEvent, endEvent)
	}
	inputBusEvent := busReads[0]
	inputBusPC := (uint32(inputBusEvent.PC.Bank) << 16) | uint32(inputBusEvent.PC.Addr)
	// Align insnEvents to the target block starting at inputBusPC
	var blockInsnEvents []trace.Event
	foundStart := false
	for _, ie := range insnEvents {
		entryAddr := (uint32(ie.Insn.Entry.PB) << 16) | uint32(ie.Insn.Entry.PC)
		if entryAddr == inputBusPC {
			foundStart = true
		}
		if foundStart {
			blockInsnEvents = append(blockInsnEvents, ie)
		}
	}
	if len(blockInsnEvents) > 0 {
		insnEvents = blockInsnEvents
	}
	firstInsn = insnEvents[0]
	firstInsnEntryAddr := (uint32(firstInsn.Insn.Entry.PB) << 16) | uint32(firstInsn.Insn.Entry.PC)
	if inputBusPC != firstInsnEntryAddr {
		return fmt.Errorf("input bus read PC $%06X does not match first instruction entry PC $%06X", inputBusPC, firstInsnEntryAddr)
	}
	if inputBusEvent.Cycle < firstInsn.Cycle || inputBusEvent.Cycle > firstInsn.Insn.Exit.Cycles {
		return fmt.Errorf("input bus read cycle %d outside first instruction window [%d, %d]",
			inputBusEvent.Cycle, firstInsn.Cycle, firstInsn.Insn.Exit.Cycles)
	}

	// Find and validate the memory write (event 139219)
	if len(busWrites) == 0 {
		return fmt.Errorf("no data bus write observed in event range %d..%d", startEvent, endEvent)
	}
	outputBusEvent := busWrites[len(busWrites)-1]
	lastInsnEntryAddr := (uint32(lastInsn.Insn.Entry.PB) << 16) | uint32(lastInsn.Insn.Entry.PC)
	outputBusPC := (uint32(outputBusEvent.PC.Bank) << 16) | uint32(outputBusEvent.PC.Addr)
	if outputBusPC != lastInsnEntryAddr {
		return fmt.Errorf("output bus write PC $%06X does not match last instruction entry PC $%06X", outputBusPC, lastInsnEntryAddr)
	}
	if outputBusEvent.Cycle < lastInsn.Cycle || outputBusEvent.Cycle > lastInsn.Insn.Exit.Cycles {
		return fmt.Errorf("output bus write cycle %d outside last instruction window [%d, %d]",
			outputBusEvent.Cycle, lastInsn.Cycle, lastInsn.Insn.Exit.Cycles)
	}

	// Derive canonical memory addresses
	inputCanonicalAddr := uint32(0x7E0000 | (inputBusEvent.Addr & 0x1FFFF))
	outputCanonicalAddr := uint32(0x7E0000 | (outputBusEvent.Addr & 0x1FFFF))
	inputValue := uint8(inputBusEvent.Value)
	recordedWriteValue := uint8(outputBusEvent.Value)

	// 5. Look up instructions in canonical recovery document
	var blockInstructions []recovery.Instruction
	for _, ie := range insnEvents {
		addr := (uint32(ie.Insn.Entry.PB) << 16) | uint32(ie.Insn.Entry.PC)
		docInst, ok := docInstByAddr[addr]
		if !ok {
			return fmt.Errorf("instruction at address $%06X not found in project document", addr)
		}
		if len(ie.Insn.Fetches) > 0 && docInst.Opcode != ie.Insn.Fetches[0].Value {
			return fmt.Errorf("instruction opcode mismatch at $%06X: document has 0x%02X, trace fetch has 0x%02X",
				addr, docInst.Opcode, ie.Insn.Fetches[0].Value)
		}
		blockInstructions = append(blockInstructions, docInst)
	}

	firstAddr := (uint32(firstInsn.Insn.Entry.PB) << 16) | uint32(firstInsn.Insn.Entry.PC)
	lastExitAddr := (uint32(lastInsn.Insn.SuccessorPC.Bank) << 16) | uint32(lastInsn.Insn.SuccessorPC.Addr)

	block := &structure.BasicBlock{
		ID:           fmt.Sprintf("block-%06x-%dinsn", firstAddr, len(blockInstructions)),
		StartAddress: firstAddr,
		EndAddress:   lastExitAddr,
		Instructions: blockInstructions,
		Successors:   []uint32{lastExitAddr},
	}

	// 6. Lift block into IR
	entryCtx := blockInstructions[0].Context
	ir, err := decomp.LiftBlock(block, entryCtx)
	if err != nil {
		return fmt.Errorf("lift block: %w", err)
	}

	// 7. Generate compilable C
	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		return fmt.Errorf("generate compilable C: %w", err)
	}

	// 8. Derive full entry CPUState directly from first retirement record
	initState := decomp.CPUState{
		A:  firstInsn.Insn.Entry.A,
		X:  firstInsn.Insn.Entry.X,
		Y:  firstInsn.Insn.Entry.Y,
		S:  firstInsn.Insn.Entry.S,
		D:  firstInsn.Insn.Entry.D,
		DB: firstInsn.Insn.Entry.DB,
		PB: firstInsn.Insn.Entry.PB,
		PC: firstInsn.Insn.Entry.PC,
		P:  firstInsn.Insn.Entry.P,
		E:  firstInsn.Insn.Entry.E,
	}

	// Derive recorded full exit CPUState directly from last retirement record
	recordedExitState := decomp.CPUState{
		A:  lastInsn.Insn.Exit.A,
		X:  lastInsn.Insn.Exit.X,
		Y:  lastInsn.Insn.Exit.Y,
		S:  lastInsn.Insn.Exit.S,
		D:  lastInsn.Insn.Exit.D,
		DB: lastInsn.Insn.Exit.DB,
		PB: lastInsn.Insn.Exit.PB,
		PC: lastInsn.Insn.Exit.PC,
		P:  lastInsn.Insn.Exit.P,
		E:  lastInsn.Insn.Exit.E,
	}
	recordedNextPC := lastExitAddr
	recordedWrites := []decomp.MemoryWrite{
		{Address: outputCanonicalAddr, Value: recordedWriteValue},
	}

	// 9. Execute Baseline on Go 65816 CPU Emulator
	baselineMem := map[uint32]uint8{
		inputCanonicalAddr:  inputValue,
		inputBusEvent.Addr: inputValue,
	}
	emuBaselineResult, err := decomp.RunEmulatorBlock(ctx, ir, initState, baselineMem)
	if err != nil {
		return fmt.Errorf("run emulator baseline: %w", err)
	}

	// 10. Execute Baseline & Variants on Compiled C Runner
	compiledRunner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		return fmt.Errorf("new compiled runner: %w", err)
	}
	defer compiledRunner.Close()

	cBatchCases := []decomp.ReplayCaseInput{
		{
			CaseID:  fmt.Sprintf("baseline-%d", inputValue),
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: inputCanonicalAddr, Value: inputValue},
			},
		},
		{
			CaseID:  "perturbation-114",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: inputCanonicalAddr, Value: 114},
			},
		},
		{
			CaseID:  "perturbation-116",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: inputCanonicalAddr, Value: 116},
			},
		},
	}

	cResults, err := compiledRunner.RunBatch(ctx, cBatchCases)
	if err != nil {
		return fmt.Errorf("compiled runner run batch: %w", err)
	}
	if len(cResults) != 3 {
		return fmt.Errorf("expected 3 compiled runner results, got %d", len(cResults))
	}
	cBaselineResult := cResults[0]

	// 11. Execute Counterfactuals on Go Emulator
	emu114Mem := map[uint32]uint8{inputCanonicalAddr: 114, inputBusEvent.Addr: 114}
	emu114Result, err := decomp.RunEmulatorBlock(ctx, ir, initState, emu114Mem)
	if err != nil {
		return fmt.Errorf("run emulator 114: %w", err)
	}

	emu116Mem := map[uint32]uint8{inputCanonicalAddr: 116, inputBusEvent.Addr: 116}
	emu116Result, err := decomp.RunEmulatorBlock(ctx, ir, initState, emu116Mem)
	if err != nil {
		return fmt.Errorf("run emulator 116: %w", err)
	}

	// 12. Complete full noncycle CPU field comparisons across all three backends
	recVsEmuState := compareFullState(recordedExitState, emuBaselineResult.State, recordedNextPC, emuBaselineResult.NextPC)
	recVsCState := compareFullState(recordedExitState, cBaselineResult.State, recordedNextPC, cBaselineResult.NextPC)
	emuVsCState := compareFullState(emuBaselineResult.State, cBaselineResult.State, emuBaselineResult.NextPC, cBaselineResult.NextPC)

	recVsEmuWritesMatch, recVsEmuWritesErr := compareWrites(recordedWrites, emuBaselineResult.Writes)
	recVsCWritesMatch, recVsCWritesErr := compareWrites(recordedWrites, cBaselineResult.Writes)
	emuVsCWritesMatch, emuVsCWritesErr := compareWrites(emuBaselineResult.Writes, cBaselineResult.Writes)

	// Check counterfactual dual-backend agreement
	v114WritesMatch, _ := compareWrites(emu114Result.Writes, cResults[1].Writes)
	v114StateMatch := compareFullState(emu114Result.State, cResults[1].State, emu114Result.NextPC, cResults[1].NextPC).Match
	v114DualAgree := v114WritesMatch && v114StateMatch

	v116WritesMatch, _ := compareWrites(emu116Result.Writes, cResults[2].Writes)
	v116StateMatch := compareFullState(emu116Result.State, cResults[2].State, emu116Result.NextPC, cResults[2].NextPC).Match
	v116DualAgree := v116WritesMatch && v116StateMatch

	allVerified := recVsEmuState.Match && recVsCState.Match && emuVsCState.Match &&
		recVsEmuWritesMatch && recVsCWritesMatch && emuVsCWritesMatch &&
		v114DualAgree && v116DualAgree

	// 13. Create output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// 14. Save case.json
	replayCase := decomp.ReplayCase{
		SchemaVersion:          "snes-routine-case-v2",
		CaseID:                 fmt.Sprintf("case-%d-genuine-wram-counter", lastInsn.ID),
		BlockID:                block.ID,
		StreamSHA256:           streamSHA,
		ROMSHA256:              actualROMSHA,
		Frame:                  firstInsn.Frame,
		EntrySeq:               firstInsn.Insn.Seq,
		ExitSeq:                lastInsn.Insn.Seq,
		StartEventID:           startEvent,
		EndEventID:             endEvent,
		StartCycle:             firstInsn.Cycle,
		EndCycle:               lastInsn.Insn.Exit.Cycles,
		InitialState:           initState,
		InitialMemory: []decomp.MemoryCell{
			{Address: inputCanonicalAddr, Value: inputValue},
		},
		ObservedExit:           recordedExitState,
		ObservedNextPC:         recordedNextPC,
		ObservedEffectsCapture: false,
		AdmissionDigest:        "",
		ObservedWrites:         recordedWrites,
	}

	caseBytes, err := json.MarshalIndent(replayCase, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal case: %w", err)
	}
	casePath := filepath.Join(*outDir, "case.json")
	if err := os.WriteFile(casePath, caseBytes, 0644); err != nil {
		return fmt.Errorf("write case.json: %w", err)
	}

	// 15. Save generated.c
	cPath := filepath.Join(*outDir, "generated.c")
	if err := os.WriteFile(cPath, []byte(cCode), 0644); err != nil {
		return fmt.Errorf("write generated.c: %w", err)
	}

	// Helper for safe single write extraction
	safeFirstWrite := func(writes []decomp.MemoryWrite) any {
		if len(writes) > 0 {
			return writes[0]
		}
		return nil
	}

	// 16. Save receipt.json
	receipt := map[string]any{
		"schema":                   "snes-139220-receipt-v1",
		"generated_at":             time.Now().UTC().Format(time.RFC3339),
		"block_id":                 block.ID,
		"start_address":            block.StartAddress,
		"end_address":              block.EndAddress,
		"observed_effects_capture": false,
		"captured_proof_eligible":  false,
		"admission_digest":         "",
		"entry_state": map[string]any{
			"a":                        initState.A,
			"a_hex":                    fmt.Sprintf("0x%04X", initState.A),
			"architectural_high_a":     (initState.A >> 8) & 0xFF,
			"architectural_high_a_hex": fmt.Sprintf("0x%02X", (initState.A>>8)&0xFF),
			"x":                        initState.X,
			"y":                        initState.Y,
			"s":                        initState.S,
			"d":                        initState.D,
			"db":                       initState.DB,
			"pb":                       initState.PB,
			"pc":                       initState.PC,
			"pc_hex":                   fmt.Sprintf("0x%04X", initState.PC),
			"p":                        initState.P,
			"p_hex":                    fmt.Sprintf("0x%02X", initState.P),
			"e":                        initState.E,
		},
		"exit_state": map[string]any{
			"a":                        emuBaselineResult.State.A,
			"a_hex":                    fmt.Sprintf("0x%04X", emuBaselineResult.State.A),
			"architectural_high_a":     (emuBaselineResult.State.A >> 8) & 0xFF,
			"architectural_high_a_hex": fmt.Sprintf("0x%02X", (emuBaselineResult.State.A>>8)&0xFF),
			"x":                        emuBaselineResult.State.X,
			"y":                        emuBaselineResult.State.Y,
			"s":                        emuBaselineResult.State.S,
			"d":                        emuBaselineResult.State.D,
			"db":                       emuBaselineResult.State.DB,
			"pb":                       emuBaselineResult.State.PB,
			"pc":                       emuBaselineResult.State.PC,
			"pc_hex":                   fmt.Sprintf("0x%04X", emuBaselineResult.State.PC),
			"p":                        emuBaselineResult.State.P,
			"p_hex":                    fmt.Sprintf("0x%02X", emuBaselineResult.State.P),
			"e":                        emuBaselineResult.State.E,
			"next_pc":                  emuBaselineResult.NextPC,
			"next_pc_hex":              fmt.Sprintf("0x%06X", emuBaselineResult.NextPC),
		},
		"compare_states": map[string]any{
			"recorded_vs_emulator":   recVsEmuState,
			"recorded_vs_compiled_c": recVsCState,
			"emulator_vs_compiled_c": emuVsCState,
		},
		"compare_writes": map[string]any{
			"recorded_vs_emulator": map[string]any{
				"match":           recVsEmuWritesMatch,
				"error":           recVsEmuWritesErr,
				"recorded_writes": recordedWrites,
				"backend_writes":  emuBaselineResult.Writes,
			},
			"recorded_vs_compiled_c": map[string]any{
				"match":           recVsCWritesMatch,
				"error":           recVsCWritesErr,
				"recorded_writes": recordedWrites,
				"backend_writes":  cBaselineResult.Writes,
			},
			"emulator_vs_compiled_c": map[string]any{
				"match":        emuVsCWritesMatch && emuVsCState.Match,
				"error":        emuVsCWritesErr,
				"state_match":  emuVsCState.Match,
				"writes_match": emuVsCWritesMatch,
			},
		},
		"counterfactual_predictions": []map[string]any{
			{
				"input_value":           114,
				"status":                "unrecorded_prediction",
				"emulator_exit_a":       emu114Result.State.A,
				"emulator_exit_a_hex":   fmt.Sprintf("0x%04X", emu114Result.State.A),
				"emulator_write":        safeFirstWrite(emu114Result.Writes),
				"compiled_c_exit_a":     cResults[1].State.A,
				"compiled_c_exit_a_hex": fmt.Sprintf("0x%04X", cResults[1].State.A),
				"compiled_c_write":      safeFirstWrite(cResults[1].Writes),
				"dual_backends_agree":   v114DualAgree,
			},
			{
				"input_value":           int(inputValue),
				"status":                "recorded_baseline",
				"emulator_exit_a":       emuBaselineResult.State.A,
				"emulator_exit_a_hex":   fmt.Sprintf("0x%04X", emuBaselineResult.State.A),
				"emulator_write":        safeFirstWrite(emuBaselineResult.Writes),
				"compiled_c_exit_a":     cBaselineResult.State.A,
				"compiled_c_exit_a_hex": fmt.Sprintf("0x%04X", cBaselineResult.State.A),
				"compiled_c_write":      safeFirstWrite(cBaselineResult.Writes),
				"dual_backends_agree":   emuVsCState.Match && emuVsCWritesMatch,
			},
			{
				"input_value":           116,
				"status":                "unrecorded_prediction",
				"emulator_exit_a":       emu116Result.State.A,
				"emulator_exit_a_hex":   fmt.Sprintf("0x%04X", emu116Result.State.A),
				"emulator_write":        safeFirstWrite(emu116Result.Writes),
				"compiled_c_exit_a":     cResults[2].State.A,
				"compiled_c_exit_a_hex": fmt.Sprintf("0x%04X", cResults[2].State.A),
				"compiled_c_write":      safeFirstWrite(cResults[2].Writes),
				"dual_backends_agree":   v116DualAgree,
			},
		},
		"verified": allVerified,
	}

	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	receiptPath := filepath.Join(*outDir, "receipt.json")
	if err := os.WriteFile(receiptPath, receiptBytes, 0644); err != nil {
		return fmt.Errorf("write receipt.json: %w", err)
	}

	// 17. Save manifest.json
	caseSHA := sha256Hex(caseBytes)
	cSHA := sha256Hex([]byte(cCode))
	receiptSHA := sha256Hex(receiptBytes)

	manifest := map[string]any{
		"schema":          "snes-delivery-manifest-v1",
		"generated_at":    time.Now().UTC().Format(time.RFC3339),
		"target_event_id": lastInsn.ID,
		"retirement_span": map[string]any{
			"start_event":                           startEvent,
			"end_event":                             endEvent,
			"start_cycle":                           firstInsn.Cycle,
			"end_cycle":                             lastInsn.Insn.Exit.Cycles,
			"first_retirement":                      firstInsn.ID,
			"input_event_id":                        inputBusEvent.ID,
			"input_cycle":                           inputBusEvent.Cycle,
			"input_ordered_before_first_retirement": inputBusEvent.ID < firstInsn.ID,
		},
		"bindings": map[string]any{
			"trace_sha256":      streamSHA,
			"rom_sha256":        actualROMSHA,
			"project_directory": *projectDir,
			"document_block_id": block.ID,
			"bus_pc_range":      fmt.Sprintf("$%06X..$%06X", firstAddr, lastExitAddr),
		},
		"artifacts": []map[string]any{
			{"path": "case.json", "sha256": caseSHA, "kind": "replay_case"},
			{"path": "generated.c", "sha256": cSHA, "kind": "compilable_c"},
			{"path": "receipt.json", "sha256": receiptSHA, "kind": "comparison_receipt"},
		},
		"evidence_boundaries": map[string]any{
			"observed_effects_capture": false,
			"captured_proof_eligible":  false,
			"admission_digest":         "",
		},
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(*outDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
		return fmt.Errorf("write manifest.json: %w", err)
	}

	// 18. Output computed JSON summary
	statusStr := "success"
	if !allVerified {
		statusStr = "verification_failure"
	}

	predSummary := make(map[string]int)
	if len(cResults[1].Writes) > 0 {
		predSummary["input_114"] = int(cResults[1].Writes[0].Value)
	}
	if len(cResults[2].Writes) > 0 {
		predSummary["input_116"] = int(cResults[2].Writes[0].Value)
	}

	summary := map[string]any{
		"status":                statusStr,
		"command":               "snesdasm replay-slice",
		"output_dir":            *outDir,
		"artifacts":             []string{casePath, manifestPath, cPath, receiptPath},
		"dual_backend_verified": allVerified,
		"baseline_input":        inputValue,
		"baseline_write":        recordedWriteValue,
		"predictions":           predSummary,
	}

	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		return err
	}

	if !allVerified {
		return fmt.Errorf("trace replay verification failed: state or write divergence detected")
	}

	return nil
}

func parseEventRange(s string) (start, end uint64, err error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected format START:END")
	}
	start, err = strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start event: %w", err)
	}
	end, err = strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end event: %w", err)
	}
	if start > end {
		return 0, 0, fmt.Errorf("start event %d > end event %d", start, end)
	}
	return start, end, nil
}

func loadAndHashEvents(tracePath, probePath string, startEvent, endEvent uint64) ([]trace.Event, string, error) {
	if tracePath != "" {
		f, err := os.Open(tracePath)
		if err != nil {
			return nil, "", fmt.Errorf("open trace file %s: %w", tracePath, err)
		}
		defer f.Close()

		hasher := sha256.New()
		tee := io.TeeReader(f, hasher)
		scanner := bufio.NewScanner(tee)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 16*1024*1024)

		var events []trace.Event
		for scanner.Scan() {
			line := scanner.Bytes()
			var header struct {
				ID uint64 `json:"id"`
			}
			if err := json.Unmarshal(line, &header); err != nil {
				return nil, "", fmt.Errorf("decode trace line header: %w", err)
			}
			if header.ID >= startEvent && header.ID <= endEvent {
				var ev trace.Event
				if err := json.Unmarshal(line, &ev); err != nil {
					return nil, "", fmt.Errorf("decode trace event %d: %w", header.ID, err)
				}
				events = append(events, ev)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", fmt.Errorf("scan trace stream: %w", err)
		}

		streamSHA := hex.EncodeToString(hasher.Sum(nil))
		if len(events) == 0 {
			return nil, "", fmt.Errorf("no events matched range %d..%d in trace %s", startEvent, endEvent, tracePath)
		}
		return events, streamSHA, nil
	}

	if probePath != "" {
		b, err := os.ReadFile(probePath)
		if err != nil {
			return nil, "", fmt.Errorf("read probe json %s: %w", probePath, err)
		}
		var probe struct {
			TraceSHA256 string        `json:"trace_sha256"`
			Events      []trace.Event `json:"selected_raw_events"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, "", fmt.Errorf("unmarshal probe json: %w", err)
		}
		var filtered []trace.Event
		for _, e := range probe.Events {
			if e.ID >= startEvent && e.ID <= endEvent {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			return nil, "", fmt.Errorf("no events matched range %d..%d in probe %s", startEvent, endEvent, probePath)
		}
		sha := probe.TraceSHA256
		if sha == "" {
			sha = pinnedStreamSHA256
		}
		return filtered, sha, nil
	}

	return nil, "", fmt.Errorf("neither -trace nor -probe path provided")
}

type StateComparison struct {
	Match       bool     `json:"match"`
	AMatch      bool     `json:"a_match"`
	HighAMatch  bool     `json:"high_a_match"`
	LowAMatch   bool     `json:"low_a_match"`
	XMatch      bool     `json:"x_match"`
	YMatch      bool     `json:"y_match"`
	SMatch      bool     `json:"s_match"`
	DMatch      bool     `json:"d_match"`
	DBMatch     bool     `json:"db_match"`
	PBMatch     bool     `json:"pb_match"`
	PMatch      bool     `json:"p_match"`
	EMatch      bool     `json:"e_match"`
	PCMatch     bool     `json:"pc_match"`
	NextPCMatch bool     `json:"next_pc_match"`
	Mismatches  []string `json:"mismatches,omitempty"`
}

func compareFullState(expected, actual decomp.CPUState, expectedNextPC, actualNextPC uint32) StateComparison {
	c := StateComparison{
		AMatch:      expected.A == actual.A,
		HighAMatch:  (expected.A >> 8) == (actual.A >> 8),
		LowAMatch:   (expected.A & 0xFF) == (actual.A & 0xFF),
		XMatch:      expected.X == actual.X,
		YMatch:      expected.Y == actual.Y,
		SMatch:      expected.S == actual.S,
		DMatch:      expected.D == actual.D,
		DBMatch:     expected.DB == actual.DB,
		PBMatch:     expected.PB == actual.PB,
		PMatch:      expected.P == actual.P,
		EMatch:      expected.E == actual.E,
		PCMatch:     expected.PC == actual.PC,
		NextPCMatch: expectedNextPC == actualNextPC,
	}
	if !c.AMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("A: want 0x%04X, got 0x%04X", expected.A, actual.A))
	}
	if !c.XMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("X: want 0x%04X, got 0x%04X", expected.X, actual.X))
	}
	if !c.YMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("Y: want 0x%04X, got 0x%04X", expected.Y, actual.Y))
	}
	if !c.SMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("S: want 0x%04X, got 0x%04X", expected.S, actual.S))
	}
	if !c.DMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("D: want 0x%04X, got 0x%04X", expected.D, actual.D))
	}
	if !c.DBMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("DB: want 0x%02X, got 0x%02X", expected.DB, actual.DB))
	}
	if !c.PBMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("PB: want 0x%02X, got 0x%02X", expected.PB, actual.PB))
	}
	if !c.PMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("P: want 0x%02X, got 0x%02X", expected.P, actual.P))
	}
	if !c.EMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("E: want %v, got %v", expected.E, actual.E))
	}
	if !c.PCMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("PC: want 0x%04X, got 0x%04X", expected.PC, actual.PC))
	}
	if !c.NextPCMatch {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf("NextPC: want 0x%06X, got 0x%06X", expectedNextPC, actualNextPC))
	}
	c.Match = len(c.Mismatches) == 0
	return c
}

func compareWrites(expected, actual []decomp.MemoryWrite) (bool, string) {
	if len(expected) != len(actual) {
		return false, fmt.Sprintf("write count mismatch: expected %d, got %d", len(expected), len(actual))
	}
	for i := range expected {
		if expected[i].Address != actual[i].Address || expected[i].Value != actual[i].Value {
			return false, fmt.Sprintf("write[%d] mismatch: expected $%06X=%d, got $%06X=%d",
				i, expected[i].Address, expected[i].Value, actual[i].Address, actual[i].Value)
		}
	}
	return true, ""
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
