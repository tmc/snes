package gsu

import "testing"

func TestStepSliceSTBPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceSTBDevice()
	GoAndRun(whole, 4)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSTBDevice()
	startSTBStepSlicePendingRAMSync(t, d)

	first := d.StepSlice(1)
	if first.Cycles != 1 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 1 cycle, 0 retired, running partial", first)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial STB committed previous byte early: RAM[0010]=%02X, want 00", got)
	}
	if got := d.RAM[0x20]; got != 0x00 {
		t.Fatalf("partial STB committed new byte early: RAM[0020]=%02X, want 00", got)
	}
	if d.stepSlice.Phase != stepSlicePhaseSTBWaitWrite ||
		d.stepSlice.Address != 0x0020 ||
		d.stepSlice.OperandLow != 0xa5 ||
		d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial STB frame=%+v, want write wait at 0020 with 1 cycle", d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused STB StepSlice: %v", err)
	}
	restored := newStepSliceSTBDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused STB StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused STB state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed STB previous byte RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x20]; got != whole.RAM[0x20] {
		t.Fatalf("resumed STB new byte RAM[0020]=%02X, want %02X before pending write commits", got, whole.RAM[0x20])
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed STB final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceSTBDevice() *Device {
	d := New([]byte{0x3d, 0x31, 0x3d, 0x32, 0x00}, nil) // ALT1; STB (R1); ALT1; STB (R2); STOP
	d.R[0] = 0x00a5
	d.R[1] = 0x0010
	d.R[2] = 0x0020
	return d
}

func startSTBStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds first STB.
	d.stepOne() // first STB; Pipeline now holds ALT1 and RAM write is pending.
	d.stepOne() // ALT1; Pipeline now holds second STB with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start STB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x32 ||
		d.stepSlice.Phase != stepSlicePhaseSTBWaitWrite ||
		d.stepSlice.RemainingCycles != 2 ||
		d.stepSlice.Address != 0x0020 ||
		d.stepSlice.OperandLow != 0xa5 {
		t.Fatalf("STB StepSlice frame=%+v, want active pending-RAM write wait", d.stepSlice)
	}
}
