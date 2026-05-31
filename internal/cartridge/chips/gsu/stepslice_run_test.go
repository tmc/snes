package gsu

import "testing"

func TestStepSliceRunResumesActiveFrame(t *testing.T) {
	tests := []struct {
		name   string
		new    func() *Device
		start  func(*testing.T, *Device)
		pauses []uint64
	}{
		{
			name:  "FMULT dispatch frame",
			new:   newStepSliceFMULTDevice,
			start: startFMULTStepSliceWait,
		},
		{
			name:   "FMULT paused wait",
			new:    newStepSliceFMULTDevice,
			start:  startFMULTStepSliceWait,
			pauses: []uint64{13},
		},
		{
			name:  "IWT dispatch frame",
			new:   newStepSliceIWTDevice,
			start: startIWTStepSliceOperandFetch,
		},
		{
			name:   "IWT paused second operand",
			new:    newStepSliceIWTDevice,
			start:  startIWTStepSliceOperandFetch,
			pauses: []uint64{2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := tt.new()
			GoAndRun(whole, 1)
			want := captureFutureStepSliceState(whole)

			d := tt.new()
			tt.start(t, d)
			for _, cycles := range tt.pauses {
				result := d.StepSlice(cycles)
				if result.RetiredOpcodes != 0 || !result.Partial {
					t.Fatalf("pause StepSlice(%d) = %+v, want zero retired partial", cycles, result)
				}
			}

			if n := d.Run(1); n != 1 {
				t.Fatalf("Run(1) from active StepSlice frame retired %d opcodes, want 1", n)
			}
			if d.stepSlice.Active {
				t.Fatalf("Run(1) left active StepSlice frame %+v", d.stepSlice)
			}
			if got := captureFutureStepSliceState(d); got != want {
				t.Fatalf("Run(1) resumed state = %+v, want whole-handler %+v", got, want)
			}
		})
	}
}
