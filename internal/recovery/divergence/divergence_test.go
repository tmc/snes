package divergence

import (
	"testing"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/trace"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name               string
		baseline           []Step
		altered            []Step
		opts               Options
		wantPrefixLen      int
		wantCategory       DivergenceCategory
		wantDecisionPC     uint32
		wantEffectAddr     uint32
		wantEffectBaseVal  uint16
		wantEffectAltVal   uint16
		hasEffect          bool
	}{
		{
			name: "identical traces",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			wantPrefixLen: 2,
			wantCategory:  DivergenceNone,
			hasEffect:     false,
		},
		{
			name: "branch divergence BNE taken vs not taken",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0xD0, Mnemonic: "BNE", Sequence: 0},
				{Address: 0x008002, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0xD0, Mnemonic: "BNE", Sequence: 0},
				{Address: 0x008010, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			wantPrefixLen:  1,
			wantCategory:   DivergenceBranchOutcome,
			wantDecisionPC: 0x008000,
			hasEffect:      false,
		},
		{
			name: "indirect jump target divergence JMP ($1000,X)",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0x7C, Mnemonic: "JMP", Sequence: 0},
				{Address: 0x009000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0x7C, Mnemonic: "JMP", Sequence: 0},
				{Address: 0x009500, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			wantPrefixLen:  1,
			wantCategory:   DivergenceIndirectTarget,
			wantDecisionPC: 0x008000,
			hasEffect:      false,
		},
		{
			name: "return target divergence RTS",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0x60, Mnemonic: "RTS", Sequence: 0},
				{Address: 0x008100, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0x60, Mnemonic: "RTS", Sequence: 0},
				{Address: 0x008200, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			wantPrefixLen:  1,
			wantCategory:   DivergenceReturnTarget,
			wantDecisionPC: 0x008000,
			hasEffect:      false,
		},
		{
			name: "register divergence LDA resulting in different accumulator and store",
			baseline: []Step{
				{
					Address:   0x008000,
					Opcode:    0xA9,
					Mnemonic:  "LDA",
					Registers: RegisterState{A: 0x0010},
					Sequence:  0,
				},
				{
					Address:   0x008002,
					Opcode:    0x8D,
					Mnemonic:  "STA",
					Registers: RegisterState{A: 0x0010},
					Writes:    []MemoryAccess{{Address: 0x7E1F05, Value: 0x10, Width: 8}},
					Sequence:  1,
				},
			},
			altered: []Step{
				{
					Address:   0x008000,
					Opcode:    0xA9,
					Mnemonic:  "LDA",
					Registers: RegisterState{A: 0x0020},
					Sequence:  0,
				},
				{
					Address:   0x008002,
					Opcode:    0x8D,
					Mnemonic:  "STA",
					Registers: RegisterState{A: 0x0020},
					Writes:    []MemoryAccess{{Address: 0x7E1F05, Value: 0x20, Width: 8}},
					Sequence:  1,
				},
			},
			wantPrefixLen:     0,
			wantCategory:      DivergenceRegisterState,
			wantDecisionPC:    0x008000,
			hasEffect:         true,
			wantEffectAddr:    0x7E1F05,
			wantEffectBaseVal: 0x10,
			wantEffectAltVal:  0x20,
		},
		{
			name: "first memory write effect detection at $7E:1F05",
			baseline: []Step{
				{
					Address:  0x008000,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E1F05, Value: 0x01, Width: 8}},
					Sequence: 0,
				},
			},
			altered: []Step{
				{
					Address:  0x008000,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E1F05, Value: 0x02, Width: 8}},
					Sequence: 0,
				},
			},
			wantPrefixLen:     0,
			wantCategory:      DivergenceMemoryWrite,
			wantDecisionPC:    0x008000,
			hasEffect:         true,
			wantEffectAddr:    0x7E1F05,
			wantEffectBaseVal: 0x01,
			wantEffectAltVal:  0x02,
		},
		{
			name: "length mismatch one run terminates early",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
				{Address: 0x008002, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 2},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
			},
			wantPrefixLen:  1,
			wantCategory:   DivergenceLengthMismatch,
			wantDecisionPC: 0x008001,
			hasEffect:      false,
		},
		{
			name: "uncorrelated divergence",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x009999, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 1},
			},
			wantPrefixLen:  1,
			wantCategory:   DivergenceUncorrelated,
			wantDecisionPC: 0x008000,
			hasEffect:      false,
		},
		{
			name: "selected addresses filtering ignores non-monitored writes",
			baseline: []Step{
				{
					Address:  0x008000,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E0000, Value: 0xAA, Width: 8}},
					Sequence: 0,
				},
				{
					Address:  0x008003,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E1F05, Value: 0x55, Width: 8}},
					Sequence: 1,
				},
			},
			altered: []Step{
				{
					Address:  0x008000,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E0000, Value: 0xBB, Width: 8}},
					Sequence: 0,
				},
				{
					Address:  0x008003,
					Opcode:   0x8D,
					Mnemonic: "STA",
					Writes:   []MemoryAccess{{Address: 0x7E1F05, Value: 0x66, Width: 8}},
					Sequence: 1,
				},
			},
			opts: Options{
				SelectedAddresses: []uint32{0x7E1F05},
			},
			wantPrefixLen:     0,
			wantCategory:      DivergenceMemoryWrite,
			wantDecisionPC:    0x008000,
			hasEffect:         true,
			wantEffectAddr:    0x7E1F05,
			wantEffectBaseVal: 0x55,
			wantEffectAltVal:  0x66,
		},
		{
			name: "max steps limits comparison",
			baseline: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xD0, Mnemonic: "BNE", Sequence: 1},
				{Address: 0x008003, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 2},
			},
			altered: []Step{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 0},
				{Address: 0x008001, Opcode: 0xD0, Mnemonic: "BNE", Sequence: 1},
				{Address: 0x008010, Opcode: 0xEA, Mnemonic: "NOP", Sequence: 2},
			},
			opts: Options{
				MaxSteps: 1,
			},
			wantPrefixLen: 1,
			wantCategory:  DivergenceNone,
			hasEffect:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := Compare(tt.baseline, tt.altered, tt.opts)
			if err != nil {
				t.Fatalf("Compare() unexpected error: %v", err)
			}
			if rep.CommonPrefixLength != tt.wantPrefixLen {
				t.Errorf("CommonPrefixLength = %d, want %d", rep.CommonPrefixLength, tt.wantPrefixLen)
			}
			if rep.Decision == nil {
				t.Fatalf("rep.Decision is nil")
			}
			if rep.Decision.Category != tt.wantCategory {
				t.Errorf("Decision.Category = %v, want %v", rep.Decision.Category, tt.wantCategory)
			}
			if tt.wantDecisionPC != 0 && rep.Decision.PC != tt.wantDecisionPC {
				t.Errorf("Decision.PC = 0x%06X, want 0x%06X", rep.Decision.PC, tt.wantDecisionPC)
			}
			if tt.hasEffect {
				if rep.FirstEffect == nil {
					t.Fatalf("FirstEffect is nil, expected effect at 0x%06X", tt.wantEffectAddr)
				}
				if rep.FirstEffect.Address != tt.wantEffectAddr {
					t.Errorf("FirstEffect.Address = 0x%06X, want 0x%06X", rep.FirstEffect.Address, tt.wantEffectAddr)
				}
				if rep.FirstEffect.BaselineValue != tt.wantEffectBaseVal {
					t.Errorf("FirstEffect.BaselineValue = 0x%02X, want 0x%02X", rep.FirstEffect.BaselineValue, tt.wantEffectBaseVal)
				}
				if rep.FirstEffect.AlteredValue != tt.wantEffectAltVal {
					t.Errorf("FirstEffect.AlteredValue = 0x%02X, want 0x%02X", rep.FirstEffect.AlteredValue, tt.wantEffectAltVal)
				}
			} else if rep.FirstEffect != nil {
				t.Errorf("FirstEffect unexpected non-nil: %+v", rep.FirstEffect)
			}
			if rep.Summary == "" {
				t.Errorf("Summary is empty")
			}
		})
	}
}

