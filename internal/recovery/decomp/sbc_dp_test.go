package decomp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

var capturedSignByteCases = []struct {
	name           string
	sourceEventIDs []uint64
	block          structure.BasicBlock
	initial        CPUState
	expected       CPUState
	memory         map[uint32]uint8
	expectedWrites []MemoryWrite
}{
	{
		name:           "positive",
		sourceEventIDs: []uint64{52089, 52093, 52096, 52100},
		block: structure.BasicBlock{
			ID:           "captured-positive",
			StartAddress: 653449,
			EndAddress:   653457,
			Instructions: []recovery.Instruction{
				{
					ID:       "c502173897867cf631180952dc608f6142d36aee187c70b4bedfaccec0d29302",
					Address:  653449,
					Bytes:    "c980",
					Opcode:   0xC9,
					Mnemonic: "cmp",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "set"},
				},
				{
					ID:       "ce89bb5d11635e59337d57b6f4a3b68e83a2790071fab6c01d91d42b643e7bc5",
					Address:  653451,
					Bytes:    "e554",
					Opcode:   0xE5,
					Mnemonic: "sbc",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
				},
				{
					ID:       "82ad4ce17af59b14be9cd3fe72232b01abd3d957baa037664f39514384d57a5d",
					Address:  653453,
					Bytes:    "49ff",
					Opcode:   0x49,
					Mnemonic: "eor",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
				},
				{
					ID:       "83e430face102a4b2c532f1087267c06fe85ea815298e659c24a8d40d35bb153",
					Address:  653455,
					Bytes:    "8555",
					Opcode:   0x85,
					Mnemonic: "sta",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
				},
			},
			Successors: []uint32{653457},
		},
		initial: CPUState{
			A:      65300,
			X:      152,
			Y:      115,
			S:      7996,
			D:      7936,
			DB:     9,
			PB:     9,
			PC:     63625,
			P:      49,
			E:      false,
			Cycles: 119041758,
		},
		expected: CPUState{
			A:      65280,
			X:      152,
			Y:      115,
			S:      7996,
			D:      7936,
			DB:     9,
			PB:     9,
			PC:     63633,
			P:      50,
			E:      false,
			Cycles: 119041838,
		},
		memory: map[uint32]uint8{
			8265556: 20,
			8265557: 0,
		},
		expectedWrites: []MemoryWrite{
			{Address: 8265557, Value: 0},
		},
	},
	{
		name:           "negative",
		sourceEventIDs: []uint64{52112, 52116, 52119, 52123},
		block: structure.BasicBlock{
			ID:           "captured-negative",
			StartAddress: 653462,
			EndAddress:   653470,
			Instructions: []recovery.Instruction{
				{
					ID:       "b91ae113eff79833b3e474bc56b111c63fa19bab9fcf28e4c33ae69eea3d0259",
					Address:  653462,
					Bytes:    "c980",
					Opcode:   0xC9,
					Mnemonic: "cmp",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
				},
				{
					ID:       "098702c771a2b70f23bd979ff651f057c36355a69cdb892bf33f062b90f008cb",
					Address:  653464,
					Bytes:    "e556",
					Opcode:   0xE5,
					Mnemonic: "sbc",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "set"},
				},
				{
					ID:       "ea4a1581e8fb8b92d6beed7a8de8c1780237365851540183f59ef563760491bb",
					Address:  653466,
					Bytes:    "49ff",
					Opcode:   0x49,
					Mnemonic: "eor",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "set"},
				},
				{
					ID:       "eac148c798a406e48d14db1f3348c16e376362d336567024a5dc0d799d10fed1",
					Address:  653468,
					Bytes:    "8557",
					Opcode:   0x85,
					Mnemonic: "sta",
					Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "set"},
				},
			},
			Successors: []uint32{653470},
		},
		initial: CPUState{
			A:      65475,
			X:      152,
			Y:      115,
			S:      7996,
			D:      7936,
			DB:     9,
			PB:     9,
			PC:     63638,
			P:      176,
			E:      false,
			Cycles: 119041900,
		},
		expected: CPUState{
			A:      65535,
			X:      152,
			Y:      115,
			S:      7996,
			D:      7936,
			DB:     9,
			PB:     9,
			PC:     63646,
			P:      177,
			E:      false,
			Cycles: 119041980,
		},
		memory: map[uint32]uint8{
			8265558: 195,
			8265559: 255,
		},
		expectedWrites: []MemoryWrite{
			{Address: 8265559, Value: 255},
		},
	},
}

