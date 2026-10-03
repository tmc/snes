package decomp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestMutableMemory_ReadAfterWrite(t *testing.T) {
	// Block that writes to memory and immediately reads it back:
	// $008000: lda.w #$1234
	// $008003: sta $7ec050
	// $008007: lda.w #$0000
	// $00800A: lda $7ec050
	block := &structure.BasicBlock{
		ID:           "bb-raw",
		StartAddress: 0x008000,
		EndAddress:   0x00800E,
		Successors:   []uint32{0x00800E},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a93412",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8003",
				Address:  0x008003,
				Bytes:    "8f50c07e",
				Opcode:   0x8F,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8007",
				Address:  0x008007,
				Bytes:    "a90000",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:800a",
				Address:  0x00800A,
				Bytes:    "af50c07e",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x00,
		PC: 0x8000,
	}
	mem := make(map[uint32]uint8)

	receipt := Compare(context.Background(), ir, "read_after_write", init, mem, DefaultVerifyConfig())
	if !receipt.Matched {
		t.Fatalf("receipt mismatch: %s", receipt.Discrepancy)
	}
	if receipt.ActualC.State.A != 0x1234 {
		t.Errorf("read-after-write failed: expected A=0x1234, got 0x%04X", receipt.ActualC.State.A)
	}
}

func TestEntryContractEnforcement(t *testing.T) {
	block := &structure.BasicBlock{
		ID:           "bb-contract",
		StartAddress: 0x008000,
		EndAddress:   0x008001,
		Successors:   []uint32{0x008001},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "ea",
				Opcode:   0xEA,
				Mnemonic: "nop",
				Context:  recovery.Context{E: "clear", M: "clear", X: "set", C: "clear"},
			},
		},
	}
	entryCtx := recovery.Context{E: "clear", M: "clear", X: "set", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatal(err)
	}

	// 0. Nil IR rejection
	nilRec := Compare(context.Background(), nil, "nil_ir", CPUState{}, nil, DefaultVerifyConfig())
	if nilRec.Matched || !strings.Contains(nilRec.Discrepancy, "nil BlockIR") {
		t.Errorf("expected failure on nil IR, got: %+v", nilRec)
	}

	// 1. M mismatch: expected M=0 (16-bit), supply M=1 (bit 0x20 set)
	badM := CPUState{P: 0x20 | 0x10, PB: 0x00, PC: 0x8000}
	recM := Compare(context.Background(), ir, "bad_m", badM, nil, DefaultVerifyConfig())
	if recM.Matched {
		t.Errorf("expected failure on M mismatch")
	}
	if !strings.Contains(recM.Discrepancy, "expected M=0") {
		t.Errorf("unexpected discrepancy: %s", recM.Discrepancy)
	}

	// 2. X mismatch: expected X=1 (8-bit), supply X=0 (bit 0x10 clear)
	badX := CPUState{P: 0x00, PB: 0x00, PC: 0x8000}
	recX := Compare(context.Background(), ir, "bad_x", badX, nil, DefaultVerifyConfig())
	if recX.Matched {
		t.Errorf("expected failure on X mismatch")
	}
	if !strings.Contains(recX.Discrepancy, "expected X=1") {
		t.Errorf("unexpected discrepancy: %s", recX.Discrepancy)
	}

	// 3. E mismatch: expected E=false, supply E=true
	badE := CPUState{P: 0x10, E: true, PB: 0x00, PC: 0x8000}
	recE := Compare(context.Background(), ir, "bad_e", badE, nil, DefaultVerifyConfig())
	if recE.Matched {
		t.Errorf("expected failure on E mismatch")
	}
	if !strings.Contains(recE.Discrepancy, "expected E=false") {
		t.Errorf("unexpected discrepancy: %s", recE.Discrepancy)
	}
}

