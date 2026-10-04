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

func TestLookupReplayFeasibility(t *testing.T) {
	ctx := context.Background()

	ctxSet := recovery.Context{E: "clear", M: "set", X: "set", C: "set"}

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
			Context:      ctxSet,
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
			Context:      ctxSet,
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
			Context:      ctxSet,
		},
	}

	block := &structure.BasicBlock{
		ID:           "block-09f882-lookupreplay",
		StartAddress: 0x09F882,
		EndAddress:   0x09F889,
		Instructions: blockInstructions,
		Successors:   []uint32{0x09F889},
	}

	ir, err := decomp.LiftBlock(block, ctxSet)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	cCode, err := decomp.GenerateCompilableC(ir)
	if err != nil {
		t.Fatalf("GenerateCompilableC failed: %v", err)
	}
	t.Logf("Generated C code:\n%s", cCode)

	initState := decomp.CPUState{
		A:  65535, // 0xFFFF
		X:  0,
		Y:  1,
		S:  509,
		D:  0x1F00,
		DB: 0x09,
		PB: 0x09,
		PC: 0xF882,
		P:  0xB1,
		E:  false,
	}

	runner, err := decomp.NewCompiledRunner(ctx, ir)
	if err != nil {
		t.Fatalf("NewCompiledRunner failed: %v", err)
	}
	defer runner.Close()

	testCases := []struct {
		inputYVal uint8
		romAddr   uint32
		romVal    uint8
		wantFullA uint16
		wantExitY uint16
	}{
		{inputYVal: 114, romAddr: 0x09FBDF, romVal: 22, wantFullA: 0xFF16, wantExitY: 114},
		{inputYVal: 115, romAddr: 0x09FBE0, romVal: 20, wantFullA: 0xFF14, wantExitY: 115},
		{inputYVal: 116, romAddr: 0x09FBE1, romVal: 19, wantFullA: 0xFF13, wantExitY: 116},
	}

	var cBatchCases []decomp.ReplayCaseInput
	for _, tc := range testCases {
		cBatchCases = append(cBatchCases, decomp.ReplayCaseInput{
			CaseID:  fmt.Sprintf("case-%d", tc.inputYVal),
			Initial: initState,
			Memory: []decomp.MemoryCell{
				{Address: 0x7E1F05, Value: tc.inputYVal},
				{Address: tc.romAddr, Value: tc.romVal},
			},
		})
	}
	cResults, err := runner.RunBatch(ctx, cBatchCases)
	if err != nil {
		t.Fatalf("runner.RunBatch failed: %v", err)
	}

	for i, tc := range testCases {
		mem := map[uint32]uint8{
			0x7E1F05:   tc.inputYVal,
			tc.romAddr: tc.romVal,
		}

		// Run Go CPU emulator
		emuRes, emuSteps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, mem)
		if err != nil {
			t.Fatalf("RunEmulatorBlock input %d failed: %v", tc.inputYVal, err)
		}

		cRes := cResults[i]

		t.Logf("Input %d: Emu NextPC=$%06X (steps=%d), C NextPC=$%06X", tc.inputYVal, emuRes.NextPC, len(emuSteps), cRes.NextPC)

		if emuRes.NextPC != 0x09F889 {
			t.Errorf("input %d: expected Emu NextPC $09F889, got $%06X", tc.inputYVal, emuRes.NextPC)
		}
		if cRes.NextPC != 0x09F889 {
			t.Errorf("input %d: expected C NextPC $09F889, got $%06X", tc.inputYVal, cRes.NextPC)
		}
		if emuRes.State.A != tc.wantFullA {
			t.Errorf("input %d: expected Emu Full A $%04X, got $%04X", tc.inputYVal, tc.wantFullA, emuRes.State.A)
		}
		if cRes.State.A != tc.wantFullA {
			t.Errorf("input %d: expected C Full A $%04X, got $%04X", tc.inputYVal, tc.wantFullA, cRes.State.A)
		}
		if emuRes.State.Y != tc.wantExitY {
			t.Errorf("input %d: expected Emu Y %d, got %d", tc.inputYVal, tc.wantExitY, emuRes.State.Y)
		}
		if cRes.State.Y != tc.wantExitY {
			t.Errorf("input %d: expected C Y %d, got %d", tc.inputYVal, tc.wantExitY, cRes.State.Y)
		}
		if len(emuRes.Writes) != 1 || emuRes.Writes[0].Address != 0x7E1F54 || emuRes.Writes[0].Value != tc.romVal {
			t.Errorf("input %d: unexpected emu writes: %+v", tc.inputYVal, emuRes.Writes)
		}
		if len(cRes.Writes) != 1 || cRes.Writes[0].Address != 0x7E1F54 || cRes.Writes[0].Value != tc.romVal {
			t.Errorf("input %d: unexpected C writes: %+v", tc.inputYVal, cRes.Writes)
		}
	}
}

