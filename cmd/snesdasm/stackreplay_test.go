package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestStackReplayTrial(t *testing.T) {
	ctx := context.Background()

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
		ID:           "block-0cc404-stackreplay",
		StartAddress: 0x0CC404,
		EndAddress:   0x0CC40A,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC40A},
	}

	ir, err := decomp.LiftBlock(block, ctxClear)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		t.Fatalf("GenerateCompilableC failed: %v", err)
	}
	t.Logf("Generated C code:\n%s", cCode)

	initState := decomp.CPUState{
		A:  3268,
		X:  224,
		Y:  0,
		S:  508, // 0x01FC
		D:  0,
		DB: 0,
		PB: 12, // 0x0C
		PC: 50180, // 0xC404
		P:  50,    // 0x32
		E:  false,
	}

	runner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		t.Fatalf("NewCompiledRunner failed: %v", err)
	}
	defer runner.Close()

	testCases := []struct {
		name     string
		inputVal uint8
		wantP    uint8
		wantNext uint32
	}{
		{name: "baseline_54", inputVal: 54, wantP: 0x30, wantNext: 0x0CC40A},
		{name: "prediction_127", inputVal: 127, wantP: 0xB0, wantNext: 0x0CC40A},
		{name: "prediction_255", inputVal: 255, wantP: 0x32, wantNext: 0x0CC40A},
	}

	var cBatchCases []decomp.ReplayCaseInput
	for _, tc := range testCases {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  tc.name,
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1E0A, Value: tc.inputVal},
			},
		})
	}

	cResults, err := runner.RunBatch(ctx, cBatchCases)
	if err != nil {
		t.Fatalf("runner.RunBatch failed: %v", err)
	}

	for i, tc := range testCases {
		mem := map[uint32]uint8{
			0x7E1E0A: tc.inputVal,
		}

		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			t.Fatalf("RunEmulatorBlockWithSteps %s failed: %v", tc.name, err)
		}

		cRes := cResults[i]

		t.Logf("[%s] Emu NextPC=$%06X S=$%04X DB=$%02X P=$%02X writes=%d",
			tc.name, emuRes.NextPC, emuRes.State.S, emuRes.State.DB, emuRes.State.P, len(emuRes.Writes))
		t.Logf("[%s] C   NextPC=$%06X S=$%04X DB=$%02X P=$%02X writes=%d",
			tc.name, cRes.NextPC, cRes.State.S, cRes.State.DB, cRes.State.P, len(cRes.Writes))

		for stepIdx, st := range emuSteps {
			t.Logf("  Emu step %d: %s at $%06X exit P=$%02X writes=%v", stepIdx, st.Instruction.Mnemonic, st.Instruction.Address, st.ExitState.P, st.Writes)
		}
		for wIdx, w := range emuRes.Writes {
			t.Logf("  Emu write %d: addr=$%06X val=0x%02X (%d)", wIdx, w.Address, w.Value, w.Value)
		}
		for wIdx, w := range cRes.Writes {
			t.Logf("  C   write %d: addr=$%06X val=0x%02X (%d)", wIdx, w.Address, w.Value, w.Value)
		}

		if emuRes.NextPC != tc.wantNext {
			t.Errorf("[%s] emu NextPC mismatch: expected $%06X, got $%06X", tc.name, tc.wantNext, emuRes.NextPC)
		}
		if cRes.NextPC != tc.wantNext {
			t.Errorf("[%s] C NextPC mismatch: expected $%06X, got $%06X", tc.name, tc.wantNext, cRes.NextPC)
		}
		if emuRes.State.S != 0x01FB {
			t.Errorf("[%s] emu S mismatch: expected $01FB, got $%04X", tc.name, emuRes.State.S)
		}
		if cRes.State.S != 0x01FB {
			t.Errorf("[%s] C S mismatch: expected $01FB, got $%04X", tc.name, cRes.State.S)
		}
		if emuRes.State.DB != 0x0C {
			t.Errorf("[%s] emu DB mismatch: expected $0C, got $%02X", tc.name, emuRes.State.DB)
		}
		if cRes.State.DB != 0x0C {
			t.Errorf("[%s] C DB mismatch: expected $0C, got $%02X", tc.name, cRes.State.DB)
		}
		if emuRes.State.P != tc.wantP {
			t.Errorf("[%s] emu P mismatch: expected $%02X, got $%02X", tc.name, tc.wantP, emuRes.State.P)
		}
		if cRes.State.P != tc.wantP {
			t.Errorf("[%s] C P mismatch: expected $%02X, got $%02X", tc.name, tc.wantP, cRes.State.P)
		}
		if len(emuRes.Writes) != 3 {
			t.Errorf("[%s] emu expected 3 writes, got %d", tc.name, len(emuRes.Writes))
		}
		if len(cRes.Writes) != 3 {
			t.Errorf("[%s] C expected 3 writes, got %d", tc.name, len(cRes.Writes))
		}

		matched, disc := decomp.CompareExecResults(emuRes, cRes)
		if !matched {
			t.Errorf("[%s] emu vs c discrepancy: %s", tc.name, disc)
		}
	}
}