func TestContextConflictAndUnknownWidth(t *testing.T) {
	block := &structure.BasicBlock{
		ID:           "bb-conflict",
		StartAddress: 0x008000,
		EndAddress:   0x008001,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "ea",
				Opcode:   0xEA,
				Mnemonic: "nop",
				Context:  recovery.Context{E: "clear", M: "set", X: "set"},
			},
		},
	}

	// Caller specifies M=clear, but instruction 0 has M=set
	conflictCtx := recovery.Context{E: "clear", M: "clear", X: "set"}
	_, err := LiftBlock(block, conflictCtx)
	if err == nil || !strings.Contains(err.Error(), "context conflict") {
		t.Errorf("expected context conflict error, got: %v", err)
	}

	// Unknown M width must be rejected
	unknownBlock := &structure.BasicBlock{
		ID:           "bb-unknown",
		StartAddress: 0x008000,
		EndAddress:   0x008001,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "ea",
				Opcode:   0xEA,
				Mnemonic: "nop",
				Context:  recovery.Context{E: "clear", M: "unknown", X: "set"},
			},
		},
	}
	_, err = LiftBlock(unknownBlock, recovery.Context{E: "clear", M: "unknown", X: "set"})
	if err == nil || !strings.Contains(err.Error(), "unresolved entry context") {
		t.Errorf("expected unresolved entry context error, got: %v", err)
	}
}

func TestNonZeroBank_Differential(t *testing.T) {
	// Block in Bank $01 ($018000):
	// $018000: lda.w #$5678
	// $018003: sta $7ec020
	block := &structure.BasicBlock{
		ID:           "bb-bank01",
		StartAddress: 0x018000,
		EndAddress:   0x018007,
		Successors:   []uint32{0x018007},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:018000",
				Address:  0x018000,
				Bytes:    "a97856",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:018003",
				Address:  0x018003,
				Bytes:    "8f20c07e",
				Opcode:   0x8F,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x01,
		PC: 0x8000,
	}
	receipt := Compare(context.Background(), ir, "nonzero_bank", init, nil, DefaultVerifyConfig())
	if !receipt.Matched {
		t.Fatalf("nonzero bank comparison failed: %s", receipt.Discrepancy)
	}
	if receipt.ActualC.State.PB != 0x01 {
		t.Errorf("expected PB=0x01, got 0x%02X", receipt.ActualC.State.PB)
	}
	if receipt.ActualC.State.A != 0x5678 {
		t.Errorf("expected A=0x5678, got 0x%04X", receipt.ActualC.State.A)
	}
}

func TestAddressAliasing_MirrorWRAM(t *testing.T) {
	// Store to Direct Page mirror $000080, then load from absolute long $7E0080
	// $008000: lda.w #$AABB
	// $008003: sta $80 (dp in bank 0)
	// $008005: lda.w #$0000
	// $008008: lda $7e0080 (long absolute WRAM)
	block := &structure.BasicBlock{
		ID:           "bb-alias",
		StartAddress: 0x008000,
		EndAddress:   0x00800C,
		Successors:   []uint32{0x00800C},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a9bbaa",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8003",
				Address:  0x008003,
				Bytes:    "8580",
				Opcode:   0x85,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8005",
				Address:  0x008005,
				Bytes:    "a90000",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8008",
				Address:  0x008008,
				Bytes:    "af80007e",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x00,
		PC: 0x8000,
	}
	receipt := Compare(context.Background(), ir, "aliasing_test", init, nil, DefaultVerifyConfig())
	if !receipt.Matched {
		t.Fatalf("aliasing comparison failed: %s", receipt.Discrepancy)
	}
	if receipt.ActualC.State.A != 0xAABB {
		t.Errorf("aliasing read-after-write failed: expected A=0xAABB, got 0x%04X", receipt.ActualC.State.A)
	}
}

