package decomp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

func makeTestInsn(addr uint32, op byte, mnem string, hexBytes string, ctx recovery.Context) recovery.Instruction {
	return recovery.Instruction{
		ID:           fmt.Sprintf("inst-%06x", addr),
		Architecture: "wdc65816",
		Address:      addr,
		Bytes:        hexBytes,
		Opcode:       op,
		Mnemonic:     mnem,
		Context:      ctx,
	}
}

// TestMultiBlock_SequentialChain tests a 2-block sequential chain:
// Block 1 modifies registers and DP memory, then falls through to Block 2 which uses them.
func TestMultiBlock_SequentialChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set", // 8-bit A
		X: "set", // 8-bit X
		C: "unknown",
	}

	// Block 1:
	// $8000: LDA #$42  (a9 42)
	// $8002: STA $10   (85 10)
	// $8004: LDX #$07  (a2 07)
	// $8006: INX       (e8)
	// $8007: CLC       (18)
	b1 := &structure.BasicBlock{
		ID:           "bb-seq-1",
		StartAddress: 0x008000,
		EndAddress:   0x008008,
		Successors:   []uint32{0x008008},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xa9, "lda", "a942", entryCtx),
			makeTestInsn(0x008002, 0x85, "sta", "8510", entryCtx),
			makeTestInsn(0x008004, 0xa2, "ldx", "a207", entryCtx),
			makeTestInsn(0x008006, 0xe8, "inx", "e8", entryCtx),
			makeTestInsn(0x008007, 0x18, "clc", "18", entryCtx),
		},
	}

	// Block 2:
	// $8008: LDA $10   (a5 10)
	// $800A: ADC #$05  (69 05)
	// $800C: STA $11   (85 11)
	// $800E: TXA       (8a)
	// $800F: ADC $11   (65 11)
	// $8011: STA $12   (85 12)
	// $8013: RTS       (60)
	b2 := &structure.BasicBlock{
		ID:           "bb-seq-2",
		StartAddress: 0x008008,
		EndAddress:   0x008014,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008008, 0xa5, "lda", "a510", entryCtx),
			makeTestInsn(0x00800A, 0x69, "adc", "6905", entryCtx),
			makeTestInsn(0x00800C, 0x85, "sta", "8511", entryCtx),
			makeTestInsn(0x00800E, 0x8a, "txa", "8a", entryCtx),
			makeTestInsn(0x00800F, 0x65, "adc", "6511", entryCtx),
			makeTestInsn(0x008011, 0x85, "sta", "8512", entryCtx),
			makeTestInsn(0x008013, 0x60, "rts", "60", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b1)
	cfg.AddBlock(b2)
	cfg.AddEdge(0x008000, 0x008008, EdgeUnconditional)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}
	if len(dag.Blocks) != 2 {
		t.Fatalf("expected 2 lifted blocks, got %d", len(dag.Blocks))
	}

	// Verify inter-block context propagation (Block 1 CLC makes C="clear" for Block 2)
	if dag.Blocks[1].EntryContext.C != "clear" {
		t.Errorf("expected Block 2 EntryContext.C to be 'clear', got %q", dag.Blocks[1].EntryContext.C)
	}

	init := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x30, // M=1, X=1
	}

	// Setup stack memory so RTS returns to 0x8034
	mem := map[uint32]uint8{
		0x01FE: 0x33, // Low byte of return address - 1
		0x01FF: 0x80, // High byte
	}

	cRes, emuRes, err := RunDAG(ctx, dag, init, mem)
	if err != nil {
		t.Fatalf("RunDAG dual-backend execution failed: %v", err)
	}

	// Assert 100% agreement on PC and CPU state
	if cRes.NextPC != 0x008034 {
		t.Errorf("expected NextPC 0x008034, got 0x%06X", cRes.NextPC)
	}
	if cRes.State.A != 0x004F {
		t.Errorf("expected A = 0x004F, got 0x%04X", cRes.State.A)
	}
	if cRes.State.X != 0x0008 {
		t.Errorf("expected X = 0x0008, got 0x%04X", cRes.State.X)
	}

	// Assert ordered writes: $10 = $42, $11 = $47, $12 = $4F
	expectedWrites := []MemoryWrite{
		{Address: 0x7E0010, Value: 0x42},
		{Address: 0x7E0011, Value: 0x47},
		{Address: 0x7E0012, Value: 0x4F},
	}
	if len(cRes.Writes) != len(expectedWrites) {
		t.Fatalf("expected %d writes, got %d", len(expectedWrites), len(cRes.Writes))
	}
	for i, want := range expectedWrites {
		got := cRes.Writes[i]
		if got.Address != want.Address || got.Value != want.Value {
			t.Errorf("write %d mismatch: want addr=$%06X val=0x%02X, got addr=$%06X val=0x%02X",
				i, want.Address, want.Value, got.Address, got.Value)
		}
	}

	if emuRes.State.A != cRes.State.A || emuRes.State.X != cRes.State.X || emuRes.NextPC != cRes.NextPC {
		t.Errorf("mismatch between emulator and C: emu=(A=%04X X=%04X PC=%06X) c=(A=%04X X=%04X PC=%06X)",
			emuRes.State.A, emuRes.State.X, emuRes.NextPC, cRes.State.A, cRes.State.X, cRes.NextPC)
	}
}

