package computation_test

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/divergence"
)

func TestExtract_ClassicTableLookup(t *testing.T) {
	// Classic trace:
	// 0: LDY $1F05      (reads $7E:1F05 = 115)
	// 1: LDA $098000,Y   (reads ROM $09:8073 = 20)
	// 2: STA $1F06      (writes $7E:1F06 = 20)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xAC,
			Mnemonic:  "LDY",
			Registers: divergence.RegisterState{Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x7E1F05, Value: 115, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008003,
			Opcode:    0xB9,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x098073, Value: 20, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008006,
			Opcode:    0x8D,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Writes:    []divergence.MemoryAccess{{Address: 0x7E1F06, Value: 20, Width: 8}},
			Sequence:  2,
		},
	}

	comp, err := computation.Extract(steps, 2, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// 1. Verify target effect.
	if comp.Target.Address != 0x7E1F06 {
		t.Errorf("Target Address = 0x%X, want 0x7E1F06", comp.Target.Address)
	}
	if comp.Target.Value != 20 {
		t.Errorf("Target Value = %d, want 20", comp.Target.Value)
	}

	// 2. Verify instructions in slice.
	if len(comp.Instructions) != 3 {
		t.Fatalf("len(comp.Instructions) = %d, want 3", len(comp.Instructions))
	}

	// 3. Verify step 0 (LDY) data dependency from memory.
	ldyInsn := comp.Instructions[0]
	if ldyInsn.Mnemonic != "LDY" {
		t.Errorf("Insn 0 mnemonic = %q, want LDY", ldyInsn.Mnemonic)
	}
	hasMemDataDep := false
	for _, dd := range ldyInsn.DataDeps {
		if dd.Kind == computation.DependencyData && strings.Contains(dd.Source, "1F05") && dd.Target == "RegY" {
			hasMemDataDep = true
			if dd.Value != 115 {
				t.Errorf("LDY DataDep value = %d, want 115", dd.Value)
			}
		}
	}
	if !hasMemDataDep {
		t.Errorf("Step 0 (LDY) missing data dependency from memory load to RegY, got: %+v", ldyInsn.DataDeps)
	}

	// 4. Verify step 1 (LDA table read) address dependency on Y.
	ldaInsn := comp.Instructions[1]
	if ldaInsn.Mnemonic != "LDA" {
		t.Errorf("Insn 1 mnemonic = %q, want LDA", ldaInsn.Mnemonic)
	}
	hasYAddrDep := false
	for _, ad := range ldaInsn.AddrDeps {
		if ad.Kind == computation.DependencyAddress && ad.Source == "RegY" {
			hasYAddrDep = true
			if ad.Value != 115 {
				t.Errorf("LDA AddrDep value = %d, want 115", ad.Value)
			}
			if ad.StepIndex != 0 {
				t.Errorf("LDA AddrDep StepIndex = %d, want 0", ad.StepIndex)
			}
		}
	}
	if !hasYAddrDep {
		t.Errorf("Step 1 (LDA) missing address dependency on RegY, got: %+v", ldaInsn.AddrDeps)
	}

	// 5. Verify inputs are identified (in_1f05 with initial value 115).
	if len(comp.Inputs) != 1 {
		t.Fatalf("len(comp.Inputs) = %d, want 1", len(comp.Inputs))
	}
	inp := comp.Inputs[0]
	if inp.Name != "in_1f05" {
		t.Errorf("Input Name = %q, want in_1f05", inp.Name)
	}
	if inp.InitialValue != 115 {
		t.Errorf("Input InitialValue = %d, want 115", inp.InitialValue)
	}

	// 6. Verify evaluator yields 20 on initial inputs.
	res, err := computation.Execute(comp, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != 20 {
		t.Errorf("Execute result = %d, want 20", res)
	}
}

func TestExtract_ArithmeticChain(t *testing.T) {
	// 0: LDA $10 (reads $10 = 10)
	// 1: CLC
	// 2: ADC #5 (adds immediate 5 -> 15)
	// 3: STA $20 (writes $20 = 15)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 10},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 10, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0x18,
			Mnemonic:  "CLC",
			Registers: divergence.RegisterState{A: 10, P: 0},
			Sequence:  1,
		},
		{
			Address:   0x008003,
			Opcode:    0x69,
			Mnemonic:  "ADC",
			Registers: divergence.RegisterState{A: 15, P: 0},
			Sequence:  2,
		},
		{
			Address:   0x008005,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 15, P: 0},
			Writes:    []divergence.MemoryAccess{{Address: 0x000020, Value: 15, Width: 8}},
			Sequence:  3,
		},
	}

	comp, err := computation.Extract(steps, 3, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(comp.Instructions) != 4 {
		t.Fatalf("len(comp.Instructions) = %d, want 4", len(comp.Instructions))
	}
	if len(comp.Inputs) != 1 {
		t.Fatalf("len(comp.Inputs) = %d, want 1", len(comp.Inputs))
	}
	if comp.Inputs[0].Name != "in_10" {
		t.Errorf("Input Name = %q, want in_10", comp.Inputs[0].Name)
	}

	// Test Execute with varying inputs.
	tests := []struct {
		name     string
		inputVal uint64
		want     uint64
	}{
		{"initial input 10", 10, 15},
		{"input 20", 20, 25},
		{"input 100", 100, 105},
		{"input 252 (overflow 8-bit)", 252, 1}, // (252 + 5) & 0xFF = 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computation.Execute(comp, map[string]uint64{"in_10": tt.inputVal})
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Execute(in_10=%d) = %d, want %d", tt.inputVal, got, tt.want)
			}
		})
	}
}

