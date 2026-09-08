package gsu

import "testing"

func newPendingSBKDevice() *Device {
	d := newStepSliceSBKDevice()
	d.RAM = make([]byte, 128<<10)
	d.Go()
	d.stepOne() // Cold NOP fetches SBK.
	d.writeRAMBufferBank(1, 0x20, 0xa5)
	return d
}

func TestStepSliceSBKPendingRAMPartitions(t *testing.T) {
	whole := newPendingSBKDevice()
	start := whole.cycles
	if n := whole.Run(1); n != 1 {
		t.Fatalf("Run retired %d", n)
	}
	want := captureFutureStepSliceState(whole)
	if whole.cycles-start != 12 {
		t.Fatalf("whole SBK cost %d, want 12", whole.cycles-start)
	}
	for _, tc := range []struct {
		name  string
		parts []uint64
	}{
		{"coarse", []uint64{10}},
		{"single cycles", []uint64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}},
		{"wait edges", []uint64{3, 1, 5, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newPendingSBKDevice()
			dispatches := 0
			hook := func(_ uint8, _ uint16, op uint8) {
				if op != 0x90 {
					t.Errorf("dispatch %02x", op)
				}
				dispatches++
			}
			d.TraceHook = hook
			result := d.StepSlice(2)
			if result.Cycles != 2 || result.RetiredOpcodes != 0 || !result.Partial || d.stepSlice.Phase != stepSlicePhaseSBKWaitLow {
				t.Fatalf("dispatch = %+v frame %+v", result, d.stepSlice)
			}
			retired := 0
			for _, cycles := range tc.parts {
				result = d.StepSlice(cycles)
				if result.Cycles != cycles {
					t.Fatalf("StepSlice(%d) consumed %d", cycles, result.Cycles)
				}
				retired += result.RetiredOpcodes
				// Resume every partial boundary through serialization into a fresh device.
				if result.Partial {
					state, err := d.Serialize()
					if err != nil {
						t.Fatal(err)
					}
					restored := newPendingSBKDevice()
					if err := restored.Unserialize(state); err != nil {
						t.Fatal(err)
					}
					restored.TraceHook = hook
					d = restored
				}
			}
			if retired != 1 || dispatches != 1 || d.stepSlice.Active {
				t.Fatalf("retired=%d dispatched=%d frame=%+v", retired, dispatches, d.stepSlice)
			}
			if got := captureFutureStepSliceState(d); got != want {
				t.Fatalf("fragmented state = %+v, want %+v", got, want)
			}
			if d.RAM[0x10020] != 0xa5 || d.RAM[0x10] != 0x34 || d.RAM[0x11] != 0 {
				t.Fatalf("RAM effects old=%02x low=%02x high=%02x", d.RAM[0x10020], d.RAM[0x10], d.RAM[0x11])
			}
			// Replace committed bytes with sentinels: retiring/flushing the high byte
			// must not replay either the prior transaction or the low-byte transaction.
			d.RAM[0x10020] = 0x5a
			d.RAM[0x10] = 0x43
			d.advanceCycles(6)
			if d.RAM[0x10020] != 0x5a || d.RAM[0x10] != 0x43 || d.RAM[0x11] != 0x12 {
				t.Fatal("buffer completion replayed or omitted a RAM effect")
			}
		})
	}
}

func TestStepSliceSBKPendingRAMVisibility(t *testing.T) {
	d := newPendingSBKDevice()
	d.StepSlice(2)
	d.StepSlice(3)
	if d.RAM[0x10020] != 0 || d.RAM[0x10] != 0 || d.ramDelay != 1 {
		t.Fatal("pending transaction committed early")
	}
	d.StepSlice(1)
	if d.RAM[0x10020] != 0xa5 || d.RAM[0x10] != 0 || d.ramAddr != 0x10 || d.ramDelay != 6 {
		t.Fatal("prior write and low-byte staging boundary mismatch")
	}
	d.StepSlice(5)
	if d.RAM[0x10] != 0 || d.RAM[0x11] != 0 {
		t.Fatal("SBK low byte committed early")
	}
	result := d.StepSlice(1)
	if result.RetiredOpcodes != 1 || d.RAM[0x10] != 0x34 || d.RAM[0x11] != 0 || d.ramAddr != 0x11 || d.ramDelay != 6 {
		t.Fatal("SBK low-byte commit and high-byte staging boundary mismatch")
	}
}
