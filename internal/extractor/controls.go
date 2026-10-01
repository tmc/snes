package extractor

import (
	"encoding/json"
	"strings"
)

// GenerateNegativeControls emits deterministic single-field corruption vectors.
// Only controls that actually alter an applicable field are emitted.
func GenerateNegativeControls(baseCase RoutineCaseV1) []RoutineCaseV1 {
	var controls []RoutineCaseV1

	clone := func() RoutineCaseV1 {
		b, _ := json.Marshal(baseCase)
		var c RoutineCaseV1
		_ = json.Unmarshal(b, &c)
		return c
	}

	// 1. initial_memory_flip (only if initial memory is non-empty)
	if len(baseCase.InitialMemory) > 0 {
		c := clone()
		c.CaseID += "_initial_memory_flip"
		c.InitialMemory[0].Value ^= 1
		controls = append(controls, c)
	}

	// 2. write_value_flip (only if observed writes are non-empty)
	if len(baseCase.ObservedWrites) > 0 {
		c := clone()
		c.CaseID += "_write_value_flip"
		c.ObservedWrites[0].Value ^= 1
		controls = append(controls, c)
	}

	// 3. writes_reordered (only if there are 2 or more writes)
	if len(baseCase.ObservedWrites) > 1 {
		c := clone()
		c.CaseID += "_writes_reordered"
		for i, j := 0, len(c.ObservedWrites)-1; i < j; i, j = i+1, j-1 {
			c.ObservedWrites[i], c.ObservedWrites[j] = c.ObservedWrites[j], c.ObservedWrites[i]
		}
		controls = append(controls, c)
	}

	// 4. exit_db_flip (guaranteed to alter DB)
	{
		c := clone()
		c.CaseID += "_exit_db_flip"
		if c.ObservedExitState.DB == 0 {
			c.ObservedExitState.DB = 1
		} else {
			c.ObservedExitState.DB = 0
		}
		controls = append(controls, c)
	}

	// 5. next_pc_flip (guaranteed to alter observed next PC)
	{
		c := clone()
		c.CaseID += "_next_pc_flip"
		if c.ObservedNextPC == 0 {
			c.ObservedNextPC = 1
		} else {
			c.ObservedNextPC = 0
		}
		controls = append(controls, c)
	}

	// 6. exit_seq_shift
	{
		c := clone()
		c.CaseID += "_exit_seq_shift"
		c.ExitSeq -= 1
		controls = append(controls, c)
	}

	// 7. rom_sha_flip
	{
		c := clone()
		c.CaseID += "_rom_sha_flip"
		c.ROMSHA256 = strings.Repeat("0", 64)
		controls = append(controls, c)
	}

	// 8. fixture_sha_flip
	{
		c := clone()
		c.CaseID += "_fixture_sha_flip"
		c.Evidence.Fixture.SHA256 = strings.Repeat("0", 64)
		controls = append(controls, c)
	}

	// 9. capture_sha_flip
	{
		c := clone()
		c.CaseID += "_capture_sha_flip"
		c.Evidence.Capture.SHA256 = strings.Repeat("0", 64)
		controls = append(controls, c)
	}

	// 10. history_sha_flip
	{
		c := clone()
		c.CaseID += "_history_sha_flip"
		c.Evidence.History.SHA256 = strings.Repeat("0", 64)
		controls = append(controls, c)
	}

	return controls
}

// SwapControlSpec represents a cross-case replay content substitution test.
type SwapControlSpec struct {
	AdmitCaseID   string        `json:"admit_case_id"`
	ReplayContent RoutineCaseV1 `json:"replay_content"`
	Policy        string        `json:"policy"`
}

// HasDistinctStateOrMemory returns true if case B differs from case A in CPU register values or memory effects.
func HasDistinctStateOrMemory(a, b RoutineCaseV1) bool {
	if a.InitialState.A != b.InitialState.A ||
		a.InitialState.X != b.InitialState.X ||
		a.InitialState.Y != b.InitialState.Y ||
		a.InitialState.S != b.InitialState.S ||
		a.InitialState.D != b.InitialState.D ||
		a.InitialState.DB != b.InitialState.DB ||
		a.InitialState.P != b.InitialState.P {
		return true
	}
	if a.ObservedExitState.A != b.ObservedExitState.A ||
		a.ObservedExitState.X != b.ObservedExitState.X ||
		a.ObservedExitState.Y != b.ObservedExitState.Y ||
		a.ObservedExitState.S != b.ObservedExitState.S ||
		a.ObservedExitState.D != b.ObservedExitState.D ||
		a.ObservedExitState.DB != b.ObservedExitState.DB ||
		a.ObservedExitState.P != b.ObservedExitState.P ||
		a.ObservedExitState.PC != b.ObservedExitState.PC ||
		a.ObservedExitState.PB != b.ObservedExitState.PB {
		return true
	}
	if a.ObservedNextPC != b.ObservedNextPC {
		return true
	}
	if len(a.InitialMemory) != len(b.InitialMemory) {
		return true
	}
	for i := range a.InitialMemory {
		if a.InitialMemory[i] != b.InitialMemory[i] {
			return true
		}
	}
	if len(a.ObservedWrites) != len(b.ObservedWrites) {
		return true
	}
	for i := range a.ObservedWrites {
		if a.ObservedWrites[i] != b.ObservedWrites[i] {
			return true
		}
	}
	return false
}

// HasDistinctContent returns true if case B represents distinct execution payload from case A.
// It verifies that case B is not identical to case A, prioritizing distinct registers/memory
// or distinct execution sequences/cycles.
func HasDistinctContent(a, b RoutineCaseV1) bool {
	if a.CaseID == b.CaseID {
		return false
	}
	if HasDistinctStateOrMemory(a, b) {
		return true
	}
	if a.EntrySeq != b.EntrySeq || a.InitialState.Cycles != b.InitialState.Cycles {
		return true
	}
	return false
}

// GenerateSwapControls creates cross-case substitution specifications.
// It verifies that each substitution pairs distinct execution content.
func GenerateSwapControls(cases []RoutineCaseV1, maxCount int) []SwapControlSpec {
	if len(cases) < 2 {
		return nil
	}

	var specs []SwapControlSpec
	for i := 0; i < len(cases) && len(specs) < maxCount; i++ {
		// First try finding a case with distinct state or memory.
		var foundMatch *RoutineCaseV1
		for j := 0; j < len(cases); j++ {
			if i == j {
				continue
			}
			if HasDistinctStateOrMemory(cases[i], cases[j]) {
				foundMatch = &cases[j]
				break
			}
		}
		// If all cases share state/memory, find a case with distinct execution sequence/timing.
		if foundMatch == nil {
			for j := 0; j < len(cases); j++ {
				if i == j {
					continue
				}
				if HasDistinctContent(cases[i], cases[j]) {
					foundMatch = &cases[j]
					break
				}
			}
		}

		if foundMatch != nil {
			specs = append(specs, SwapControlSpec{
				AdmitCaseID:   cases[i].CaseID,
				ReplayContent: *foundMatch,
				Policy:        "keep admitted A case_hash/admission_digest when substituting B content; must refuse",
			})
		}
	}
	return specs
}