func TestExtract_MultiOperand(t *testing.T) {
	// 0: LDA $10 (reads $10 = 3)
	// 1: ADC $11 (reads $11 = 4, adds to A -> 7)
	// 2: STA $12 (writes $12 = 7)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 3},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 3, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0x65,
			Mnemonic:  "ADC",
			Registers: divergence.RegisterState{A: 7},
			Reads:     []divergence.MemoryAccess{{Address: 0x000011, Value: 4, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008004,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 7},
			Writes:    []divergence.MemoryAccess{{Address: 0x000012, Value: 7, Width: 8}},
			Sequence:  2,
		},
	}

	comp, err := computation.Extract(steps, 2, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Verify both inputs in_10 and in_11 are discovered.
	inputNames := make(map[string]bool)
	for _, inp := range comp.Inputs {
		inputNames[inp.Name] = true
	}
	if !inputNames["in_10"] || !inputNames["in_11"] {
		t.Errorf("Expected inputs in_10 and in_11, got: %+v", comp.Inputs)
	}

	// Test Execute with varying multi-operand combinations.
	tests := []struct {
		name string
		in10 uint64
		in11 uint64
		want uint64
	}{
		{"initial 3 + 4", 3, 4, 7},
		{"add 20 + 30", 20, 30, 50},
		{"add 100 + 50", 100, 50, 150},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computation.Execute(comp, map[string]uint64{
				"in_10": tt.in10,
				"in_11": tt.in11,
			})
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Execute(in_10=%d, in_11=%d) = %d, want %d", tt.in10, tt.in11, got, tt.want)
			}
		})
	}
}

func TestExtract_GeneratedC(t *testing.T) {
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xAC,
			Mnemonic:  "LDY",
			Registers: divergence.RegisterState{Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x7E1F05, Value: 115, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008003,
			Opcode:    0xB9,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x098073, Value: 20, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008006,
			Opcode:    0x8D,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Writes:    []divergence.MemoryAccess{{Address: 0x7E1F06, Value: 20, Width: 8}},
			Sequence:  2,
		},
	}

	comp, err := computation.Extract(steps, 2, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	cCode := comp.GeneratedC
	if !strings.Contains(cCode, "#include <stdint.h>") {
		t.Errorf("GeneratedC missing #include <stdint.h>:\n%s", cCode)
	}
	if !strings.Contains(cCode, "uint8_t compute(uint8_t in_1f05)") {
		t.Errorf("GeneratedC missing function signature compute(uint8_t in_1f05):\n%s", cCode)
	}
	if !strings.Contains(cCode, "return a;") {
		t.Errorf("GeneratedC missing return statement:\n%s", cCode)
	}
	if !strings.Contains(cCode, "rom_098000") {
		t.Errorf("GeneratedC missing ROM table reference:\n%s", cCode)
	}
}

