package gsu

import "testing"

// TestStepRetiresWholeHandlerAtomically characterizes the current Step
// contract. A smaller future StepSlice-style API should invert this test:
// sub-handler grants should retire no logical opcode and serialize the
// in-flight handler state.
func TestStepRetiresWholeHandlerAtomically(t *testing.T) {
	ref := New([]byte{0x9f, 0x00}, nil) // FMULT; STOP
	ref.R[6] = 0x0200
	ref.R[0] = 0x0200
	GoAndRun(ref, 1)
	wantCycles := ref.Cycles()
	wantR0 := ref.R[0]
	if wantR0 == 0x0200 {
		t.Fatalf("FMULT did not execute in reference run")
	}

	d := New([]byte{0x9f, 0x00}, nil)
	d.R[6] = 0x0200
	d.R[0] = 0x0200
	d.Go()
	d.stepOne() // retire the cold NOP so the pipeline holds FMULT

	r0Before := d.R[0]
	d.Step(3)

	if d.R[0] == r0Before {
		t.Fatalf("intra-handler yield API now exists: rewrite this as the partial-slice contract")
	}
	if d.R[0] != wantR0 {
		t.Fatalf("Step retired FMULT with R0=%04X, whole-handler R0=%04X", d.R[0], wantR0)
	}
	if d.Cycles() != wantCycles {
		t.Fatalf("Step retired FMULT for %d cycles, whole-handler cost %d", d.Cycles(), wantCycles)
	}
	if d.stepDebt == 0 {
		t.Fatalf("expected handler overrun parked in stepDebt after sub-handler Step")
	}
}
