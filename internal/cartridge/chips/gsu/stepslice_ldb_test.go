package gsu

import "testing"

func TestStepSliceLDBPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceLDBDevice()
	runWholeStepSliceLDB(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceLDBDevice()
	startLDBStepSlicePendingRAMSync(t, d)

	first := d.StepSlice(1)
	if first.Cycles != 1 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 1 cycle, 0 retired, running partial", first)
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
	if got := restored.R[0]; got != 0x00a5 {
		t.Fatalf("resumed LDB R0=%04X, want 00A5", got)
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

func TestStepSliceLDBVariantsMatchWhole(t *testing.T) {
	tests := []struct {
		name   string
		prefix uint8
	}{
		{name: "LDB", prefix: 0x3d},
		{name: "ALT3 LDB", prefix: 0x3f},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := newStepSliceLDBVariantDevice(tt.prefix)
			runWholeStepSliceLDB(whole)
			want := captureFutureStepSliceState(whole)

			d := newStepSliceLDBVariantDevice(tt.prefix)
			startLDBStepSlicePendingRAMSync(t, d)
			result := d.StepSlice(2)
			if result.Cycles != 2 || result.RetiredOpcodes != 1 || !result.Running || result.Partial {
				t.Fatalf("StepSlice = %+v, want 2 cycles, 1 retired, running non-partial", result)
			}
			if got := d.R[0]; got != 0x00a5 {
				t.Fatalf("R0=%04X, want 00A5", got)
			}
			if got := captureFutureStepSliceState(d); got != want {
				t.Fatalf("sliced %s final state = %+v, want whole-handler %+v",
					tt.name, got, want)
			}
		})
	}
}

func newStepSliceLDBDevice() *Device {
	return newStepSliceLDBVariantDevice(0x3d)
}

func newStepSliceLDBVariantDevice(prefix uint8) *Device {
	d := New([]byte{0xb3, 0x3d, 0x31, prefix, 0x42, 0x00}, nil) // FROM R3; ALT1; STB (R1); ALT; LDB (R2); STOP
	d.R[0] = 0xcccc
	d.R[1] = 0x0010
	d.R[2] = 0x0010
	d.R[3] = 0x00a5
	return d
}

func runWholeStepSliceLDB(d *Device) {
	GoAndRun(d, 5)
}

func startLDBStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FROM R3.
	d.stepOne() // FROM R3; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds ALT1/ALT3 and RAM write is pending.
	d.stepOne() // ALT1/ALT3; Pipeline now holds LDB with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start LDB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0x42 ||
		d.stepSlice.Phase != stepSlicePhaseLDBWaitRead ||
		d.stepSlice.RemainingCycles != 2 ||
		d.stepSlice.DstReg != 0 ||
		d.stepSlice.Bank != 0 ||
		d.stepSlice.Address != 0x0010 ||
		d.RAMAddr != 0x0010 ||
		d.R[0] != 0xcccc {
		t.Fatalf("LDB StepSlice frame=%+v RAMAddr=%04X R0=%04X, want active RAM-read wait",
			d.stepSlice, d.RAMAddr, d.R[0])
	}
}