// TestMultiBlock_DiamondBranch tests diamond / branching control flow:
// Block 1 compares DP $10 with 10 and branches:
//
//	-> Taken: Block 2 (sets A=$22, STA $20, BRA Join)
//	-> Fallthrough: Block 3 (sets A=$11, STA $20, BRA Join)
//	-> Block 4 Join (LDX $20, INX, STX $21, RTS)
func TestMultiBlock_DiamondBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set", // 8-bit A
		X: "set", // 8-bit X
		C: "unknown",
	}

	// Block 1: Entry & Branch ($8000..$8006)
	// $8000: LDA $10   (a5 10)
	// $8002: CMP #$0A  (c9 0a)
	// $8004: BEQ $8010 (f0 0a) -> Taken=$8010, Fallthrough=$8006
	b1 := &structure.BasicBlock{
		ID:           "bb-diamond-1",
		StartAddress: 0x008000,
		EndAddress:   0x008006,
		Successors:   []uint32{0x008010, 0x008006},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xa5, "lda", "a510", entryCtx),
			makeTestInsn(0x008002, 0xc9, "cmp", "c90a", entryCtx),
			makeTestInsn(0x008004, 0xf0, "beq", "f00a", entryCtx),
		},
	}

	// Block 3: Fallthrough path ($8006..$800C)
	// $8006: LDA #$11  (a9 11)
	// $8008: STA $20   (85 20)
	// $800A: BRA $8016 (80 0a)
	b3 := &structure.BasicBlock{
		ID:           "bb-diamond-3-fallthrough",
		StartAddress: 0x008006,
		EndAddress:   0x00800C,
		Successors:   []uint32{0x008016},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008006, 0xa9, "lda", "a911", entryCtx),
			makeTestInsn(0x008008, 0x85, "sta", "8520", entryCtx),
			makeTestInsn(0x00800A, 0x80, "bra", "800a", entryCtx),
		},
	}

	// Block 2: Taken path ($8010..$8016)
	// $8010: LDA #$22  (a9 22)
	// $8012: STA $20   (85 20)
	// $8014: BRA $8016 (80 00)
	b2 := &structure.BasicBlock{
		ID:           "bb-diamond-2-taken",
		StartAddress: 0x008010,
		EndAddress:   0x008016,
		Successors:   []uint32{0x008016},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008010, 0xa9, "lda", "a922", entryCtx),
			makeTestInsn(0x008012, 0x85, "sta", "8520", entryCtx),
			makeTestInsn(0x008014, 0x80, "bra", "8000", entryCtx),
		},
	}

	// Block 4: Join ($8016..$801C)
	// $8016: LDX $20   (a6 20)
	// $8018: INX       (e8)
	// $8019: STX $21   (86 21)
	// $801B: RTS       (60)
	b4 := &structure.BasicBlock{
		ID:           "bb-diamond-4-join",
		StartAddress: 0x008016,
		EndAddress:   0x00801C,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008016, 0xa6, "ldx", "a620", entryCtx),
			makeTestInsn(0x008018, 0xe8, "inx", "e8", entryCtx),
			makeTestInsn(0x008019, 0x86, "stx", "8621", entryCtx),
			makeTestInsn(0x00801B, 0x60, "rts", "60", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b1)
	cfg.AddBlock(b3)
	cfg.AddBlock(b2)
	cfg.AddBlock(b4)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	tests := []struct {
		name       string
		inputVal   uint8
		wantA      uint16
		wantX      uint16
		wantWrites []MemoryWrite
	}{
		{
			name:     "branch_taken_path",
			inputVal: 0x0A, // 10 == 10, BEQ taken
			wantA:    0x0022,
			wantX:    0x0023,
			wantWrites: []MemoryWrite{
				{Address: 0x7E0020, Value: 0x22},
				{Address: 0x7E0021, Value: 0x23},
			},
		},
		{
			name:     "branch_fallthrough_path",
			inputVal: 0x05, // 5 != 10, BEQ not taken
			wantA:    0x0011,
			wantX:    0x0012,
			wantWrites: []MemoryWrite{
				{Address: 0x7E0020, Value: 0x11},
				{Address: 0x7E0021, Value: 0x12},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			init := CPUState{
				PC: 0x8000,
				PB: 0x00,
				S:  0x01FD,
				P:  0x30,
			}
			mem := map[uint32]uint8{
				0x7E0010: tt.inputVal,
				0x01FE:   0x45, // Stack return address lo
				0x01FF:   0x80, // Stack return address hi (return to $8046)
			}

			cRes, emuRes, err := RunDAG(ctx, dag, init, mem)
			if err != nil {
				t.Fatalf("RunDAG dual-backend execution failed: %v", err)
			}

			if cRes.NextPC != 0x008046 {
				t.Errorf("expected NextPC 0x008046, got 0x%06X", cRes.NextPC)
			}
			if cRes.State.A != tt.wantA {
				t.Errorf("A mismatch: want 0x%04X, got 0x%04X", tt.wantA, cRes.State.A)
			}
			if cRes.State.X != tt.wantX {
				t.Errorf("X mismatch: want 0x%04X, got 0x%04X", tt.wantX, cRes.State.X)
			}

			if len(cRes.Writes) != len(tt.wantWrites) {
				t.Fatalf("writes length mismatch: want %d, got %d", len(tt.wantWrites), len(cRes.Writes))
			}
			for i, want := range tt.wantWrites {
				got := cRes.Writes[i]
				if got.Address != want.Address || got.Value != want.Value {
					t.Errorf("write %d mismatch: want addr=$%06X val=0x%02X, got addr=$%06X val=0x%02X",
						i, want.Address, want.Value, got.Address, got.Value)
				}
			}

			// Verify 100% agreement between Go CPU and compiled C
			if cRes.State != emuRes.State || cRes.NextPC != emuRes.NextPC {
				t.Errorf("mismatch between emulator and C: emu=%+v c=%+v", emuRes, cRes)
			}
		})
	}
}

