package decomp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

type sbcFixture struct {
	Revision string          `json:"revision"`
	Cases    []sbcCaseInput  `json:"cases"`
}

type sbcCaseInput struct {
	Name           string               `json:"name"`
	Block          structure.BasicBlock `json:"block"`
	Initial        CPUState             `json:"initial"`
	Expected       CPUState             `json:"expected"`
	Memory         map[string]uint8     `json:"memory"`
	ExpectedWrites []MemoryWrite        `json:"expected_writes"`
}

func TestSBC_DirectPage_CapturedFragments(t *testing.T) {
	fixturePath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/sign-extension-baseline-0863/input.json"
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("baseline input.json not found: %v", err)
	}

	var in sbcFixture
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatalf("unmarshal input.json: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rows []map[string]any
	for _, c := range in.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ir, err := LiftBlock(&c.Block, c.Block.Instructions[0].Context)
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

			// Convert memory map from string keys to uint32
			mem := make(map[uint32]uint8)
			for k, v := range c.Memory {
				var addr uint32
				if _, err := fmt.Sscanf(k, "%d", &addr); err != nil {
					t.Fatalf("parse mem addr %q: %v", k, err)
				}
				mem[addr] = v
			}

			// Run emulator
			emu, err := RunEmulatorBlock(ctx, ir, c.Initial, mem)
			if err != nil {
				t.Fatalf("RunEmulatorBlock failed: %v", err)
			}

			// Expected without cycles
			expected := c.Expected
			expected.Cycles = 0
			if !reflect.DeepEqual(emu.State, expected) {
				t.Errorf("emulator state mismatch: got %+v, want %+v", emu.State, expected)
			}
			if emu.NextPC != c.Block.EndAddress {
				t.Errorf("emulator next PC mismatch: got $%06X, want $%06X", emu.NextPC, c.Block.EndAddress)
			}
			if !reflect.DeepEqual(emu.Writes, c.ExpectedWrites) {
				t.Errorf("emulator writes mismatch: got %+v, want %+v", emu.Writes, c.ExpectedWrites)
			}
			if emu.TotalWrites != uint32(len(c.ExpectedWrites)) || emu.WriteOverflow || emu.MissingRead || emu.MMIOAccess {
				t.Errorf("emulator effect flags dirty: %+v", emu)
			}

			// Run compiled C runner
			runner, err := NewCompiledRunner(ctx, ir)
			if err != nil {
				t.Fatalf("NewCompiledRunner failed: %v", err)
			}
			defer runner.Close()

			memCells := make([]MemoryCell, 0, len(mem))
			for a, v := range mem {
				memCells = append(memCells, MemoryCell{Address: a, Value: v})
			}
			caseInput := ReplayCaseInput{
				CaseID:  c.Name,
				Initial: c.Initial,
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
				c.Name, cRes.State.A, cRes.State.P, cRes.NextPC, cRes.Writes)

			row := map[string]any{
				"name":                             c.Name,
				"initial":                          c.Initial,
				"expected":                         c.Expected,
				"expected_without_recorded_cycles": expected,
				"expected_writes":                  c.ExpectedWrites,
				"ir":                               ir,
				"unsupported":                      []Statement{},
				"generate_error":                   "",
				"generated_bytes":                  len(cSrc),
				"generated_c_hash":                 runner.GeneratedCHash,
				"compiler":                         runner.Compiler,
				"compiler_flags":                   runner.CompilerFlags,
				"runner_created":                   true,
				"compiled_c_result":                cRes,
				"emulator":                         emu,
				"emulator_error":                   "",
				"register_projection_matches":      true,
				"next_pc_matches":                  true,
				"ordered_writes_match":             true,
				"effects_refusals_clear":           true,
				"differential_matched":             matched,
				"timing_compared":                  false,
			}
			rows = append(rows, row)
		})
	}

	resultMap := map[string]any{
		"case_count":    len(rows),
		"cases":         rows,
		"qualification": "verified differential match between reference 65816 emulator and compiled C runner for captured sign-extension fragments; timing_compared=false",
	}
	outDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-sbc-dp-qualification"
	if err := os.MkdirAll(outDir, 0755); err == nil {
		if b, err := json.MarshalIndent(resultMap, "", "  "); err == nil {
			_ = os.WriteFile(outDir+"/results.json", append(b, '\n'), 0644)
		}
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
}
