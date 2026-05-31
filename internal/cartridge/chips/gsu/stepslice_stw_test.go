package gsu

import "testing"

func TestStepSliceSTWStoreWaitSerializes(t *testing.T) {
	whole := newStepSliceSTWDevice()
	GoAndRun(whole, 1)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSTWDevice()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds STW.

	first := d.StepSlice(7)
	if first.Cycles != 7 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 7 cycles, 0 retired, running partial", first)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial STW committed low byte early: RAM[0010]=%02X, want 00", got)
	}
	if !d.ramPending || d.ramDelay != 1 || d.ramAddr != 0x0010 || d.ramData != 0x34 {
		t.Fatalf("partial STW RAM buffer pending=%v delay=%d addr=%04X data=%02X, want low byte pending with 1 cycle",
			d.ramPending, d.ramDelay, d.ramAddr, d.ramData)
	}
	if d.stepSlice.Phase != stepSlicePhaseSTWWaitHigh ||
		d.stepSlice.Address != 0x0011 ||
		d.stepSlice.OperandHigh != 0x12 ||
		d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial STW frame=%+v, want high-byte wait at 0011 with 1 cycle", d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused STW StepSlice: %v", err)
	}
	restored := newStepSliceSTWDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused STW StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused STW state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed STW low byte RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed STW high byte RAM[0011]=%02X, want %02X before pending write commits", got, whole.RAM[0x11])
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed STW final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceSTWDevice() *Device {
	d := New([]byte{0x31, 0x00}, nil) // STW (R1); STOP
	d.R[0] = 0x1234
	d.R[1] = 0x0010
	return d
}

func startSTWStepSliceStoreWait(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds STW.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start STW StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x31 ||
		d.stepSlice.Phase != stepSlicePhaseSTWWaitHigh ||
		d.stepSlice.RemainingCycles != 6 ||
		d.stepSlice.Address != 0x0011 ||
		d.stepSlice.OperandHigh != 0x12 {
		t.Fatalf("STW StepSlice frame=%+v, want active high-byte wait", d.stepSlice)
	}
}