// TestMultiBlock_RegisterWidthPropagation tests inter-block register width mode change:
// Block 1 executes REP #$20 (clearing M, making A 16-bit) and jumps to Block 2.
// Block 2 executes 16-bit LDA and STA.
func TestMultiBlock_RegisterWidthPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set", // 8-bit A at entry
		X: "set",
		C: "unknown",
	}

	// Block 1:
	// $8000: REP #$20  (c2 20) -> clears M, makes A 16-bit
	// $8002: BRA $8004 (80 00)
	b1 := &structure.BasicBlock{
		ID:           "bb-mode-1",
		StartAddress: 0x008000,
		EndAddress:   0x008004,
		Successors:   []uint32{0x008004},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xc2, "rep", "c220", entryCtx),
			makeTestInsn(0x008002, 0x80, "bra", "8000", entryCtx),
		},
	}

	// Block 2:
	// In 16-bit mode:
	// $8004: LDA #$1234 (a9 34 12) -> 3 bytes
	// $8007: STA $10    (85 10)    -> 2 bytes (writes 16-bit word to $10, $11)
	// $8009: RTS        (60)       -> 1 byte
	ctx16 := recovery.Context{
		E: "clear",
		M: "clear", // 16-bit
		X: "set",
		C: "unknown",
	}
	b2 := &structure.BasicBlock{
		ID:           "bb-mode-2",
		StartAddress: 0x008004,
		EndAddress:   0x00800A,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008004, 0xa9, "lda", "a93412", ctx16),
			makeTestInsn(0x008007, 0x85, "sta", "8510", ctx16),
			makeTestInsn(0x008009, 0x60, "rts", "60", ctx16),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b1)
	cfg.AddBlock(b2)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	// Block 2 should have inherited M="clear"
	if dag.Blocks[1].EntryContext.M != "clear" {
		t.Fatalf("expected Block 2 EntryContext.M to be 'clear', got %q", dag.Blocks[1].EntryContext.M)
	}

	init := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x30, // Initially M=1, X=1
	}
	mem := map[uint32]uint8{
		0x01FE: 0x50,
		0x01FF: 0x80,
	}

	cRes, emuRes, err := RunDAG(ctx, dag, init, mem)
	if err != nil {
		t.Fatalf("RunDAG failed: %v", err)
	}

	if cRes.State.A != 0x1234 {
		t.Errorf("expected A = 0x1234, got 0x%04X", cRes.State.A)
	}
	// Check 16-bit memory write: $10 = $34, $11 = $12
	expectedWrites := []MemoryWrite{
		{Address: 0x7E0010, Value: 0x34},
		{Address: 0x7E0011, Value: 0x12},
	}
	if len(cRes.Writes) != len(expectedWrites) {
		t.Fatalf("expected %d writes, got %d", len(expectedWrites), len(cRes.Writes))
	}
	for i, want := range expectedWrites {
		if cRes.Writes[i] != want {
			t.Errorf("write %d mismatch: want %+v, got %+v", i, want, cRes.Writes[i])
		}
	}

	if cRes.State != emuRes.State || cRes.NextPC != emuRes.NextPC {
		t.Errorf("mismatch between emulator and C: emu=%+v c=%+v", emuRes, cRes)
	}
}

