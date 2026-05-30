package gsu

import "testing"

func TestCycleHookObservesHandlerWait(t *testing.T) {
	d := New([]byte{0x9f, 0x00}, nil) // FMULT; STOP
	d.R[6] = 0x0200
	d.R[0] = 0x0200

	inFMULTHandler := false
	var handlerHooks []struct {
		n      uint64
		cycles uint64
		r0     uint16
	}
	d.TraceHookEx = func(phase TracePhase, _ uint8, _ uint16, op uint8, _ uint64) {
		if phase == TracePhasePostPeek && op == 0x9f {
			inFMULTHandler = true
		}
	}
	d.CycleHook = func(n, cycles uint64) {
		if inFMULTHandler {
			handlerHooks = append(handlerHooks, struct {
				n      uint64
				cycles uint64
				r0     uint16
			}{n: n, cycles: cycles, r0: d.R[0]})
		}
	}

	GoAndRun(d, 1)

	if len(handlerHooks) != 1 {
		t.Fatalf("handler CycleHook calls=%d, want 1: %+v", len(handlerHooks), handlerHooks)
	}
	hook := handlerHooks[0]
	if hook.n != 14 {
		t.Fatalf("handler CycleHook n=%d, want 14", hook.n)
	}
	if hook.r0 != d.R[0] {
		t.Fatalf("handler CycleHook saw R0=%04X, final R0=%04X", hook.r0, d.R[0])
	}
	if hook.cycles != d.Cycles() {
		t.Fatalf("handler CycleHook cycles=%d, final cycles=%d", hook.cycles, d.Cycles())
	}
}

func TestCycleHookRunsAfterPendingRAMCommit(t *testing.T) {
	d := New(nil, nil)
	d.writeRAMBuffer(0x0010, 0x5a)

	var got byte
	d.CycleHook = func(_, _ uint64) {
		got = d.RAM[0x0010]
	}
	d.advanceCycles(d.busWaitCycles())

	if got != 0x5a {
		t.Fatalf("CycleHook saw RAM[0010]=%02X, want committed 5a", got)
	}
}
