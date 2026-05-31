package gsu

import "testing"

func TestStepSliceLDWPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceLDWDevice()
	runWholeStepSliceLDW(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceLDWDevice()
	startLDWStepSlicePendingRAMSync(t, d)

	first := d.StepSlice(3)
	if first.Cycles != 3 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 3 cycles, 0 retired, running partial", first)
	}
	if d.R[0] != 0xcccc {
		t.Fatalf("partial LDW wrote R0=%04X before RAM sync completed, want CCCC", d.R[0])
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial LDW committed pending byte early: RAM[0010]=%02X, want 00", got)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial LDW RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused LDW StepSlice: %v", err)
	}
	restored := newStepSliceLDWDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused LDW StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused LDW state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.R[0]; got != 0xbea5 {
		t.Fatalf("resumed LDW R0=%04X, want BEA5", got)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed LDW RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed LDW RAM[0011]=%02X, want %02X", got, whole.RAM[0x11])
	}
	if restored.ramPending {
		t.Fatalf("resumed LDW left pending RAM buffer: delay=%d", restored.ramDelay)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed LDW final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceLDWDevice() *Device {
	d := New([]byte{0xb3, 0x3d, 0x31, 0x42, 0x00}, nil) // FROM R3; ALT1; STB (R1); LDW (R2); STOP
	d.R[0] = 0xcccc
	d.R[1] = 0x0010
	d.R[2] = 0x0010
	d.R[3] = 0x00a5
	d.RAM[0x11] = 0xbe
	return d
}

func runWholeStepSliceLDW(d *Device) {
	GoAndRun(d, 4)
}

func startLDWStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FROM R3.
	d.stepOne() // FROM R3; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds LDW with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start LDW StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x42 ||
		d.stepSlice.Phase != stepSlicePhaseLDWWaitRead ||
		d.stepSlice.RemainingCycles != 4 ||
		d.stepSlice.DstReg != 0 ||
		d.stepSlice.Bank != 0 ||
		d.stepSlice.Address != 0x0010 ||
		d.RAMAddr != 0x0010 ||
		d.R[0] != 0xcccc {
		t.Fatalf("LDW StepSlice frame=%+v RAMAddr=%04X R0=%04X, want active RAM-word wait",
			d.stepSlice, d.RAMAddr, d.R[0])
	}
}