func TestStackReplayCommand(t *testing.T) {
	const (
		romPath   = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc"
		tracePath = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl"
	)
	if _, err := os.Stat(romPath); err != nil {
		t.Skipf("skipping: ROM not found: %v", err)
	}
	if _, err := os.Stat(tracePath); err != nil {
		t.Skipf("skipping: trace not found: %v", err)
	}

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := []string{
		"stack-replay",
		"-rom", romPath,
		"-trace", tracePath,
		"-out", outDir,
		"-format", "json",
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run stack-replay failed: %v, stderr: %s", err, stderr.String())
	}

	// Verify required files created
	for _, f := range []string{"case.json", "generated.c", "timeline.json", "receipt.json", "manifest.json"} {
		p := filepath.Join(outDir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing expected output file %s: %v", f, err)
		}
	}

	// Read and parse manifest.json
	manifestBytes, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest StackBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	if genCSHA, ok := manifest.ArtifactDigests["generated_c_sha256"]; !ok || genCSHA == "" {
		t.Errorf("manifest artifact_digests missing generated_c_sha256: %+v", manifest.ArtifactDigests)
	}

	// Read and parse case.json
	caseBytes, err := os.ReadFile(filepath.Join(outDir, "case.json"))
	if err != nil {
		t.Fatalf("read case.json: %v", err)
	}
	var caseObj struct {
		Cases []StackCaseSpec `json:"cases"`
	}
	if err := json.Unmarshal(caseBytes, &caseObj); err != nil {
		t.Fatalf("unmarshal case.json: %v", err)
	}
	if len(caseObj.Cases) != 3 {
		t.Fatalf("expected 3 cases in case.json, got %d", len(caseObj.Cases))
	}
	if caseObj.Cases[0].CaseID != "baseline_54" {
		t.Errorf("expected case 0 case_id=baseline_54, got %s", caseObj.Cases[0].CaseID)
	}
	if len(caseObj.Cases[0].InitialMemory) == 0 || caseObj.Cases[0].InitialMemory[0].Address != 0x7E1E0A || caseObj.Cases[0].InitialMemory[0].Value != 54 {
		t.Errorf("expected initial_memory record at 0x7E1E0A=54, got %+v", caseObj.Cases[0].InitialMemory)
	}

	// Read and parse timeline.json
	timelineBytes, err := os.ReadFile(filepath.Join(outDir, "timeline.json"))
	if err != nil {
		t.Fatalf("read timeline.json: %v", err)
	}
	var timeline []StackTimelineStep
	if err := json.Unmarshal(timelineBytes, &timeline); err != nil {
		t.Fatalf("unmarshal timeline.json: %v", err)
	}
	if len(timeline) != 4 {
		t.Fatalf("expected 4 timeline steps, got %d", len(timeline))
	}
	// Step 3 (PLB) must record stack read 0x7E01FB=12
	if len(timeline[2].Recorded.Reads) != 1 || timeline[2].Recorded.Reads[0].Address != 0x7E01FB || timeline[2].Recorded.Reads[0].Value != 12 {
		t.Errorf("timeline step 3 expected read 0x7E01FB=12, got %+v", timeline[2].Recorded.Reads)
	}
	// Step 4 (INC) must record counter read 0x7E1E0A=54
	if len(timeline[3].Recorded.Reads) != 1 || timeline[3].Recorded.Reads[0].Address != 0x7E1E0A || timeline[3].Recorded.Reads[0].Value != 54 {
		t.Errorf("timeline step 4 expected read 0x7E1E0A=54, got %+v", timeline[3].Recorded.Reads)
	}
	// Steps must have intermediate CState populated
	for idx, st := range timeline {
		if st.Recorded.CState.PC == 0 {
			t.Errorf("step %d missing recorded CState", idx+1)
		}
	}

	// Read and parse receipt.json
	receiptBytes, err := os.ReadFile(filepath.Join(outDir, "receipt.json"))
	if err != nil {
		t.Fatalf("read receipt.json: %v", err)
	}
	var receipt StackReplayReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatalf("unmarshal receipt.json: %v", err)
	}

	if receipt.Status != "success" {
		t.Errorf("expected receipt.Status=success, got %s", receipt.Status)
	}
	if !receipt.BaselineRawVerified {
		t.Errorf("expected BaselineRawVerified=true")
	}
	if !receipt.DualBackendVerified {
		t.Errorf("expected DualBackendVerified=true")
	}
	if !receipt.ThreeWritesVerified {
		t.Errorf("expected ThreeWritesVerified=true")
	}
	if !receipt.ExpectedMatchVerified {
		t.Errorf("expected ExpectedMatchVerified=true")
	}
	if len(receipt.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(receipt.Results))
	}
	for _, res := range receipt.Results {
		if !res.Verified {
			t.Errorf("expected case %s to be verified", res.CaseID)
		}
		if res.TotalWrites != 3 {
			t.Errorf("expected case %s total_writes=3, got %d", res.CaseID, res.TotalWrites)
		}
		if res.WriteOverflow {
			t.Errorf("expected case %s write_overflow=false", res.CaseID)
		}
		if res.MissingRead {
			t.Errorf("expected case %s missing_read=false", res.CaseID)
		}
	}
	if receipt.Results[0].CaseID != "baseline_54" {
		t.Errorf("expected result 0 case_id=baseline_54, got %s", receipt.Results[0].CaseID)
	}
}