func TestStepFromObservation(t *testing.T) {
	obs := cpu.Observation{
		Entry: cpu.Snapshot{
			PC:     0x8000,
			PB:     0x00,
			Cycles: 100,
		},
		Exit: cpu.Snapshot{
			PC:     0x8002,
			PB:     0x00,
			A:      0x1234,
			Cycles: 102,
		},
		NumFetches: 2,
	}
	obs.Fetches[0] = cpu.Fetch{Addr: 0x008000, Value: 0xA9} // LDA imm
	obs.Fetches[1] = cpu.Fetch{Addr: 0x008001, Value: 0x34}

	step := StepFromObservation(obs, 5)
	if step.Address != 0x008000 {
		t.Errorf("step.Address = 0x%06X, want 0x008000", step.Address)
	}
	if step.Opcode != 0xA9 {
		t.Errorf("step.Opcode = 0x%02X, want 0xA9", step.Opcode)
	}
	if step.Mnemonic != "LDA" {
		t.Errorf("step.Mnemonic = %q, want LDA", step.Mnemonic)
	}
	if step.Registers.A != 0x1234 {
		t.Errorf("step.Registers.A = 0x%04X, want 0x1234", step.Registers.A)
	}
	if step.Sequence != 5 {
		t.Errorf("step.Sequence = %d, want 5", step.Sequence)
	}

	steps := StepsFromObservations([]cpu.Observation{obs})
	if len(steps) != 1 || steps[0].Sequence != 0 {
		t.Errorf("StepsFromObservations returned unexpected slice: %+v", steps)
	}
}

func TestStepFromTraceEvent(t *testing.T) {
	ev := trace.Event{
		Kind:  "cpu_insn",
		Cycle: 42,
		Insn: &trace.Insn{
			Seq: 10,
			Entry: trace.Registers{
				PC: 0x8000,
				PB: 0x00,
			},
			Exit: trace.Registers{
				PC: 0x8002,
				PB: 0x00,
				A:  0x0042,
			},
			Fetches: []trace.FetchRecord{
				{Addr: 0x008000, Value: 0xA9, Role: "opcode"},
			},
		},
	}

	step, ok := StepFromTraceEvent(ev, 0)
	if !ok {
		t.Fatalf("StepFromTraceEvent failed")
	}
	if step.Address != 0x008000 || step.Opcode != 0xA9 || step.Registers.A != 0x0042 {
		t.Errorf("unexpected step: %+v", step)
	}

	events := []trace.Event{ev}
	steps := StepsFromTraceEvents(events)
	if len(steps) != 1 {
		t.Errorf("StepsFromTraceEvents returned len %d, want 1", len(steps))
	}
}

func TestIsMMIO(t *testing.T) {
	if !IsMMIO(0x002100) {
		t.Errorf("0x002100 should be MMIO")
	}
	if !IsMMIO(0x804200) {
		t.Errorf("0x804200 should be MMIO")
	}
	if IsMMIO(0x7E1F05) {
		t.Errorf("0x7E1F05 is WRAM, not MMIO")
	}
}