func TestSBC_DirectPage_CapturedFragments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, c := range capturedSignByteCases {
		t.Run(c.name, func(t *testing.T) {
			ir, err := LiftBlock(&c.block, c.block.Instructions[0].Context)
			if err != nil {
				t.Fatalf("LiftBlock failed: %v", err)
			}
			if ir.UnsupportedCount != 0 {
				t.Fatalf("expected 0 unsupported statements, got %d", ir.UnsupportedCount)
			}
			if ir.LoweredCount != 4 {
				t.Fatalf("expected 4 lowered statements, got %d", ir.LoweredCount)
			}

			// Generate compilable C
			cSrc, err := GenerateCompilableC(ir)
			if err != nil {
				t.Fatalf("GenerateCompilableC failed: %v", err)
			}
			if len(cSrc) == 0 {
				t.Fatal("empty generated C source")
			}

			// Run emulator
			emu, err := RunEmulatorBlock(ctx, ir, c.initial, c.memory)
			if err != nil {
				t.Fatalf("RunEmulatorBlock failed: %v", err)
			}

			// Expected without cycles
			expected := c.expected
			expected.Cycles = 0
			if !reflect.DeepEqual(emu.State, expected) {
				t.Errorf("emulator state mismatch: got %+v, want %+v", emu.State, expected)
			}
			if emu.NextPC != c.block.EndAddress {
				t.Errorf("emulator next PC mismatch: got $%06X, want $%06X", emu.NextPC, c.block.EndAddress)
			}
			if !reflect.DeepEqual(emu.Writes, c.expectedWrites) {
				t.Errorf("emulator writes mismatch: got %+v, want %+v", emu.Writes, c.expectedWrites)
			}
			if emu.TotalWrites != uint32(len(c.expectedWrites)) || emu.WriteOverflow || emu.MissingRead || emu.MMIOAccess {
				t.Errorf("emulator effect flags dirty: %+v", emu)
			}

			// Run compiled C runner
			runner, err := NewCompiledRunner(ctx, ir)
			if err != nil {
				t.Fatalf("NewCompiledRunner failed: %v", err)
			}
			defer runner.Close()

			memCells := make([]MemoryCell, 0, len(c.memory))
			for a, v := range c.memory {
				memCells = append(memCells, MemoryCell{Address: a, Value: v})
			}
			caseInput := ReplayCaseInput{
				CaseID:  c.name,
				Initial: c.initial,
				Memory:  memCells,
			}
			cResults, err := runner.RunBatch(ctx, []ReplayCaseInput{caseInput})
			if err != nil {
				t.Fatalf("runner.RunBatch failed: %v", err)
			}
			if len(cResults) != 1 {
				t.Fatalf("expected 1 result, got %d", len(cResults))
			}
			cRes := cResults[0]

			// Compare emulator vs C runner
			matched, discrepancy := CompareExecResults(emu, cRes)
			if !matched {
				t.Fatalf("differential mismatch between emulator and compiled C: %s", discrepancy)
			}

			t.Logf("PASS: %s -> A=$%04X, P=$%02X, NextPC=$%06X, Writes=%+v",
				c.name, cRes.State.A, cRes.State.P, cRes.NextPC, cRes.Writes)
		})
	}
}

