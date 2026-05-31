package gsu

import "testing"

func TestStepSliceSMSStoreWaitSerializes(t *testing.T) {
	whole := newStepSliceSMSDevice()
	runWholeStepSliceSMS(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSMSDevice()
	startSMSStepSliceStoreWait(t, d)

	operand := d.StepSlice(1)
	if operand.Cycles != 1 || operand.RetiredOpcodes != 0 || !operand.Running || !operand.Partial {
		t.Fatalf("operand StepSlice = %+v, want 1 cycle, 0 retired, running partial", operand)
	}
	if d.stepSlice.Phase != stepSlicePhaseSMSWaitHigh ||
		d.stepSlice.OperandLow != 0x34 ||
		d.stepSlice.OperandHigh != 0x12 ||
		d.stepSlice.Address != 0x0011 ||
		d.stepSlice.RemainingCycles != 5 ||
		d.RAMAddr != 0x0010 {
		t.Fatalf("operand SMS frame=%+v RAMAddr=%04X, want high-byte wait at 0011",
			d.stepSlice, d.RAMAddr)
	}
	if !d.ramPending || d.ramDelay != 5 || d.ramAddr != 0x0010 || d.ramData != 0x34 {
		t.Fatalf("operand SMS RAM buffer pending=%v delay=%d addr=%04X data=%02X, want low byte pending",
			d.ramPending, d.ramDelay, d.ramAddr, d.ramData)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("operand SMS committed low byte early: RAM[0010]=%02X, want 00", got)
	}

	wait := d.StepSlice(4)
	if wait.Cycles != 4 || wait.RetiredOpcodes != 0 || !wait.Running || !wait.Partial {
		t.Fatalf("wait StepSlice = %+v, want 4 cycles, 0 retired, running partial", wait)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial SMS RAM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial SMS committed low byte early: RAM[0010]=%02X, want 00", got)
	}
	if got := d.RAM[0x11]; got != 0x00 {
		t.Fatalf("partial SMS committed high byte early: RAM[0011]=%02X, want 00", got)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused SMS StepSlice: %v", err)
	}
	restored := newStepSliceSMSDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused SMS StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused SMS state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	final := restored.StepSlice(1)
	if final.Cycles != 1 || final.RetiredOpcodes != 1 || !final.Running || final.Partial {
		t.Fatalf("final StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", final)
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed SMS low byte RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed SMS high byte RAM[0011]=%02X, want %02X before pending write commits",
			got, whole.RAM[0x11])
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed SMS final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceSMSMatchesWhole(t *testing.T) {
	whole := newStepSliceSMSDevice()
	runWholeStepSliceSMS(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSMSDevice()
	startSMSStepSliceStoreWait(t, d)
	result := d.StepSlice(6)
	if result.Cycles != 6 || result.RetiredOpcodes != 1 || !result.Running || result.Partial {
		t.Fatalf("StepSlice = %+v, want 6 cycles, 1 retired, running non-partial", result)
	}
	if got := captureFutureStepSliceState(d); got != want {
		t.Fatalf("sliced SMS final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceSMSPendingRAMSyncBeforeStoreSerializes(t *testing.T) {
	whole := newStepSliceSMSPendingRAMDevice()
	runWholeStepSliceSMSPendingRAM(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceSMSPendingRAMDevice()
	startSMSStepSlicePendingRAMSync(t, d)

	operand := d.StepSlice(1)
	if operand.Cycles != 1 || operand.RetiredOpcodes != 0 || !operand.Running || !operand.Partial {
		t.Fatalf("operand StepSlice = %+v, want 1 cycle, 0 retired, running partial", operand)
	}
	if d.stepSlice.Phase != stepSlicePhaseSMSWaitLow ||
		d.stepSlice.OperandLow != 0x34 ||
		d.stepSlice.OperandHigh != 0x12 ||
		d.stepSlice.Address != 0x0010 ||
		d.stepSlice.RemainingCycles != 2 ||
		d.RAMAddr != 0x0010 {
		t.Fatalf("operand SMS frame=%+v RAMAddr=%04X, want low-byte wait at 0010",
			d.stepSlice, d.RAMAddr)
	}
	if !d.ramPending || d.ramDelay != 2 || d.ramAddr != 0x0020 || d.ramData != 0xa5 {
		t.Fatalf("operand SMS RAM buffer pending=%v delay=%d addr=%04X data=%02X, want old byte pending",
			d.ramPending, d.ramDelay, d.ramAddr, d.ramData)
	}
	if got := d.RAM[0x20]; got != 0x00 {
		t.Fatalf("operand SMS committed old byte early: RAM[0020]=%02X, want 00", got)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("operand SMS staged new low byte early: RAM[0010]=%02X, want 00", got)
	}

	wait := d.StepSlice(1)
	if wait.Cycles != 1 || wait.RetiredOpcodes != 0 || !wait.Running || !wait.Partial {
		t.Fatalf("wait StepSlice = %+v, want 1 cycle, 0 retired, running partial", wait)
	}
	if !d.ramPending || d.ramDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial SMS RAM pending=%v delay=%d frame=%+v, want 1 old-buffer cycle remaining",
			d.ramPending, d.ramDelay, d.stepSlice)
	}
	if got := d.RAM[0x20]; got != 0x00 {
		t.Fatalf("partial SMS committed old byte early: RAM[0020]=%02X, want 00", got)
	}
	if got := d.RAM[0x10]; got != 0x00 {
		t.Fatalf("partial SMS staged new low byte early: RAM[0010]=%02X, want 00", got)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused SMS pending-RAM StepSlice: %v", err)
	}
	restored := newStepSliceSMSPendingRAMDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused SMS pending-RAM StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused SMS pending-RAM state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	final := restored.StepSlice(6)
	if final.Cycles != 6 || final.RetiredOpcodes != 1 || !final.Running || final.Partial {
		t.Fatalf("final StepSlice = %+v, want 6 cycles, 1 retired, running non-partial", final)
	}
	if got := restored.RAM[0x20]; got != whole.RAM[0x20] {
		t.Fatalf("resumed SMS old byte RAM[0020]=%02X, want %02X", got, whole.RAM[0x20])
	}
	if got := restored.RAM[0x10]; got != whole.RAM[0x10] {
		t.Fatalf("resumed SMS low byte RAM[0010]=%02X, want %02X", got, whole.RAM[0x10])
	}
	if got := restored.RAM[0x11]; got != whole.RAM[0x11] {
		t.Fatalf("resumed SMS high byte RAM[0011]=%02X, want %02X before pending write commits",
			got, whole.RAM[0x11])
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed SMS pending-RAM final state = %+v, want whole-handler %+v", got, want)
	}
}

func newStepSliceSMSDevice() *Device {
	d := New([]byte{0x3e, 0xa5, 0x08, 0x00}, nil) // ALT2; SMS R5,($08); STOP
	d.CLSR = 1
	d.R[5] = 0x1234
	return d
}

func runWholeStepSliceSMS(d *Device) {
	GoAndRun(d, 2)
}

func newStepSliceSMSPendingRAMDevice() *Device {
	d := New([]byte{0xb3, 0x3d, 0x31, 0x3e, 0xa5, 0x08, 0x00, 0x00}, nil) // FROM R3; ALT1; STB (R1); ALT2; SMS R5,($08); STOP
	d.CLSR = 1
	d.R[1] = 0x0020
	d.R[3] = 0x00a5
	d.R[5] = 0x1234
	return d
}

func runWholeStepSliceSMSPendingRAM(d *Device) {
	GoAndRun(d, 5)
}

func startSMSStepSliceStoreWait(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds ALT2.
	d.stepOne() // ALT2; Pipeline now holds SMS.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 1 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start SMS StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xa5 ||
		d.stepSlice.Phase != stepSlicePhaseSMSFetchAddr ||
		d.stepSlice.Mode != Alt2 ||
		d.stepSlice.SrcReg != 5 ||
		d.stepSlice.Bank != 0 ||
		d.RAMAddr != 0 ||
		d.ramPending {
		t.Fatalf("SMS StepSlice frame=%+v RAMAddr=%04X ramPending=%v, want active operand-fetch frame for R5",
			d.stepSlice, d.RAMAddr, d.ramPending)
	}
}

func startSMSStepSlicePendingRAMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds FROM R3.
	d.stepOne() // FROM R3; Pipeline now holds ALT1.
	d.stepOne() // ALT1; Pipeline now holds STB.
	d.stepOne() // STB; Pipeline now holds ALT2 and RAM write is pending.
	d.stepOne() // ALT2; Pipeline now holds SMS with pending RAM delay.

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 1 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start SMS pending-RAM StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xa5 ||
		d.stepSlice.Phase != stepSlicePhaseSMSFetchAddr ||
		d.stepSlice.Mode != Alt2 ||
		d.stepSlice.SrcReg != 5 ||
		d.stepSlice.Bank != 0 ||
		d.RAMAddr != 0x0020 ||
		!d.ramPending ||
		d.ramDelay != 3 {
		t.Fatalf("SMS pending-RAM frame=%+v RAMAddr=%04X pending=%v delay=%d, want operand frame with old write pending",
			d.stepSlice, d.RAMAddr, d.ramPending, d.ramDelay)
	}
}