// TestMultiBlock_CompiledDAGRunner verifies that NewCompiledDAGRunner creates a reusable runner.
func TestMultiBlock_CompiledDAGRunner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set",
		X: "set",
		C: "unknown",
	}

	b1 := &structure.BasicBlock{
		ID:           "bb-runner-1",
		StartAddress: 0x008000,
		EndAddress:   0x008004,
		Successors:   []uint32{0x008004},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xa9, "lda", "a955", entryCtx),
			makeTestInsn(0x008002, 0x85, "sta", "8520", entryCtx),
		},
	}
	b2 := &structure.BasicBlock{
		ID:           "bb-runner-2",
		StartAddress: 0x008004,
		EndAddress:   0x008007,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008004, 0xe6, "inc", "e620", entryCtx), // INC $20 -> $56
			makeTestInsn(0x008006, 0x60, "rts", "60", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b1)
	cfg.AddBlock(b2)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	runner, err := NewCompiledDAGRunner(ctx, dag)
	if err != nil {
		t.Fatalf("NewCompiledDAGRunner failed: %v", err)
	}
	defer runner.Close()

	if runner.StartAddress != 0x008000 {
		t.Errorf("expected StartAddress 0x008000, got 0x%06X", runner.StartAddress)
	}

	// Test executing a case through the runner
	res, err := runner.RunCase(ctx, ReplayCase{
		InitialState: CPUState{
			PC: 0x8000,
			PB: 0x00,
			S:  0x01FD,
			P:  0x30,
		},
		InitialMemory: []MemoryCell{
			{Address: 0x01FE, Value: 0x77},
			{Address: 0x01FF, Value: 0x80},
		},
	})
	if err != nil {
		t.Fatalf("runner.RunCase failed: %v", err)
	}

	if res.NextPC != 0x008078 {
		t.Errorf("expected NextPC 0x008078, got 0x%06X", res.NextPC)
	}
	// Expected writes: $20 = 0x55, then read-modify-write on $20 = 0x56
	if len(res.Writes) < 2 {
		t.Fatalf("expected at least 2 writes, got %d", len(res.Writes))
	}
	if res.Writes[len(res.Writes)-1].Value != 0x56 {
		t.Errorf("expected final write value 0x56, got 0x%02X", res.Writes[len(res.Writes)-1].Value)
	}
}

