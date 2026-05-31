package gsu

import "testing"

func TestStepSliceROMBPendingROMSyncSerializes(t *testing.T) {
	whole := newStepSliceROMBDevice()
	runWholeStepSliceROMB(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceROMBDevice()
	startROMBStepSlicePendingROMSync(t, d)

	first := d.StepSlice(3)
	if first.Cycles != 3 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 3 cycles, 0 retired, running partial", first)
	}
	if d.ROMBR != 0x00 {
		t.Fatalf("partial ROMB changed ROMBR=%02X before ROM sync completed, want 00", d.ROMBR)
	}
	if !d.romPending || d.romDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial ROMB ROM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.romPending, d.romDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused ROMB StepSlice: %v", err)
	}
	restored := newStepSliceROMBDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused ROMB StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused ROMB state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if restored.ROMBR != 0x3f {
		t.Fatalf("resumed ROMB ROMBR=%02X, want 3F", restored.ROMBR)
	}
	if restored.romData != 0x7d {
		t.Fatalf("resumed ROMB romData=%02X, want 7D from old ROMBR", restored.romData)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed ROMB final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceROMBDevice() *Device {
	rom := make([]byte, 0x20)
	rom[0] = 0x3f // ALT3
	rom[1] = 0xdf // ROMB
	rom[2] = 0x00 // STOP
	rom[0x10] = 0x7d
	d := New(rom, nil)
	d.R[0] = 0x00bf
	return d
}

func runWholeStepSliceROMB(d *Device) {
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT3.
	d.stepOne() // ALT3; Pipeline now holds ROMB.
	d.setReg(14, 0x0010)
	d.Run(1)
}

func startROMBStepSlicePendingROMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT3.
	d.stepOne() // ALT3; Pipeline now holds ROMB.
	d.setReg(14, 0x0010)

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start ROMB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xdf ||
		d.stepSlice.Phase != stepSlicePhaseROMBWaitSet ||
		d.stepSlice.RemainingCycles != 4 ||
		d.stepSlice.SrcReg != 0 ||
		d.ROMBR != 0x00 {
		t.Fatalf("ROMB StepSlice frame=%+v ROMBR=%02X, want active ROM-bank wait",
			d.stepSlice, d.ROMBR)
	}
}
