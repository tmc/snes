package callsummary_test

import (
	"errors"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis/callsummary"
	"github.com/tmc/snes/internal/recovery/structure"
)

func TestAnalyzePreserveDB(t *testing.T) {
	tests := []struct {
		name       string
		insns      []recovery.Instruction
		wantDB     callsummary.Status
		wantDBVal  uint16
		wantS      callsummary.Status
		wantDelta  int
		wantErr    error
	}{
		{
			name: "phb_nop_plb_rts",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x8B, Mnemonic: "PHB"},
				{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008002, Opcode: 0xAB, Mnemonic: "PLB"},
				{Address: 0x008003, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantDB:    callsummary.Preserved,
			wantS:     callsummary.Preserved,
			wantDelta: 0,
		},
		{
			name: "phb_phd_pld_plb_rts",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x8B, Mnemonic: "PHB"},
				{Address: 0x008001, Opcode: 0x0B, Mnemonic: "PHD"},
				{Address: 0x008002, Opcode: 0x2B, Mnemonic: "PLD"},
				{Address: 0x008003, Opcode: 0xAB, Mnemonic: "PLB"},
				{Address: 0x008004, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantDB:    callsummary.Preserved,
			wantS:     callsummary.Preserved,
			wantDelta: 0,
		},
		{
			name: "untouched_db_rts",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantDB:    callsummary.Preserved,
			wantS:     callsummary.Preserved,
			wantDelta: 0,
		},
		{
			name: "guaranteed_db_via_lda_pha_plb",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xA9, Bytes: "a90c", Mnemonic: "LDA"},
				{Address: 0x008002, Opcode: 0x48, Mnemonic: "PHA"},
				{Address: 0x008003, Opcode: 0xAB, Mnemonic: "PLB"},
				{Address: 0x008004, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantDB:    callsummary.Guaranteed,
			wantDBVal: 0x0C,
			wantS:     callsummary.Preserved,
			wantDelta: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := callsummary.AnalyzeInstructions(tt.insns)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AnalyzeInstructions() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AnalyzeInstructions() unexpected err: %v", err)
			}

			dbState := got.Register(callsummary.RegDB)
			if dbState.Status != tt.wantDB {
				t.Errorf("DB status = %v, want %v", dbState.Status, tt.wantDB)
			}
			if tt.wantDB == callsummary.Guaranteed && dbState.Value != tt.wantDBVal {
				t.Errorf("DB value = $%04X, want $%04X", dbState.Value, tt.wantDBVal)
			}

			sState := got.Register(callsummary.RegS)
			if sState.Status != tt.wantS {
				t.Errorf("S status = %v, want %v", sState.Status, tt.wantS)
			}
			if got.StackDelta != tt.wantDelta {
				t.Errorf("StackDelta = %d, want %d", got.StackDelta, tt.wantDelta)
			}
		})
	}
}