// TestMultiBlock_ValidationErrors checks failure modes for invalid CFG or DAG.
func TestMultiBlock_ValidationErrors(t *testing.T) {
	entryCtx := recovery.Context{E: "clear", M: "set", X: "set"}

	// 1. Nil CFG
	if _, err := LiftDAG(nil, entryCtx); err == nil || !strings.Contains(err.Error(), "nil RoutineCFG") {
		t.Errorf("expected nil RoutineCFG error, got %v", err)
	}

	// 2. Empty CFG
	emptyCFG := NewRoutineCFG(0x008000)
	if _, err := LiftDAG(emptyCFG, entryCtx); err == nil || !strings.Contains(err.Error(), "contains no basic blocks") {
		t.Errorf("expected empty CFG error, got %v", err)
	}

	// 3. Entry block missing
	b := &structure.BasicBlock{
		ID:           "b1",
		StartAddress: 0x009000,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x009000, 0xea, "nop", "ea", entryCtx),
		},
	}
	cfgNoEntry := NewRoutineCFG(0x008000)
	cfgNoEntry.AddBlock(b)
	if _, err := LiftDAG(cfgNoEntry, entryCtx); err == nil || !strings.Contains(err.Error(), "entry block $008000 not found") {
		t.Errorf("expected entry block not found error, got %v", err)
	}

	// 4. Nil DAG for codegen
	if _, err := GenerateCompilableDAG_C(nil); err == nil || !strings.Contains(err.Error(), "nil BlockDAG") {
		t.Errorf("expected nil BlockDAG error, got %v", err)
	}

	// 5. Edge source block not found
	cfgBadEdge := NewRoutineCFG(0x008000)
	cfgBadEdge.AddBlock(&structure.BasicBlock{
		ID:           "b1",
		StartAddress: 0x008000,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xea, "nop", "ea", entryCtx),
		},
	})
	cfgBadEdge.AddEdge(0x007000, 0x008000, EdgeUnconditional)
	if _, err := LiftDAG(cfgBadEdge, entryCtx); err == nil || !strings.Contains(err.Error(), "edge source block $007000 not found") {
		t.Errorf("expected edge source not found error, got %v", err)
	}

	// 6. Conflicting CFG edge vs block successor
	cfgConflict := NewRoutineCFG(0x008000)
	cfgConflict.AddBlock(&structure.BasicBlock{
		ID:           "b1",
		StartAddress: 0x008000,
		Successors:   []uint32{0x008005},
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xea, "nop", "ea", entryCtx),
		},
	})
	cfgConflict.AddBlock(&structure.BasicBlock{
		ID:           "b2",
		StartAddress: 0x008005,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008005, 0x60, "rts", "60", entryCtx),
		},
	})
	cfgConflict.AddBlock(&structure.BasicBlock{
		ID:           "b3",
		StartAddress: 0x008010,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008010, 0x60, "rts", "60", entryCtx),
		},
	})
	cfgConflict.AddEdge(0x008000, 0x008010, EdgeUnconditional)
	dagConflict, err := LiftDAG(cfgConflict, entryCtx)
	if err == nil {
		_, err = GenerateCompilableDAG_C(dagConflict)
	}
	if err == nil || !strings.Contains(err.Error(), "conflicts with block successor") {
		t.Errorf("expected CFG edge conflict error, got %v", err)
	}
}

