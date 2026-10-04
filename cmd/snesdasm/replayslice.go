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

type RunHeaderInfo struct {
	ID                 uint64   `json:"id"`
	Schema             int      `json:"schema"`
	Kind               string   `json:"kind"`
	Frame              uint64   `json:"frame"`
	ROMSHA256          string   `json:"rom_sha256"`
	Mapper             string   `json:"mapper"`
	EngineRevision     string   `json:"engine_revision"`
	Start              string   `json:"start"`
	InitialStateSHA256 string   `json:"initial_state_sha256"`
	Events             []string `json:"events,omitempty"`
}

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

	// 1. Read and validate actual ROM bytes and content hash
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

	// 2. Load, hash, and join-validate active project document
	if *projectDir == "" {
		return fmt.Errorf("missing required -project flag")
	}
	docPath := filepath.Join(*projectDir, "recovery.json")
	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		return fmt.Errorf("read project document %s: %w", docPath, err)
	}
	docSHA256 := sha256Hex(docBytes)

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return fmt.Errorf("load project document %s: %w", *projectDir, err)
	}
	if doc.ROM.NormalizedSHA256 != actualROMSHA {
		return fmt.Errorf("project doc ROM SHA256 %s differs from actual ROM %s", doc.ROM.NormalizedSHA256, actualROMSHA)
	}
	if doc.ROM.OriginalSHA256 != actualROMSHA {
		return fmt.Errorf("project doc original ROM SHA256 %s differs from actual ROM %s", doc.ROM.OriginalSHA256, actualROMSHA)
	}
	if doc.ROM.Mapper != "lorom" {
		return fmt.Errorf("unsupported doc mapper %q, expected lorom", doc.ROM.Mapper)
	}

	// Index document instructions by (Address, Context) to avoid overwriting context variants
	type docContextKey struct {
		addr       uint32
		e, m, x, c string
	}
	docInstByContext := make(map[docContextKey]recovery.Instruction, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		k := docContextKey{
			addr: inst.Address,
			e:    inst.Context.E,
			m:    inst.Context.M,
			x:    inst.Context.X,
			c:    inst.Context.C,
		}
		if _, exists := docInstByContext[k]; exists {
			return fmt.Errorf("ambiguous duplicate instruction in document at $%06X with context %+v", inst.Address, k)
		}
		docInstByContext[k] = inst
	}

	// 3. Load and hash events from trace stream or explicit probe fallback
	isRetainedFixture := *tracePath == "" && *probePath != ""
	events, streamSHA, runHeader, fixtureSHA, err := loadAndHashEvents(*tracePath, *probePath, startEvent, endEvent)
	if err != nil {
		return fmt.Errorf("load trace events: %w", err)
	}

	// Validate run header joins when available
	if runHeader != nil {
		if runHeader.ROMSHA256 != "" && runHeader.ROMSHA256 != actualROMSHA {
			return fmt.Errorf("trace run header ROM SHA256 %s differs from actual ROM %s", runHeader.ROMSHA256, actualROMSHA)
		}
		if runHeader.Mapper != "" && runHeader.Mapper != doc.ROM.Mapper {
			return fmt.Errorf("trace run header mapper %s differs from document mapper %s", runHeader.Mapper, doc.ROM.Mapper)
		}
	}

	// Verify monotonic event ID ordering
	var lastBusCycle uint64
	for i, e := range events {
		if i > 0 && e.ID <= events[i-1].ID {
			return fmt.Errorf("trace events not strictly monotonic in ID: event %d followed by %d", events[i-1].ID, e.ID)
		}
		if e.Kind == "bus" {
			if e.Cycle < lastBusCycle {
				return fmt.Errorf("bus cycles not monotonic: event %d (cycle %d) < previous cycle %d", e.ID, e.Cycle, lastBusCycle)
			}
			lastBusCycle = e.Cycle
		}
	}
	if startEvent == 139205 {
		if len(events) == 0 || events[0].ID != 139205 {
			return fmt.Errorf("expected preceding instruction event 139205 at start of interval")
		}
	}

	// 4. Derive and validate raw bus and retirement events, enforcing exact in-span physical effects
	var (
		insnEvents         []trace.Event
		dataReads          []trace.Event
		dataWrites         []trace.Event
		mmioRefusedCount   int
		unsupportedRefused int
	)
	for _, e := range events {
		switch e.Kind {
		case "cpu_insn":
			insnEvents = append(insnEvents, e)
		case "bus":
			if e.Op != "read" && e.Op != "write" {
				return fmt.Errorf("refusal: unknown bus op %q on event %d", e.Op, e.ID)
			}
			if e.Space == "cpu" {
				// Code/operand fetch read
				if e.Op != "read" {
					return fmt.Errorf("refusal: non-read cpu-space bus op %q on event %d", e.Op, e.ID)
				}
				continue
			}
			if decomp.IsMMIOAddr(e.Addr) {
				mmioRefusedCount++
			}
			isWRAM := e.Space == "wram" || e.Space == "ram"
			if !isWRAM {
				unsupportedRefused++
			}
			if e.Width != 0 && e.Width != 1 {
				return fmt.Errorf("refusal: unsupported non-byte bus width %d on event %d", e.Width, e.ID)
			}
			if e.Value > 255 {
				return fmt.Errorf("refusal: out-of-range byte value %d on event %d", e.Value, e.ID)
			}
			if e.Op == "read" {
				dataReads = append(dataReads, e)
			} else if e.Op == "write" {
				dataWrites = append(dataWrites, e)
			}
		default:
			return fmt.Errorf("refusal: unsupported event kind %q on event %d", e.Kind, e.ID)
		}
	}

	if mmioRefusedCount > 0 {
		return fmt.Errorf("refusal: detected %d MMIO bus access(es) in span", mmioRefusedCount)
	}
	if unsupportedRefused > 0 {
		return fmt.Errorf("refusal: detected %d unsupported bus space access(es) in span", unsupportedRefused)
	}
	if len(dataReads) != 1 {
		return fmt.Errorf("refusal: expected exactly 1 data read in span, observed %d", len(dataReads))
	}
	if len(dataWrites) != 1 {
		return fmt.Errorf("refusal: expected exactly 1 data write in span, observed %d", len(dataWrites))
	}

	inputBusEvent := dataReads[0]
	outputBusEvent := dataWrites[0]

	// Find and align insnEvents to the target 4-instruction block starting at inputBusPC
	inputBusPC := (uint32(inputBusEvent.PC.Bank) << 16) | uint32(inputBusEvent.PC.Addr)
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
	if len(blockInsnEvents) != 4 {
		return fmt.Errorf("expected 4 instructions in target block, found %d", len(blockInsnEvents))
	}
	insnEvents = blockInsnEvents
	firstInsn := insnEvents[0]
	lastInsn := insnEvents[len(insnEvents)-1]

	// Verify CPU sequence continuity and single frame
	for i := 1; i < len(insnEvents); i++ {
		if insnEvents[i].Insn.Seq != insnEvents[i-1].Insn.Seq+1 {
			return fmt.Errorf("non-consecutive CPUSeq in block: %d followed by %d",
				insnEvents[i-1].Insn.Seq, insnEvents[i].Insn.Seq)
		}
		if insnEvents[i].Frame != insnEvents[i-1].Frame {
			return fmt.Errorf("cross-frame execution in block: frame %d to %d",
				insnEvents[i-1].Frame, insnEvents[i].Frame)
		}
		if insnEvents[i].Insn.Status != "retired" {
			return fmt.Errorf("instruction event %d has unretired status %q", insnEvents[i].ID, insnEvents[i].Insn.Status)
		}
	}

	// Validate preceding retirement event 139205 continuity
	if startEvent == 139205 {
		prec := events[0]
		if prec.Kind != "cpu_insn" || prec.Insn.Status != "retired" {
			return fmt.Errorf("preceding event 139205 is not a retired cpu_insn")
		}
		if prec.Insn.Seq != firstInsn.Insn.Seq-1 {
			return fmt.Errorf("preceding event Seq %d != first instruction Seq %d - 1", prec.Insn.Seq, firstInsn.Insn.Seq)
		}
		if prec.Frame != firstInsn.Frame {
			return fmt.Errorf("preceding event frame %d != first instruction frame %d", prec.Frame, firstInsn.Frame)
		}
		if prec.Insn.Exit.Cycles != firstInsn.Cycle {
			return fmt.Errorf("preceding exit cycle %d != first entry cycle %d", prec.Insn.Exit.Cycles, firstInsn.Cycle)
		}
		succAddr := (uint32(prec.Insn.SuccessorPC.Bank) << 16) | uint32(prec.Insn.SuccessorPC.Addr)
		firstAddrEntry := (uint32(firstInsn.Insn.Entry.PB) << 16) | uint32(firstInsn.Insn.Entry.PC)
		if succAddr != firstAddrEntry {
			return fmt.Errorf("preceding successor PC $%06X != first entry PC $%06X", succAddr, firstAddrEntry)
		}
		if prec.Insn.Exit.A != firstInsn.Insn.Entry.A || prec.Insn.Exit.P != firstInsn.Insn.Entry.P {
			return fmt.Errorf("preceding exit CPU state differs from first entry state")
		}
	}

	// Validate input read order & bounds
	firstInsnEntryAddr := (uint32(firstInsn.Insn.Entry.PB) << 16) | uint32(firstInsn.Insn.Entry.PC)
	if inputBusPC != firstInsnEntryAddr {
		return fmt.Errorf("input bus read PC $%06X does not match first instruction entry PC $%06X", inputBusPC, firstInsnEntryAddr)
	}
	if inputBusEvent.ID >= firstInsn.ID {
		return fmt.Errorf("input bus read event %d must be ordered before first instruction retirement %d",
			inputBusEvent.ID, firstInsn.ID)
	}
	if inputBusEvent.Cycle < firstInsn.Cycle || inputBusEvent.Cycle > firstInsn.Insn.Exit.Cycles {
		return fmt.Errorf("input bus read cycle %d outside first instruction window [%d, %d]",
			inputBusEvent.Cycle, firstInsn.Cycle, firstInsn.Insn.Exit.Cycles)
	}

	// Validate output write order & bounds
	lastInsnEntryAddr := (uint32(lastInsn.Insn.Entry.PB) << 16) | uint32(lastInsn.Insn.Entry.PC)
	outputBusPC := (uint32(outputBusEvent.PC.Bank) << 16) | uint32(outputBusEvent.PC.Addr)
	if outputBusPC != lastInsnEntryAddr {
		return fmt.Errorf("output bus write PC $%06X does not match last instruction entry PC $%06X", outputBusPC, lastInsnEntryAddr)
	}
	if outputBusEvent.ID > lastInsn.ID {
		return fmt.Errorf("output bus write event %d must be ordered within last instruction retirement %d",
			outputBusEvent.ID, lastInsn.ID)
	}
	if outputBusEvent.Cycle < lastInsn.Cycle || outputBusEvent.Cycle > lastInsn.Insn.Exit.Cycles {
		return fmt.Errorf("output bus write cycle %d outside last instruction window [%d, %d]",
			outputBusEvent.Cycle, lastInsn.Cycle, lastInsn.Insn.Exit.Cycles)
	}

	// Canonical memory address derivation and expectation checks
	inputCanonicalAddr := uint32(0x7E0000 | (inputBusEvent.Addr & 0x1FFFF))
	outputCanonicalAddr := uint32(0x7E0000 | (outputBusEvent.Addr & 0x1FFFF))
	if inputCanonicalAddr != 0x7E1F05 {
		return fmt.Errorf("unexpected input canonical address $%06X, expected $7E1F05", inputCanonicalAddr)
	}
	if outputCanonicalAddr != 0x7E1F05 {
		return fmt.Errorf("unexpected output canonical address $%06X, expected $7E1F05", outputCanonicalAddr)
	}
	inputValue := uint8(inputBusEvent.Value)
	recordedWriteValue := uint8(outputBusEvent.Value)
	if inputValue != 115 {
		return fmt.Errorf("unexpected input value %d, expected 115", inputValue)
	}
	if recordedWriteValue != 120 {
		return fmt.Errorf("unexpected recorded write value %d, expected 120", recordedWriteValue)
	}

	// 5. Look up instructions in document by context and verify all fetched bytes, offsets, and IDs against document and ROM
	var blockInstructions []recovery.Instruction
	for _, ie := range insnEvents {
		addr := (uint32(ie.Insn.Entry.PB) << 16) | uint32(ie.Insn.Entry.PC)
		eFlag := "clear"
		if ie.Insn.Entry.E {
			eFlag = "set"
		}
		mFlag := "clear"
		if ie.Insn.Entry.P&0x20 != 0 {
			mFlag = "set"
		}
		xFlag := "clear"
		if ie.Insn.Entry.P&0x10 != 0 {
			xFlag = "set"
		}
		cFlag := "clear"
		if ie.Insn.Entry.P&0x01 != 0 {
			cFlag = "set"
		}
		targetKey := docContextKey{addr: addr, e: eFlag, m: mFlag, x: xFlag, c: cFlag}
		docInst, ok := docInstByContext[targetKey]
		if !ok {
			return fmt.Errorf("instruction at address $%06X with context %+v not found in project document", addr, targetKey)
		}

		// Validate document instruction offset
		expectedOffset, ok := snesLoROMOffset(docInst.Address, len(romBytes))
		if !ok || docInst.Offset != uint32(expectedOffset) {
			return fmt.Errorf("instruction at $%06X has document offset %d, expected %d", docInst.Address, docInst.Offset, expectedOffset)
		}

		// Validate canonical instruction ID
		computedID := recovery.ComputeInstructionID(actualROMSHA, docInst.Address, docInst.Offset, docInst.Bytes, docInst.Context)
		if docInst.ID != computedID {
			return fmt.Errorf("instruction at $%06X ID mismatch: doc has %s, computed %s", docInst.Address, docInst.ID, computedID)
		}

		docBytesDecoded, err := hex.DecodeString(docInst.Bytes)
		if err != nil {
			return fmt.Errorf("decode doc instruction bytes %q at $%06X: %w", docInst.Bytes, addr, err)
		}
		if len(docBytesDecoded) == 0 || len(ie.Insn.Fetches) == 0 {
			return fmt.Errorf("non-empty instruction fetch requirement failed at $%06X", addr)
		}
		if len(ie.Insn.Fetches) != len(docBytesDecoded) {
			return fmt.Errorf("instruction fetch length mismatch at $%06X: document has %d bytes, trace has %d fetches",
				addr, len(docBytesDecoded), len(ie.Insn.Fetches))
		}
		if docInst.Opcode != ie.Insn.Fetches[0].Value {
			return fmt.Errorf("instruction opcode mismatch at $%06X: document has 0x%02X, first fetch has 0x%02X",
				addr, docInst.Opcode, ie.Insn.Fetches[0].Value)
		}

		for fIdx, fetch := range ie.Insn.Fetches {
			expectedFetchAddr := addr + uint32(fIdx)
			if fetch.Addr != expectedFetchAddr {
				return fmt.Errorf("fetch[%d] at $%06X has address $%06X, expected $%06X",
					fIdx, addr, fetch.Addr, expectedFetchAddr)
			}
			if fetch.Value != docBytesDecoded[fIdx] {
				return fmt.Errorf("fetch[%d] mismatch at $%06X: document has 0x%02X, trace fetch has 0x%02X",
					fIdx, addr, docBytesDecoded[fIdx], fetch.Value)
			}
			romOff, inROM := snesLoROMOffset(fetch.Addr, len(romBytes))
			if !inROM {
				return fmt.Errorf("instruction fetch $%06X outside ROM range", fetch.Addr)
			}
			if romBytes[romOff] != fetch.Value {
				return fmt.Errorf("fetch[%d] at $%06X (ROM offset 0x%06X) mismatch: ROM has 0x%02X, trace fetch has 0x%02X",
					fIdx, fetch.Addr, romOff, romBytes[romOff], fetch.Value)
			}
			if fetch.ROMOffset != nil && *fetch.ROMOffset != uint32(romOff) {
				return fmt.Errorf("fetch[%d] at $%06X claims ROM offset 0x%06X, but computed LoROM offset is 0x%06X",
					fIdx, fetch.Addr, *fetch.ROMOffset, romOff)
			}
			if fIdx == 0 && fetch.Role != "opcode" {
				return fmt.Errorf("fetch[0] at $%06X has role %q, expected 'opcode'", addr, fetch.Role)
			}
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

	// 9. Execute Baseline on Go 65816 CPU Emulator with step-by-step recording
	baselineMem := map[uint32]uint8{
		inputCanonicalAddr: inputValue,
		inputBusEvent.Addr: inputValue,
	}
	emuBaselineResult, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, baselineMem)
	if err != nil {
		return fmt.Errorf("run emulator baseline with steps: %w", err)
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
		{
			CaseID:  "named-noop",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: inputCanonicalAddr, Value: inputValue},
			},
		},
		{
			CaseID:  fmt.Sprintf("reset-baseline-%d", inputValue),
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: inputCanonicalAddr, Value: inputValue},
			},
		},
	}

	cResults, err := compiledRunner.RunBatch(ctx, cBatchCases)
	if err != nil {
		return fmt.Errorf("compiled runner run batch: %w", err)
	}
	if len(cResults) != 5 {
		return fmt.Errorf("expected 5 compiled runner results, got %d", len(cResults))
	}
	cBaselineResult := cResults[0]

	// 11. Execute Counterfactuals, No-Op, and Reset Baseline on Go Emulator
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

	emuNoopResult, err := decomp.RunEmulatorBlock(ctx, ir, initState, baselineMem)
	if err != nil {
		return fmt.Errorf("run emulator named noop: %w", err)
	}

	emuResetResult, err := decomp.RunEmulatorBlock(ctx, ir, initState, baselineMem)
	if err != nil {
		return fmt.Errorf("run emulator reset baseline: %w", err)
	}

	// 12. Complete full noncycle CPU field comparisons across all three backends
	recVsEmuState := compareFullState(recordedExitState, emuBaselineResult.State, recordedNextPC, emuBaselineResult.NextPC)
	recVsCState := compareFullState(recordedExitState, cBaselineResult.State, recordedNextPC, cBaselineResult.NextPC)
	emuVsCState := compareFullState(emuBaselineResult.State, cBaselineResult.State, emuBaselineResult.NextPC, cBaselineResult.NextPC)

	recVsEmuWritesMatch, recVsEmuWritesErr := compareWrites(recordedWrites, emuBaselineResult.Writes)
	recVsCWritesMatch, recVsCWritesErr := compareWrites(recordedWrites, cBaselineResult.Writes)
	emuVsCWritesMatch, emuVsCWritesErr := compareWrites(emuBaselineResult.Writes, cBaselineResult.Writes)

	cVsEmuMatched, cVsEmuDiscrepancy := decomp.CompareExecResults(emuBaselineResult, cBaselineResult)

	// Check counterfactual dual-backend agreement
	v114WritesMatch, _ := compareWrites(emu114Result.Writes, cResults[1].Writes)
	v114StateMatch := compareFullState(emu114Result.State, cResults[1].State, emu114Result.NextPC, cResults[1].NextPC).Match
	v114DualAgree := v114WritesMatch && v114StateMatch

	v116WritesMatch, _ := compareWrites(emu116Result.Writes, cResults[2].Writes)
	v116StateMatch := compareFullState(emu116Result.State, cResults[2].State, emu116Result.NextPC, cResults[2].NextPC).Match
	v116DualAgree := v116WritesMatch && v116StateMatch

	noopWritesMatch, _ := compareWrites(emuNoopResult.Writes, cResults[3].Writes)
	noopStateMatch := compareFullState(emuNoopResult.State, cResults[3].State, emuNoopResult.NextPC, cResults[3].NextPC).Match
	noopDualAgree := noopWritesMatch && noopStateMatch

	resetWritesMatch, _ := compareWrites(emuResetResult.Writes, cResults[4].Writes)
	resetStateMatch := compareFullState(emuResetResult.State, cResults[4].State, emuResetResult.NextPC, cResults[4].NextPC).Match
	resetDualAgree := resetWritesMatch && resetStateMatch

	// Compare no-op and reset explicitly against baseline results
	noopVsBaselineEmuMatch, noopVsBaselineEmuDisc := decomp.CompareExecResults(emuBaselineResult, emuNoopResult)
	noopVsBaselineCMatch, noopVsBaselineCDisc := decomp.CompareExecResults(cBaselineResult, cResults[3])
	resetVsBaselineEmuMatch, resetVsBaselineEmuDisc := decomp.CompareExecResults(emuBaselineResult, emuResetResult)
	resetVsBaselineCMatch, resetVsBaselineCDisc := decomp.CompareExecResults(cBaselineResult, cResults[4])

	// Check refusal and write overflow conditions across all executed cases
	allCasesRefusalsClean := true
	for i, res := range []decomp.ExecResult{emuBaselineResult, emu114Result, emu116Result, emuNoopResult, emuResetResult} {
		if res.MissingRead || res.MMIOAccess || res.WriteOverflow || res.TotalWrites != 1 {
			allCasesRefusalsClean = false
			break
		}
		if i < len(cResults) {
			cRes := cResults[i]
			if cRes.MissingRead || cRes.MMIOAccess || cRes.WriteOverflow || cRes.TotalWrites != 1 {
				allCasesRefusalsClean = false
				break
			}
		}
	}

	// 13. Build 4-instruction side-by-side recorded vs predicted timeline
	var timelineSteps []InstructionTimelineStep
	timelineAllMatched := true
	for i, ie := range insnEvents {
		stepAddr := (uint32(ie.Insn.Entry.PB) << 16) | uint32(ie.Insn.Entry.PC)
		var stepRecordedEffects []TimelineBusEffect
		if i == 0 {
			stepRecordedEffects = append(stepRecordedEffects, TimelineBusEffect{
				Op:      "read",
				Address: inputCanonicalAddr,
				Value:   inputValue,
			})
		} else if i == len(insnEvents)-1 {
			stepRecordedEffects = append(stepRecordedEffects, TimelineBusEffect{
				Op:      "write",
				Address: outputCanonicalAddr,
				Value:   recordedWriteValue,
			})
		}

		stepRecordedEntry := decomp.CPUState{
			A:  ie.Insn.Entry.A,
			X:  ie.Insn.Entry.X,
			Y:  ie.Insn.Entry.Y,
			S:  ie.Insn.Entry.S,
			D:  ie.Insn.Entry.D,
			DB: ie.Insn.Entry.DB,
			PB: ie.Insn.Entry.PB,
			PC: ie.Insn.Entry.PC,
			P:  ie.Insn.Entry.P,
			E:  ie.Insn.Entry.E,
		}
		stepRecordedExit := decomp.CPUState{
			A:  ie.Insn.Exit.A,
			X:  ie.Insn.Exit.X,
			Y:  ie.Insn.Exit.Y,
			S:  ie.Insn.Exit.S,
			D:  ie.Insn.Exit.D,
			DB: ie.Insn.Exit.DB,
			PB: ie.Insn.Exit.PB,
			PC: ie.Insn.Exit.PC,
			P:  ie.Insn.Exit.P,
			E:  ie.Insn.Exit.E,
		}

		emuStep := emuSteps[i]
		var stepPredictedEffects []TimelineBusEffect
		for _, r := range emuStep.Reads {
			stepPredictedEffects = append(stepPredictedEffects, TimelineBusEffect{
				Op:      "read",
				Address: r.Address,
				Value:   r.Value,
			})
		}
		for _, w := range emuStep.Writes {
			stepPredictedEffects = append(stepPredictedEffects, TimelineBusEffect{
				Op:      "write",
				Address: w.Address,
				Value:   w.Value,
			})
		}

		stepEntryPC := uint32(emuStep.EntryState.PC) | (uint32(emuStep.EntryState.PB) << 16)
		recStepEntryPC := uint32(ie.Insn.Entry.PC) | (uint32(ie.Insn.Entry.PB) << 16)
		entryStateComp := compareFullState(stepRecordedEntry, emuStep.EntryState, recStepEntryPC, stepEntryPC)

		stepNextPC := uint32(emuStep.ExitState.PC) | (uint32(emuStep.ExitState.PB) << 16)
		recStepNextPC := uint32(ie.Insn.Exit.PC) | (uint32(ie.Insn.Exit.PB) << 16)
		exitStateComp := compareFullState(stepRecordedExit, emuStep.ExitState, recStepNextPC, stepNextPC)

		effectsMatch := len(stepRecordedEffects) == len(stepPredictedEffects)
		if effectsMatch {
			for eIdx := range stepRecordedEffects {
				if stepRecordedEffects[eIdx] != stepPredictedEffects[eIdx] {
					effectsMatch = false
					break
				}
			}
		}

		stepMatch := entryStateComp.Match && exitStateComp.Match && effectsMatch
		if !stepMatch {
			timelineAllMatched = false
		}

		timelineSteps = append(timelineSteps, InstructionTimelineStep{
			StepIndex:        i,
			Address:          stepAddr,
			AddressHex:       fmt.Sprintf("$%06X", stepAddr),
			Mnemonic:         blockInstructions[i].Mnemonic,
			BytesHex:         blockInstructions[i].Bytes,
			EventID:          ie.ID,
			Cycle:            ie.Cycle,
			Seq:              ie.Insn.Seq,
			RecordedEntry:    stepRecordedEntry,
			RecordedExit:     stepRecordedExit,
			RecordedEffects:  stepRecordedEffects,
			PredictedEntry:   emuStep.EntryState,
			PredictedExit:    emuStep.ExitState,
			PredictedEffects: stepPredictedEffects,
			EntryStateMatch:  entryStateComp,
			ExitStateMatch:   exitStateComp,
			EffectsMatch:     effectsMatch,
			StepMatch:        stepMatch,
		})
	}

	// Verify inter-step continuity
	for i := 1; i < len(emuSteps); i++ {
		if emuSteps[i].EntryState != emuSteps[i-1].ExitState {
			timelineAllMatched = false
		}
	}

	allVerified := recVsEmuState.Match && recVsCState.Match && emuVsCState.Match &&
		recVsEmuWritesMatch && recVsCWritesMatch && emuVsCWritesMatch &&
		v114DualAgree && v116DualAgree && noopDualAgree && resetDualAgree &&
		noopVsBaselineEmuMatch && noopVsBaselineCMatch &&
		resetVsBaselineEmuMatch && resetVsBaselineCMatch &&
		cVsEmuMatched && timelineAllMatched && allCasesRefusalsClean &&
		len(cResults[1].Writes) == 1 && cResults[1].Writes[0].Value == 119 &&
		len(cResults[2].Writes) == 1 && cResults[2].Writes[0].Value == 121 &&
		len(cResults[3].Writes) == 1 && cResults[3].Writes[0].Value == 120 &&
		len(cResults[4].Writes) == 1 && cResults[4].Writes[0].Value == 120

	// 14. Create output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// 15. Save case.json
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

	// 16. Save generated.c
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

	// 17. Save timeline.json
	timelineArtifact := TimelineArtifact{
		Schema:                    "snes-instruction-timeline-v1",
		GeneratedAt:               time.Now().UTC().Format(time.RFC3339),
		BlockID:                   block.ID,
		StartAddress:              block.StartAddress,
		EndAddress:                block.EndAddress,
		Steps:                     timelineSteps,
		DualBackendBlockAgreement: cVsEmuMatched,
		AllMatched:                timelineAllMatched,
	}
	timelineBytes, err := json.MarshalIndent(timelineArtifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal timeline: %w", err)
	}
	timelinePath := filepath.Join(*outDir, "timeline.json")
	if err := os.WriteFile(timelinePath, timelineBytes, 0644); err != nil {
		return fmt.Errorf("write timeline.json: %w", err)
	}

	// 18. Save receipt.json
	receipt := map[string]any{
		"schema":                   "snes-139220-receipt-v1",
		"generated_at":             time.Now().UTC().Format(time.RFC3339),
		"block_id":                 block.ID,
		"start_address":            block.StartAddress,
		"end_address":              block.EndAddress,
		"stream_sha256":            streamSHA,
		"rom_sha256":               actualROMSHA,
		"doc_sha256":               docSHA256,
		"observed_effects_capture": false,
		"captured_proof_eligible":  false,
		"admission_digest":         "",
		"refusal_metadata": map[string]any{
			"total_data_reads":          len(dataReads),
			"total_data_writes":         len(dataWrites),
			"extra_reads_refused":       0,
			"extra_writes_refused":      0,
			"mmio_effects_refused":      0,
			"unsupported_space_refused": 0,
			"missing_read_refused":      false,
			"write_overflow_refused":    false,
			"all_cases_clean":           allCasesRefusalsClean,
		},
		"baseline_identity_verifications": map[string]any{
			"noop_vs_baseline_emulator": map[string]any{
				"match":       noopVsBaselineEmuMatch,
				"discrepancy": noopVsBaselineEmuDisc,
			},
			"noop_vs_baseline_compiled_c": map[string]any{
				"match":       noopVsBaselineCMatch,
				"discrepancy": noopVsBaselineCDisc,
			},
			"reset_vs_baseline_emulator": map[string]any{
				"match":       resetVsBaselineEmuMatch,
				"discrepancy": resetVsBaselineEmuDisc,
			},
			"reset_vs_baseline_compiled_c": map[string]any{
				"match":       resetVsBaselineCMatch,
				"discrepancy": resetVsBaselineCDisc,
			},
		},
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
		"compare_exec_results": map[string]any{
			"match":       cVsEmuMatched,
			"discrepancy": cVsEmuDiscrepancy,
		},
		"counterfactual_predictions": []map[string]any{
			{
				"case_id":               "perturbation-114",
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
				"case_id":               fmt.Sprintf("baseline-%d", inputValue),
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
				"case_id":               "perturbation-116",
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
			{
				"case_id":               "named-noop",
				"input_value":           int(inputValue),
				"status":                "named_noop_baseline_identity",
				"emulator_exit_a":       emuNoopResult.State.A,
				"emulator_exit_a_hex":   fmt.Sprintf("0x%04X", emuNoopResult.State.A),
				"emulator_write":        safeFirstWrite(emuNoopResult.Writes),
				"compiled_c_exit_a":     cResults[3].State.A,
				"compiled_c_exit_a_hex": fmt.Sprintf("0x%04X", cResults[3].State.A),
				"compiled_c_write":      safeFirstWrite(cResults[3].Writes),
				"dual_backends_agree":   noopDualAgree,
			},
			{
				"case_id":               fmt.Sprintf("reset-baseline-%d", inputValue),
				"input_value":           int(inputValue),
				"status":                "repeat_baseline_reset",
				"emulator_exit_a":       emuResetResult.State.A,
				"emulator_exit_a_hex":   fmt.Sprintf("0x%04X", emuResetResult.State.A),
				"emulator_write":        safeFirstWrite(emuResetResult.Writes),
				"compiled_c_exit_a":     cResults[4].State.A,
				"compiled_c_exit_a_hex": fmt.Sprintf("0x%04X", cResults[4].State.A),
				"compiled_c_write":      safeFirstWrite(cResults[4].Writes),
				"dual_backends_agree":   resetDualAgree,
			},
		},
		"timeline_verified": timelineAllMatched,
		"verified":          allVerified,
	}

	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	receiptPath := filepath.Join(*outDir, "receipt.json")
	if err := os.WriteFile(receiptPath, receiptBytes, 0644); err != nil {
		return fmt.Errorf("write receipt.json: %w", err)
	}

	// 19. Save manifest.json
	caseSHA := sha256Hex(caseBytes)
	cSHA := sha256Hex([]byte(cCode))
	timelineSHA := sha256Hex(timelineBytes)
	receiptSHA := sha256Hex(receiptBytes)

	manifestEvidenceMode := "authentic_trace_stream"
	if isRetainedFixture {
		manifestEvidenceMode = "retained_probe_fixture"
	}

	manifestBindings := map[string]any{
		"trace_sha256":      streamSHA,
		"rom_sha256":        actualROMSHA,
		"doc_sha256":        docSHA256,
		"project_directory": *projectDir,
		"document_block_id": block.ID,
		"bus_pc_range":      fmt.Sprintf("$%06X..$%06X", firstAddr, lastExitAddr),
	}
	if runHeader != nil {
		manifestBindings["engine_revision"] = runHeader.EngineRevision
		manifestBindings["initial_state_sha256"] = runHeader.InitialStateSHA256
		manifestBindings["run_schema"] = runHeader.Schema
	}
	if fixtureSHA != "" {
		manifestBindings["probe_fixture_sha256"] = fixtureSHA
	}

	manifest := map[string]any{
		"schema":          "snes-delivery-manifest-v1",
		"generated_at":    time.Now().UTC().Format(time.RFC3339),
		"target_event_id": lastInsn.ID,
		"evidence_mode":   manifestEvidenceMode,
		"retirement_span": map[string]any{
			"start_event":                           startEvent,
			"end_event":                             endEvent,
			"start_cycle":                           firstInsn.Cycle,
			"end_cycle":                             lastInsn.Insn.Exit.Cycles,
			"first_retirement":                      firstInsn.ID,
			"last_retirement":                       lastInsn.ID,
			"input_event_id":                        inputBusEvent.ID,
			"input_cycle":                           inputBusEvent.Cycle,
			"input_ordered_before_first_retirement": inputBusEvent.ID < firstInsn.ID,
			"output_event_id":                       outputBusEvent.ID,
			"output_cycle":                          outputBusEvent.Cycle,
			"frame":                                 firstInsn.Frame,
			"start_seq":                             firstInsn.Insn.Seq,
			"end_seq":                               lastInsn.Insn.Seq,
		},
		"bindings": manifestBindings,
		"artifacts": []map[string]any{
			{"path": "case.json", "sha256": caseSHA, "kind": "replay_case"},
			{"path": "generated.c", "sha256": cSHA, "kind": "compilable_c"},
			{"path": "timeline.json", "sha256": timelineSHA, "kind": "per_instruction_timeline"},
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

	// 20. Output computed JSON summary with safe prediction extraction
	statusStr := "success"
	if !allVerified {
		statusStr = "verification_failure"
	}

	predSummary := map[string]int{}
	if len(cResults) > 1 && len(cResults[1].Writes) > 0 {
		predSummary["input_114"] = int(cResults[1].Writes[0].Value)
	}
	if len(cResults) > 2 && len(cResults[2].Writes) > 0 {
		predSummary["input_116"] = int(cResults[2].Writes[0].Value)
	}
	if len(cResults) > 3 && len(cResults[3].Writes) > 0 {
		predSummary["named_noop"] = int(cResults[3].Writes[0].Value)
	}
	if len(cResults) > 4 && len(cResults[4].Writes) > 0 {
		predSummary["reset_baseline"] = int(cResults[4].Writes[0].Value)
	}

	summary := map[string]any{
		"status":                  statusStr,
		"command":                 "snesdasm replay-slice",
		"output_dir":              *outDir,
		"artifacts":               []string{casePath, manifestPath, cPath, timelinePath, receiptPath},
		"dual_backend_verified":   allVerified,
		"timeline_steps_verified": len(timelineSteps),
		"baseline_input":          inputValue,
		"baseline_write":          recordedWriteValue,
		"predictions":             predSummary,
	}

	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		return err
	}

	if !allVerified {
		return fmt.Errorf("trace replay verification failed: state or write divergence detected")
	}

	return nil
}

func snesLoROMOffset(addr uint32, romSize int) (int, bool) {
	if addr > 0xFFFFFF || addr&0xFFFF < 0x8000 || (addr>>16) == 0x7E || (addr>>16) == 0x7F {
		return 0, false
	}
	off := int(((addr>>16)&0x7F)*0x8000 + (addr & 0x7FFF))
	return off, off < romSize
}

type InstructionTimelineStep struct {
	StepIndex        int                 `json:"step_index"`
	Address          uint32              `json:"address"`
	AddressHex       string              `json:"address_hex"`
	Mnemonic         string              `json:"mnemonic"`
	BytesHex         string              `json:"bytes_hex"`
	EventID          uint64              `json:"event_id"`
	Cycle            uint64              `json:"cycle"`
	Seq              uint64              `json:"seq"`
	RecordedEntry    decomp.CPUState     `json:"recorded_entry"`
	RecordedExit     decomp.CPUState     `json:"recorded_exit"`
	RecordedEffects  []TimelineBusEffect `json:"recorded_effects,omitempty"`
	PredictedEntry   decomp.CPUState     `json:"predicted_entry"`
	PredictedExit    decomp.CPUState     `json:"predicted_exit"`
	PredictedEffects []TimelineBusEffect `json:"predicted_effects,omitempty"`
	EntryStateMatch  StateComparison     `json:"entry_state_match"`
	ExitStateMatch   StateComparison     `json:"exit_state_match"`
	EffectsMatch     bool                `json:"effects_match"`
	StepMatch        bool                `json:"step_match"`
}

type TimelineBusEffect struct {
	Op      string `json:"op"`
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

type TimelineArtifact struct {
	Schema                    string                    `json:"schema"`
	GeneratedAt               string                    `json:"generated_at"`
	BlockID                   string                    `json:"block_id"`
	StartAddress              uint32                    `json:"start_address"`
	EndAddress                uint32                    `json:"end_address"`
	Steps                     []InstructionTimelineStep `json:"steps"`
	DualBackendBlockAgreement bool                      `json:"dual_backend_block_agreement"`
	AllMatched                bool                      `json:"all_matched"`
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

func loadAndHashEvents(tracePath, probePath string, startEvent, endEvent uint64) ([]trace.Event, string, *RunHeaderInfo, string, error) {
	if tracePath != "" {
		f, err := os.Open(tracePath)
		if err != nil {
			return nil, "", nil, "", fmt.Errorf("open trace file %s: %w", tracePath, err)
		}
		defer f.Close()

		hasher := sha256.New()
		tee := io.TeeReader(f, hasher)
		scanner := bufio.NewScanner(tee)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 16*1024*1024)

		var events []trace.Event
		var runHeader *RunHeaderInfo
		for scanner.Scan() {
			line := scanner.Bytes()
			var header struct {
				ID uint64 `json:"id"`
			}
			if err := json.Unmarshal(line, &header); err != nil {
				return nil, "", nil, "", fmt.Errorf("decode trace line header: %w", err)
			}
			if header.ID == 0 {
				var rawRun struct {
					ID     uint64 `json:"id"`
					Schema int    `json:"schema"`
					Kind   string `json:"kind"`
					Frame  uint64 `json:"frame"`
					Run    struct {
						ROMSHA256          string   `json:"rom_sha256"`
						Mapper             string   `json:"mapper"`
						EngineRevision     string   `json:"engine_revision"`
						Start              string   `json:"start"`
						InitialStateSHA256 string   `json:"initial_state_sha256"`
						Events             []string `json:"events"`
					} `json:"run"`
				}
				if err := json.Unmarshal(line, &rawRun); err == nil && rawRun.Kind == "run" {
					runHeader = &RunHeaderInfo{
						ID:                 rawRun.ID,
						Schema:             rawRun.Schema,
						Kind:               rawRun.Kind,
						Frame:              rawRun.Frame,
						ROMSHA256:          rawRun.Run.ROMSHA256,
						Mapper:             rawRun.Run.Mapper,
						EngineRevision:     rawRun.Run.EngineRevision,
						Start:              rawRun.Run.Start,
						InitialStateSHA256: rawRun.Run.InitialStateSHA256,
						Events:             rawRun.Run.Events,
					}
				}
			}
			if header.ID >= startEvent && header.ID <= endEvent {
				var ev trace.Event
				if err := json.Unmarshal(line, &ev); err != nil {
					return nil, "", nil, "", fmt.Errorf("decode trace event %d: %w", header.ID, err)
				}
				events = append(events, ev)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", nil, "", fmt.Errorf("scan trace stream: %w", err)
		}

		streamSHA := hex.EncodeToString(hasher.Sum(nil))
		if len(events) == 0 {
			return nil, "", nil, "", fmt.Errorf("no events matched range %d..%d in trace %s", startEvent, endEvent, tracePath)
		}
		return events, streamSHA, runHeader, "", nil
	}

	if probePath != "" {
		b, err := os.ReadFile(probePath)
		if err != nil {
			return nil, "", nil, "", fmt.Errorf("read probe json %s: %w", probePath, err)
		}
		fixtureSHA := sha256Hex(b)

		var probe struct {
			TraceSHA256 string        `json:"trace_sha256"`
			Events      []trace.Event `json:"selected_raw_events"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, "", nil, "", fmt.Errorf("unmarshal probe json: %w", err)
		}
		var filtered []trace.Event
		for _, e := range probe.Events {
			if e.ID >= startEvent && e.ID <= endEvent {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			return nil, "", nil, "", fmt.Errorf("no events matched range %d..%d in probe %s", startEvent, endEvent, probePath)
		}
		sha := probe.TraceSHA256
		if sha == "" {
			sha = pinnedStreamSHA256
		}
		return filtered, sha, nil, fixtureSHA, nil
	}

	return nil, "", nil, "", fmt.Errorf("neither -trace nor -probe path provided")
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