func TestStackReplay_MissingCounterRefusal(t *testing.T) {
	ctx := context.Background()
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
		ID:           "block-0cc404-refusal-test",
		StartAddress: 0x0CC404,
		EndAddress:   0x0CC40A,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC40A},
	}

	ir, err := decomp.LiftBlock(block, ctxClear)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	initState := decomp.CPUState{
		A:  3268,
		X:  224,
		Y:  0,
		S:  508,
		D:  0,
		DB: 0,
		PB: 12,
		PC: 50180,
		P:  50,
		E:  false,
	}

	// 1. Emulator control: when 0x7E1E0A is missing from memory map, execution fails / reports uninitialized read
	emptyMem := map[uint32]uint8{}
	_, _, err = decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, emptyMem)
	if err == nil {
		t.Errorf("expected emulator to fail / report uninitialized read when 0x7E1E0A missing, but got nil error")
	}

	// 2. Compiled C control: when 0x7E1E0A is missing from memory callback, MissingRead / refusal reported
	runner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		t.Fatalf("NewCompiledRunner failed: %v", err)
	}
	defer runner.Close()

	cBatch := []decomp.ReplayCaseInput{
		{
			CaseID:  "missing_counter",
			Initial: initState,
			Memory:  nil, // no 0x7E1E0A provided
		},
	}
	cResults, err := runner.RunBatch(ctx, cBatch)
	if err != nil {
		t.Fatalf("runner.RunBatch failed: %v", err)
	}
	if len(cResults) != 1 {
		t.Fatalf("expected 1 result, got %d", len(cResults))
	}
	if !cResults[0].MissingRead {
		t.Errorf("expected compiled runner to report MissingRead=true when counter missing, got false")
	}
	if cResults[0].MissingAddr != 0x7E1E0A {
		t.Errorf("expected MissingAddr=0x7E1E0A, got 0x%06X", cResults[0].MissingAddr)
	}
}

func TestStackReplay_AlteredStackFalsifiers(t *testing.T) {
	ctx := context.Background()
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
		ID:           "block-0cc404-falsifier",
		StartAddress: 0x0CC404,
		EndAddress:   0x0CC40A,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC40A},
	}

	ir, err := decomp.LiftBlock(block, ctxClear)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	// 1. Altered initial stack pointer S (e.g. 0x01FD instead of 0x01FC):
	// Must falsify stack write locations (writes to 0x01FD, 0x01FC instead of 0x01FC, 0x01FB)
	alteredInit := decomp.CPUState{
		A:  3268,
		X:  224,
		Y:  0,
		S:  509, // 0x01FD altered
		D:  0,
		DB: 0,
		PB: 12,
		PC: 50180,
		P:  50,
		E:  false,
	}
	mem := map[uint32]uint8{0x7E1E0A: 54}
	emuRes, _, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, alteredInit, mem)
	if err != nil {
		t.Fatalf("RunEmulatorBlockWithSteps failed: %v", err)
	}
	if len(emuRes.Writes) == 3 && (emuRes.Writes[0].Address == 0x7E01FC && emuRes.Writes[1].Address == 0x7E01FB) {
		t.Errorf("altered stack pointer S=0x01FD should have changed write addresses, but writes matched normal: %+v", emuRes.Writes)
	}

	// 2. Altered memory input (e.g. 99 instead of 54):
	// Must produce output 100 instead of baseline 55
	memAltered := map[uint32]uint8{0x7E1E0A: 99}
	normalInit := decomp.CPUState{
		A: 3268, X: 224, Y: 0, S: 508, D: 0, DB: 0, PB: 12, PC: 50180, P: 50, E: false,
	}
	emuResAlt, _, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, normalInit, memAltered)
	if err != nil {
		t.Fatalf("RunEmulatorBlockWithSteps failed: %v", err)
	}
	if len(emuResAlt.Writes) != 3 || emuResAlt.Writes[2].Value == 55 {
		t.Errorf("altered input 99 should have produced write 100, got: %+v", emuResAlt.Writes)
	}
}

