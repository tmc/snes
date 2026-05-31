package gsu

import (
	"strings"
	"testing"
)

func TestFutureStepSliceContract(t *testing.T) {
	const missingAPI = "(*Device).StepSlice(masterCycles uint64) StepSliceResult"
	assertions := []string{
		"StepSlice(13) during FMULT (14-cycle handler wait) retires zero logical opcodes",
		"Serialize/Unserialize preserves the in-flight handler, cycle debt, PC, pipeline, buffers, registers, and flags",
		"a second StepSlice(1) resumes the same FMULT handler and retires exactly one logical opcode",
		"the resumed final state matches whole-handler GoAndRun for registers, SFR, buffers, PC, pipeline, and Cycles",
		"Run(n) remains opcode-granular and does not expose partial handler state to existing tests",
	}
	for _, assertion := range assertions {
		if assertion == "" {
			t.Fatal("empty partial-step assertion")
		}
	}

	t.Skipf("%s is not implemented; enable these assertions before the GSU partial-step refactor: %s",
		missingAPI, strings.Join(assertions, "; "))
}
