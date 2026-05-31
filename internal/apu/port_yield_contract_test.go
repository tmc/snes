package apu

import (
	"reflect"
	"strings"
	"testing"
)

func TestFutureAPUPortYieldContract(t *testing.T) {
	const missingAPI = "APU yield-capable run method"

	f := apuPortYieldFixture{
		name:         "MOV $F4,A pauses before CPU-visible port publish",
		pc:           0x0200,
		program:      []uint8{0xC4, 0xF4, 0x00}, // MOV dp,A; NOP
		a:            0x5A,
		port:         0,
		initialOut:   0x00,
		publishedOut: 0x5A,
		opcodeCycles: 4,
	}
	f.check(t)
	f.checkCurrentBehavior(t)

	apu := f.newAPU()
	first, ok := callFutureAPUPortRun(t, apu, uint64(f.opcodeCycles), apuFutureSyncPostCPU)
	if !ok {
		assertions := []string{
			"a post-CPU APU catch-up run can stop at an SPC700 output-port write boundary",
			"the stop happens before the CPU-visible OutPorts byte is published",
			"SaveState/LoadState preserves the paused port-write state",
			"a direct port read stops before the queued output byte while safety-mode resume publishes it",
			"the current Run opcode-granular behavior remains unchanged for existing callers",
		}
		for _, assertion := range assertions {
			if assertion == "" {
				t.Fatal("empty APU port-yield assertion")
			}
		}
		t.Skipf("%s is not implemented; enable these assertions before the APU port-yield refactor: %s",
			missingAPI, strings.Join(assertions, "; "))
	}
	if !first.yielded {
		t.Fatalf("future APU run result yielded=false, want true at port-write boundary")
	}
	if got := apu.ReadPort(f.port); got != f.initialOut {
		t.Fatalf("port visible at pause = %02X, want %02X", got, f.initialOut)
	}

	paused := captureAPUPortYieldState(apu)
	state := apu.SaveState()
	restored := NewAPU()
	restored.LoadState(state)
	if got := captureAPUPortYieldState(restored); got != paused {
		t.Fatalf("paused port-yield state changed across SaveState/LoadState: got %+v, want %+v",
			got, paused)
	}

	read, ok := callFutureAPUPortRun(t, restored, uint64(f.opcodeCycles), apuFutureSyncPortRead)
	if !ok {
		t.Fatal("future APU run method disappeared after first successful call")
	}
	if !read.yielded {
		t.Fatalf("future read sync yielded=false, want true before port publish")
	}
	if got := restored.ReadPort(f.port); got != f.initialOut {
		t.Fatalf("port after read sync = %02X, want %02X", got, f.initialOut)
	}

	_, ok = callFutureAPUPortRun(t, restored, uint64(f.opcodeCycles), apuFutureSyncSafety)
	if !ok {
		t.Fatal("future APU run method disappeared before safety resume")
	}
	if got := restored.ReadPort(f.port); got != f.publishedOut {
		t.Fatalf("port after safety resume = %02X, want %02X", got, f.publishedOut)
	}
}

type apuPortYieldFixture struct {
	name         string
	pc           uint16
	program      []uint8
	a            uint8
	port         uint32
	initialOut   uint8
	publishedOut uint8
	opcodeCycles int
}

func (f apuPortYieldFixture) check(t *testing.T) {
	t.Helper()
	if len(f.program) < 2 {
		t.Fatalf("program length = %d, want at least 2", len(f.program))
	}
	if f.program[0] != 0xC4 || f.program[1] != 0xF4 {
		t.Fatalf("program starts %02X %02X, want C4 F4 for MOV $F4,A",
			f.program[0], f.program[1])
	}
	if f.opcodeCycles <= 0 {
		t.Fatalf("opcodeCycles = %d, want positive", f.opcodeCycles)
	}
}

func (f apuPortYieldFixture) newAPU() *APU {
	apu := NewAPU()
	apu.Control = 0
	apu.Processor.PC = f.pc
	apu.Processor.A = f.a
	copy(apu.RAM[f.pc:], f.program)
	return apu
}

func (f apuPortYieldFixture) checkCurrentBehavior(t *testing.T) {
	t.Helper()

	apu := f.newAPU()
	apu.Run()
	if got := apu.ReadPort(f.port); got != f.initialOut {
		t.Fatalf("current Run published port after one tick = %02X, want %02X",
			got, f.initialOut)
	}
	if apu.pendingOutPortMask&(1<<f.port) == 0 {
		t.Fatalf("current Run pendingOutPortMask = %02X, want port %d queued",
			apu.pendingOutPortMask, f.port)
	}

	state := apu.SaveState()
	restored := NewAPU()
	restored.LoadState(state)
	for i := 1; i < f.opcodeCycles; i++ {
		restored.Run()
	}
	if got := restored.ReadPort(f.port); got != f.publishedOut {
		t.Fatalf("current restored Run published port = %02X, want %02X",
			got, f.publishedOut)
	}
}

