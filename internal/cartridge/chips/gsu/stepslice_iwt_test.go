package gsu

import "testing"

func TestStepSliceIWTOperandFetchSerializes(t *testing.T) {
	whole := newStepSliceIWTDevice()
	GoAndRun(whole, 1)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceIWTDevice()
	startIWTStepSliceOperandFetch(t, d)

	first := d.StepSlice(2)
	if first.Cycles != 2 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 2 cycles, 0 retired, running partial", first)
	}
	if d.R[5] != 0 {
		t.Fatalf("partial IWT wrote R5=%04X before high byte fetch, want 0000", d.R[5])
	}
	if d.R[15] != 2 || d.Pipeline != 0x12 {
		t.Fatalf("partial IWT R15=%04X Pipeline=%02X, want R15=0002 Pipeline=12",
			d.R[15], d.Pipeline)
	}
	if d.stepSlice.Phase != stepSlicePhaseIWTFetchHigh || d.stepSlice.OperandLow != 0x34 {
		t.Fatalf("partial IWT frame=%+v, want high-fetch phase with low byte 34", d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused IWT StepSlice: %v", err)
	}
	restored := newStepSliceIWTDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused IWT StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused IWT state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(2)
	if second.Cycles != 2 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 2 cycles, 1 retired, running non-partial", second)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed IWT final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceIWTDevice() *Device {
	return New([]byte{0xf5, 0x34, 0x12, 0x00}, nil) // IWT R5,#$1234; STOP
}

func startIWTStepSliceOperandFetch(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds IWT.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start IWT StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xf5 ||
		d.stepSlice.Phase != stepSlicePhaseIWTFetchLow ||
		d.stepSlice.DstReg != 5 {
		t.Fatalf("IWT StepSlice frame=%+v, want active low-fetch frame for R5", d.stepSlice)
	}
}
