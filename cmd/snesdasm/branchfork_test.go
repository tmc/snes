package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestBranchForkFeasibility(t *testing.T) {
	ctx := context.Background()

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

	ir, err := decomp.LiftBlock(block, ctxClear)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		t.Fatalf("GenerateCompilableC failed: %v", err)
	}
	t.Logf("Generated C code:\n%s", cCode)

	// Initial CPUState matching event 29897 entry:
	// a: 46860 (0xB70C), x: 224, y: 0, s: 508, d: 0, db: 0, pb: 12, pc: 49440 (0xC120), p: 48 (0x30), e: false
	initState := decomp.CPUState{
		A:  46860,
		X:  224,
		Y:  0,
		S:  508,
		D:  0,
		DB: 0,
		PB: 12,
		PC: 49440,
		P:  48,
		E:  false,
	}

	runner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		t.Fatalf("NewCompiledRunner failed: %v", err)
	}
	defer runner.Close()

	testCases := []struct {
		inputVal   uint8
		wantNextPC uint32
	}{
		{inputVal: 3, wantNextPC: 0x0CC133},
		{inputVal: 7, wantNextPC: 0x0CC133},
		{inputVal: 8, wantNextPC: 0x0CC126},
		{inputVal: 9, wantNextPC: 0x0CC126},
	}

	var cBatchCases []decomp.ReplayCaseInput
	for _, tc := range testCases {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  fmt.Sprintf("case-%d", tc.inputVal),
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x000011, Value: tc.inputVal},
			},
		})
	}
	cResults, err := runner.RunBatch(ctx, cBatchCases)
	if err != nil {
		t.Fatalf("runner.RunBatch failed: %v", err)
	}

	for i, tc := range testCases {
		mem := map[uint32]uint8{
			0x000011: tc.inputVal,
		}

		// Run Go CPU Emulator
		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			t.Fatalf("RunEmulatorBlock input %d failed: %v", tc.inputVal, err)
		}

		cRes := cResults[i]

		t.Logf("Input %d: Emu NextPC=$%06X (steps=%d), C NextPC=$%06X", tc.inputVal, emuRes.NextPC, len(emuSteps), cRes.NextPC)

		if emuRes.NextPC != tc.wantNextPC {
			t.Errorf("input %d: expected Emu NextPC $%06X, got $%06X", tc.inputVal, tc.wantNextPC, emuRes.NextPC)
		}
		if cRes.NextPC != tc.wantNextPC {
			t.Errorf("input %d: expected C NextPC $%06X, got $%06X", tc.inputVal, tc.wantNextPC, cRes.NextPC)
		}
		if emuRes.NextPC != cRes.NextPC {
			t.Errorf("input %d: NextPC mismatch between Emu ($%06X) and C ($%06X)", tc.inputVal, emuRes.NextPC, cRes.NextPC)
		}
		if len(emuRes.Writes) != 0 {
			t.Errorf("input %d: expected 0 writes, got %d", tc.inputVal, len(emuRes.Writes))
		}
		if len(cRes.Writes) != 0 {
			t.Errorf("input %d: expected 0 writes in C, got %d", tc.inputVal, len(cRes.Writes))
		}
	}
}

func TestBranchForkCommand(t *testing.T) {
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
		"branch-fork",
		"-rom", romPath,
		"-trace", tracePath,
		"-out", outDir,
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run branch-fork failed: %v, stderr: %s", err, stderr.String())
	}

	// Verify required files created
	for _, f := range []string{"case.json", "generated.c", "timeline.json", "receipt.json"} {
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
	var receipt BranchForkReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatalf("unmarshal receipt.json: %v", err)
	}

	if !receipt.DualBackendVerified {
		t.Errorf("expected DualBackendVerified=true")
	}
	if !receipt.ZeroWritesVerified {
		t.Errorf("expected ZeroWritesVerified=true")
	}
	if len(receipt.Results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(receipt.Results))
	}
}

