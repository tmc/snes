package counterfactual_test

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/counterfactual"
	"github.com/tmc/snes/internal/recovery/divergence"
)

func makeTableLookupComputation() *computation.Computation {
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
		panic(err)
	}

	// Populate ROM values for table lookup.
	comp.ROMData[0x098000+0] = 5
	comp.ROMData[0x098000+114] = 19
	comp.ROMData[0x098000+115] = 20
	comp.ROMData[0x098000+116] = 21
	comp.ROMData[0x098000+255] = 99

	return comp
}

func makeBranchComputation() *computation.Computation {
	// Trace:
	// $008000: BEQ $008008     (takes branch to $008008 if in_branch != 0)
	// $008002: LDA #$01        (fall-through when branch not taken)
	// $008004: STA $20
	// $008006: BRA $00800C     (jump to end)
	// $008008: LDA #$02        (target when branch taken)
	// $00800A: STA $20
	return &computation.Computation{
		Target: computation.OutputEffect{
			Address:     0x000020,
			Value:       2,
			Width:       8,
			StepIndex:   5,
			Description: "store to $00:0020",
		},
		Inputs: []computation.InputVariable{
			{
				Name:         "in_branch",
				Type:         "uint8",
				Source:       "in_branch",
				InitialValue: 1,
			},
		},
		Instructions: []computation.SliceInstruction{
			{
				StepIndex: 0,
				Address:   0x008000,
				Opcode:    0xF0,
				Mnemonic:  "BEQ",
				Operands:  "$008008",
				DataDeps: []computation.Dependency{
					{Kind: computation.DependencyData, Source: "in_branch", Target: "Cond", Value: 1},
				},
			},
			{
				StepIndex: 1,
				Address:   0x008002,
				Opcode:    0xA9,
				Mnemonic:  "LDA",
				Operands:  "#$01",
			},
			{
				StepIndex: 2,
				Address:   0x008004,
				Opcode:    0x85,
				Mnemonic:  "STA",
				Operands:  "$20",
			},
			{
				StepIndex: 3,
				Address:   0x008006,
				Opcode:    0x80,
				Mnemonic:  "BRA",
				Operands:  "$00800C",
			},
			{
				StepIndex: 4,
				Address:   0x008008,
				Opcode:    0xA9,
				Mnemonic:  "LDA",
				Operands:  "#$02",
			},
			{
				StepIndex: 5,
				Address:   0x00800A,
				Opcode:    0x85,
				Mnemonic:  "STA",
				Operands:  "$20",
			},
		},
	}
}

func TestNewWorkbench(t *testing.T) {
	tests := []struct {
		name    string
		comp    *computation.Computation
		wantErr bool
	}{
		{
			name:    "nil computation",
			comp:    nil,
			wantErr: true,
		},
		{
			name:    "valid computation",
			comp:    makeTableLookupComputation(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wb, err := counterfactual.NewWorkbench(tt.comp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewWorkbench() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if wb == nil {
					t.Fatal("expected non-nil Workbench")
				}
				if wb.BaselineInputs["in_1f05"] != 115 {
					t.Errorf("BaselineInputs[in_1f05] = %d, want 115", wb.BaselineInputs["in_1f05"])
				}
			}
		})
	}
}