func TestSBC_DirectPage_IndependentQualification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Helper to build a single-instruction SBC dp block
	makeSBCBlock := func(dpOffset uint8) *structure.BasicBlock {
		return &structure.BasicBlock{
			ID:           fmt.Sprintf("sbc-dp-%02x", dpOffset),
			StartAddress: 0x008000,
			EndAddress:   0x008002,
			Successors:   []uint32{0x008002},
			Instructions: []recovery.Instruction{
				{
					ID:       fmt.Sprintf("inst:sbc:%02x", dpOffset),
					Address:  0x008000,
					Bytes:    fmt.Sprintf("e5%02x", dpOffset),
					Opcode:   0xE5,
					Mnemonic: "sbc",
					Context: recovery.Context{
						E: "clear",
						M: "set",
						X: "set",
						C: "unknown",
					},
				},
			},
		}
	}

	tests := []struct {
		name       string
		dpOffset   uint8
		dReg       uint16
		initialA   uint16
		initialP   uint8
		memVal     uint8
		wantA      uint16
		wantC      bool
		wantZ      bool
		wantV      bool
		wantN      bool
	}{
		{
			name:     "no borrow: 0x50 - 0x20 with C=1",
			dpOffset: 0x10,
			dReg:     0x0000,
			initialA: 0x1250, // high A=0x12 preserved
			initialP: 0x31,   // C=1
			memVal:   0x20,
			wantA:    0x1230,
			wantC:    true,
			wantZ:    false,
			wantV:    false,
			wantN:    false,
		},
		{
			name:     "with incoming borrow: 0x50 - 0x20 with C=0",
			dpOffset: 0x10,
			dReg:     0x0000,
			initialA: 0xFF50, // high A=0xFF preserved
			initialP: 0x30,   // C=0
			memVal:   0x20,
			wantA:    0xFF2F,
			wantC:    true,
			wantZ:    false,
			wantV:    false,
			wantN:    false,
		},
		{
			name:     "resulting in borrow: 0x20 - 0x50 with C=1",
			dpOffset: 0x20,
			dReg:     0x0000,
			initialA: 0x0020, // high A=0x00 preserved
			initialP: 0x31,   // C=1
			memVal:   0x50,
			wantA:    0x00D0,
			wantC:    false, // borrow occurred
			wantZ:    false,
			wantV:    false,
			wantN:    true, // bit 7 set
		},
		{
			name:     "zero result: 0x42 - 0x42 with C=1",
			dpOffset: 0x05,
			dReg:     0x1F00, // nonzero D register ($1F00)
			initialA: 0x3442,
			initialP: 0x31, // C=1
			memVal:   0x42,
			wantA:    0x3400,
			wantC:    true,
			wantZ:    true, // zero
			wantV:    false,
			wantN:    false,
		},
		{
			name:     "positive signed overflow: 0x80 - 0x01 with C=1",
			dpOffset: 0x40,
			dReg:     0x0000,
			initialA: 0x0080, // -128 signed
			initialP: 0x37,   // M/X/I/C=1, V=0
			memVal:   0x01,   // +1
			wantA:    0x007F, // -128 - 1 = -129 -> wraps to +127
			wantC:    true,   // 0x80 >= 0x01 (no borrow)
			wantZ:    false,
			wantV:    true,   // overflow! (- - + = +)
			wantN:    false,
		},
		{
			name:     "negative signed overflow: 0x7F - 0xFF with C=1",
			dpOffset: 0x30,
			dReg:     0x1F00,
			initialA: 0x007F, // +127 signed
			initialP: 0x35,   // M/X/I/C=1, V=0
			memVal:   0xFF,   // -1 signed
			wantA:    0x0080, // 127 - (-1) = 128 -> wraps to -128
			wantC:    false,  // 0x7F < 0xFF unsigned (borrow occurred)
			wantZ:    false,
			wantV:    true,   // overflow! (+ - - = -)
			wantN:    true,   // bit 7 set
		},
		{
			name:     "borrow and incoming-V reset: 0x7F - 0xFF with C=0",
			dpOffset: 0x30,
			dReg:     0x1F00,
			initialA: 0xFF7F, // high A=0xFF preserved
			initialP: 0x76,   // incoming V=1 (bit 6 set), C=0
			memVal:   0xFF,   // 127 - (-1) - 1 = 127
			wantA:    0xFF7F,
			wantC:    false,  // borrow occurred
			wantZ:    false,
			wantV:    false,  // incoming V=1 reset to 0!
			wantN:    false,
		},
		{
			name:     "zero, no borrow: 0x00 - 0x00 with C=1",
			dpOffset: 0x00,
			dReg:     0x1F00,
			initialA: 0xFF00, // high A=0xFF preserved
			initialP: 0x75,   // incoming V=1
			memVal:   0x00,
			wantA:    0xFF00,
			wantC:    true,
			wantZ:    true,   // zero
			wantV:    false,  // incoming V=1 reset to 0!
			wantN:    false,
		},
		{
			name:     "borrow: 0x00 - 0x01 with C=0",
			dpOffset: 0x10,
			dReg:     0x0000,
			initialA: 0x0000,
			initialP: 0x76, // incoming V=1, C=0 -> 0 - 1 - 1 = -2 = 0xFE
			memVal:   0x01,
			wantA:    0x00FE,
			wantC:    false, // borrow
			wantZ:    false,
			wantV:    false, // incoming V=1 reset to 0!
			wantN:    true,  // bit 7 set
		},
		{
			name:     "boundary 0xFF - 0xFF with C=1",
			dpOffset: 0x55,
			dReg:     0x1F00,
			initialA: 0xFFFF,
			initialP: 0x31,
			memVal:   0xFF,
			wantA:    0xFF00,
			wantC:    true,
			wantZ:    true,
			wantV:    false,
			wantN:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := makeSBCBlock(tt.dpOffset)
			entryCtx := recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}
			ir, err := LiftBlock(block, entryCtx)
			if err != nil {
				t.Fatalf("LiftBlock: %v", err)
			}

			// Effective address in WRAM
			effAddr := (uint32(tt.dReg) + uint32(tt.dpOffset)) & 0xFFFF
			wramAddr := 0x7E0000 | effAddr

			init := CPUState{
				A:  tt.initialA,
				X:  0x0098,
				Y:  0x0073,
				S:  0x1F3C,
				D:  tt.dReg,
				DB: 0x09,
				PB: 0x00,
				PC: 0x8000,
				P:  tt.initialP,
				E:  false,
			}
			mem := map[uint32]uint8{
				wramAddr: tt.memVal,
			}

			// Run emulator
			emu, err := RunEmulatorBlock(ctx, ir, init, mem)
			if err != nil {
				t.Fatalf("RunEmulatorBlock: %v", err)
			}

			// Run compiled C runner
			runner, err := NewCompiledRunner(ctx, ir)
			if err != nil {
				t.Fatalf("NewCompiledRunner: %v", err)
			}
			defer runner.Close()

			memCells := []MemoryCell{{Address: wramAddr, Value: tt.memVal}}
			cResults, err := runner.RunBatch(ctx, []ReplayCaseInput{
				{CaseID: tt.name, Initial: init, Memory: memCells},
			})
			if err != nil {
				t.Fatalf("RunBatch: %v", err)
			}
			cRes := cResults[0]

			// Verify differential match
			matched, discrepancy := CompareExecResults(emu, cRes)
			if !matched {
				t.Fatalf("differential mismatch: %s", discrepancy)
			}

			// Verify architectural expectations
			if cRes.State.A != tt.wantA {
				t.Errorf("A mismatch: got $%04X, want $%04X", cRes.State.A, tt.wantA)
			}
			gotC := (cRes.State.P & 0x01) != 0
			gotZ := (cRes.State.P & 0x02) != 0
			gotV := (cRes.State.P & 0x40) != 0
			gotN := (cRes.State.P & 0x80) != 0

			if gotC != tt.wantC {
				t.Errorf("Carry flag mismatch: got %v, want %v (P=$%02X)", gotC, tt.wantC, cRes.State.P)
			}
			if gotZ != tt.wantZ {
				t.Errorf("Zero flag mismatch: got %v, want %v (P=$%02X)", gotZ, tt.wantZ, cRes.State.P)
			}
			if gotV != tt.wantV {
				t.Errorf("Overflow flag mismatch: got %v, want %v (P=$%02X)", gotV, tt.wantV, cRes.State.P)
			}
			if gotN != tt.wantN {
				t.Errorf("Negative flag mismatch: got %v, want %v (P=$%02X)", gotN, tt.wantN, cRes.State.P)
			}

			// Verify untouched registers
			if cRes.State.X != init.X || cRes.State.Y != init.Y || cRes.State.S != init.S || cRes.State.D != init.D || cRes.State.DB != init.DB || cRes.State.PB != init.PB {
				t.Errorf("untouched register modified: initial=%+v, final=%+v", init, cRes.State)
			}

			// Verify zero writes and clean effect flags
			if cRes.TotalWrites != 0 || cRes.WriteOverflow || cRes.MissingRead || cRes.MMIOAccess {
				t.Errorf("compiled C effect flags dirty: %+v", cRes)
			}
			if emu.TotalWrites != 0 || emu.WriteOverflow || emu.MissingRead || emu.MMIOAccess {
				t.Errorf("emulator effect flags dirty: %+v", emu)
			}
		})
	}
}