func TestStackReplay_PrefixEffectsGate(t *testing.T) {
	ctx := context.Background()
	ctxClear := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}

	blockInstructions := []recovery.Instruction{
		{
			ID: "insn-0cc404", Architecture: "wdc65816", Address: 0x0CC404, Offset: 0x064404,
			Bytes: "8b", Opcode: 0x8B, Mnemonic: "phb", Mode: "implied", Context: ctxClear,
		},
		{
			ID: "insn-0cc405", Architecture: "wdc65816", Address: 0x0CC405, Offset: 0x064405,
			Bytes: "4b", Opcode: 0x4B, Mnemonic: "phk", Mode: "implied", Context: ctxClear,
		},
		{
			ID: "insn-0cc406", Architecture: "wdc65816", Address: 0x0CC406, Offset: 0x064406,
			Bytes: "ab", Opcode: 0xAB, Mnemonic: "plb", Mode: "implied", Context: ctxClear,
		},
		{
			ID: "insn-0cc407", Architecture: "wdc65816", Address: 0x0CC407, Offset: 0x064407,
			Bytes: "ee0a1e", Opcode: 0xEE, Mnemonic: "inc", Mode: "absolute", Context: ctxClear,
		},
	}

	fullBlock := &structure.BasicBlock{
		ID:           "block-0cc404-prefixgate",
		StartAddress: 0x0CC404,
		EndAddress:   0x0CC40A,
		Instructions: blockInstructions,
		Successors:   []uint32{0x0CC40A},
	}
	ir, err := decomp.LiftBlock(fullBlock, ctxClear)
	if err != nil {
		t.Fatalf("LiftBlock: %v", err)
	}

	initState := decomp.CPUState{
		A: 3268, X: 224, Y: 0, S: 508, D: 0, DB: 0, PB: 12, PC: 50180, P: 50, E: false,
	}
	mem := map[uint32]uint8{0x7E1E0A: 54}

	_, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
	if err != nil {
		t.Fatalf("RunEmulatorBlockWithSteps: %v", err)
	}

	// Build prefix runners for 1..4
	for k := 1; k <= 4; k++ {
		prefixEnd := blockInstructions[k-1].Address + uint32(len(blockInstructions[k-1].Bytes)/2)
		prefixBlock := &structure.BasicBlock{
			ID:           "block-prefix",
			StartAddress: 0x0CC404,
			EndAddress:   prefixEnd,
			Instructions: blockInstructions[:k],
			Successors:   []uint32{prefixEnd},
		}
		prefixIR, err := decomp.LiftBlock(prefixBlock, ctxClear)
		if err != nil {
			t.Fatalf("LiftBlock prefix %d: %v", k, err)
		}
		runner, err := decomp.NewCompiledRunner(ctx, prefixIR)
		if err != nil {
			t.Fatalf("NewCompiledRunner prefix %d: %v", k, err)
		}
		defer runner.Close()

		res, err := runner.RunBatch(ctx, []decomp.ReplayCaseInput{
			{CaseID: "baseline", Initial: initState, Memory: []decomp.MemoryCell{{Address: 0x7E1E0A, Value: 54}}},
		})
		if err != nil {
			t.Fatalf("RunBatch prefix %d: %v", k, err)
		}
		cStepRes := res[0]

		// Build expected cumulative writes through step k-1
		var expWrites []decomp.MemoryWrite
		for s := 0; s < k; s++ {
			expWrites = append(expWrites, emuSteps[s].Writes...)
		}

		if len(cStepRes.Writes) != len(expWrites) {
			t.Errorf("prefix %d write count mismatch: got %d, want %d", k, len(cStepRes.Writes), len(expWrites))
		}
		if cStepRes.TotalWrites != uint32(len(expWrites)) {
			t.Errorf("prefix %d total writes mismatch: got %d, want %d", k, cStepRes.TotalWrites, len(expWrites))
		}
		for wIdx := range expWrites {
			if cStepRes.Writes[wIdx] != expWrites[wIdx] {
				t.Errorf("prefix %d write %d mismatch: got %+v, want %+v", k, wIdx, cStepRes.Writes[wIdx], expWrites[wIdx])
			}
		}
		if cStepRes.WriteOverflow || cStepRes.MissingRead || cStepRes.MMIOAccess {
			t.Errorf("prefix %d refusal flags set: overflow=%v missing_read=%v mmio=%v",
				k, cStepRes.WriteOverflow, cStepRes.MissingRead, cStepRes.MMIOAccess)
		}
	}
}

