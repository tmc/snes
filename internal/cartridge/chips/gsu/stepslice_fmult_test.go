package gsu

import "testing"

func TestStepSliceFMULTFromOpcodeBoundary(t *testing.T) {
	whole := newStepSliceFMULTDevice()
	GoAndRun(whole, 1)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceFMULTDevice()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FMULT.

	first := d.StepSlice(15)
	if first.Cycles != 15 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 15 cycles, 0 retired, running partial", first)
	}
	if d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("remaining FMULT wait=%d, want 1", d.stepSlice.RemainingCycles)
	}
	if d.R[0] != whole.R[0] {
		t.Fatalf("FMULT result not visible during wait: R0=%04X, want %04X", d.R[0], whole.R[0])
	}
	if d.R[15] != 1 {
		t.Fatalf("partial FMULT R15=%04X, want 0001 before post-step increment", d.R[15])
	}

	second := d.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := captureFutureStepSliceState(d); got != want {
		t.Fatalf("sliced FMULT final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceFMULTPausedWaitSerializes(t *testing.T) {
	whole := newStepSliceFMULTDevice()
	GoAndRun(whole, 1)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceFMULTDevice()
	startFMULTStepSliceWait(t, d)
	first := d.StepSlice(13)
	if first.Cycles != 13 || first.RetiredOpcodes != 0 || !first.Partial {
		t.Fatalf("paused StepSlice = %+v, want 13 cycles, 0 retired, partial", first)
	}
	if d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("remaining FMULT wait=%d, want 1", d.stepSlice.RemainingCycles)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused FMULT StepSlice: %v", err)
	}
	restored := newStepSliceFMULTDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused FMULT StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused FMULT state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || second.Partial {
		t.Fatalf("resumed StepSlice = %+v, want 1 cycle, 1 retired, non-partial", second)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed FMULT final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceFMULTDevice() *Device {
	d := New([]byte{0x9f, 0x00}, nil) // FMULT; STOP
	d.R[0] = 0x0080
	d.R[6] = 0x0100
	return d
}

func startFMULTStepSliceWait(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FMULT.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start FMULT StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x9f ||
		d.stepSlice.Phase != stepSlicePhaseFMULTWait ||
		d.stepSlice.RemainingCycles != 14 {
		t.Fatalf("FMULT StepSlice frame=%+v, want active 14-cycle wait", d.stepSlice)
	}
}
