package gsu

import "testing"

func TestStepSliceFrameSerializes(t *testing.T) {
	tests := []struct {
		name  string
		frame stepSliceFrame
	}{
		{
			name: "zero",
		},
		{
			name:  "active",
			frame: testStepSliceFrame(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(nil, nil)
			d.stepSlice = tt.frame

			state, err := d.Serialize()
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}
			restored := New(nil, nil)
			if err := restored.Unserialize(state); err != nil {
				t.Fatalf("Unserialize: %v", err)
			}
			if restored.stepSlice != tt.frame {
				t.Fatalf("stepSlice=%+v, want %+v", restored.stepSlice, tt.frame)
			}
		})
	}
}

func TestStepSliceFrameClearsOnResetAndStop(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		d := New(nil, nil)
		d.stepSlice = testStepSliceFrame()
		d.Reset()
		if d.stepSlice != (stepSliceFrame{}) {
			t.Fatalf("Reset left stepSlice=%+v", d.stepSlice)
		}
	})

	t.Run("stop", func(t *testing.T) {
		d := New(nil, nil)
		d.stepSlice = testStepSliceFrame()
		d.Stop()
		if d.stepSlice != (stepSliceFrame{}) {
			t.Fatalf("Stop left stepSlice=%+v", d.stepSlice)
		}
	})
}

func testStepSliceFrame() stepSliceFrame {
	return stepSliceFrame{
		Active:          true,
		Op:              0x9f,
		PBR:             0x01,
		PC:              0x2345,
		Phase:           2,
		Mode:            Alt1,
		SrcReg:          6,
		DstReg:          0,
		Nibble:          0x0f,
		RemainingCycles: 13,
		PostPending:     true,
		PrefixPending:   true,
	}
}
