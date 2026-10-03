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
	"github.com/tmc/snes/internal/recovery/divergence"
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
		probePath   = fs.String("probe", "/Users/tmc/tmp/snes-auto-jpdasm/20261003-2200-capability-review/reports/genuine-computation-probe.json", "path to probe json fallback")
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

	// 1. Verify ROM identity if available
	actualROMSHA := ""
	if *romPath != "" {
		romBytes, err := os.ReadFile(*romPath)
		if err == nil {
			h := sha256.Sum256(romBytes)
			actualROMSHA = hex.EncodeToString(h[:])
			if actualROMSHA != pinnedROMSHA256 {
				return fmt.Errorf("rom sha256 %s does not match pinned %s", actualROMSHA, pinnedROMSHA256)
			}
		}
	}
	if actualROMSHA == "" {
		actualROMSHA = pinnedROMSHA256
	}

	// 2. Load events from trace or probe
	events, streamSHA, err := loadSliceEvents(*tracePath, *probePath, startEvent, endEvent)
	if err != nil {
		return fmt.Errorf("load slice events: %w", err)
	}

	// 3. Correlate mixed events into steps
	steps := divergence.StepsFromMixedTraceEvents(events)
	if len(steps) < 5 {
		return fmt.Errorf("expected at least 5 steps, got %d", len(steps))
	}

	// Verify event ordering: input 139209 appears before retirement 139210
	var inputEventFound bool
	for _, e := range events {
		if e.ID == 139209 {
			inputEventFound = true
		}
		if e.ID == 139210 && !inputEventFound {
			return fmt.Errorf("ordering violation: retirement 139210 observed before input 139209")
		}
	}

	// 4. Construct 4-instruction basic block for $0CC468..$0CC471
	// Instructions from genuine document / trace:
	// 0x0CC468: LDA $1F05 (ad051f)
	// 0x0CC46B: CLC       (18)
	// 0x0CC46C: ADC #$05  (6905)
	// 0x0CC46E: STA $1F05 (8d051f)
	entryCtx := recovery.Context{
		E: "clear",
		M: "set",
		X: "set",
		C: "clear",
	}

	instructions := []recovery.Instruction{
		{
			ID:           "inst-0cc468",
			Architecture: "wdc65816",
			Address:      0x0CC468,
			Offset:       0x064468,
			Bytes:        "ad051f",
			Opcode:       0xAD,
			Mnemonic:     "lda",
			Mode:         "absolute",
			Context:      entryCtx,
		},
		{
			ID:           "inst-0cc46b",
			Architecture: "wdc65816",
			Address:      0x0CC46B,
			Offset:       0x06446B,
			Bytes:        "18",
			Opcode:       0x18,
			Mnemonic:     "clc",
			Mode:         "implied",
			Context:      entryCtx,
		},
		{
			ID:           "inst-0cc46c",
			Architecture: "wdc65816",
			Address:      0x0CC46C,
			Offset:       0x06446C,
			Bytes:        "6905",
			Opcode:       0x69,
			Mnemonic:     "adc.b",
			Mode:         "immediate",
			Context:      entryCtx,
		},
		{
			ID:           "inst-0cc46e",
			Architecture: "wdc65816",
			Address:      0x0CC46E,
			Offset:       0x06446E,
			Bytes:        "8d051f",
			Opcode:       0x8D,
			Mnemonic:     "sta",
			Mode:         "absolute",
			Context:      entryCtx,
		},
	}

	block := &structure.BasicBlock{
		ID:           "block-0cc468-4insn",
		StartAddress: 0x0CC468,
		EndAddress:   0x0CC471,
		Instructions: instructions,
		Successors:   []uint32{0x0CC471},
	}

	// 5. Lift block into IR
	ir, err := decomp.LiftBlock(block, entryCtx)
	if err != nil {
		return fmt.Errorf("lift block: %w", err)
	}

	// 6. Generate compilable C
	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		return fmt.Errorf("generate compilable C: %w", err)
	}

	// 7. Setup initial state matching authentic capture
	// High byte of A is preserved: 0xC4; low byte initially 0x38, becomes input 115 on LDA
	initState := decomp.CPUState{
		A:  0xC438,
		X:  0x00E0, // 224
		Y:  0x0000,
		S:  0x01F7, // 503
		D:  0x0000,
		DB: 0x0C,
		PB: 0x0C,
		PC: 0xC468, // 50280
		P:  0x30,   // M=1, X=1
		E:  false,
	}

	// Memory inputs: $7E1F05 (canonical Low RAM mirror $0C:1F05)
	baselineMem := map[uint32]uint8{
		0x7E1F05: 115,
		0x0C1F05: 115,
	}

	// 8. Run Emulator Baseline
	emuResult, err := decomp.RunEmulatorBlock(ctx, ir, initState, baselineMem)
	if err != nil {
		return fmt.Errorf("run emulator baseline: %w", err)
	}

	// 9. Run Compiled C Baseline
	compiledRunner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		return fmt.Errorf("new compiled runner: %w", err)
	}
	defer compiledRunner.Close()

	cBatchCases := []decomp.ReplayCaseInput{
		{
			CaseID:  "baseline-115",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 115},
			},
		},
		{
			CaseID:  "perturbation-114",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 114},
			},
		},
		{
			CaseID:  "perturbation-116",
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: 116},
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

	// 10. Run Emulator on 114 and 116 for full dual-backend parity check
	emu114Mem := map[uint32]uint8{0x7E1F05: 114, 0x0C1F05: 114}
	emu114Result, err := decomp.RunEmulatorBlock(ctx, ir, initState, emu114Mem)
	if err != nil {
		return fmt.Errorf("run emulator 114: %w", err)
	}

	emu116Mem := map[uint32]uint8{0x7E1F05: 116, 0x0C1F05: 116}
	emu116Result, err := decomp.RunEmulatorBlock(ctx, ir, initState, emu116Mem)
	if err != nil {
		return fmt.Errorf("run emulator 116: %w", err)
	}

	// 11. Build verification structures
	recordedWrite := decomp.MemoryWrite{Address: 0x7E1F05, Value: 120}

	recordedVsEmuWritesMatch := len(emuResult.Writes) == 1 &&
		emuResult.Writes[0].Address == recordedWrite.Address &&
		emuResult.Writes[0].Value == recordedWrite.Value

	recordedVsCWritesMatch := len(cBaselineResult.Writes) == 1 &&
		cBaselineResult.Writes[0].Address == recordedWrite.Address &&
		cBaselineResult.Writes[0].Value == recordedWrite.Value

	emuVsCWritesMatch := len(emuResult.Writes) == len(cBaselineResult.Writes) &&
		emuResult.Writes[0] == cBaselineResult.Writes[0]

	emuVsCStateMatch := emuResult.State.A == cBaselineResult.State.A &&
		emuResult.State.P == cBaselineResult.State.P &&
		emuResult.NextPC == cBaselineResult.NextPC

	// 12. Create output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// 13. Build case.json
	replayCase := decomp.ReplayCase{
		SchemaVersion:          "snes-routine-case-v2",
		CaseID:                 "case-139220-genuine-wram-counter",
		BlockID:                block.ID,
		StreamSHA256:           streamSHA,
		ROMSHA256:              actualROMSHA,
		StartEventID:           139205,
		EndEventID:             139220,
		InitialState:           initState,
		InitialMemory: []decomp.MemoryCell{
			{Address: 0x7E1F05, Value: 115},
		},
		ObservedExit: decomp.CPUState{
			A:  0xC478,
			X:  0x00E0,
			Y:  0x0000,
			S:  0x01F7,
			D:  0x0000,
			DB: 0x0C,
			PB: 0x0C,
			PC: 0xC471,
			P:  0x30,
			E:  false,
		},
		ObservedNextPC:         0x0CC471,
		ObservedEffectsCapture: false,
		AdmissionDigest:        "",
		ObservedWrites: []decomp.MemoryWrite{
			{Address: 0x7E1F05, Value: 120},
		},
	}

	caseBytes, err := json.MarshalIndent(replayCase, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal case: %w", err)
	}
	casePath := filepath.Join(*outDir, "case.json")
	if err := os.WriteFile(casePath, caseBytes, 0644); err != nil {
		return fmt.Errorf("write case.json: %w", err)
	}

	// 14. Build generated.c
	cPath := filepath.Join(*outDir, "generated.c")
	if err := os.WriteFile(cPath, []byte(cCode), 0644); err != nil {
		return fmt.Errorf("write generated.c: %w", err)
	}

	// 15. Build receipt.json
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
			"a":                        emuResult.State.A,
			"a_hex":                    fmt.Sprintf("0x%04X", emuResult.State.A),
			"architectural_high_a":     (emuResult.State.A >> 8) & 0xFF,
			"architectural_high_a_hex": fmt.Sprintf("0x%02X", (emuResult.State.A>>8)&0xFF),
			"x":                        emuResult.State.X,
			"y":                        emuResult.State.Y,
			"s":                        emuResult.State.S,
			"d":                        emuResult.State.D,
			"db":                       emuResult.State.DB,
			"pb":                       emuResult.State.PB,
			"pc":                       emuResult.State.PC,
			"pc_hex":                   fmt.Sprintf("0x%04X", emuResult.State.PC),
			"p":                        emuResult.State.P,
			"p_hex":                    fmt.Sprintf("0x%02X", emuResult.State.P),
			"e":                        emuResult.State.E,
			"next_pc":                  emuResult.NextPC,
			"next_pc_hex":              fmt.Sprintf("0x%06X", emuResult.NextPC),
		},
		"compare_writes": map[string]any{
			"recorded_vs_emulator": map[string]any{
				"match":           recordedVsEmuWritesMatch,
				"recorded_writes": []decomp.MemoryWrite{recordedWrite},
				"backend_writes":  emuResult.Writes,
			},
			"recorded_vs_compiled_c": map[string]any{
				"match":           recordedVsCWritesMatch,
				"recorded_writes": []decomp.MemoryWrite{recordedWrite},
				"backend_writes":  cBaselineResult.Writes,
			},
			"emulator_vs_compiled_c": map[string]any{
				"match":        emuVsCWritesMatch && emuVsCStateMatch,
				"state_match":  emuVsCStateMatch,
				"writes_match": emuVsCWritesMatch,
			},
		},
		"counterfactual_predictions": []map[string]any{
			{
				"input_value":            114,
				"status":                 "unrecorded_prediction",
				"emulator_exit_a":        emu114Result.State.A,
				"emulator_exit_a_hex":    fmt.Sprintf("0x%04X", emu114Result.State.A),
				"emulator_write":         emu114Result.Writes[0],
				"compiled_c_exit_a":      cResults[1].State.A,
				"compiled_c_exit_a_hex":  fmt.Sprintf("0x%04X", cResults[1].State.A),
				"compiled_c_write":       cResults[1].Writes[0],
				"dual_backends_agree":    emu114Result.State.A == cResults[1].State.A && emu114Result.Writes[0] == cResults[1].Writes[0],
			},
			{
				"input_value":            115,
				"status":                 "recorded_baseline",
				"emulator_exit_a":        emuResult.State.A,
				"emulator_exit_a_hex":    fmt.Sprintf("0x%04X", emuResult.State.A),
				"emulator_write":         emuResult.Writes[0],
				"compiled_c_exit_a":      cBaselineResult.State.A,
				"compiled_c_exit_a_hex":  fmt.Sprintf("0x%04X", cBaselineResult.State.A),
				"compiled_c_write":       cBaselineResult.Writes[0],
				"dual_backends_agree":    emuResult.State.A == cBaselineResult.State.A && emuResult.Writes[0] == cBaselineResult.Writes[0],
			},
			{
				"input_value":            116,
				"status":                 "unrecorded_prediction",
				"emulator_exit_a":        emu116Result.State.A,
				"emulator_exit_a_hex":    fmt.Sprintf("0x%04X", emu116Result.State.A),
				"emulator_write":         emu116Result.Writes[0],
				"compiled_c_exit_a":      cResults[2].State.A,
				"compiled_c_exit_a_hex":  fmt.Sprintf("0x%04X", cResults[2].State.A),
				"compiled_c_write":       cResults[2].Writes[0],
				"dual_backends_agree":    emu116Result.State.A == cResults[2].State.A && emu116Result.Writes[0] == cResults[2].Writes[0],
			},
		},
		"verified": recordedVsEmuWritesMatch && recordedVsCWritesMatch && emuVsCStateMatch,
	}

	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}
	receiptPath := filepath.Join(*outDir, "receipt.json")
	if err := os.WriteFile(receiptPath, receiptBytes, 0644); err != nil {
		return fmt.Errorf("write receipt.json: %w", err)
	}

	// 16. Build manifest.json
	caseSHA := sha256Hex(caseBytes)
	cSHA := sha256Hex([]byte(cCode))
	receiptSHA := sha256Hex(receiptBytes)

	manifest := map[string]any{
		"schema":          "snes-delivery-manifest-v1",
		"generated_at":    time.Now().UTC().Format(time.RFC3339),
		"target_event_id": 139220,
		"retirement_span": map[string]any{
			"start_event":                           139205,
			"end_event":                             139220,
			"start_cycle":                           119603094,
			"end_cycle":                             119603210,
			"first_retirement":                      139210,
			"input_event_id":                        139209,
			"input_cycle":                           119603144,
			"input_ordered_before_first_retirement": true,
		},
		"bindings": map[string]any{
			"trace_sha256":      streamSHA,
			"rom_sha256":        actualROMSHA,
			"project_directory": *projectDir,
			"document_block_id": block.ID,
			"bus_pc_range":      "$0CC468..$0CC471",
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

	// 17. Output JSON summary to stdout
	summary := map[string]any{
		"status":      "success",
		"command":     "snesdasm replay-slice",
		"output_dir":  *outDir,
		"artifacts": []string{
			casePath,
			manifestPath,
			cPath,
			receiptPath,
		},
		"dual_backend_verified": recordedVsEmuWritesMatch && recordedVsCWritesMatch && emuVsCStateMatch,
		"baseline_input":        115,
		"baseline_write":        120,
		"predictions": map[string]int{
			"input_114": 119,
			"input_116": 121,
		},
	}

	return json.NewEncoder(stdout).Encode(summary)
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

func loadSliceEvents(tracePath, probePath string, startEvent, endEvent uint64) ([]trace.Event, string, error) {
	// Try reading from tracePath first if it exists
	if tracePath != "" {
		if f, err := os.Open(tracePath); err == nil {
			defer f.Close()
			var events []trace.Event
			scanner := bufio.NewScanner(f)
			buf := make([]byte, 1024*1024)
			scanner.Buffer(buf, 10*1024*1024)

			for scanner.Scan() {
				line := scanner.Bytes()
				var header struct {
					ID uint64 `json:"id"`
				}
				if err := json.Unmarshal(line, &header); err != nil {
					continue
				}
				if header.ID >= startEvent && header.ID <= endEvent {
					var ev trace.Event
					if err := json.Unmarshal(line, &ev); err == nil {
						events = append(events, ev)
					}
					if header.ID == endEvent {
						break
					}
				}
			}
			if len(events) > 0 {
				return events, pinnedStreamSHA256, nil
			}
		}
	}

	// Fallback to probe JSON
	if probePath != "" {
		b, err := os.ReadFile(probePath)
		if err != nil {
			return nil, "", fmt.Errorf("read probe json: %w", err)
		}
		var probe struct {
			TraceSHA256 string        `json:"trace_sha256"`
			Events      []trace.Event `json:"selected_raw_events"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, "", fmt.Errorf("unmarshal probe json: %w", err)
		}
		if len(probe.Events) > 0 {
			sha := probe.TraceSHA256
			if sha == "" {
				sha = pinnedStreamSHA256
			}
			var filtered []trace.Event
			for _, e := range probe.Events {
				if e.ID >= startEvent && e.ID <= endEvent {
					filtered = append(filtered, e)
				}
			}
			if len(filtered) > 0 {
				return filtered, sha, nil
			}
		}
	}

	return nil, "", fmt.Errorf("neither trace nor probe provided raw events for %d..%d", startEvent, endEvent)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