func TestAnalyzeModifyAndPreserveRegisters(t *testing.T) {
	tests := []struct {
		name       string
		insns      []recovery.Instruction
		wantA      callsummary.Status
		wantX      callsummary.Status
		wantY      callsummary.Status
		wantDP     callsummary.Status
		wantDB     callsummary.Status
		wantFlagZ  callsummary.Status
		wantFlagN  callsummary.Status
	}{
		{
			name: "modify_x_via_inx",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xE8, Mnemonic: "INX"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantA:     callsummary.Preserved,
			wantX:     callsummary.Clobbered,
			wantY:     callsummary.Preserved,
			wantDP:    callsummary.Preserved,
			wantDB:    callsummary.Preserved,
			wantFlagZ: callsummary.Clobbered,
			wantFlagN: callsummary.Clobbered,
		},
		{
			name: "preserve_x_via_phx_plx",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xDA, Mnemonic: "PHX"},
				{Address: 0x008001, Opcode: 0xE8, Mnemonic: "INX"},
				{Address: 0x008002, Opcode: 0xFA, Mnemonic: "PLX"},
				{Address: 0x008003, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantA:     callsummary.Preserved,
			wantX:     callsummary.Preserved,
			wantY:     callsummary.Preserved,
			wantDP:    callsummary.Preserved,
			wantDB:    callsummary.Preserved,
			wantFlagZ: callsummary.Clobbered,
			wantFlagN: callsummary.Clobbered,
		},
		{
			name: "modify_a_via_lda_mem",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xAD, Bytes: "ad0020", Mnemonic: "LDA"},
				{Address: 0x008003, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantA:     callsummary.Clobbered,
			wantX:     callsummary.Preserved,
			wantY:     callsummary.Preserved,
			wantDP:    callsummary.Preserved,
			wantDB:    callsummary.Preserved,
			wantFlagZ: callsummary.Clobbered,
			wantFlagN: callsummary.Clobbered,
		},
		{
			name: "preserve_a_via_pha_pla",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x48, Mnemonic: "PHA"},
				{Address: 0x008001, Opcode: 0xA9, Bytes: "a955", Mnemonic: "LDA"},
				{Address: 0x008003, Opcode: 0x68, Mnemonic: "PLA"},
				{Address: 0x008004, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantA:     callsummary.Preserved,
			wantX:     callsummary.Preserved,
			wantY:     callsummary.Preserved,
			wantDP:    callsummary.Preserved,
			wantDB:    callsummary.Preserved,
			wantFlagZ: callsummary.Clobbered,
			wantFlagN: callsummary.Clobbered,
		},
		{
			name: "preserve_dp_via_phd_pld",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x0B, Mnemonic: "PHD"},
				{Address: 0x008001, Opcode: 0x5B, Mnemonic: "TCD"},
				{Address: 0x008002, Opcode: 0x2B, Mnemonic: "PLD"},
				{Address: 0x008003, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantA:     callsummary.Preserved,
			wantX:     callsummary.Preserved,
			wantY:     callsummary.Preserved,
			wantDP:    callsummary.Preserved,
			wantDB:    callsummary.Preserved,
			wantFlagZ: callsummary.Clobbered,
			wantFlagN: callsummary.Clobbered,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := callsummary.AnalyzeInstructions(tt.insns)
			if err != nil {
				t.Fatalf("AnalyzeInstructions() unexpected err: %v", err)
			}

			if got.Register(callsummary.RegA).Status != tt.wantA {
				t.Errorf("A = %v, want %v", got.Register(callsummary.RegA).Status, tt.wantA)
			}
			if got.Register(callsummary.RegX).Status != tt.wantX {
				t.Errorf("X = %v, want %v", got.Register(callsummary.RegX).Status, tt.wantX)
			}
			if got.Register(callsummary.RegY).Status != tt.wantY {
				t.Errorf("Y = %v, want %v", got.Register(callsummary.RegY).Status, tt.wantY)
			}
			if got.Register(callsummary.RegDP).Status != tt.wantDP {
				t.Errorf("DP = %v, want %v", got.Register(callsummary.RegDP).Status, tt.wantDP)
			}
			if got.Register(callsummary.RegDB).Status != tt.wantDB {
				t.Errorf("DB = %v, want %v", got.Register(callsummary.RegDB).Status, tt.wantDB)
			}
			if got.Flag(callsummary.FlagZ).Status != tt.wantFlagZ {
				t.Errorf("FlagZ = %v, want %v", got.Flag(callsummary.FlagZ).Status, tt.wantFlagZ)
			}
			if got.Flag(callsummary.FlagN).Status != tt.wantFlagN {
				t.Errorf("FlagN = %v, want %v", got.Flag(callsummary.FlagN).Status, tt.wantFlagN)
			}
		})
	}
}