func TestExtract_Pruning(t *testing.T) {
	// Steps:
	// 0: LDX #$55       (unrelated!)
	// 1: LDA $10        (reads $10 = 12)
	// 2: STX $50        (unrelated store!)
	// 3: STA $20        (target store from A)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA2,
			Mnemonic:  "LDX",
			Registers: divergence.RegisterState{X: 0x55},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 12, X: 0x55},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 12, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008004,
			Opcode:    0x86,
			Mnemonic:  "STX",
			Registers: divergence.RegisterState{A: 12, X: 0x55},
			Writes:    []divergence.MemoryAccess{{Address: 0x000050, Value: 0x55, Width: 8}},
			Sequence:  2,
		},
		{
			Address:   0x008006,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 12, X: 0x55},
			Writes:    []divergence.MemoryAccess{{Address: 0x000020, Value: 12, Width: 8}},
			Sequence:  3,
		},
	}

	comp, err := computation.Extract(steps, 3, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Should only contain steps 1 (LDA) and 3 (STA).
	if len(comp.Instructions) != 2 {
		t.Fatalf("len(comp.Instructions) = %d, want 2 (unrelated pruned)", len(comp.Instructions))
	}
	if comp.Instructions[0].StepIndex != 1 || comp.Instructions[1].StepIndex != 3 {
		t.Errorf("Instructions step indices = [%d, %d], want [1, 3]",
			comp.Instructions[0].StepIndex, comp.Instructions[1].StepIndex)
	}
}

func TestExtract_StopAtMemoryBoundary(t *testing.T) {
	// Trace:
	// 0: LDA #$42
	// 1: STA $10   (writes $10 = 0x42)
	// 2: LDA $10   (reads $10 = 0x42)
	// 3: STA $20   (target)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA9,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 0x42},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 0x42},
			Writes:    []divergence.MemoryAccess{{Address: 0x000010, Value: 0x42, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008004,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 0x42},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 0x42, Width: 8}},
			Sequence:  2,
		},
		{
			Address:   0x008006,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 0x42},
			Writes:    []divergence.MemoryAccess{{Address: 0x000020, Value: 0x42, Width: 8}},
			Sequence:  3,
		},
	}

	// 1. With StopAtMemoryBoundary = true, it stops at step 2 and treats $10 as free input.
	compStop, err := computation.Extract(steps, 3, computation.ExtractOptions{StopAtMemoryBoundary: true})
	if err != nil {
		t.Fatalf("Extract (stop=true) failed: %v", err)
	}
	if len(compStop.Instructions) != 2 {
		t.Errorf("StopAtMemoryBoundary=true instructions = %d, want 2", len(compStop.Instructions))
	}
	if len(compStop.Inputs) != 1 || compStop.Inputs[0].Name != "in_10" {
		t.Errorf("StopAtMemoryBoundary=true inputs = %+v, want in_10", compStop.Inputs)
	}

	// 2. With StopAtMemoryBoundary = false, it traces through step 1 to step 0.
	compTrace, err := computation.Extract(steps, 3, computation.ExtractOptions{StopAtMemoryBoundary: false})
	if err != nil {
		t.Fatalf("Extract (stop=false) failed: %v", err)
	}
	if len(compTrace.Instructions) != 4 {
		t.Errorf("StopAtMemoryBoundary=false instructions = %d, want 4", len(compTrace.Instructions))
	}
}

func TestExtract_Errors(t *testing.T) {
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xEA,
			Mnemonic:  "NOP",
			Sequence:  0,
		},
	}

	tests := []struct {
		name      string
		steps     []divergence.Step
		targetIdx int
		wantErr   string
	}{
		{
			name:      "empty steps",
			steps:     nil,
			targetIdx: 0,
			wantErr:   "empty steps trace",
		},
		{
			name:      "negative target step",
			steps:     steps,
			targetIdx: -1,
			wantErr:   "out of bounds",
		},
		{
			name:      "target step exceeds bounds",
			steps:     steps,
			targetIdx: 5,
			wantErr:   "out of bounds",
		},
		{
			name:      "step has no write or register def",
			steps:     steps,
			targetIdx: 0,
			wantErr:   "no memory write or register definition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := computation.Extract(tt.steps, tt.targetIdx, computation.ExtractOptions{})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestExtract_RegisterTarget(t *testing.T) {
	// Target is register definition, not a store.
	// 0: LDA $10 (val 10)
	// 1: ADC #5  (val 15, target)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 10},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 10, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0x69,
			Mnemonic:  "ADC",
			Registers: divergence.RegisterState{A: 15},
			Sequence:  1,
		},
	}

	comp, err := computation.Extract(steps, 1, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if comp.Target.Address != 0 {
		t.Errorf("Target Address = %d, want 0 (register target)", comp.Target.Address)
	}
	if comp.Target.Value != 15 {
		t.Errorf("Target Value = %d, want 15", comp.Target.Value)
	}

	res, err := computation.Execute(comp, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != 15 {
		t.Errorf("Execute result = %d, want 15", res)
	}
}

func TestExtract_MaxBackwardSteps(t *testing.T) {
	// Trace:
	// 0: LDA #$10
	// 1: STA $10
	// 2: LDA $10
	// 3: STA $20
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA9,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 0x10},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 0x10},
			Writes:    []divergence.MemoryAccess{{Address: 0x000010, Value: 0x10, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008004,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 0x10},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 0x10, Width: 8}},
			Sequence:  2,
		},
		{
			Address:   0x008006,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 0x10},
			Writes:    []divergence.MemoryAccess{{Address: 0x000020, Value: 0x10, Width: 8}},
			Sequence:  3,
		},
	}

	// Limit backward steps to 1 (only step 2 can be visited, step 1 and 0 are not).
	comp, err := computation.Extract(steps, 3, computation.ExtractOptions{MaxBackwardSteps: 1})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// With MaxBackwardSteps: 1, step 1 is not reached, so memory $10 becomes an input.
	if len(comp.Instructions) != 2 {
		t.Errorf("Instructions len = %d, want 2 (steps 2 and 3)", len(comp.Instructions))
	}
	if len(comp.Inputs) != 1 || comp.Inputs[0].Name != "in_10" {
		t.Errorf("Inputs = %+v, want [in_10]", comp.Inputs)
	}
}

