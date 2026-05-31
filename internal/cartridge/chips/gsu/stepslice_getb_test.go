package gsu

import "testing"

func TestStepSliceGETBPendingROMSyncSerializes(t *testing.T) {
	whole := newStepSliceGETBDevice()
	runWholeStepSliceGETB(whole)
	want := captureFutureStepSliceState(whole)

	d := newStepSliceGETBDevice()
	startGETBStepSlicePendingROMSync(t, d)

	first := d.StepSlice(3)
	if first.Cycles != 3 || first.RetiredOpcodes != 0 || !first.Running || !first.Partial {
		t.Fatalf("first StepSlice = %+v, want 3 cycles, 0 retired, running partial", first)
	}
	if d.R[0] != 0xcccc {
		t.Fatalf("partial GETB wrote R0=%04X before ROM sync completed, want CCCC", d.R[0])
	}
	if !d.romPending || d.romDelay != 1 || d.stepSlice.RemainingCycles != 1 {
		t.Fatalf("partial GETB ROM pending=%v delay=%d frame=%+v, want 1 cycle remaining",
			d.romPending, d.romDelay, d.stepSlice)
	}

	paused := captureFutureStepSliceState(d)
	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize paused GETB StepSlice: %v", err)
	}
	restored := newStepSliceGETBDevice()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize paused GETB StepSlice: %v", err)
	}
	if got := captureFutureStepSliceState(restored); got != paused {
		t.Fatalf("paused GETB state changed across Serialize/Unserialize: got %+v, want %+v",
			got, paused)
	}

	second := restored.StepSlice(1)
	if second.Cycles != 1 || second.RetiredOpcodes != 1 || !second.Running || second.Partial {
		t.Fatalf("second StepSlice = %+v, want 1 cycle, 1 retired, running non-partial", second)
	}
	if got := restored.R[0]; got != 0x007b {
		t.Fatalf("resumed GETB R0=%04X, want 007B", got)
	}
	if got := captureFutureStepSliceState(restored); got != want {
		t.Fatalf("resumed GETB final state = %+v, want whole-handler %+v", got, want)
	}
}

func TestStepSliceGETBVariantsMatchWhole(t *testing.T) {
	tests := []struct {
		name   string
		prefix uint8
		want   uint16
	}{
		{name: "GETB", want: 0x0080},
		{name: "GETBH", prefix: 0x3d, want: 0x80cc},
		{name: "GETBL", prefix: 0x3e, want: 0x1280},
		{name: "GETBS", prefix: 0x3f, want: 0xff80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := newStepSliceGETBVariantDevice(tt.prefix)
			runWholeStepSliceGETBVariant(whole, tt.prefix)
			wantState := captureFutureStepSliceState(whole)

			d := newStepSliceGETBVariantDevice(tt.prefix)
			startGETBStepSliceVariant(t, d, tt.prefix)
			result := d.StepSlice(4)
			if result.Cycles != 4 || result.RetiredOpcodes != 1 || !result.Running || result.Partial {
				t.Fatalf("StepSlice = %+v, want 4 cycles, 1 retired, running non-partial", result)
			}
			if got := d.R[0]; got != tt.want {
				t.Fatalf("R0=%04X, want %04X", got, tt.want)
			}
			if got := captureFutureStepSliceState(d); got != wantState {
				t.Fatalf("sliced %s final state = %+v, want whole-handler %+v",
					tt.name, got, wantState)
			}
		})
	}
}

func newStepSliceGETBDevice() *Device {
	rom := make([]byte, 0x20)
	rom[0] = 0xef // GETB
	rom[1] = 0x00 // STOP
	rom[0x10] = 0x7b
	d := New(rom, nil)
	d.R[0] = 0xcccc
	return d
}

func runWholeStepSliceGETB(d *Device) {
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds GETB.
	d.setReg(14, 0x0010)
	d.Run(1)
}

func newStepSliceGETBVariantDevice(prefix uint8) *Device {
	rom := make([]byte, 0x20)
	if prefix != 0 {
		rom[0] = prefix
		rom[1] = 0xef // GETB-family opcode.
		rom[2] = 0x00 // STOP
	} else {
		rom[0] = 0xef // GETB
		rom[1] = 0x00 // STOP
	}
	rom[0x10] = 0x80
	d := New(rom, nil)
	d.R[0] = 0x12cc
	return d
}

func runWholeStepSliceGETBVariant(d *Device, prefix uint8) {
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds prefix or GETB.
	if prefix != 0 {
		d.stepOne() // ALT prefix; Pipeline now holds GETB.
	}
	d.setReg(14, 0x0010)
	d.Run(1)
}

func startGETBStepSlicePendingROMSync(t *testing.T, d *Device) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds GETB.
	d.setReg(14, 0x0010)

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start GETB StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xef ||
		d.stepSlice.Phase != stepSlicePhaseGETBWaitRead ||
		d.stepSlice.RemainingCycles != 4 ||
		d.stepSlice.DstReg != 0 ||
		d.R[0] != 0xcccc {
		t.Fatalf("GETB StepSlice frame=%+v R0=%04X, want active ROM-read wait",
			d.stepSlice, d.R[0])
	}
}

func startGETBStepSliceVariant(t *testing.T, d *Device, prefix uint8) {
	t.Helper()
	d.Go()
	d.stepOne() // cold NOP; Pipeline now holds prefix or GETB.
	if prefix != 0 {
		d.stepOne() // ALT prefix; Pipeline now holds GETB.
	}
	d.setReg(14, 0x0010)

	result := d.StepSlice(d.nextOpcodeFetchCycles())
	if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial {
		t.Fatalf("start GETB-family StepSlice = %+v, want dispatch-only partial", result)
	}
	if !d.stepSlice.Active || d.stepSlice.Op != 0xef ||
		d.stepSlice.Phase != stepSlicePhaseGETBWaitRead ||
		d.stepSlice.RemainingCycles != 4 {
		t.Fatalf("GETB-family StepSlice frame=%+v, want active ROM-read wait",
			d.stepSlice)
	}
}