func TestMMIORejection(t *testing.T) {
	// Block attempting to write to PPU MMIO register $2100 (INIDISP)
	// $008000: lda.w #$000F
	// $008003: sta $002100
	block := &structure.BasicBlock{
		ID:           "bb-mmio",
		StartAddress: 0x008000,
		EndAddress:   0x008007,
		Successors:   []uint32{0x008007},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a90f00",
				Opcode:   0xA9,
				Mnemonic: "lda.w",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:8003",
				Address:  0x008003,
				Bytes:    "8f002100",
				Opcode:   0x8F,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x00,
		PC: 0x8000,
	}
	receipt := Compare(context.Background(), ir, "mmio_test", init, nil, DefaultVerifyConfig())
	if receipt.Matched {
		t.Fatalf("expected MMIO access to be rejected, but comparison passed")
	}
	if !strings.Contains(receipt.Discrepancy, "unsupported MMIO access") {
		t.Errorf("unexpected discrepancy: %s", receipt.Discrepancy)
	}
}

func TestUninitializedMemoryRejection(t *testing.T) {
	// Block reading from uninitialized memory address $7EC999
	// $008000: lda $7ec999
	block := &structure.BasicBlock{
		ID:           "bb-uninit",
		StartAddress: 0x008000,
		EndAddress:   0x008004,
		Successors:   []uint32{0x008004},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "af99c97e",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock failed: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x00,
		PC: 0x8000,
	}
	receipt := Compare(context.Background(), ir, "uninit_test", init, nil, DefaultVerifyConfig())
	if receipt.Matched {
		t.Fatalf("expected uninitialized memory read to be rejected, but comparison passed")
	}
	if !strings.Contains(receipt.Discrepancy, "uninitialized memory address") {
		t.Errorf("unexpected discrepancy: %s", receipt.Discrepancy)
	}
}

func TestFaultyCChecker_CatchesDiscrepancies(t *testing.T) {
	baseExpected := ExecResult{
		State: CPUState{
			A:  0x1111,
			X:  0x2222,
			Y:  0x3333,
			S:  0x01FE,
			D:  0x0100,
			DB: 0x7E,
			PB: 0x00,
			P:  0x31, // M=1, X=1, C=1
			E:  false,
			PC: 0x8100,
		},
		NextPC: 0x008100,
		Writes: []MemoryWrite{
			{Address: 0x7EC000, Value: 0xAA},
			{Address: 0x7EC001, Value: 0xBB},
		},
	}

	tests := []struct {
		name       string
		mutate     func(res *ExecResult)
		wantSubstr string
	}{
		{
			name:       "A_mismatch",
			mutate:     func(res *ExecResult) { res.State.A = 0x9999 },
			wantSubstr: "A mismatch",
		},
		{
			name:       "X_mismatch",
			mutate:     func(res *ExecResult) { res.State.X = 0x9999 },
			wantSubstr: "X mismatch",
		},
		{
			name:       "Y_mismatch",
			mutate:     func(res *ExecResult) { res.State.Y = 0x9999 },
			wantSubstr: "Y mismatch",
		},
		{
			name:       "S_mismatch",
			mutate:     func(res *ExecResult) { res.State.S = 0x0100 },
			wantSubstr: "S mismatch",
		},
		{
			name:       "D_mismatch",
			mutate:     func(res *ExecResult) { res.State.D = 0x0000 },
			wantSubstr: "D mismatch",
		},
		{
			name:       "DB_mismatch",
			mutate:     func(res *ExecResult) { res.State.DB = 0x00 },
			wantSubstr: "DB mismatch",
		},
		{
			name:       "PB_mismatch",
			mutate:     func(res *ExecResult) { res.State.PB = 0x01 },
			wantSubstr: "PB mismatch",
		},
		{
			name:       "PC_mismatch",
			mutate:     func(res *ExecResult) { res.State.PC = 0x9999 },
			wantSubstr: "PC mismatch",
		},
		{
			name:       "P_Flags_mismatch",
			mutate:     func(res *ExecResult) { res.State.P ^= 0x01 }, // flip Carry
			wantSubstr: "Flags mismatch",
		},
		{
			name:       "E_mismatch",
			mutate:     func(res *ExecResult) { res.State.E = true },
			wantSubstr: "E mismatch",
		},
		{
			name:       "NextPC_mismatch",
			mutate:     func(res *ExecResult) { res.NextPC = 0x008200 },
			wantSubstr: "NextPC mismatch",
		},
		{
			name: "Writes_extra",
			mutate: func(res *ExecResult) {
				res.Writes = append(res.Writes, MemoryWrite{Address: 0x7EC002, Value: 0xCC})
			},
			wantSubstr: "Write count mismatch",
		},
		{
			name:       "Writes_missing",
			mutate:     func(res *ExecResult) { res.Writes = res.Writes[:1] },
			wantSubstr: "Write count mismatch",
		},
		{
			name:       "Writes_wrong_value",
			mutate:     func(res *ExecResult) { res.Writes[0].Value = 0xFF },
			wantSubstr: "Write[0] mismatch",
		},
		{
			name:       "Writes_wrong_address",
			mutate:     func(res *ExecResult) { res.Writes[1].Address = 0x7EC005 },
			wantSubstr: "Write[1] mismatch",
		},
		{
			name:       "Write_overflow_mismatch",
			mutate:     func(res *ExecResult) { res.WriteOverflow = true },
			wantSubstr: "Write overflow mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := baseExpected
			actual.Writes = make([]MemoryWrite, len(baseExpected.Writes))
			copy(actual.Writes, baseExpected.Writes)
			tt.mutate(&actual)

			matched, disc := CompareExecResults(baseExpected, actual)
			if matched {
				t.Fatalf("expected discrepancy for %s, but checker passed", tt.name)
			}
			if !strings.Contains(disc, tt.wantSubstr) {
				t.Errorf("discrepancy = %q, want substring %q", disc, tt.wantSubstr)
			}
		})
	}
}

