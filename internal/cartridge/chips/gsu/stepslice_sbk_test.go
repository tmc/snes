package gsu

import "testing"

func TestStepSliceSBKStoreWaitSerializes(t *testing.T) {
	whole := newStepSliceSBKDevice()
	GoAndRun(whole, 1)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSBKDevice()
	startSBKStepSliceStoreWait(t, d)

	first := d.StepSlice(5)
	if first.Cycles != 5 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 5 cycles, 0 retired, running partial", first)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial SBK committed low byte early: RAM[0010]=%02X, want 00", got)
	}
	if !d.ramPending || d.ramDelay != 1 || d.ramAddr != 0x0010 || d.ramData != 0x34 {
		t.Fatalf("partial SBK RAM buffer pending=%v delay=%d addr=%04X data=%02X, want low byte pending with 1 cycle",
			d.ramPending, d.ramDelay, d.ramAddr, d.ramData)
	}
	if d.stepSlice.Phase != stepSlicePhaseSBKWaitHigh ||
		d.stepSlice.Address != 0x0011 ||
		d.stepSlice.OperandHigh != 0x12 ||
		d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial SBK frame=%+v, want high-byte wait at 0011 with 1 cycle", d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused SBK StepSlice: %v", err)
	}
	restored := newStepSliceSBKDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused SBK StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused SBK state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed SBK low byte RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed SBK high byte RAM[0011]=%02X, want %02X before pending write commits", got, whole.RAM[0x11])
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed SBK final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceSBKDevice() *Device {
	d := New([]byte{0x90, 0x00}, nil) // SBK; STOP
	d.R[0] = 0x1234
	d.RAMAddr = 0x0010
	return d
}

func startSBKStepSliceStoreWait(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds SBK.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start SBK StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x90 ||
		d.stepSlice.Phase != stepSlicePhaseSBKWaitHigh ||
		d.stepSlice.RemainingCycles != 6 ||
		d.stepSlice.Address != 0x0011 ||
		d.stepSlice.OperandHigh != 0x12 {
		t.Fatalf("SBK StepSlice frame=%+v, want active high-byte wait", d.stepSlice)
	}
}