func TestSBC_DirectPage_RefusalControls(t *testing.T) {
	block := &structure.BasicBlock{
		ID:           "sbc-refusal",
		StartAddress: 0x008000,
		EndAddress:   0x008002,
		Successors:   []uint32{0x008002},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:sbc:refusal",
				Address:  0x008000,
				Bytes:    "e510",
				Opcode:   0xE5,
				Mnemonic: "sbc",
			},
		},
	}

	// 1. M16 context refusal (16-bit accumulator unsupported for this slice)
	t.Run("m16_refusal", func(t *testing.T) {
		m16Ctx := recovery.Context{E: "clear", M: "clear", X: "set", C: "clear"}
		ir, err := LiftBlock(block, m16Ctx)
		if err != nil {
			t.Fatalf("LiftBlock errored instead of marking unsupported: %v", err)
		}
		if ir.UnsupportedCount != 1 || ir.LoweredCount != 0 {
			t.Errorf("expected 1 unsupported statement for M16, got unsupported=%d, lowered=%d", ir.UnsupportedCount, ir.LoweredCount)
		}
	})

	// 2. Emulation mode refusal (E=true unsupported for this slice)
	t.Run("emulation_refusal", func(t *testing.T) {
		emuCtx := recovery.Context{E: "set", M: "set", X: "set", C: "clear"}
		ir, err := LiftBlock(block, emuCtx)
		if err != nil {
			t.Fatalf("LiftBlock errored instead of marking unsupported: %v", err)
		}
		if ir.UnsupportedCount != 1 || ir.LoweredCount != 0 {
			t.Errorf("expected 1 unsupported statement for E=true, got unsupported=%d, lowered=%d", ir.UnsupportedCount, ir.LoweredCount)
		}
	})

	// 3. Decimal mode entry-contract refusal (P.D=1 rejected by EnforceEntryContract)
	t.Run("decimal_refusal", func(t *testing.T) {
		nativeM8Ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}
		ir, err := LiftBlock(block, nativeM8Ctx)
		if err != nil {
			t.Fatalf("LiftBlock: %v", err)
		}
		decimalState := CPUState{
			PB: 0x00,
			PC: 0x8000,
			P:  0x38, // bit 3 (0x08) is D=1
			E:  false,
		}
		err = EnforceEntryContract(ir, decimalState)
		if err == nil {
			t.Fatal("expected decimal mode entry-contract refusal, got nil")
		}
		t.Logf("confirmed decimal refusal: %v", err)
	})

	// 4. Decimal mode RunBatch admission refusal (P.D=1 rejected by enforceContextContract in RunBatch before launch)
	t.Run("decimal_runbatch_refusal", func(t *testing.T) {
		nativeM8Ctx := recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}
		ir, err := LiftBlock(block, nativeM8Ctx)
		if err != nil {
			t.Fatalf("LiftBlock: %v", err)
		}
		runner, err := NewCompiledRunner(context.Background(), ir)
		if err != nil {
			t.Fatalf("NewCompiledRunner: %v", err)
		}
		defer runner.Close()

		decimalState := CPUState{
			PB: 0x00,
			PC: 0x8000,
			P:  0x39, // bit 3 (0x08) is D=1, C=1
			E:  false,
		}
		memCells := []MemoryCell{{Address: 0x7E0010, Value: 0x20}}
		_, err = runner.RunBatch(context.Background(), []ReplayCaseInput{
			{CaseID: "decimal_case", Initial: decimalState, Memory: memCells},
		})
		if err == nil {
			t.Fatal("expected decimal mode RunBatch refusal, got nil")
		}
		if !strings.Contains(err.Error(), "decimal mode (D=1) is unsupported") {
			t.Fatalf("unexpected error message: %v", err)
		}
		t.Logf("confirmed RunBatch decimal refusal: %v", err)
	})
}

