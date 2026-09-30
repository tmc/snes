package decomp

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// TestWordAddressing_RegressionW5toW8 validates 16-bit word read/write addressing behavior
// across bank boundaries ($xx:FFFF -> $(xx+1):0000) for absolute, long, and direct-page modes.
//
// Regression evidence (review 6B96B786 / coordinator prompt 20260930-B5AB46F8-unblock-high-byte.md):
// Commit 9d7b1980 introduced an in-bank high-byte clamp ((addr & 0xFF0000) | ((addr + 1) & 0xFFFF))
// across all addressing modes in mem_read16_raw and mem_write16.
// This caused:
// - W5 (LDA $FFFF abs DBR=$7E): read $7E:0000 instead of $7F:0000 (missing read)
// - W6 (STA $FFFF abs DBR=$7E): wrote $7E:0000=BE instead of $7F:0000=BE (silent wrong write)
// - W7 (LDA $7EFFFF long): read $7E:0000 instead of $7F:0000
// - SST vector 1d n 3218 (ORA $A6D5,X DBR=$6B eff $6B:FFFF): read $6B:0000 instead of $6C:0000
//
// The fix restores 24-bit linear carry ((addr + 1) & 0xFFFFFF) in generic 16-bit helpers.
func TestWordAddressing_RegressionW5toW8(t *testing.T) {
	tests := []struct {
		name       string
		op         uint8
		mnemonic   string
		bytes      []byte
		base       uint32
		initP      uint8
		initA      uint16
		initX      uint16
		initD      uint16
		initDB     uint8
		mem        []MemoryCell
		wantWrites []MemoryWrite
		wantA      uint16
	}{
		{
			name:     "W5_LDA_abs_7EFFFF_carry_to_7F0000",
			op:       0xAD,
			mnemonic: "lda",
			bytes:    []byte{0xAD, 0xFF, 0xFF}, // LDA $FFFF (M=0, 16-bit)
			base:     0x008000,
			initP:    0x10, // M=0 (16-bit A), X=1 (8-bit index)
			initDB:   0x7E,
			mem: []MemoryCell{
				{Address: 0x7EFFFF, Value: 0x34},
				{Address: 0x7F0000, Value: 0x12},
			},
			wantA: 0x1234,
		},
		{
			name:     "W6_STA_abs_7EFFFF_carry_to_7F0000_silent_wrong_write_regression",
			op:       0x8D,
			mnemonic: "sta",
			bytes:    []byte{0x8D, 0xFF, 0xFF}, // STA $FFFF (M=0, 16-bit)
			base:     0x008000,
			initP:    0x10, // M=0 (16-bit A), X=1 (8-bit index)
			initA:    0xBEEF,
			initDB:   0x7E,
			wantWrites: []MemoryWrite{
				{Address: 0x7EFFFF, Value: 0xEF},
				{Address: 0x7F0000, Value: 0xBE}, // Bug in 9d7b1980 wrote to 0x7E0000!
			},
			wantA: 0xBEEF,
		},
		{
			name:     "W7_LDA_long_7EFFFF_carry_to_7F0000",
			op:       0xAF,
			mnemonic: "lda",
			bytes:    []byte{0xAF, 0xFF, 0xFF, 0x7E}, // LDA $7EFFFF (M=0, 16-bit)
			base:     0x008000,
			initP:    0x10,
			mem: []MemoryCell{
				{Address: 0x7EFFFF, Value: 0x34},
				{Address: 0x7F0000, Value: 0x12},
			},
			wantA: 0x1234,
		},
		{
			name:     "W8_LDA_dp_00FFFF_with_D_FF00",
			op:       0xA5,
			mnemonic: "lda",
			bytes:    []byte{0xA5, 0xFF}, // LDA $FF (D=$FF00 -> eff $00:FFFF)
			base:     0x008000,
			initP:    0x10,
			initD:    0xFF00,
			initDB:   0x00,
			mem: []MemoryCell{
				{Address: 0x00FFFF, Value: 0x78},
				{Address: 0x000000, Value: 0x56}, // Mirrored at $7E0000 on canonical SNES bus
				{Address: 0x7E0000, Value: 0x56},
				{Address: 0x010000, Value: 0x56},
			},
			wantA: 0x5678,
		},
		{
			name:     "SST_1d_n_3218_ORA_indexed_carry_to_6C0000",
			op:       0x1D,
			mnemonic: "ora",
			bytes:    []byte{0x1D, 0xD5, 0xA6}, // ORA $A6D5,X (eff $6B:FFFF when X=$592A)
			base:     0x008000,
			initP:    0x00, // 16-bit A, 16-bit index
			initA:    0x0000,
			initX:    0x592A, // $A6D5 + $592A = $FFFF
			initDB:   0x6B,
			mem: []MemoryCell{
				{Address: 0x6BFFFF, Value: 0x55},
				{Address: 0x6C0000, Value: 0xAA}, // High byte must carry to $6C:0000
			},
			wantA: 0xAA55,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mFlag, xFlag := "clear", "clear"
			if tc.initP&0x20 != 0 {
				mFlag = "set"
			}
			if tc.initP&0x10 != 0 {
				xFlag = "set"
			}
			ctx := recovery.Context{E: "clear", M: mFlag, X: xFlag, C: "clear"}

			inst := recovery.Instruction{
				ID:           fmt.Sprintf("inst-%06x", tc.base),
				Architecture: "65816",
				Address:      tc.base,
				Bytes:        hex.EncodeToString(tc.bytes),
				Opcode:       tc.op,
				Mnemonic:     tc.mnemonic,
				Context:      ctx,
			}
			n := uint32(len(tc.bytes))
			blk := &structure.BasicBlock{
				ID:           fmt.Sprintf("bb-%06x", tc.base),
				StartAddress: tc.base,
				EndAddress:   tc.base + n,
				Instructions: []recovery.Instruction{inst},
				Successors:   []uint32{tc.base + n},
			}

			ir, err := LiftBlock(blk, ctx)
			if err != nil {
				t.Fatalf("LiftBlock failed: %v", err)
			}

			src, err := GenerateCompilableC(ir)
			if err != nil {
				t.Fatalf("GenerateCompilableC failed: %v", err)
			}

			init := CPUState{
				A:  tc.initA,
				X:  tc.initX,
				D:  tc.initD,
				DB: tc.initDB,
				PB: uint8(tc.base >> 16),
				PC: uint16(tc.base),
				S:  0x1FDD,
				P:  tc.initP,
				E:  false,
			}

			c := ReplayCase{
				CaseID:        tc.name,
				InitialState:  init,
				InitialMemory: tc.mem,
			}

			fnName := fmt.Sprintf("execute_block_%06x", tc.base)
			res, err := runCompiledBlock(context.Background(), t, src, fnName, []ReplayCase{c})
			if err != nil {
				t.Fatalf("runCompiledBlock failed: %v", err)
			}
			if len(res) != 1 {
				t.Fatalf("got %d results, want 1", len(res))
			}

			r := res[0]
			if r.MissingRead {
				t.Fatalf("unexpected missing read at $%06X", r.MissingAddr)
			}
			if r.State.A != tc.wantA {
				t.Errorf("Exit A = $%04X, want $%04X", r.State.A, tc.wantA)
			}

			if tc.wantWrites != nil {
				ok, disc := CompareWrites(tc.wantWrites, r.Writes)
				if !ok {
					t.Errorf("writes divergence: %s\ngot:  %+v\nwant: %+v", disc, r.Writes, tc.wantWrites)
				}
			}
		})
	}
}

func runCompiledBlock(ctx context.Context, t *testing.T, cSource string, fnName string, cases []ReplayCase) ([]ExecResult, error) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	baseTmp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(baseTmp, 0755)
	tmpDir, err := os.MkdirTemp(baseTmp, "block-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cPath := filepath.Join(tmpDir, "block.c")
	if err := os.WriteFile(cPath, []byte(cSource), 0644); err != nil {
		t.Fatalf("write runner code: %v", err)
	}

	runner, err := NewCompiledRegionRunnerWithROM(ctx, cPath, fnName, nil)
	if err != nil {
		return nil, fmt.Errorf("compile runner: %w", err)
	}
	defer runner.Close()

	return runner.RunBatch(ctx, cases)
}