// TestMultiBlock_TerminalBlockPC tests terminal block PC advancement:
// A terminal basic block (LDA #$42 at $008000, EndAddress $008002, no successors):
// verifies Go CPU and compiled C both exit with PC=0x008002 and A=0x42.
func TestMultiBlock_TerminalBlockPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set", // 8-bit A
		X: "set", // 8-bit X
		C: "unknown",
	}

	b := &structure.BasicBlock{
		ID:           "bb-term-1",
		StartAddress: 0x008000,
		EndAddress:   0x008002,
		Successors:   nil,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xa9, "lda", "a942", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	init := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x30, // M=1, X=1
	}

	cRes, emuRes, err := RunDAG(ctx, dag, init, nil)
	if err != nil {
		t.Fatalf("RunDAG dual-backend execution failed: %v", err)
	}

	if cRes.State.PC != 0x8002 || cRes.NextPC != 0x008002 {
		t.Errorf("C runner PC mismatch: got PC=$%04X NextPC=$%06X, want PC=$8002 NextPC=$008002", cRes.State.PC, cRes.NextPC)
	}
	if cRes.State.A != 0x0042 {
		t.Errorf("C runner A mismatch: got A=$%04X, want $0042", cRes.State.A)
	}
	if emuRes.State.PC != 0x8002 || emuRes.NextPC != 0x008002 {
		t.Errorf("emulator PC mismatch: got PC=$%04X NextPC=$%06X, want PC=$8002 NextPC=$008002", emuRes.State.PC, emuRes.NextPC)
	}
	if emuRes.State.A != 0x0042 {
		t.Errorf("emulator A mismatch: got A=$%04X, want $0042", emuRes.State.A)
	}
}

// TestMultiBlock_CFGEdgeOnlySequential tests explicit CFG edges driving execution:
// Block 1 at $008000 (NOP, EndAddress $008001, empty Successors) and Block 2 at $008001
// (LDA #$42, EndAddress $008003) connected via cfg.AddEdge(0x008000, 0x008001, EdgeUnconditional):
// verifies both C and Go CPU execute block 1 then block 2, ending at PC=0x008003, A=0x42.
func TestMultiBlock_CFGEdgeOnlySequential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set",
		X: "set",
		C: "unknown",
	}

	b1 := &structure.BasicBlock{
		ID:           "bb-cfg-1",
		StartAddress: 0x008000,
		EndAddress:   0x008001,
		Successors:   nil, // explicitly empty Successors
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xea, "nop", "ea", entryCtx),
		},
	}

	b2 := &structure.BasicBlock{
		ID:           "bb-cfg-2",
		StartAddress: 0x008001,
		EndAddress:   0x008003,
		Successors:   nil,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008001, 0xa9, "lda", "a942", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b1)
	cfg.AddBlock(b2)
	cfg.AddEdge(0x008000, 0x008001, EdgeUnconditional)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	init := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x30,
	}

	cRes, emuRes, err := RunDAG(ctx, dag, init, nil)
	if err != nil {
		t.Fatalf("RunDAG failed: %v", err)
	}

	if cRes.State.PC != 0x8003 || cRes.NextPC != 0x008003 {
		t.Errorf("C runner PC mismatch: got PC=$%04X NextPC=$%06X, want PC=$8003 NextPC=$008003", cRes.State.PC, cRes.NextPC)
	}
	if cRes.State.A != 0x0042 {
		t.Errorf("C runner A mismatch: got A=$%04X, want $0042", cRes.State.A)
	}
	if emuRes.State.PC != 0x8003 || emuRes.NextPC != 0x008003 {
		t.Errorf("emulator PC mismatch: got PC=$%04X NextPC=$%06X, want PC=$8003 NextPC=$008003", emuRes.State.PC, emuRes.NextPC)
	}
	if emuRes.State.A != 0x0042 {
		t.Errorf("emulator A mismatch: got A=$%04X, want $0042", emuRes.State.A)
	}
}