func TestExportSBCReceipt(t *testing.T) {
	outPath := os.Getenv("EXPORT_SBC_RECEIPT")
	if outPath == "" {
		t.Skip("skipping receipt generation (EXPORT_SBC_RECEIPT not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rows []map[string]any
	for _, c := range capturedSignByteCases {
		ir, err := LiftBlock(&c.block, c.block.Instructions[0].Context)
		if err != nil {
			t.Fatalf("LiftBlock: %v", err)
		}
		cSrc, err := GenerateCompilableC(ir)
		if err != nil {
			t.Fatalf("GenerateCompilableC: %v", err)
		}
		wrapperSrc := GenerateMultiCaseRunnerC(cSrc, ir.StartAddress)
		wrapperHash := fmt.Sprintf("%x", sha256.Sum256([]byte(wrapperSrc)))

		emu, err := RunEmulatorBlock(ctx, ir, c.initial, c.memory)
		if err != nil {
			t.Fatalf("RunEmulatorBlock: %v", err)
		}

		runner, err := NewCompiledRunner(ctx, ir)
		if err != nil {
			t.Fatalf("NewCompiledRunner: %v", err)
		}

		// Read runner binary and hash before Close()
		binBytes, err := os.ReadFile(runner.BinPath)
		if err != nil {
			runner.Close()
			t.Fatalf("read runner binary: %v", err)
		}
		binHash := fmt.Sprintf("%x", sha256.Sum256(binBytes))

		memCells := make([]MemoryCell, 0, len(c.memory))
		for a, v := range c.memory {
			memCells = append(memCells, MemoryCell{Address: a, Value: v})
		}
		cResults, err := runner.RunBatch(ctx, []ReplayCaseInput{
			{CaseID: c.name, Initial: c.initial, Memory: memCells},
		})
		runner.Close()
		if err != nil {
			t.Fatalf("RunBatch: %v", err)
		}
		cRes := cResults[0]

		matched, discrepancy := CompareExecResults(emu, cRes)
		if !matched {
			t.Fatalf("mismatch: %s", discrepancy)
		}

		expected := c.expected
		expected.Cycles = 0

		row := map[string]any{
			"name":                             c.name,
			"source_stream_sha256":             "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
			"source_event_ids":                 c.sourceEventIDs,
			"initial_state":                    c.initial,
			"expected_state":                   c.expected,
			"expected_without_recorded_cycles": expected,
			"expected_writes":                  c.expectedWrites,
			"ir":                               ir,
			"generated_c_source":               cSrc,
			"generated_c_hash":                 runner.GeneratedCHash,
			"wrapper_c_hash":                   wrapperHash,
			"compiler":                         runner.Compiler,
			"compiler_flags":                   runner.CompilerFlags,
			"runner_binary_hash":               binHash,
			"compiled_c_result":                cRes,
			"emulator_result":                  emu,
			"differential_matched":             matched,
			"register_projection_matches":      reflect.DeepEqual(emu.State, expected),
			"next_pc_matches":                  emu.NextPC == c.block.EndAddress,
			"ordered_writes_match":             reflect.DeepEqual(emu.Writes, c.expectedWrites),
			"effects_refusals_clear":           emu.TotalWrites == uint32(len(c.expectedWrites)) && !emu.WriteOverflow && !emu.MissingRead && !emu.MMIOAccess,
			"timing_compared":                  false,
		}
		rows = append(rows, row)
	}

	receipt := map[string]any{
		"revision":   "HEAD",
		"scope":      "tested binary native M8 inputs, bank-zero effective address with low WRAM mirror canonicalization or explicit supplied upper-bank-zero memory; not arbitrary D mapping to WRAM, comprehensive ISA qualification or a proven backend. Timing remains excluded.",
		"case_count": len(rows),
		"cases":      rows,
	}

	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0644); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	t.Logf("wrote receipt to %s", outPath)
}
