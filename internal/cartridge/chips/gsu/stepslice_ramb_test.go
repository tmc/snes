package gsu

import "testing"

func TestStepSliceRAMBPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceRAMBDevice()
	runWholeStepSliceRAMB(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceRAMBDevice()
	startRAMBStepSlicePendingRAMSync(t, d)

	first := d.StepSlice(1)
	if first.Cycles != 1 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 1 cycle, 0 retired, running partial", first)
	}
	if d.RAMBR != 0x00 {
		t.Fatalf("partial RAMB changed RAMBR=%02X before RAM sync completed, want 00", d.RAMBR)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial RAMB committed pending byte early: RAM[0010]=%02X, want 00", got)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial RAMB RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused RAMB StepSlice: %v", err)
	}
	restored := newStepSliceRAMBDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused RAMB StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused RAMB state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if restored.RAMBR != 0x01 {
		t.Fatalf("resumed RAMB RAMBR=%02X, want 01", restored.RAMBR)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed RAMB RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if restored.ramPending {
		t.Fatalf("resumed RAMB left pending RAM buffer: delay=%d", restored.ramDelay)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed RAMB final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceRAMBDevice() *Device {
	d := New([]byte{0x3d, 0x31, 0x3e, 0xdf, 0x00}, nil) // ALT1; STB (R1); ALT2; RAMB; STOP
	d.R[0] = 0x00a5
	d.R[1] = 0x0010
	return d
}

func runWholeStepSliceRAMB(d *Device) {
	GoAndRun(d, 4)
}

func startRAMBStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds ALT2 and RAM write is pending.
	d.stepOne() // ALT2; Pipeline now holds RAMB with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start RAMB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xdf ||
		d.stepSlice.Phase != stepSlicePhaseRAMBWaitSet ||
		d.stepSlice.RemainingCycles != 2 ||
		d.stepSlice.SrcReg != 0 ||
		d.RAMBR != 0x00 {
		t.Fatalf("RAMB StepSlice frame=%+v RAMBR=%02X, want active RAM-bank wait",
			d.stepSlice, d.RAMBR)
	}
}