func TestLookupReplayCommand(t *testing.T) {
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
		"lookup-replay",
		"-rom", romPath,
		"-trace", tracePath,
		"-out", outDir,
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run lookup-replay failed: %v, stderr: %s", err, stderr.String())
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
	var receipt LookupReceipt
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
	if !receipt.SingleWriteVerified {
		t.Errorf("expected SingleWriteVerified=true")
	}
	if !receipt.StepAccessesVerified {
		t.Errorf("expected StepAccessesVerified=true")
	}
	if len(receipt.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(receipt.Results))
	}
	for _, r := range receipt.Results {
		if !r.Verified || !r.EmuMatchesC || !r.MatchesExpected {
			t.Errorf("case %s not verified: %+v", r.CaseID, r)
		}
	}

	// Verify case.json has exported non-empty case specifications and replay_cases
	caseBytes, err := os.ReadFile(filepath.Join(outDir, "case.json"))
	if err != nil {
		t.Fatalf("read case.json: %v", err)
	}
	var caseObj struct {
		CaseID        string                   `json:"case_id"`
		Cases         []LookupCaseSpec         `json:"cases"`
		ExpectedSpecs []LookupCaseSpec         `json:"expected_specs"`
		ReplayCases   []decomp.ReplayCaseInput `json:"replay_cases"`
	}
	if err := json.Unmarshal(caseBytes, &caseObj); err != nil {
		t.Fatalf("unmarshal case.json: %v", err)
	}
	if len(caseObj.Cases) != 3 {
		t.Fatalf("expected 3 cases in case.json, got %d", len(caseObj.Cases))
	}
	if len(caseObj.ExpectedSpecs) != 3 {
		t.Fatalf("expected 3 expected_specs in case.json, got %d", len(caseObj.ExpectedSpecs))
	}
	if len(caseObj.ReplayCases) != 3 {
		t.Fatalf("expected 3 replay_cases in case.json, got %d", len(caseObj.ReplayCases))
	}
	for i, rc := range caseObj.ReplayCases {
		if rc.CaseID == "" || rc.Initial.PC != 0xF882 || len(rc.Memory) != 4 {
			t.Errorf("replay_cases[%d] invalid: %+v", i, rc)
		}
	}
	for i, c := range caseObj.Cases {
		if c.CaseID == "" || c.Kind == "" || c.ExpectedROMAddr == "" || c.WantNextPC == 0 || len(c.Memory) != 4 {
			t.Errorf("case[%d] has empty fields in case.json: %+v", i, c)
		}
	}

	// Verify timeline.json has raw recorded states and dynamic accesses
	timelineBytes, err := os.ReadFile(filepath.Join(outDir, "timeline.json"))
	if err != nil {
		t.Fatalf("read timeline.json: %v", err)
	}
	var timeline []LookupTimelineStep
	if err := json.Unmarshal(timelineBytes, &timeline); err != nil {
		t.Fatalf("unmarshal timeline.json: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("expected 3 timeline steps, got %d", len(timeline))
	}
	if timeline[0].Recorded.EntryY != "$0045" || timeline[0].Recorded.ExitY != "$0073" {
		t.Errorf("step 0 recorded EntryY/ExitY unexpected: entry=%s, exit=%s", timeline[0].Recorded.EntryY, timeline[0].Recorded.ExitY)
	}
	if timeline[0].Recorded.OperandEventID != 52076 {
		t.Errorf("step 0 expected OperandEventID 52076, got %d", timeline[0].Recorded.OperandEventID)
	}
	if timeline[1].Recorded.OperandEventID != 52081 {
		t.Errorf("step 1 expected OperandEventID 52081, got %d", timeline[1].Recorded.OperandEventID)
	}
	if timeline[2].Recorded.OperandEventID != 52085 {
		t.Errorf("step 2 expected OperandEventID 52085, got %d", timeline[2].Recorded.OperandEventID)
	}
	if len(timeline[0].Recorded.Reads) != 1 || len(timeline[1].Recorded.Reads) != 1 || len(timeline[2].Recorded.Writes) != 1 {
		t.Errorf("timeline recorded reads/writes counts unexpected: step0 reads=%d, step1 reads=%d, step2 writes=%d",
			len(timeline[0].Recorded.Reads), len(timeline[1].Recorded.Reads), len(timeline[2].Recorded.Writes))
	}
	for sIdx := 0; sIdx < 3; sIdx++ {
		if len(timeline[sIdx].Predictions) != 2 {
			t.Errorf("step %d expected 2 predictions, got %d", sIdx, len(timeline[sIdx].Predictions))
		}
	}
}