func TestReceiptSaveLoad(t *testing.T) {
	tempDir := t.TempDir()
	receiptPath := ReceiptPath(tempDir, "bb-test")

	dummyIR := &BlockIR{
		BlockID:      "bb-test",
		StartAddress: 0x008000,
		EntryContext: recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Mnemonic: "nop", Bytes: "ea"},
		},
	}
	codeHash := ComputeBlockCodeHash(dummyIR)
	cCode := "/* valid C */"
	cHash := ComputeCHash(cCode)
	mem := map[uint32]uint8{0x7E0010: 0x42}
	memHash, memCells := ComputeInitialMemory(mem)

	initState := CPUState{A: 0x1234, PB: 0x00, PC: 0x8000}
	orig := ComparisonReceipt{
		Metadata: ReceiptMetadata{
			BlockID:             "bb-test",
			StartAddress:        0x008000,
			CodeHash:            codeHash,
			GeneratedCHash:      cHash,
			Compiler:            "cc (clang)",
			CompilerFlags:       "cc -O0 -Wall -Werror -Wno-unused-function -Wno-unused-label",
			ROMSHA256:           "rom_sha_1234",
			ProjectRevision:     "rev_abcd",
			Context:             dummyIR.EntryContext,
			MemoryPolicy:        "snes_wram_mirror_v1",
			InitialMemHash:      memHash,
			InitialCPUStateHash: ComputeCPUStateHash(initState),
			Timestamp:           time.Now().UTC().Format(time.RFC3339),
		},
		CaseName:   "test_case",
		Matched:    true,
		Initial:    initState,
		InitialMem: memCells,
		Expected:   ExecResult{State: CPUState{A: 0x1234, PB: 0x00, PC: 0x8001}, NextPC: 0x008001},
		ActualC:    ExecResult{State: CPUState{A: 0x1234, PB: 0x00, PC: 0x8001}, NextPC: 0x008001},
	}

	if err := SaveReceipt(receiptPath, orig); err != nil {
		t.Fatalf("SaveReceipt failed: %v", err)
	}

	loaded, err := LoadReceipt(receiptPath)
	if err != nil {
		t.Fatalf("LoadReceipt failed: %v", err)
	}

	if loaded.CaseName != orig.CaseName || !loaded.Matched || loaded.ActualC.State.A != 0x1234 {
		t.Errorf("loaded receipt mismatch: got %+v, want %+v", loaded, orig)
	}

	// 1. Fresh receipt: matches everything and is eligible
	fresh := loaded
	ValidateReceiptFreshness(&fresh, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if fresh.Metadata.IsStale || !fresh.EligibleMatched() {
		t.Errorf("expected fresh receipt to be non-stale and eligible, got stale=%v, reason=%s", fresh.Metadata.IsStale, fresh.Metadata.StaleReason)
	}

	// 2. Missing expected ROM SHA256 -> stale & ineligible
	missingExpROM := loaded
	ValidateReceiptFreshness(&missingExpROM, dummyIR, cCode, mem, "", "rev_abcd")
	if !missingExpROM.Metadata.IsStale || missingExpROM.EligibleMatched() {
		t.Errorf("expected receipt with missing expected ROM to be stale and ineligible")
	}

	// 3. Missing receipt ROM SHA256 -> stale & ineligible
	missingRecROM := loaded
	missingRecROM.Metadata.ROMSHA256 = ""
	ValidateReceiptFreshness(&missingRecROM, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !missingRecROM.Metadata.IsStale || missingRecROM.EligibleMatched() {
		t.Errorf("expected receipt with missing receipt ROM to be stale and ineligible")
	}

	// 4. Changed ROM SHA256 -> stale & ineligible
	staleROM := loaded
	ValidateReceiptFreshness(&staleROM, dummyIR, cCode, mem, "different_rom_sha", "rev_abcd")
	if !staleROM.Metadata.IsStale || staleROM.EligibleMatched() {
		t.Errorf("expected receipt with different ROM SHA to be stale and ineligible")
	}

	// 5. Missing expected ProjectRevision -> stale & ineligible
	missingExpRev := loaded
	ValidateReceiptFreshness(&missingExpRev, dummyIR, cCode, mem, "rom_sha_1234", "")
	if !missingExpRev.Metadata.IsStale || missingExpRev.EligibleMatched() {
		t.Errorf("expected receipt with missing expected revision to be stale and ineligible")
	}

	// 6. Missing receipt ProjectRevision -> stale & ineligible
	missingRecRev := loaded
	missingRecRev.Metadata.ProjectRevision = ""
	ValidateReceiptFreshness(&missingRecRev, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !missingRecRev.Metadata.IsStale || missingRecRev.EligibleMatched() {
		t.Errorf("expected receipt with missing receipt revision to be stale and ineligible")
	}

	// 7. Changed ProjectRevision (CLI vs Server revision drift) -> stale & ineligible
	staleRev := loaded
	ValidateReceiptFreshness(&staleRev, dummyIR, cCode, mem, "rom_sha_1234", "different_rev")
	if !staleRev.Metadata.IsStale || staleRev.EligibleMatched() {
		t.Errorf("expected receipt with different revision to be stale and ineligible")
	}

	// 8. Changed runtime memory -> stale & ineligible
	staleMem := loaded
	diffMem := map[uint32]uint8{0x7E0010: 0x99}
	ValidateReceiptFreshness(&staleMem, dummyIR, cCode, diffMem, "rom_sha_1234", "rev_abcd")
	if !staleMem.Metadata.IsStale || staleMem.EligibleMatched() {
		t.Errorf("expected receipt with different memory to be stale and ineligible")
	}

	// 9. Changed CodeHash -> stale & ineligible
	diffIR := &BlockIR{
		BlockID:      "bb-test",
		StartAddress: 0x008000,
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Mnemonic: "nop", Bytes: "ea"},
			{Address: 0x008001, Mnemonic: "nop", Bytes: "ea"},
		},
	}
	staleCode := loaded
	ValidateReceiptFreshness(&staleCode, diffIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !staleCode.Metadata.IsStale || staleCode.EligibleMatched() {
		t.Errorf("expected receipt with different code to be stale and ineligible")
	}

	// 10. Tampered initial memory fixture (cell changed without updating metadata) -> stale
	tamperedMem := loaded
	tamperedMem.InitialMem = append([]MemoryCell(nil), loaded.InitialMem...)
	tamperedMem.InitialMem[0] = MemoryCell{Address: 0x7E0010, Value: 0x99}
	ValidateReceiptFreshness(&tamperedMem, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !tamperedMem.Metadata.IsStale || tamperedMem.EligibleMatched() {
		t.Errorf("expected receipt with tampered initial memory fixture to be stale and ineligible")
	}
	if !strings.Contains(tamperedMem.Metadata.StaleReason, "tampered initial memory fixture") {
		t.Errorf("expected 'tampered initial memory fixture' reason, got: %s", tamperedMem.Metadata.StaleReason)
	}

	// 11. Missing initial memory hash -> stale
	missingMemHash := loaded
	missingMemHash.Metadata.InitialMemHash = ""
	ValidateReceiptFreshness(&missingMemHash, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !missingMemHash.Metadata.IsStale || missingMemHash.EligibleMatched() {
		t.Errorf("expected receipt with missing initial memory hash to be stale and ineligible")
	}

	// 12. Tampered initial CPU state (state changed without updating metadata) -> stale
	tamperedState := loaded
	tamperedState.Initial.A = 0x9999
	ValidateReceiptFreshness(&tamperedState, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !tamperedState.Metadata.IsStale || tamperedState.EligibleMatched() {
		t.Errorf("expected receipt with tampered initial CPU state to be stale and ineligible")
	}
	if !strings.Contains(tamperedState.Metadata.StaleReason, "tampered initial CPU state") {
		t.Errorf("expected 'tampered initial CPU state' reason, got: %s", tamperedState.Metadata.StaleReason)
	}

	// 13. Missing initial CPU state hash -> stale
	missingStateHash := loaded
	missingStateHash.Metadata.InitialCPUStateHash = ""
	ValidateReceiptFreshness(&missingStateHash, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !missingStateHash.Metadata.IsStale || missingStateHash.EligibleMatched() {
		t.Errorf("expected receipt with missing initial CPU state hash to be stale and ineligible")
	}

	// 14. Empty memory fixture has defined EmptyMemoryHash and remains eligible when matched
	emptyMem := map[uint32]uint8{}
	emptyHash, emptyCells := ComputeInitialMemory(emptyMem)
	if emptyHash != EmptyMemoryHash {
		t.Errorf("ComputeInitialMemory(empty) = %q, want EmptyMemoryHash = %q", emptyHash, EmptyMemoryHash)
	}
	emptyReceipt := loaded
	emptyReceipt.InitialMem = emptyCells
	emptyReceipt.Metadata.InitialMemHash = emptyHash
	ValidateReceiptFreshness(&emptyReceipt, dummyIR, cCode, emptyMem, "rom_sha_1234", "rev_abcd")
	if emptyReceipt.Metadata.IsStale || !emptyReceipt.EligibleMatched() {
		t.Errorf("expected empty-memory receipt to be non-stale and eligible, got stale=%v, reason=%s", emptyReceipt.Metadata.IsStale, emptyReceipt.Metadata.StaleReason)
	}

	// 15. Unsupported memory policy -> stale
	badPolicy := loaded
	badPolicy.Metadata.MemoryPolicy = "unknown_policy_v99"
	ValidateReceiptFreshness(&badPolicy, dummyIR, cCode, mem, "rom_sha_1234", "rev_abcd")
	if !badPolicy.Metadata.IsStale || badPolicy.EligibleMatched() {
		t.Errorf("expected receipt with unsupported memory policy to be stale and ineligible")
	}
}

func TestAddressAliasing_Banks02And82(t *testing.T) {
	// Block in bank $02 testing mirror Low RAM in banks $02 and $82 vs canonical $7E
	// $028000: sta $020080 (store through bank $02 Low RAM alias)
	// $028004: lda $7e0080 (load through WRAM canonical address)
	// $028008: sta $820080 (store through bank $82 Low RAM alias)
	// $02800C: lda $020080 (load through bank $02 Low RAM alias)
	block := &structure.BasicBlock{
		ID:           "bb-alias-02-82",
		StartAddress: 0x028000,
		EndAddress:   0x028010,
		Successors:   []uint32{0x028010},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:028000",
				Address:  0x028000,
				Bytes:    "8f800002",
				Opcode:   0x8F,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:028004",
				Address:  0x028004,
				Bytes:    "af80007e",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:028008",
				Address:  0x028008,
				Bytes:    "8f800082",
				Opcode:   0x8F,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
			{
				ID:       "inst:02800c",
				Address:  0x02800C,
				Bytes:    "af800002",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock: %v", err)
	}

	init := CPUState{
		A:  0xBEEF,
		S:  0x01FF,
		PB: 0x02,
		PC: 0x8000,
	}
	mem := map[uint32]uint8{
		0x7E0080: 0x11,
		0x7E0081: 0x22,
	}

	receipt := CompareBlockExecution(t, ir, "bank02_82_mirror_alias", init, mem)
	if !receipt.Matched {
		t.Fatalf("aliasing across banks $02 and $82 failed: %s", receipt.Discrepancy)
	}
	if receipt.Expected.State.A != 0xBEEF || receipt.ActualC.State.A != 0xBEEF {
		t.Errorf("expected A=0xBEEF, got emu=0x%04X, c=0x%04X", receipt.Expected.State.A, receipt.ActualC.State.A)
	}
}

func TestNonZeroBank_InitializedDataRead(t *testing.T) {
	// Block in bank $02 reading data from initial memory in bank $02
	// $028000: lda $028050
	block := &structure.BasicBlock{
		ID:           "bb-nonzero-dataread",
		StartAddress: 0x028000,
		EndAddress:   0x028004,
		Successors:   []uint32{0x028004},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:028000",
				Address:  0x028000,
				Bytes:    "af508002",
				Opcode:   0xAF,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
			},
		},
	}

	entryCtx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"}
	ir, err := LiftBlock(block, entryCtx)
	if err != nil {
		t.Fatalf("LiftBlock: %v", err)
	}

	init := CPUState{
		A:  0x0000,
		S:  0x01FF,
		PB: 0x02,
		PC: 0x8000,
	}
	// Data stored at $028050 and $028051 (16-bit read)
	mem := map[uint32]uint8{
		0x028050: 0x42,
		0x028051: 0x24,
	}

	receipt := CompareBlockExecution(t, ir, "nonzero_bank_initial_data_read", init, mem)
	if !receipt.Matched {
		t.Fatalf("nonzero bank data read failed: %s", receipt.Discrepancy)
	}
	if receipt.Expected.State.A != 0x2442 || receipt.ActualC.State.A != 0x2442 {
		t.Errorf("expected A=0x2442, got emu=0x%04X, c=0x%04X", receipt.Expected.State.A, receipt.ActualC.State.A)
	}
}

func TestDeterministicConflictingAliasDetection(t *testing.T) {
	ir := &BlockIR{
		BlockID:      "bb-test",
		StartAddress: 0x008000,
		EntryContext: recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Mnemonic: "nop", Bytes: "ea"},
		},
	}
	init := CPUState{
		PB: 0x00,
		PC: 0x8000,
	}
	// 0x000080 and 0x7E0080 are aliases of canonical address $7E0080, but have conflicting values
	conflictingMem := map[uint32]uint8{
		0x000080: 0x11,
		0x7E0080: 0x22,
	}

	_, err := RunEmulatorBlock(context.Background(), ir, init, conflictingMem)
	if err == nil || !strings.Contains(err.Error(), "conflicting initial memory values") {
		t.Errorf("expected conflicting initial memory values error from RunEmulatorBlock, got: %v", err)
	}

	_, err = RunRawCompilableC(context.Background(), ir.StartAddress, "void f(){}", init, conflictingMem)
	if err == nil || !strings.Contains(err.Error(), "conflicting initial memory values") {
		t.Errorf("expected conflicting initial memory values error from RunRawCompilableC, got: %v", err)
	}
}

func TestFailClosed_EntryValidation(t *testing.T) {
	// 1. Nil IR to Compare
	nilReceipt := Compare(context.Background(), nil, "nil_ir", CPUState{}, nil, DefaultVerifyConfig())
	if nilReceipt.Matched || !strings.Contains(nilReceipt.Discrepancy, "nil BlockIR") {
		t.Errorf("expected 'nil BlockIR' discrepancy for nil IR, got: %s", nilReceipt.Discrepancy)
	}

	// 2. Malformed context value
	dummyBlock := &structure.BasicBlock{
		ID:           "bb-malformed",
		StartAddress: 0x008000,
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Mnemonic: "nop", Bytes: "ea", Context: recovery.Context{E: "invalid_val", M: "clear", X: "clear"}},
		},
	}
	_, err := LiftBlock(dummyBlock, recovery.Context{})
	if err == nil || !strings.Contains(err.Error(), "malformed context value") {
		t.Errorf("expected malformed context error, got: %v", err)
	}

	// 3. Impossible 65816 context: emulation mode (E=1) with 16-bit accumulator (M=0)
	dummyBlock.Instructions[0].Context = recovery.Context{E: "set", M: "clear", X: "set"}
	_, err = LiftBlock(dummyBlock, recovery.Context{})
	if err == nil || !strings.Contains(err.Error(), "impossible 65816 context") {
		t.Errorf("expected impossible 65816 context error for E=1/M=0, got: %v", err)
	}

	// 4. Impossible 65816 context: emulation mode (E=1) with 16-bit index (X=0)
	dummyBlock.Instructions[0].Context = recovery.Context{E: "set", M: "set", X: "clear"}
	_, err = LiftBlock(dummyBlock, recovery.Context{})
	if err == nil || !strings.Contains(err.Error(), "impossible 65816 context") {
		t.Errorf("expected impossible 65816 context error for E=1/X=0, got: %v", err)
	}

	// 5. Wrong start PC: block starts at $008000, init state has PC $009000
	validIR := &BlockIR{
		BlockID:      "bb-valid",
		StartAddress: 0x008000,
		EntryContext: recovery.Context{E: "clear", M: "clear", X: "clear"},
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Mnemonic: "nop", Bytes: "ea"},
		},
	}
	mismatchedInit := CPUState{
		PB: 0x00,
		PC: 0x9000,
	}
	err = EnforceEntryContract(validIR, mismatchedInit)
	if err == nil || !strings.Contains(err.Error(), "initial CPU PC $009000 does not match block start address $008000") {
		t.Errorf("expected start PC mismatch error, got: %v", err)
	}

	// 6. Decimal mode entry invariant: init with P D=1 (bit 3)
	decimalInit := CPUState{
		PB: 0x00,
		PC: 0x8000,
		P:  0x08, // D=1
	}
	err = EnforceEntryContract(validIR, decimalInit)
	if err == nil || !strings.Contains(err.Error(), "decimal mode (D=1) is unsupported as entry invariant") {
		t.Errorf("expected decimal mode unsupported error, got: %v", err)
	}
}

func TestDirectionReviewOutcomeFlags(t *testing.T) {
	tests := []struct {
		name  string
		base  ExecResult
		alter func(*ExecResult)
	}{
		{"missing read", ExecResult{}, func(r *ExecResult) { r.MissingRead = true; r.MissingAddr = 0x7e0010 }},
		{"device access", ExecResult{}, func(r *ExecResult) { r.MMIOAccess = true; r.MMIOAddr = 0x002104 }},
		{"overflow write count", ExecResult{Writes: make([]MemoryWrite, 1024), TotalWrites: 2048, WriteOverflow: true}, func(r *ExecResult) { r.TotalWrites++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected := tt.base
			expected.State = CPUState{PC: 0x8000, P: 0x30}
			expected.NextPC = 0x8000
			if matched, why := CompareExecResults(expected, expected); !matched {
				t.Fatalf("equal baseline did not match: %s", why)
			}
			actual := expected
			tt.alter(&actual)
			if matched, why := CompareExecResults(expected, actual); matched {
				t.Fatal("different bounded outcome reported matched")
			} else {
				t.Logf("discrepancy: %s", why)
			}
		})
	}
}

