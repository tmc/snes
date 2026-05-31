package gsu

import "testing"

func TestStepSliceLDBPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceLDBDevice()
	runWholeStepSliceLDB(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceLDBDevice()
	startLDBStepSlicePendingRAMSync(t, d)

	first := d.StepSlice(3)
	if first.Cycles != 3 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 3 cycles, 0 retired, running partial", first)
	}
	if d.R[0] != 0xcccc {
		t.Fatalf("partial LDB wrote R0=%04X before RAM sync completed, want CCCC", d.R[0])
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial LDB committed pending byte early: RAM[0010]=%02X, want 00", got)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial LDB RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused LDB StepSlice: %v", err)
	}
	restored := newStepSliceLDBDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused LDB StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused LDB state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.R[0]; got != 0x007b {
		t.Fatalf("resumed LDB R0=%04X, want 007B", got)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed LDB RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if restored.ramPending {
		t.Fatalf("resumed LDB left pending RAM buffer: delay=%d", restored.ramDelay)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed LDB final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceLDBDevice() *Device {
	d := New([]byte{0x3d, 0x41, 0x00}, nil) // ALT1; LDB (R1); STOP
	d.R[0] = 0xcccc
	d.R[1] = 0x0010
	return d
}

func runWholeStepSliceLDB(d *Device) {
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds LDB.
	d.writeRAMBuffer(0x0010, 0x7b)
	d.Run(1)
}

func startLDBStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds LDB.
	d.writeRAMBuffer(0x0010, 0x7b)

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start LDB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x41 ||
		d.stepSlice.Phase != stepSlicePhaseLDBWaitRead ||
		d.stepSlice.RemainingCycles != 4 ||
		d.stepSlice.Address != 0x0010 ||
		d.RAMAddr != 0x0010 ||
		d.R[0] != 0xcccc {
		t.Fatalf("LDB StepSlice frame=%+v RAMAddr=%04X R0=%04X, want active RAM-read wait",
			d.stepSlice, d.RAMAddr, d.R[0])
	}
}
