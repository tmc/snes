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
		t.Errorf("expected 3 results, got %d", len(receipt.Results))
	}
}

