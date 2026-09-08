package gsu

import (
	"bytes"
	"testing"
)

func newCacheBoundaryIWTDevice() *Device {
	rom := make([]byte, 32)
	rom[14], rom[15], rom[16] = 0xf5, 0x34, 0x12
	d := New(rom, nil)
	copy(d.Cache[:16], rom[:16])
	d.cacheValid[0] = true
	d.Pipeline = 0xf5
	d.R[15] = 15
	d.Go()
	return d
}

func TestStepSliceOperandCacheBoundaryBudget(t *testing.T) {
	whole := newCacheBoundaryIWTDevice()
	if n := whole.Run(1); n != 1 {
		t.Fatalf("Run retired %d", n)
	}
	d := newCacheBoundaryIWTDevice()
	if r := d.StepSlice(2); r.Cycles != 2 || !r.Partial {
		t.Fatalf("dispatch = %+v", r)
	}
	before, err := d.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	// fetch8 increments R15 before refilling the pipeline. The next address
	// crosses into an invalid cache line and requires all 16 bus reads.
	if r := d.StepSlice(95); r.Cycles != 0 || r.RetiredOpcodes != 0 || !r.Partial {
		t.Fatalf("insufficient cache-fill budget advanced: %+v", r)
	}
	after, err := d.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected cache fill changed state")
	}
	restored := newCacheBoundaryIWTDevice()
	if err := restored.Unserialize(after); err != nil {
		t.Fatal(err)
	}
	if r := restored.StepSlice(96); r.Cycles != 96 || r.RetiredOpcodes != 0 || !r.Partial {
		t.Fatalf("cache fill = %+v", r)
	}
	if r := restored.StepSlice(2); r.Cycles != 2 || r.RetiredOpcodes != 1 || r.Partial {
		t.Fatalf("high operand = %+v", r)
	}
	if got, want := captureFutureStepSliceState(restored), captureFutureStepSliceState(whole); got != want {
		t.Fatalf("fragmented = %+v, whole = %+v", got, want)
	}
	if restored.R[5] != 0x1234 {
		t.Fatalf("IWT R5 = %04x", restored.R[5])
	}
}
