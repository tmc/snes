package gsu

import "testing"

func TestStepSliceLMPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceLMDevice(0x3d)
	runWholeStepSliceLM(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceLMDevice(0x3d)
	startLMStepSliceOperandFetch(t, d)

	low := d.StepSlice(1)
	if low.Cycles != 1 || low.RetiredOpcodes != 0 || !low.Running || !low.Partial {
		t.Fatalf("low StepSlice = %+v, want 1 cycle, 0 retired, running partial", low)
	}
	if d.stepSlice.Phase != stepSlicePhaseIWTFetchHigh || d.stepSlice.OperandLow != 0x10 {
		t.Fatalf("low LM frame=%+v, want high-fetch phase with low byte 10", d.stepSlice)
	}
	if d.R[5] != 0xcccc {
		t.Fatalf("partial LM wrote R5=%04X before RAM sync completed, want CCCC", d.R[5])
	}

	high := d.StepSlice(1)
	if high.Cycles != 1 || high.RetiredOpcodes != 0 || !high.Running || !high.Partial {
		t.Fatalf("high StepSlice = %+v, want 1 cycle, 0 retired, running partial", high)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial LM RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}
	if d.stepSlice.Phase != stepSlicePhaseLMWaitRead ||
		d.stepSlice.Address != 0x0010 ||
		d.stepSlice.OperandHigh != 0x00 ||
		d.RAMAddr != 0x0010 ||
		d.R[5] != 0xcccc {
		t.Fatalf("high LM frame=%+v RAMAddr=%04X R5=%04X, want active RAM-read wait",
			d.stepSlice, d.RAMAddr, d.R[5])
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial LM committed pending byte early: RAM[0010]=%02X, want 00", got)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused LM StepSlice: %v", err)
	}
	restored := newStepSliceLMDevice(0x3d)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused LM StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused LM state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	final := restored.StepSlice(1)
	if final.Cycles != 1 || final.RetiredOpcodes != 1 || !final.Running || final.Partial {
		t.Fatalf("final StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", final)
	}
	if got := restored.R[5]; got != 0xbea5 {
		t.Fatalf("resumed LM R5=%04X, want BEA5", got)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed LM RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed LM RAM[0011]=%02X, want %02X", got, whole.RAM[0x11])
	}
	if restored.ramPending {
		t.Fatalf("resumed LM left pending RAM buffer: delay=%d", restored.ramDelay)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed LM final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceLMVariantsMatchWhole(t *testing.T) {
	tests := []struct {
		name   string
		prefix uint8
	}{
		{name: "LM", prefix: 0x3d},
		{name: "ALT3 LM", prefix: 0x3f},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := newStepSliceLMDevice(tt.prefix)
			runWholeStepSliceLM(whole)
			want := captureFutureStepSliceState(whole)

			d := newStepSliceLMDevice(tt.prefix)
			startLMStepSliceOperandFetch(t, d)
			result := d.StepSlice(3)
			if result.Cycles != 3 || result.RetiredOpcodes != 1 || !result.Running || result.Partial {
				t.Fatalf("StepSlice = %+v, want 3 cycles, 1 retired, running non-partial", result)
			}
			if got := d.R[5]; got != 0xbea5 {
				t.Fatalf("R5=%04X, want BEA5", got)
			}
			if got := captureFutureStepSliceState(d); got != want {
				t.Fatalf("sliced %s final state = %+v, want whole-handler %+v",
					tt.name, got, want)
			}
		})
	}
}

func newStepSliceLMDevice(prefix uint8) *Device {
	ram := make([]byte, 64*1024)
	ram[0x11] = 0xbe
	d := New([]byte{0xb3, 0x3d, 0x31, prefix, 0xf5, 0x10, 0x00, 0x00}, ram) // FROM R3; ALT1; STB (R1); ALT; LM R5,($0010); STOP
	d.CLSR = 1
	d.R[1] = 0x0010
	d.R[3] = 0x00a5
	d.R[5] = 0xcccc
	return d
}

func runWholeStepSliceLM(d *Device) {
	GoAndRun(d, 5)
}

func startLMStepSliceOperandFetch(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FROM R3.
	d.stepOne() // FROM R3; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds ALT1/ALT3 and RAM write is pending.
	d.stepOne() // ALT1/ALT3; Pipeline now holds LM with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 1 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start LM StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xf5 ||
		d.stepSlice.Phase != stepSlicePhaseIWTFetchLow ||
		d.stepSlice.Mode != d.alt() ||
		d.stepSlice.DstReg != 5 ||
		d.stepSlice.Bank != 0 ||
		d.R[5] != 0xcccc {
		t.Fatalf("LM StepSlice frame=%+v R5=%04X, want active low-fetch frame for R5",
			d.stepSlice, d.R[5])
	}
	if !d.ramPending || d.ramDelay != 3 {
		t.Fatalf("start LM RAM pending=%v delay=%d, want 3 cycles after dispatch",
			d.ramPending, d.ramDelay)
	}
}
