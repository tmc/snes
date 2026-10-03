package computation_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/divergence"
	"github.com/tmc/snes/internal/trace"
)

type genuineProbeData struct {
	TraceSHA256       string        `json:"trace_sha256"`
	SelectedRawEvents []trace.Event `json:"selected_raw_events"`
}

func TestGenuine139220Extraction(t *testing.T) {
	probePath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-2200-capability-review/reports/genuine-computation-probe.json"
	raw, err := os.ReadFile(probePath)
	if err != nil {
		t.Skipf("genuine probe file not found at %s: %v", probePath, err)
	}

	var data genuineProbeData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal genuine probe data: %v", err)
	}

	if len(data.SelectedRawEvents) == 0 {
		t.Fatalf("no raw events in probe data")
	}

	// Ingest the mixed trace stream containing both cpu_insn and bus records
	steps := divergence.StepsFromTraceEvents(data.SelectedRawEvents)
	if len(steps) != 5 {
		t.Fatalf("expected 5 steps from 5 cpu_insn events, got %d", len(steps))
	}

	// Verify step 1: LDA $1F05 owns read of 115
	ldaStep := steps[1]
	if ldaStep.Mnemonic != "LDA" {
		t.Errorf("step 1 mnemonic = %q, want 'LDA'", ldaStep.Mnemonic)
	}
	if len(ldaStep.Reads) != 1 {
		t.Fatalf("step 1 expected 1 read, got %d", len(ldaStep.Reads))
	}
	if ldaStep.Reads[0].Address != 0x7E1F05 || ldaStep.Reads[0].Value != 115 {
		t.Errorf("step 1 read = 0x%06X : %d, want $7E:1F05 : 115", ldaStep.Reads[0].Address, ldaStep.Reads[0].Value)
	}

	// Verify step 4: STA $1F05 owns write of 120
	staStep := steps[4]
	if staStep.Mnemonic != "STA" {
		t.Errorf("step 4 mnemonic = %q, want 'STA'", staStep.Mnemonic)
	}
	if len(staStep.Writes) != 1 {
		t.Fatalf("step 4 expected 1 write, got %d", len(staStep.Writes))
	}
	if staStep.Writes[0].Address != 0x7E1F05 || staStep.Writes[0].Value != 120 {
		t.Errorf("step 4 write = 0x%06X : %d, want $7E:1F05 : 120", staStep.Writes[0].Address, staStep.Writes[0].Value)
	}

	// Extract computation slice targeting step 4 (STA $1F05 at event 139220)
	comp, err := computation.Extract(steps, 4, computation.ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract computation failed on genuine 139220: %v", err)
	}

	if comp.Target.Address != 0x7E1F05 {
		t.Errorf("target address = 0x%06X, want 0x7E1F05", comp.Target.Address)
	}
	if comp.Target.Value != 120 {
		t.Errorf("target value = %d, want 120", comp.Target.Value)
	}

	// Verify identified input variable: in_1f05 = 115
	if len(comp.Inputs) == 0 {
		t.Fatalf("expected at least 1 input variable, got 0")
	}
	found1F05 := false
	for _, in := range comp.Inputs {
		if in.Address == 0x7E1F05 && in.InitialValue == 115 {
			found1F05 = true
			break
		}
	}
	if !found1F05 {
		t.Errorf("did not find input variable for $7E:1F05 with initial value 115: %+v", comp.Inputs)
	}

	// Execute with baseline input 115 -> must yield target 120
	baseVal, err := computation.Execute(comp, map[string]uint64{"in_1f05": 115})
	if err != nil {
		t.Fatalf("Execute with baseline input 115 failed: %v", err)
	}
	if baseVal != 120 {
		t.Errorf("Execute(in_1f05=115) = %d, want 120", baseVal)
	}

	// Execute with counterfactual input 114 -> must yield 119
	v114, err := computation.Execute(comp, map[string]uint64{"in_1f05": 114})
	if err != nil {
		t.Fatalf("Execute with input 114 failed: %v", err)
	}
	if v114 != 119 {
		t.Errorf("Execute(in_1f05=114) = %d, want 119", v114)
	}

	// Execute with counterfactual input 116 -> must yield 121
	v116, err := computation.Execute(comp, map[string]uint64{"in_1f05": 116})
	if err != nil {
		t.Fatalf("Execute with input 116 failed: %v", err)
	}
	if v116 != 121 {
		t.Errorf("Execute(in_1f05=116) = %d, want 121", v116)
	}
}
