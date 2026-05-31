package gsu

import "testing"

func TestStepSliceGETCPendingROMSyncSerializes(t *testing.T) {
	whole := newStepSliceGETCDevice()
	runWholeStepSliceGETC(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceGETCDevice()
	startGETCStepSlicePendingROMSync(t, d)

	first := d.StepSlice(3)
	if first.Cycles != 3 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 3 cycles, 0 retired, running partial", first)
	}
	if d.COLR != 0xc0 {
		t.Fatalf("partial GETC wrote COLR=%02X before ROM sync completed, want C0", d.COLR)
	}
	if !d.romPending || d.romDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial GETC ROM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.romPending, d.romDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused GETC StepSlice: %v", err)
	}
	restored := newStepSliceGETCDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused GETC StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused GETC state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.COLR; got != 0xca {
		t.Fatalf("resumed GETC COLR=%02X, want CA", got)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed GETC final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceGETCDevice() *Device {
	rom := make([]byte, 0x20)
	rom[0] = 0xdf // GETC
	rom[1] = 0x00 // STOP
	rom[0x10] = 0xab
	d := New(rom, nil)
	d.COLR = 0xc0
	d.POR = porHighNibble
	return d
}

func runWholeStepSliceGETC(d *Device) {
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds GETC.
	d.setReg(14, 0x0010)
	d.Run(1)
}

func startGETCStepSlicePendingROMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds GETC.
	d.setReg(14, 0x0010)

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start GETC StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xdf ||
		d.stepSlice.Phase != stepSlicePhaseGETCWaitRead ||
		d.stepSlice.RemainingCycles != 4 ||
		d.COLR != 0xc0 {
		t.Fatalf("GETC StepSlice frame=%+v COLR=%02X, want active ROM-read wait",
			d.stepSlice, d.COLR)
	}
}
