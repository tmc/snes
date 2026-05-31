package gsu

import "testing"

func TestStepSliceRunResumesActiveFrame(t *testing.T) {
	tests := []struct {
		name     string
		new      func() *Device
		start    func(*testing.T, *Device)
		pauses   []uint64
		whole    int
		wholeRun func(*Device)
	}{
		{
			name:  "FMULT dispatch frame",
			new:   newStepSliceFMULTDevice,
			start: startFMULTStepSliceWait,
			whole: 1,
		},
		{
			name:   "FMULT paused wait",
			new:    newStepSliceFMULTDevice,
			start:  startFMULTStepSliceWait,
			pauses: []uint64{13},
			whole:  1,
		},
		{
			name:  "IWT dispatch frame",
			new:   newStepSliceIWTDevice,
			start: startIWTStepSliceOperandFetch,
			whole: 1,
		},
		{
			name:   "IWT paused second operand",
			new:    newStepSliceIWTDevice,
			start:  startIWTStepSliceOperandFetch,
			pauses: []uint64{2},
			whole:  1,
		},
		{
			name:  "STW dispatch frame",
			new:   newStepSliceSTWDevice,
			start: startSTWStepSliceStoreWait,
			whole: 1,
		},
		{
			name:   "STW paused high-byte write",
			new:    newStepSliceSTWDevice,
			start:  startSTWStepSliceStoreWait,
			pauses: []uint64{5},
			whole:  1,
		},
		{
			name:  "STB dispatch frame",
			new:   newStepSliceSTBDevice,
			start: startSTBStepSlicePendingRAMSync,
			whole: 4,
		},
		{
			name:   "STB paused pending RAM sync",
			new:    newStepSliceSTBDevice,
			start:  startSTBStepSlicePendingRAMSync,
			pauses: []uint64{1},
			whole:  4,
		},
		{
			name:     "GETB dispatch frame",
			new:      newStepSliceGETBDevice,
			start:    startGETBStepSlicePendingROMSync,
			wholeRun: runWholeStepSliceGETB,
		},
		{
			name:     "GETB paused pending ROM sync",
			new:      newStepSliceGETBDevice,
			start:    startGETBStepSlicePendingROMSync,
			pauses:   []uint64{3},
			wholeRun: runWholeStepSliceGETB,
		},
		{
			name:     "GETC dispatch frame",
			new:      newStepSliceGETCDevice,
			start:    startGETCStepSlicePendingROMSync,
			wholeRun: runWholeStepSliceGETC,
		},
		{
			name:     "GETC paused pending ROM sync",
			new:      newStepSliceGETCDevice,
			start:    startGETCStepSlicePendingROMSync,
			pauses:   []uint64{3},
			wholeRun: runWholeStepSliceGETC,
		},
		{
			name:     "ROMB dispatch frame",
			new:      newStepSliceROMBDevice,
			start:    startROMBStepSlicePendingROMSync,
			wholeRun: runWholeStepSliceROMB,
		},
		{
			name:     "ROMB paused pending ROM sync",
			new:      newStepSliceROMBDevice,
			start:    startROMBStepSlicePendingROMSync,
			pauses:   []uint64{3},
			wholeRun: runWholeStepSliceROMB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whole := tt.new()
			if tt.wholeRun != nil {
				tt.wholeRun(whole)
			} else {
				GoAndRun(whole, tt.whole)
			}
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