func TestAnalyzeFlags(t *testing.T) {
	tests := []struct {
		name       string
		insns      []recovery.Instruction
		wantM      callsummary.Status
		wantMVal   bool
		wantX      callsummary.Status
		wantXVal   bool
		wantC      callsummary.Status
		wantCVal   bool
	}{
		{
			name: "sep_m_set",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xE2, Bytes: "e220", Mnemonic: "SEP"},
				{Address: 0x008002, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantM:    callsummary.Guaranteed,
			wantMVal: true,
			wantX:    callsummary.Preserved,
			wantC:    callsummary.Preserved,
		},
		{
			name: "rep_mx_clear",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xC2, Bytes: "c230", Mnemonic: "REP"},
				{Address: 0x008002, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantM:    callsummary.Guaranteed,
			wantMVal: false,
			wantX:    callsummary.Guaranteed,
			wantXVal: false,
			wantC:    callsummary.Preserved,
		},
		{
			name: "sec_set_c",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x38, Mnemonic: "SEC"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantM:    callsummary.Preserved,
			wantX:    callsummary.Preserved,
			wantC:    callsummary.Guaranteed,
			wantCVal: true,
		},
		{
			name: "php_rep_plp_preserves_flags",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x08, Mnemonic: "PHP"},
				{Address: 0x008001, Opcode: 0xC2, Bytes: "c220", Mnemonic: "REP"},
				{Address: 0x008003, Opcode: 0x28, Mnemonic: "PLP"},
				{Address: 0x008004, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantM: callsummary.Preserved,
			wantX: callsummary.Preserved,
			wantC: callsummary.Preserved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := callsummary.AnalyzeInstructions(tt.insns)
			if err != nil {
				t.Fatalf("AnalyzeInstructions() unexpected err: %v", err)
			}

			fm := got.Flag(callsummary.FlagM)
			if fm.Status != tt.wantM {
				t.Errorf("FlagM status = %v, want %v", fm.Status, tt.wantM)
			}
			if tt.wantM == callsummary.Guaranteed && fm.Value != tt.wantMVal {
				t.Errorf("FlagM value = %v, want %v", fm.Value, tt.wantMVal)
			}

			fx := got.Flag(callsummary.FlagX)
			if fx.Status != tt.wantX {
				t.Errorf("FlagX status = %v, want %v", fx.Status, tt.wantX)
			}
			if tt.wantX == callsummary.Guaranteed && fx.Value != tt.wantXVal {
				t.Errorf("FlagX value = %v, want %v", fx.Value, tt.wantXVal)
			}

			fc := got.Flag(callsummary.FlagC)
			if fc.Status != tt.wantC {
				t.Errorf("FlagC status = %v, want %v", fc.Status, tt.wantC)
			}
			if tt.wantC == callsummary.Guaranteed && fc.Value != tt.wantCVal {
				t.Errorf("FlagC value = %v, want %v", fc.Value, tt.wantCVal)
			}
		})
	}
}

func TestAnalyzeUnbalancedStack(t *testing.T) {
	tests := []struct {
		name      string
		insns     []recovery.Instruction
		wantErr   error
		wantDelta int
	}{
		{
			name: "unpopped_phb",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x8B, Mnemonic: "PHB"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantErr:   callsummary.ErrUnbalancedStack,
			wantDelta: -1,
		},
		{
			name: "unpopped_pha_8bit",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x48, Mnemonic: "PHA"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantErr:   callsummary.ErrUnbalancedStack,
			wantDelta: -1,
		},
		{
			name: "stack_underflow_pla_without_push",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0x68, Mnemonic: "PLA"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantErr:   callsummary.ErrStackUnderflow,
			wantDelta: -1,
		},
		{
			name: "no_return_instruction",
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
			},
			wantErr: callsummary.ErrNoReturn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract, err := callsummary.AnalyzeInstructions(tt.insns)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AnalyzeInstructions() err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == callsummary.ErrUnbalancedStack && contract.StackDelta != tt.wantDelta {
				t.Errorf("StackDelta = %d, want %d", contract.StackDelta, tt.wantDelta)
			}
			if contract.IsBalanced() {
				t.Errorf("contract.IsBalanced() = true, want false")
			}
		})
	}
}

func TestCallerCalleeDisciplineVerification(t *testing.T) {
	tests := []struct {
		name    string
		callOp  byte
		insns   []recovery.Instruction
		wantErr error
	}{
		{
			name:   "jsr_matched_with_rts",
			callOp: callsummary.CallJSR,
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantErr: nil,
		},
		{
			name:   "jsr_mismatched_with_rtl",
			callOp: callsummary.CallJSR,
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008001, Opcode: 0x6B, Mnemonic: "RTL"},
			},
			wantErr: callsummary.ErrMismatchedReturn,
		},
		{
			name:   "jsl_matched_with_rtl",
			callOp: callsummary.CallJSL,
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008001, Opcode: 0x6B, Mnemonic: "RTL"},
			},
			wantErr: nil,
		},
		{
			name:   "jsl_mismatched_with_rts",
			callOp: callsummary.CallJSL,
			insns: []recovery.Instruction{
				{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
				{Address: 0x008001, Opcode: 0x60, Mnemonic: "RTS"},
			},
			wantErr: callsummary.ErrMismatchedReturn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract, err := callsummary.AnalyzeInstructions(tt.insns, callsummary.WithCallOp(tt.callOp))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AnalyzeInstructions() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AnalyzeInstructions() unexpected err: %v", err)
			}

			// Also verify via VerifyDiscipline
			if vErr := callsummary.VerifyDiscipline(tt.callOp, contract); vErr != nil {
				t.Errorf("VerifyDiscipline() failed: %v", vErr)
			}
		})
	}
}

