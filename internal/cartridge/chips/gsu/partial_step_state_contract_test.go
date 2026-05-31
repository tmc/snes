package gsu

import (
	"reflect"
	"testing"
)

func TestFutureStepSliceInFlightSerializationContract(t *testing.T) {
	fixtures := []inFlightStepSliceFixture{
		{
			name:          "FMULT paused multiply wait",
			rom:           []byte{0x9f, 0x00}, // FMULT; STOP
			regs:          [16]uint16{0: 0x0080, 6: 0x0100},
			wholeRetires:  1,
			handlerCycles: 14,
			slices: []inFlightStepSliceWindow{
				{cycles: 13, retires: 0},
				{cycles: 1, retires: 1},
			},
		},
		{
			name:          "IWT paused second operand fetch",
			rom:           []byte{0xf5, 0x34, 0x12, 0x00}, // IWT R5,#$1234; STOP
			wholeRetires:  1,
			handlerCycles: 4,
			slices: []inFlightStepSliceWindow{
				{cycles: 2, retires: 0},
				{cycles: 2, retires: 1},
			},
		},
		{
			name:          "STW paused high-byte write",
			rom:           []byte{0x31, 0x00}, // STW (R1); STOP
			regs:          [16]uint16{0: 0x1234, 1: 0x0010},
			wholeRetires:  1,
			handlerCycles: 6,
			slices: []inFlightStepSliceWindow{
				{cycles: 5, retires: 0},
				{cycles: 1, retires: 1},
			},
		},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			f.check(t)

			whole := f.newDevice()
			GoAndRun(whole, f.wholeRetires)
			wantFinal := captureFutureStepSliceState(whole)

			sliced := f.newStepSliceDevice(t)
			first, ok := callFutureStepSlice(t, sliced, f.slices[0].cycles)
			if !ok {
				t.Skip("(*Device).StepSlice(masterCycles uint64) StepSliceResult is not implemented")
			}
			if first.retires != f.slices[0].retires {
				t.Fatalf("first StepSlice retired %d opcodes, want %d",
					first.retires, f.slices[0].retires)
			}
			paused := captureFutureStepSliceState(sliced)

			state, err := sliced.Serialize()
			if err != nil {
				t.Fatalf("Serialize paused StepSlice state: %v", err)
			}
			restored := f.newDevice()
			if err := restored.Unserialize(state); err != nil {
				t.Fatalf("Unserialize paused StepSlice state: %v", err)
			}
			if got := captureFutureStepSliceState(restored); got != paused {
				t.Fatalf("paused visible state changed across Serialize/Unserialize: got %+v, want %+v",
					got, paused)
			}

			second, ok := callFutureStepSlice(t, restored, f.slices[1].cycles)
			if !ok {
				t.Fatal("StepSlice disappeared after first successful call")
			}
			if second.retires != f.slices[1].retires {
				t.Fatalf("second StepSlice retired %d opcodes, want %d",
					second.retires, f.slices[1].retires)
			}
			if got := captureFutureStepSliceState(restored); got != wantFinal {
				t.Fatalf("serialized StepSlice final state = %+v, want whole-handler GoAndRun %+v",
					got, wantFinal)
			}
		})
	}
}

type inFlightStepSliceFixture struct {
	name          string
	rom           []byte
	regs          [16]uint16
	cfgr          uint8
	clsr          uint8
	wholeRetires  int
	handlerCycles uint64
	slices        []inFlightStepSliceWindow
}

type inFlightStepSliceWindow struct {
	cycles  uint64
	retires int
}

type futureStepSliceCallResult struct {
	retires int
}

func (f inFlightStepSliceFixture) check(t *testing.T) {
	t.Helper()
	if len(f.slices) != 2 {
		t.Fatalf("fixture has %d slices, want 2", len(f.slices))
	}
	var cycles uint64
	var retires int
	for _, slice := range f.slices {
		if slice.cycles == 0 {
			t.Fatal("zero-cycle StepSlice window")
		}
		cycles += slice.cycles
		retires += slice.retires
	}
	if cycles != f.handlerCycles {
		t.Fatalf("slice cycles = %d, want handler cycles %d", cycles, f.handlerCycles)
	}
	if retires != f.wholeRetires {
		t.Fatalf("slice retire count = %d, want whole-run retire count %d",
			retires, f.wholeRetires)
	}
	if f.slices[0].retires != 0 {
		t.Fatalf("first StepSlice retires %d opcodes, want 0", f.slices[0].retires)
	}
	if f.slices[1].retires != f.wholeRetires {
		t.Fatalf("second StepSlice retires %d opcodes, want %d",
			f.slices[1].retires, f.wholeRetires)
	}
}

func (f inFlightStepSliceFixture) newDevice() *Device {
	d := New(append([]byte(nil), f.rom...), nil)
	d.R = f.regs
	d.CFGR = f.cfgr
	d.CLSR = f.clsr
	return d
}

func (f inFlightStepSliceFixture) newStepSliceDevice(t *testing.T) *Device {
	t.Helper()
	d := f.newDevice()
	switch f.name {
	case "FMULT paused multiply wait":
		startFMULTStepSliceWait(t, d)
	case "IWT paused second operand fetch":
		startIWTStepSliceOperandFetch(t, d)
	case "STW paused high-byte write":
		startSTWStepSliceStoreWait(t, d)
	default:
		t.Fatalf("no StepSlice setup for %q", f.name)
	}
	return d
}

func callFutureStepSlice(t *testing.T, d *Device, cycles uint64) (futureStepSliceCallResult, bool) {
	t.Helper()
	method := reflect.ValueOf(d).MethodByName("StepSlice")
	if !method.IsValid() {
		return futureStepSliceCallResult{}, false
	}
	methodType := method.Type()
	if methodType.NumIn() != 1 || methodType.In(0).Kind() != reflect.Uint64 || methodType.NumOut() != 1 {
		t.Fatalf("StepSlice type = %s, want func(uint64) StepSliceResult", methodType)
	}
	out := method.Call([]reflect.Value{reflect.ValueOf(cycles)})
	retires, ok := stepSliceRetiredOpcodes(out[0])
	if !ok {
		t.Fatalf("StepSlice result type = %s, want retired opcode count", out[0].Type())
	}
	return futureStepSliceCallResult{retires: retires}, true
}

func stepSliceRetiredOpcodes(v reflect.Value) (int, bool) {
	if !v.IsValid() {
		return 0, false
	}
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if n, ok := intFromReflect(v); ok {
		return n, true
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	for _, name := range []string{"Retired", "Retires", "RetiredOpcodes", "OpcodesRetired"} {
		field := v.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		if n, ok := intFromReflect(field); ok {
			return n, true
		}
	}
	for _, name := range []string{"Retired", "Retires", "RetiredOpcodes", "OpcodesRetired"} {
		method := v.MethodByName(name)
		if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
			continue
		}
		out := method.Call(nil)
		if n, ok := intFromReflect(out[0]); ok {
			return n, true
		}
	}
	return 0, false
}

func intFromReflect(v reflect.Value) (int, bool) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int(v.Uint()), true
	default:
		return 0, false
	}
}