func TestExtract_RegisterTransfers(t *testing.T) {
	// 0: LDA $10 (val 5)
	// 1: TAX     (X = 5)
	// 2: INX     (X = 6)
	// 3: TXA     (A = 6)
	// 4: STA $20 (writes 6)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xA5,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 5},
			Reads:     []divergence.MemoryAccess{{Address: 0x000010, Value: 5, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008002,
			Opcode:    0xAA,
			Mnemonic:  "TAX",
			Registers: divergence.RegisterState{A: 5, X: 5},
			Sequence:  1,
		},
		{
			Address:   0x008003,
			Opcode:    0xE8,
			Mnemonic:  "INX",
			Registers: divergence.RegisterState{A: 5, X: 6},
			Sequence:  2,
		},
		{
			Address:   0x008004,
			Opcode:    0x8A,
			Mnemonic:  "TXA",
			Registers: divergence.RegisterState{A: 6, X: 6},
			Sequence:  3,
		},
		{
			Address:   0x008005,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 6, X: 6},
			Writes:    []divergence.MemoryAccess{{Address: 0x000020, Value: 6, Width: 8}},
			Sequence:  4,
		},
	}

	comp, err := computation.Extract(steps, 4, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(comp.Instructions) != 5 {
		t.Fatalf("Instructions count = %d, want 5", len(comp.Instructions))
	}

	res, err := computation.Execute(comp, map[string]uint64{"in_10": 10})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != 11 {
		t.Errorf("Execute(in_10=10) = %d, want 11", res)
	}
}

func TestExtract_LogicAndSBC(t *testing.T) {
	// 0: LDA #$FF
	// 1: AND #$0F (A = 0x0F)
	// 2: ORA #$50 (A = 0x5F)
	// 3: EOR #$05 (A = 0x5A)
	// 4: SEC
	// 5: SBC #$0A (A = 0x50)
	// 6: STA $20
	steps := []divergence.Step{
		{Address: 0x8000, Opcode: 0xA9, Mnemonic: "LDA", Registers: divergence.RegisterState{A: 0xFF}, Sequence: 0},
		{Address: 0x8002, Opcode: 0x29, Mnemonic: "AND", Registers: divergence.RegisterState{A: 0x0F}, Sequence: 1},
		{Address: 0x8004, Opcode: 0x09, Mnemonic: "ORA", Registers: divergence.RegisterState{A: 0x5F}, Sequence: 2},
		{Address: 0x8006, Opcode: 0x49, Mnemonic: "EOR", Registers: divergence.RegisterState{A: 0x5A}, Sequence: 3},
		{Address: 0x8008, Opcode: 0x38, Mnemonic: "SEC", Registers: divergence.RegisterState{A: 0x5A, P: 1}, Sequence: 4},
		{Address: 0x8009, Opcode: 0xE9, Mnemonic: "SBC", Registers: divergence.RegisterState{A: 0x50, P: 1}, Sequence: 5},
		{
			Address:   0x800B,
			Opcode:    0x85,
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 0x50, P: 1},
			Writes:    []divergence.MemoryAccess{{Address: 0x20, Value: 0x50, Width: 8}},
			Sequence:  6,
		},
	}

	comp, err := computation.Extract(steps, 6, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	res, err := computation.Execute(comp, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != 0x50 {
		t.Errorf("Execute result = 0x%X, want 0x50", res)
	}
}

func TestExecute_Nil(t *testing.T) {
	_, err := computation.Execute(nil, nil)
	if err == nil {
		t.Error("expected error for nil computation, got nil")
	}
}