func TestAddPerturbation_Validation(t *testing.T) {
	comp := makeTableLookupComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	tests := []struct {
		name      string
		overrides map[string]uint64
		wantErr   bool
	}{
		{
			name:      "valid variable",
			overrides: map[string]uint64{"in_1f05": 114},
			wantErr:   false,
		},
		{
			name:      "unknown variable",
			overrides: map[string]uint64{"nonexistent": 42},
			wantErr:   true,
		},
		{
			name:      "mixed valid and unknown",
			overrides: map[string]uint64{"in_1f05": 114, "unknown": 1},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := wb.AddPerturbation(tt.name, tt.overrides, "test desc")
			if (err != nil) != tt.wantErr {
				t.Fatalf("AddPerturbation() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBaselineRun_NoOpMatch(t *testing.T) {
	comp := makeTableLookupComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Add a no-op edit (no overrides) and an explicit identical override.
	if err := wb.AddPerturbation("noop_empty", nil, "no-op empty overrides"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}
	if err := wb.AddPerturbation("noop_same_val", map[string]uint64{"in_1f05": 115}, "no-op identical value"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if report.BaselineOutput != 20 {
		t.Errorf("BaselineOutput = %d, want 20", report.BaselineOutput)
	}
	if len(report.Cases) != 2 {
		t.Fatalf("len(Cases) = %d, want 2", len(report.Cases))
	}

	for _, c := range report.Cases {
		if c.Status != counterfactual.StatusBaselineMatch {
			t.Errorf("Case %q status = %q, want %q", c.Perturbation.Name, c.Status, counterfactual.StatusBaselineMatch)
		}
		if c.OutputDiffers {
			t.Errorf("Case %q OutputDiffers = true, want false", c.Perturbation.Name)
		}
		if c.OutputValue != 20 {
			t.Errorf("Case %q OutputValue = %d, want 20", c.Perturbation.Name, c.OutputValue)
		}
		if c.DivergenceReport == nil || c.DivergenceReport.Decision.Category != divergence.DivergenceNone {
			t.Errorf("Case %q expected DivergenceNone, got %+v", c.Perturbation.Name, c.DivergenceReport)
		}
	}
}

func TestStandardNeighborhood(t *testing.T) {
	comp := makeTableLookupComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Test invalid variable error.
	if err := wb.AddStandardNeighborhood("invalid_var"); err == nil {
		t.Error("AddStandardNeighborhood with invalid var expected error, got nil")
	}

	// Add standard neighborhood with -1, 0, +1 deltas.
	if err := wb.AddStandardNeighborhood("in_1f05", -1, 0, 1); err != nil {
		t.Fatalf("AddStandardNeighborhood failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if len(report.Cases) != 3 {
		t.Fatalf("len(Cases) = %d, want 3", len(report.Cases))
	}

	caseMap := make(map[string]counterfactual.CaseResult)
	for _, c := range report.Cases {
		caseMap[c.Perturbation.Name] = c
	}

	// 114 (-1): output 19, divergence.
	cMinus := caseMap["in_1f05-1"]
	if cMinus.OutputValue != 19 {
		t.Errorf("in_1f05-1 output = %d, want 19", cMinus.OutputValue)
	}
	if !cMinus.OutputDiffers {
		t.Errorf("in_1f05-1 OutputDiffers = false, want true")
	}
	if cMinus.Status != counterfactual.StatusPredictedDivergence {
		t.Errorf("in_1f05-1 status = %q, want %q", cMinus.Status, counterfactual.StatusPredictedDivergence)
	}

	// 115 (+0): output 20, baseline match.
	cZero := caseMap["in_1f05+0"]
	if cZero.OutputValue != 20 {
		t.Errorf("in_1f05+0 output = %d, want 20", cZero.OutputValue)
	}
	if cZero.OutputDiffers {
		t.Errorf("in_1f05+0 OutputDiffers = true, want false")
	}
	if cZero.Status != counterfactual.StatusBaselineMatch {
		t.Errorf("in_1f05+0 status = %q, want %q", cZero.Status, counterfactual.StatusBaselineMatch)
	}

	// 116 (+1): output 21, divergence.
	cPlus := caseMap["in_1f05+1"]
	if cPlus.OutputValue != 21 {
		t.Errorf("in_1f05+1 output = %d, want 21", cPlus.OutputValue)
	}
	if !cPlus.OutputDiffers {
		t.Errorf("in_1f05+1 OutputDiffers = false, want true")
	}
	if cPlus.Status != counterfactual.StatusPredictedDivergence {
		t.Errorf("in_1f05+1 status = %q, want %q", cPlus.Status, counterfactual.StatusPredictedDivergence)
	}
}

func TestAddBoundaries(t *testing.T) {
	comp := makeTableLookupComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Unknown variable should fail.
	if err := wb.AddBoundaries("bad_var"); err == nil {
		t.Error("AddBoundaries bad_var expected error, got nil")
	}

	if err := wb.AddBoundaries("in_1f05"); err != nil {
		t.Fatalf("AddBoundaries failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if len(report.Cases) != 2 {
		t.Fatalf("len(Cases) = %d, want 2", len(report.Cases))
	}

	caseMap := make(map[string]counterfactual.CaseResult)
	for _, c := range report.Cases {
		caseMap[c.Perturbation.Name] = c
	}

	// Boundary min (0) -> ROMData[0] = 5
	cMin := caseMap["in_1f05_min"]
	if cMin.Inputs["in_1f05"] != 0 {
		t.Errorf("in_1f05_min input = %d, want 0", cMin.Inputs["in_1f05"])
	}
	if cMin.OutputValue != 5 {
		t.Errorf("in_1f05_min output = %d, want 5", cMin.OutputValue)
	}
	if !cMin.OutputDiffers {
		t.Errorf("in_1f05_min OutputDiffers = false, want true")
	}

	// Boundary max (255) -> ROMData[255] = 99
	cMax := caseMap["in_1f05_max"]
	if cMax.Inputs["in_1f05"] != 255 {
		t.Errorf("in_1f05_max input = %d, want 255", cMax.Inputs["in_1f05"])
	}
	if cMax.OutputValue != 99 {
		t.Errorf("in_1f05_max output = %d, want 99", cMax.OutputValue)
	}
	if !cMax.OutputDiffers {
		t.Errorf("in_1f05_max OutputDiffers = false, want true")
	}
}

func TestSensitivityDetection(t *testing.T) {
	// Build a computation with two inputs:
	// in_used: loaded and stored to $20 -> changes output
	// in_unused: never read or referenced -> does not affect output
	comp := &computation.Computation{
		Target: computation.OutputEffect{
			Address:     0x000020,
			Value:       10,
			Width:       8,
			StepIndex:   1,
			Description: "store to $00:0020",
		},
		Inputs: []computation.InputVariable{
			{
				Name:         "in_used",
				Type:         "uint8",
				Source:       "Mem:$00:0010",
				Address:      0x000010,
				InitialValue: 10,
			},
			{
				Name:         "in_unused",
				Type:         "uint8",
				Source:       "Mem:$00:0012",
				Address:      0x000012,
				InitialValue: 50,
			},
		},
		Instructions: []computation.SliceInstruction{
			{
				StepIndex: 0,
				Address:   0x008000,
				Opcode:    0xA5,
				Mnemonic:  "LDA",
				Operands:  "$10",
				DataDeps: []computation.Dependency{
					{Kind: computation.DependencyData, Source: "Mem:$00:0010", Target: "RegA", Value: 10},
				},
			},
			{
				StepIndex: 1,
				Address:   0x008002,
				Opcode:    0x85,
				Mnemonic:  "STA",
				Operands:  "$20",
			},
		},
	}

	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Perturb sensitive input.
	if err := wb.AddPerturbation("used_perturb", map[string]uint64{"in_used": 25}, "change sensitive input"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}
	// Perturb unused input.
	if err := wb.AddPerturbation("unused_perturb", map[string]uint64{"in_unused": 99}, "change unused input"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	sensMap := make(map[string]counterfactual.InputSensitivity)
	for _, s := range report.Sensitivities {
		sensMap[s.Variable] = s
	}

	usedSens, ok := sensMap["in_used"]
	if !ok {
		t.Fatal("missing in_used sensitivity")
	}
	if !usedSens.Sensitive {
		t.Errorf("in_used Sensitive = false, want true")
	}

	unusedSens, ok := sensMap["in_unused"]
	if !ok {
		t.Fatal("missing in_unused sensitivity")
	}
	if unusedSens.Sensitive {
		t.Errorf("in_unused Sensitive = true, want false")
	}
}

func TestDivergenceReporting_InternalBranch(t *testing.T) {
	comp := makeBranchComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Baseline has in_branch = 1:
	// Branch BEQ $8008 is taken.
	// LDA #$02, STA $20 -> output 2.

	// Perturb with in_branch = 0:
	// Branch BEQ is NOT taken -> falls through to $8002 (LDA #$01, STA $20, BRA $800C).
	// Output 1.
	if err := wb.AddPerturbation("branch_alt", map[string]uint64{"in_branch": 0}, "trigger alternate branch path"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if report.BaselineOutput != 2 {
		t.Errorf("BaselineOutput = %d, want 2", report.BaselineOutput)
	}

	if len(report.Cases) != 1 {
		t.Fatalf("len(Cases) = %d, want 1", len(report.Cases))
	}

	c := report.Cases[0]
	if c.Status != counterfactual.StatusPredictedDivergence {
		t.Errorf("Case status = %q, want %q", c.Status, counterfactual.StatusPredictedDivergence)
	}
	if c.OutputValue != 1 {
		t.Errorf("Case OutputValue = %d, want 1", c.OutputValue)
	}
	if !c.OutputDiffers {
		t.Errorf("Case OutputDiffers = false, want true")
	}

	if c.DivergenceReport == nil || c.DivergenceReport.Decision == nil {
		t.Fatal("expected non-nil DivergenceReport.Decision")
	}

	dec := c.DivergenceReport.Decision
	if dec.Category != divergence.DivergenceBranchOutcome {
		t.Errorf("Divergence category = %q, want %q", dec.Category, divergence.DivergenceBranchOutcome)
	}
	if dec.PC != 0x008000 {
		t.Errorf("Divergence PC = 0x%X, want 0x008000", dec.PC)
	}
}

func TestDivergenceReporting_ValueChange(t *testing.T) {
	// Simple arithmetic:
	// 0: LDA $10 (val 10)
	// 1: CLC
	// 2: ADC #$05
	// 3: STA $20 (store 15)
	comp := &computation.Computation{
		Target: computation.OutputEffect{
			Address:     0x000020,
			Value:       15,
			Width:       8,
			StepIndex:   3,
			Description: "store to $00:0020",
		},
		Inputs: []computation.InputVariable{
			{
				Name:         "in_10",
				Type:         "uint8",
				Source:       "Mem:$00:0010",
				Address:      0x000010,
				InitialValue: 10,
			},
		},
		Instructions: []computation.SliceInstruction{
			{
				StepIndex: 0,
				Address:   0x008000,
				Opcode:    0xA5,
				Mnemonic:  "LDA",
				Operands:  "$10",
				DataDeps: []computation.Dependency{
					{Kind: computation.DependencyData, Source: "Mem:$00:0010", Target: "RegA", Value: 10},
				},
			},
			{
				StepIndex: 1,
				Address:   0x008002,
				Opcode:    0x18,
				Mnemonic:  "CLC",
				Operands:  "",
			},
			{
				StepIndex: 2,
				Address:   0x008003,
				Opcode:    0x69,
				Mnemonic:  "ADC",
				Operands:  "#$05",
			},
			{
				StepIndex: 3,
				Address:   0x008005,
				Opcode:    0x85,
				Mnemonic:  "STA",
				Operands:  "$20",
			},
		},
	}

	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	if err := wb.AddPerturbation("val_change", map[string]uint64{"in_10": 20}, "different arithmetic input"); err != nil {
		t.Fatalf("AddPerturbation failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	c := report.Cases[0]
	if c.OutputValue != 25 {
		t.Errorf("OutputValue = %d, want 25", c.OutputValue)
	}
	if !c.OutputDiffers {
		t.Errorf("OutputDiffers = false, want true")
	}
	if c.DivergenceReport == nil || c.DivergenceReport.FirstEffect == nil {
		t.Fatal("expected FirstEffect in DivergenceReport")
	}

	eff := c.DivergenceReport.FirstEffect
	if eff.Address != 0x000020 {
		t.Errorf("Effect Address = 0x%X, want 0x000020", eff.Address)
	}
	if eff.BaselineValue != 15 {
		t.Errorf("Effect BaselineValue = %d, want 15", eff.BaselineValue)
	}
	if eff.AlteredValue != 25 {
		t.Errorf("Effect AlteredValue = %d, want 25", eff.AlteredValue)
	}
}

func TestWorkbenchReport_Summary(t *testing.T) {
	comp := makeTableLookupComputation()
	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	_ = wb.AddPerturbation("noop", nil, "no-op")
	_ = wb.AddStandardNeighborhood("in_1f05", -1, 1)

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	if report.Summary == "" {
		t.Fatal("Summary should not be empty")
	}

	for _, expectedSubstring := range []string{
		"Counterfactual Workbench:",
		"Baseline Output: 20",
		"Cases: 3 total",
		"Sensitivity Analysis:",
		"in_1f05: sensitive",
		"Perturbation Outcomes:",
		"noop: output 20 (matches baseline)",
		"in_1f05-1: output 19 (differs)",
	} {
		if !strings.Contains(report.Summary, expectedSubstring) {
			t.Errorf("Summary missing substring %q\nFull summary:\n%s", expectedSubstring, report.Summary)
		}
	}
}

func TestUninitializedWorkbenchErrors(t *testing.T) {
	var wb *counterfactual.Workbench
	if err := wb.AddPerturbation("test", nil, ""); err == nil {
		t.Error("AddPerturbation on nil expected error, got nil")
	}
	if err := wb.AddStandardNeighborhood("test"); err == nil {
		t.Error("AddStandardNeighborhood on nil expected error, got nil")
	}
	if err := wb.AddBoundaries("test"); err == nil {
		t.Error("AddBoundaries on nil expected error, got nil")
	}
	if _, err := wb.Run(); err == nil {
		t.Error("Run on nil expected error, got nil")
	}
}
