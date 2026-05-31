package gsu

import "testing"

func TestStepSliceLMSPendingRAMSyncSerializes(t *testing.T) {
	whole := newStepSliceLMSDevice(0x3d)
	runWholeStepSliceLMS(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceLMSDevice(0x3d)
	startLMSStepSliceOperandFetch(t, d)

	operand := d.StepSlice(1)
	if operand.Cycles != 1 || operand.RetiredOpcodes != 0 || !operand.Running || !operand.Partial {
		t.Fatalf("operand StepSlice = %+v, want 1 cycle, 0 retired, running partial", operand)
	}
	if d.stepSlice.Phase != stepSlicePhaseLMSWaitRead ||
		d.stepSlice.OperandLow != 0x08 ||
		d.stepSlice.Address != 0x0010 ||
		d.stepSlice.RemainingCycles != 2 ||
		d.RAMAddr != 0x0010 ||
		d.R[5] != 0xcccc {
		t.Fatalf("operand LMS frame=%+v RAMAddr=%04X R5=%04X, want active RAM-read wait",
			d.stepSlice, d.RAMAddr, d.R[5])
	}

	wait := d.StepSlice(1)
	if wait.Cycles != 1 || wait.RetiredOpcodes != 0 || !wait.Running || !wait.Partial {
		t.Fatalf("wait StepSlice = %+v, want 1 cycle, 0 retired, running partial", wait)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial LMS RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}
	if d.R[5] != 0xcccc {
		t.Fatalf("partial LMS wrote R5=%04X before RAM sync completed, want CCCC", d.R[5])
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial LMS committed pending byte early: RAM[0010]=%02X, want 00", got)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused LMS StepSlice: %v", err)
	}
	restored := newStepSliceLMSDevice(0x3d)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused LMS StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused LMS state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	final := restored.StepSlice(1)
	if final.Cycles != 1 || final.RetiredOpcodes != 1 || !final.Running || final.Partial {
		t.Fatalf("final StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", final)
	}
	if got := restored.R[5]; got != 0xbea5 {
		t.Fatalf("resumed LMS R5=%04X, want BEA5", got)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed LMS RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed LMS RAM[0011]=%02X, want %02X", got, whole.RAM[0x11])
	}
	if restored.ramPending {
		t.Fatalf("resumed LMS left pending RAM buffer: delay=%d", restored.ramDelay)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed LMS final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceLMSVariantsMatchWhole(t *testing.T) {
	tests := []struct {
		name   string
		prefix uint8
	}{
		{name: "LMS", prefix: 0x3d},
		{name: "ALT3 LMS", prefix: 0x3f},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := newStepSliceLMSDevice(tt.prefix)
			runWholeStepSliceLMS(whole)
			want := captureFutureStepSliceState(whole)

			d := newStepSliceLMSDevice(tt.prefix)
			startLMSStepSliceOperandFetch(t, d)
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

func newStepSliceLMSDevice(prefix uint8) *Device {
	ram := make([]byte, 64*1024)
	ram[0x11] = 0xbe
	d := New([]byte{0xb3, 0x3d, 0x31, prefix, 0xa5, 0x08, 0x00, 0x00}, ram) // FROM R3; ALT1; STB (R1); ALT; LMS R5,($08); STOP
	d.CLSR = 1
	d.R[1] = 0x0010
	d.R[3] = 0x00a5
	d.R[5] = 0xcccc
	return d
}

func runWholeStepSliceLMS(d *Device) {
	GoAndRun(d, 5)
}

func startLMSStepSliceOperandFetch(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FROM R3.
	d.stepOne() // FROM R3; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds ALT1/ALT3 and RAM write is pending.
	d.stepOne() // ALT1/ALT3; Pipeline now holds LMS with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 1 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start LMS StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xa5 ||
		d.stepSlice.Phase != stepSlicePhaseLMSFetchAddr ||
		d.stepSlice.Mode != d.alt() ||
		d.stepSlice.DstReg != 5 ||
		d.stepSlice.Bank != 0 ||
		d.R[5] != 0xcccc {
		t.Fatalf("LMS StepSlice frame=%+v R5=%04X, want active operand-fetch frame for R5",
			d.stepSlice, d.R[5])
	}
	if !d.ramPending || d.ramDelay != 3 {
		t.Fatalf("start LMS RAM pending=%v delay=%d, want 3 cycles after dispatch",
			d.ramPending, d.ramDelay)
	}
}