type apuFutureSyncMode uint8

const (
	apuFutureSyncPostCPU apuFutureSyncMode = iota
	apuFutureSyncPortRead
	apuFutureSyncPortWrite
	apuFutureSyncSafety
)

type apuFuturePortRunResult struct {
	yielded bool
}

func callFutureAPUPortRun(t *testing.T, apu *APU, targetCycles uint64, mode apuFutureSyncMode) (apuFuturePortRunResult, bool) {
	t.Helper()

	for _, name := range []string{"RunUntil", "StepUntil", "RunUntilYield", "StepUntilYield"} {
		method := reflect.ValueOf(apu).MethodByName(name)
		if !method.IsValid() {
			continue
		}
		args := futureAPUPortRunArgs(t, method.Type(), targetCycles, mode)
		out := method.Call(args)
		if len(out) != 1 {
			t.Fatalf("%s returned %d values, want one yield result", name, len(out))
		}
		yielded, ok := futureAPUPortRunYielded(out[0])
		if !ok {
			t.Fatalf("%s result type = %s, want bool or result with yield reason", name, out[0].Type())
		}
		return apuFuturePortRunResult{yielded: yielded}, true
	}
	return apuFuturePortRunResult{}, false
}

func futureAPUPortRunArgs(t *testing.T, method reflect.Type, targetCycles uint64, mode apuFutureSyncMode) []reflect.Value {
	t.Helper()

	switch method.NumIn() {
	case 1:
		return []reflect.Value{futureAPUUintArg(t, method.In(0), targetCycles)}
	case 3:
		return []reflect.Value{
			futureAPUUintArg(t, method.In(0), targetCycles),
			futureAPUUintArg(t, method.In(1), 1),
			futureAPUUintArg(t, method.In(2), 1),
		}
	case 4:
		return []reflect.Value{
			futureAPUUintArg(t, method.In(0), targetCycles),
			futureAPUUintArg(t, method.In(1), 1),
			futureAPUUintArg(t, method.In(2), 1),
			futureAPUModeArg(t, method.In(3), mode),
		}
	default:
		t.Fatalf("future APU run method type = %s, want 1, 3, or 4 arguments", method)
		return nil
	}
}

func futureAPUUintArg(t *testing.T, typ reflect.Type, value uint64) reflect.Value {
	t.Helper()

	switch typ.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		v := reflect.New(typ).Elem()
		v.SetUint(value)
		return v
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v := reflect.New(typ).Elem()
		v.SetInt(int64(value))
		return v
	default:
		t.Fatalf("future APU run argument type = %s, want integer", typ)
		return reflect.Value{}
	}
}

func futureAPUModeArg(t *testing.T, typ reflect.Type, mode apuFutureSyncMode) reflect.Value {
	t.Helper()
	return futureAPUUintArg(t, typ, uint64(mode))
}

func futureAPUPortRunYielded(v reflect.Value) (bool, bool) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false, false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() != 0, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() != 0, true
	case reflect.Struct:
		return futureAPUStructYielded(v)
	default:
		return false, false
	}
}

func futureAPUStructYielded(v reflect.Value) (bool, bool) {
	for _, name := range []string{"Yielded", "Paused", "Stopped"} {
		field := v.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.Bool {
			return field.Bool(), true
		}
	}
	for _, name := range []string{"Yield", "Reason", "YieldReason"} {
		field := v.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		if yielded, ok := futureAPUPortRunYielded(field); ok {
			return yielded, true
		}
	}
	return false, false
}

type apuPortYieldState struct {
	PC                 uint16
	A                  uint8
	Cycles             uint64
	ProcessorCycles    uint64
	Pending            uint32
	PendingOutPortMask uint8
	PendingOutPorts    [4]uint8
	OutPorts           [4]uint8
	MicroOp            apuMicroOp
}

func captureAPUPortYieldState(apu *APU) apuPortYieldState {
	return apuPortYieldState{
		PC:                 apu.Processor.PC,
		A:                  apu.Processor.A,
		Cycles:             apu.cycles,
		ProcessorCycles:    apu.Processor.Cycles,
		Pending:            apu.pending,
		PendingOutPortMask: apu.pendingOutPortMask,
		PendingOutPorts:    apu.pendingOutPorts,
		OutPorts:           apu.OutPorts,
		MicroOp:            apu.microOp,
	}
}
