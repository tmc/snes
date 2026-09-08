package gsu

import (
	"bytes"
	"testing"
)

func TestStepSliceUnsupportedBoundariesDoNotAdvance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		op     byte
		prefix bool
	}{
		{"cold NOP", 0x01, false}, {"STOP", 0x00, false}, {"branch", 0x05, false},
		{"PLOT", 0x4c, false}, {"MULT", 0x80, false}, {"IBT", 0xa0, false},
		{"ALT prefix", 0x3d, false}, {"prefixed IWT", 0xf5, true},
		{"LDW without pending write", 0x41, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New([]byte{tc.op, 0}, nil)
			d.Go()
			d.Pipeline = tc.op
			d.withPrefix = tc.prefix
			before, err := d.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if r := d.StepSlice(1000); r.Cycles != 0 || r.RetiredOpcodes != 0 || r.Partial {
				t.Fatalf("unsupported opcode progressed: %+v", r)
			}
			after, err := d.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("unsupported boundary changed device")
			}
		})
	}
}
