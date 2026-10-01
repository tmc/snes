package decomp

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestConnectedNewOpcodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    []byte
		initial CPUState
		memory  []MemoryCell
		reg     Register
		value   uint16
		p       byte
		next    uint32
		writes  []MemoryWrite
		targets []uint32
	}{
		{name: "PLA8 preserves A high", code: []byte{0x68}, initial: CPUState{A: 0xab11, S: 0x1fd, P: 0x30}, memory: []MemoryCell{{0x7e01fe, 0x80}}, reg: RegA, value: 0xab80, p: 0xb0, next: 0x008001},
		{name: "PLA16 both bytes", code: []byte{0x68}, initial: CPUState{S: 0x1fd, P: 0x10}, memory: []MemoryCell{{0x7e01fe, 0}, {0x7e01ff, 0x80}}, reg: RegA, value: 0x8000, p: 0x90, next: 0x008001},
		{name: "PLY8 flags", code: []byte{0x7a}, initial: CPUState{Y: 7, S: 0x1fd, P: 0x30}, memory: []MemoryCell{{0x7e01fe, 0}}, reg: RegY, value: 0, p: 0x32, next: 0x008001},
		{name: "PLY16 both bytes", code: []byte{0x7a}, initial: CPUState{S: 0x1fd, P: 0x20}, memory: []MemoryCell{{0x7e01fe, 0x12}, {0x7e01ff, 0x80}}, reg: RegY, value: 0x8012, p: 0xa0, next: 0x008001},
		{name: "JSL ordered bank high low", code: []byte{0x22, 0x23, 0x81, 9}, initial: CPUState{S: 0x1fd, P: 0x30}, p: 0x30, next: 0x098123, writes: []MemoryWrite{{0x7e01fd, 0}, {0x7e01fc, 0x80}, {0x7e01fb, 3}}},
		{name: "LDA long indirect Y", code: []byte{0xb7, 0}, initial: CPUState{A: 0xab00, Y: 1, S: 0x1fd, P: 0x30}, memory: []MemoryCell{{0x7e0000, 0}, {0x7e0001, 0x20}, {0x7e0002, 0x7e}, {0x7e2001, 0x80}}, reg: RegA, value: 0xab80, p: 0xb0, next: 0x008002},
		{name: "LDA pointer bank0 wrap word carry", code: []byte{0xb7, 0xff}, initial: CPUState{D: 0xff00, Y: 1, S: 0x1fd, P: 0x10}, memory: []MemoryCell{{0x00ffff, 0xfe}, {0x7e0000, 0xff}, {0x7e0001, 0x7e}, {0x7effff, 0xef}, {0x7f0000, 0xbe}}, reg: RegA, value: 0xbeef, p: 0x90, next: 0x008002},
		{name: "JML pointer bank0 wrap", code: []byte{0xdc, 0xff, 0xff}, initial: CPUState{S: 0x1fd, P: 0x30}, memory: []MemoryCell{{0x00ffff, 0x23}, {0x7e0000, 0x81}, {0x7e0001, 9}}, p: 0x30, next: 0x098123, targets: []uint32{0x098123}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := recovery.Context{E: "clear", M: "clear", X: "clear", C: "unknown"}
			if tc.initial.P&0x20 != 0 {
				ctx.M = "set"
			}
			if tc.initial.P&0x10 != 0 {
				ctx.X = "set"
			}
			tc.initial.PC = 0x8000
			inst := recovery.Instruction{ID: "opcode", Address: 0x008000, Opcode: tc.code[0], Bytes: hex.EncodeToString(tc.code), Context: ctx}
			block, err := LiftBlock(&structure.BasicBlock{ID: "opcode", StartAddress: 0x008000, EndAddress: 0x008000 + uint32(len(tc.code)), Instructions: []recovery.Instruction{inst}, Successors: []uint32{tc.next}}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			if block.UnsupportedCount != 0 {
				t.Fatal("opcode not lifted")
			}
			for i := range block.Statements {
				if block.Statements[i].Kind == "jump_indirect" {
					block.Statements[i].AllowedTargets = tc.targets
				}
			}
			source, err := GenerateCompilableC(block)
			if err != nil {
				t.Fatal(err)
			}
			results, err := runCompiledBlock(context.Background(), t, source, "execute_block_008000", []ReplayCase{{CaseID: tc.name, InitialState: tc.initial, InitialMemory: tc.memory}})
			if err != nil {
				t.Fatal(err)
			}
			got := results[0]
			if got.MissingRead || got.MMIOAccess || got.NextPC != tc.next || got.State.P != tc.p {
				t.Fatalf("result %+v", got)
			}
			value := got.State.A
			if tc.reg == RegY {
				value = got.State.Y
			}
			if tc.reg != "" && value != tc.value {
				t.Fatalf("register %s=%04X want %04X", tc.reg, value, tc.value)
			}
			if ok, detail := CompareWrites(tc.writes, got.Writes); !ok {
				t.Fatal(detail)
			}
		})
	}
}