func TestAnalyzeRoutineCFG(t *testing.T) {
	// Test diamond CFG:
	// entry (0x8000): PHB; BNE 0x8008;
	// path1 (0x8004): INX; BRA 0x800A;
	// path2 (0x8008): INY;
	// join  (0x800A): PLB; RTS;
	entryBlock := &structure.BasicBlock{
		ID:           "entry",
		StartAddress: 0x008000,
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Opcode: 0x8B, Mnemonic: "PHB"},
			{Address: 0x008001, Opcode: 0xD0, Bytes: "d005", Mnemonic: "BNE"}, // target 0x8008
		},
		Successors: []uint32{0x008003, 0x008008},
	}
	path1Block := &structure.BasicBlock{
		ID:           "path1",
		StartAddress: 0x008003,
		Instructions: []recovery.Instruction{
			{Address: 0x008003, Opcode: 0xE8, Mnemonic: "INX"},
			{Address: 0x008004, Opcode: 0x80, Bytes: "8004", Mnemonic: "BRA"}, // target 0x800A
		},
		Successors: []uint32{0x00800A},
	}
	path2Block := &structure.BasicBlock{
		ID:           "path2",
		StartAddress: 0x008008,
		Instructions: []recovery.Instruction{
			{Address: 0x008008, Opcode: 0xC8, Mnemonic: "INY"},
		},
		Successors: []uint32{0x00800A},
	}
	joinBlock := &structure.BasicBlock{
		ID:           "join",
		StartAddress: 0x00800A,
		Instructions: []recovery.Instruction{
			{Address: 0x00800A, Opcode: 0xAB, Mnemonic: "PLB"},
			{Address: 0x00800B, Opcode: 0x60, Mnemonic: "RTS"},
		},
		Successors: nil,
	}

	ast := &callsummary.RoutineAST{
		EntryAddress: 0x008000,
		Blocks:       []*structure.BasicBlock{entryBlock, path1Block, path2Block, joinBlock},
	}

	contract, err := callsummary.AnalyzeRoutine(ast)
	if err != nil {
		t.Fatalf("AnalyzeRoutine() unexpected err: %v", err)
	}

	// In both paths, PHB is restored by PLB
	if !contract.Preserves(callsummary.RegDB) {
		t.Errorf("DB = %v, want Preserved", contract.Register(callsummary.RegDB))
	}
	if !contract.IsBalanced() {
		t.Errorf("StackDelta = %d, want 0", contract.StackDelta)
	}

	// Path 1 modified X, Path 2 modified Y -> both X and Y should be clobbered
	if contract.Register(callsummary.RegX).Status != callsummary.Clobbered {
		t.Errorf("X = %v, want Clobbered", contract.Register(callsummary.RegX))
	}
	if contract.Register(callsummary.RegY).Status != callsummary.Clobbered {
		t.Errorf("Y = %v, want Clobbered", contract.Register(callsummary.RegY))
	}

	// A and DP were untouched on all paths -> should be Preserved!
	if !contract.Preserves(callsummary.RegA) {
		t.Errorf("A = %v, want Preserved", contract.Register(callsummary.RegA))
	}
	if !contract.Preserves(callsummary.RegDP) {
		t.Errorf("DP = %v, want Preserved", contract.Register(callsummary.RegDP))
	}
}

func TestAnalyzeRoutineCycle(t *testing.T) {
	// Routine with a cycle: block 1 loops back to block 0
	b0 := &structure.BasicBlock{
		ID:           "b0",
		StartAddress: 0x008000,
		Instructions: []recovery.Instruction{
			{Address: 0x008000, Opcode: 0xEA, Mnemonic: "NOP"},
		},
		Successors: []uint32{0x008001},
	}
	b1 := &structure.BasicBlock{
		ID:           "b1",
		StartAddress: 0x008001,
		Instructions: []recovery.Instruction{
			{Address: 0x008001, Opcode: 0x80, Bytes: "80fd", Mnemonic: "BRA"}, // loops to 0x8000
		},
		Successors: []uint32{0x008000},
	}

	ast := &callsummary.RoutineAST{
		EntryAddress: 0x008000,
		Blocks:       []*structure.BasicBlock{b0, b1},
	}

	_, err := callsummary.AnalyzeRoutine(ast)
	if !errors.Is(err, callsummary.ErrCyclicRoutine) {
		t.Fatalf("AnalyzeRoutine() err = %v, want ErrCyclicRoutine", err)
	}
}