// TestMultiBlock_RefuseDecimalMode tests that RunCompiledDAG and RunEmulatorDAG refuse execution
// when P has the decimal flag set (P & 0x08 != 0), or when runtime context contradicts lifted context.
func TestMultiBlock_RefuseDecimalMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entryCtx := recovery.Context{
		E: "clear",
		M: "set",
		X: "set",
		C: "unknown",
	}

	b := &structure.BasicBlock{
		ID:           "bb-refusal-1",
		StartAddress: 0x008000,
		EndAddress:   0x008002,
		Instructions: []recovery.Instruction{
			makeTestInsn(0x008000, 0xa9, "lda", "a942", entryCtx),
		},
	}

	cfg := NewRoutineCFG(0x008000)
	cfg.AddBlock(b)

	dag, err := LiftDAG(cfg, entryCtx)
	if err != nil {
		t.Fatalf("LiftDAG failed: %v", err)
	}

	// 1. Decimal mode refusal: P bit 3 (0x08) is set
	decimalInit := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x38, // M=1, X=1, D=1
	}

	if _, err := RunCompiledDAG(ctx, dag, decimalInit, nil); err == nil || !strings.Contains(err.Error(), "decimal mode (D=1) is unsupported") {
		t.Errorf("expected RunCompiledDAG decimal refusal, got: %v", err)
	}
	if _, err := RunEmulatorDAG(ctx, dag, decimalInit, nil); err == nil || !strings.Contains(err.Error(), "decimal mode (D=1) is unsupported") {
		t.Errorf("expected RunEmulatorDAG decimal refusal, got: %v", err)
	}

	// 2. Runtime context mismatch refusal: M=0 (16-bit) when lifted context specifies M=1 (8-bit)
	mismatchInit := CPUState{
		PC: 0x8000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x10, // M=0, X=1
	}
	if _, err := RunCompiledDAG(ctx, dag, mismatchInit, nil); err == nil || !strings.Contains(err.Error(), "expected M=1") {
		t.Errorf("expected RunCompiledDAG context mismatch refusal, got: %v", err)
	}
	if _, err := RunEmulatorDAG(ctx, dag, mismatchInit, nil); err == nil || !strings.Contains(err.Error(), "expected M=1") {
		t.Errorf("expected RunEmulatorDAG context mismatch refusal, got: %v", err)
	}

	// 3. Entry address mismatch refusal: PC=0x9000 vs DAG entry 0x008000
	wrongPCInit := CPUState{
		PC: 0x9000,
		PB: 0x00,
		S:  0x01FD,
		P:  0x30,
	}
	if _, err := RunCompiledDAG(ctx, dag, wrongPCInit, nil); err == nil || !strings.Contains(err.Error(), "entry contract violation") {
		t.Errorf("expected RunCompiledDAG entry PC mismatch refusal, got: %v", err)
	}
	if _, err := RunEmulatorDAG(ctx, dag, wrongPCInit, nil); err == nil || !strings.Contains(err.Error(), "entry contract violation") {
		t.Errorf("expected RunEmulatorDAG entry PC mismatch refusal, got: %v", err)
	}
}
