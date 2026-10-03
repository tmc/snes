package counterfactual_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/counterfactual"
	"github.com/tmc/snes/internal/recovery/divergence"
	"github.com/tmc/snes/internal/trace"
)

type genuineProbeData struct {
	TraceSHA256       string        `json:"trace_sha256"`
	SelectedRawEvents []trace.Event `json:"selected_raw_events"`
}

func TestGenuine139220CounterfactualWorkbench(t *testing.T) {
	probePath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-2200-capability-review/reports/genuine-computation-probe.json"
	raw, err := os.ReadFile(probePath)
	if err != nil {
		t.Skipf("genuine probe file not found at %s: %v", probePath, err)
	}

	var data genuineProbeData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal genuine probe data: %v", err)
	}

	steps := divergence.StepsFromTraceEvents(data.SelectedRawEvents)
	comp, err := computation.Extract(steps, 4, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract failed on genuine 139220: %v", err)
	}

	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		t.Fatalf("NewWorkbench failed: %v", err)
	}

	// Add counterfactual neighborhood around input $7E:1F05 (114 and 116)
	if err := wb.AddStandardNeighborhood("in_1f05", -1, 1); err != nil {
		t.Fatalf("AddStandardNeighborhood failed: %v", err)
	}

	// Add no-op baseline reproduction
	if err := wb.AddPerturbation("noop-baseline", map[string]uint64{"in_1f05": 115}, "reproduce baseline with same input"); err != nil {
		t.Fatalf("AddPerturbation noop failed: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		t.Fatalf("Workbench Run failed: %v", err)
	}

	if report.BaselineOutput != 120 {
		t.Errorf("BaselineOutput = %d, want 120", report.BaselineOutput)
	}
	if len(report.Cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(report.Cases))
	}

	// Case 0: in_1f05 = 114 -> Output 119
	c0 := report.Cases[0]
	if c0.OutputValue != 119 {
		t.Errorf("case 0 (114) output = %d, want 119", c0.OutputValue)
	}
	if !c0.OutputDiffers {
		t.Errorf("case 0 expected OutputDiffers = true")
	}
	if c0.Status != counterfactual.StatusPredictedDivergence {
		t.Errorf("case 0 status = %q, want %q", c0.Status, counterfactual.StatusPredictedDivergence)
	}
	if c0.DivergenceReport == nil || c0.DivergenceReport.FirstEffect == nil {
		t.Fatalf("case 0 expected non-nil FirstEffect divergence")
	}
	if c0.DivergenceReport.FirstEffect.Address != 0x7E1F05 {
		t.Errorf("case 0 effect address = 0x%06X, want 0x7E1F05", c0.DivergenceReport.FirstEffect.Address)
	}
	if c0.DivergenceReport.FirstEffect.AlteredValue != 119 {
		t.Errorf("case 0 altered value = %d, want 119", c0.DivergenceReport.FirstEffect.AlteredValue)
	}

	// Case 1: in_1f05 = 116 -> Output 121
	c1 := report.Cases[1]
	if c1.OutputValue != 121 {
		t.Errorf("case 1 (116) output = %d, want 121", c1.OutputValue)
	}
	if !c1.OutputDiffers {
		t.Errorf("case 1 expected OutputDiffers = true")
	}

	// Case 2: in_1f05 = 115 (no-op) -> Output 120 (matches baseline)
	c2 := report.Cases[2]
	if c2.OutputValue != 120 {
		t.Errorf("case 2 (115 noop) output = %d, want 120", c2.OutputValue)
	}
	if c2.OutputDiffers {
		t.Errorf("case 2 expected OutputDiffers = false")
	}
	if c2.Status != counterfactual.StatusBaselineMatch {
		t.Errorf("case 2 status = %q, want %q", c2.Status, counterfactual.StatusBaselineMatch)
	}

	// Verify input sensitivity
	if len(report.Sensitivities) != 1 {
		t.Fatalf("expected 1 sensitivity record, got %d", len(report.Sensitivities))
	}
	if !report.Sensitivities[0].Sensitive {
		t.Errorf("expected in_1f05 to be marked sensitive")
	}
}
