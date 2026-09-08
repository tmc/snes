package apu

import (
	"reflect"
	"testing"
)

func inputFixture(code []byte) *APU {
	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x200
	a.Processor.Y = 0x20
	a.InPorts = [4]byte{0x10, 0x30, 0, 0}
	copy(a.RAM[0x200:], code)
	copy(a.RAM[0x200+len(code):], []byte{0x2f, 0xfe})
	return a
}

func TestInputReadCPUWriteBoundary(t *testing.T) {
	cases := []struct {
		name         string
		code         []byte
		read, finish uint64
		check        func(*APU) bool
	}{
		{"mov-a", []byte{0xe4, 0xf4}, 5, 6, func(a *APU) bool { return a.Processor.A == 0x20 }},
		{"mov-y", []byte{0xeb, 0xf4}, 5, 6, func(a *APU) bool { return a.Processor.Y == 0x20 }},
		{"cmp-y", []byte{0x7e, 0xf4}, 5, 6, func(a *APU) bool { return a.Processor.Z }},
		{"cmp-imm", []byte{0x78, 0x20, 0xf4}, 7, 10, func(a *APU) bool { return a.Processor.Z }},
		{"movw", []byte{0xba, 0xf4}, 5, 10, func(a *APU) bool { return a.Processor.A == 0x20 }},
	}
	for _, tc := range cases {
		for _, delta := range []int{-1, 0, 1} {
			name := []string{"before", "at", "after"}[delta+1]
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				a := inputFixture(tc.code)
				var reads []TimingEvent
				a.Trace = func(e TimingEvent) {
					if e.Kind == "input-read" {
						reads = append(reads, e)
					}
				}
				target := uint64(int(tc.read) + delta)
				result := a.RunUntilTarget(target, SyncPortWrite)
				if delta == 0 && result.Yield != YieldAPUPortRead {
					t.Fatalf("boundary yield=%d", result.Yield)
				}
				a.WritePort(0, 0x20)
				a.RunUntilTarget(tc.finish, SyncSafety)
				if got := tc.check(a); got != (delta <= 0) {
					t.Fatalf("new value observed=%v, write delta=%d", got, delta)
				}
				if len(reads) == 0 || reads[0].Cycle != tc.read {
					t.Fatalf("reads=%+v", reads)
				}
				if a.inputOp.active {
					t.Fatalf("instruction did not retire: %+v", a.inputOp)
				}
			})
		}
	}
}

func TestInputReadWordSeparateHalves(t *testing.T) {
	a := inputFixture([]byte{0xba, 0xf4})
	a.RunUntilTarget(5, SyncPortWrite)
	a.WritePort(0, 0x21)
	a.RunUntilTarget(9, SyncPortWrite)
	a.WritePort(0, 0x99) // Cannot alter the already-read low byte.
	a.WritePort(1, 0x43)
	a.RunUntilTarget(10, SyncSafety)
	if a.Processor.A != 0x21 || a.Processor.Y != 0x43 {
		t.Fatalf("YA=%02x%02x", a.Processor.Y, a.Processor.A)
	}
}

func TestInputReadSavedContinuation(t *testing.T) {
	for _, code := range [][]byte{{0xe4, 0xf4}, {0xeb, 0xf4}, {0x7e, 0xf4}, {0x78, 0x20, 0xf4}, {0xba, 0xf4}} {
		end := uint64(6)
		if code[0] == 0x78 || code[0] == 0xba {
			end = 10
		}
		for tick := uint64(2); tick < end; tick++ {
			a := inputFixture(code)
			a.RunUntilTarget(tick, SyncSafety)
			b := NewAPU()
			if err := b.LoadState(a.SaveState()); err != nil {
				t.Fatalf("opcode=%02x tick=%d: %v", code[0], tick, err)
			}
			a.WritePort(0, 0x20)
			b.WritePort(0, 0x20)
			a.WritePort(1, 0x43)
			b.WritePort(1, 0x43)
			a.RunUntilTarget(end, SyncSafety)
			b.RunUntilTarget(end, SyncSafety)
			if !reflect.DeepEqual(a.SaveState(), b.SaveState()) {
				t.Fatalf("opcode=%02x tick=%d restore diverged", code[0], tick)
			}
		}
	}
}

func TestInputClockStateAdmission(t *testing.T) {
	a := inputFixture([]byte{0xe4, 0xf4})
	a.RunUntilTarget(5, SyncPortWrite)
	original := a.SaveState()
	for _, mutate := range []func(*APUState){
		func(s *APUState) { s.ClockVersion = 0 },
		func(s *APUState) { s.InputRead.Phase = 8 },
		func(s *APUState) { s.InputRead.Addr = 0xff },
		func(s *APUState) { s.InputRead.Active = false },
		func(s *APUState) { s.Pending = 1 },
	} {
		state := a.SaveState()
		mutate(&state)
		if err := a.LoadState(state); err == nil {
			t.Fatal("invalid clock/input state accepted")
		}
		if !reflect.DeepEqual(original, a.SaveState()) {
			t.Fatal("rejected restore mutated APU")
		}
	}
}

func TestSMPClockChargesTwoTicksPerOpcodeCycle(t *testing.T) {
	a := inputFixture([]byte{0x00, 0x00, 0x00}) // Two-cycle NOPs.
	a.RunUntilTarget(4, SyncSafety)
	if a.Processor.PC != 0x201 || a.Processor.Cycles != 2 {
		t.Fatalf("after4 SMP clocks PC=%04x opcode cycles=%d", a.Processor.PC, a.Processor.Cycles)
	}
	a.RunUntilTarget(8, SyncSafety)
	if a.Processor.PC != 0x202 || a.Processor.Cycles != 4 {
		t.Fatalf("after8 SMP clocks PC=%04x opcode cycles=%d", a.Processor.PC, a.Processor.Cycles)
	}
}

func TestOutputDummyReadHalfCycle(t *testing.T) {
	for _, code := range [][]byte{{0xc4, 0xf4}, {0xcb, 0xf4}, {0xd8, 0xf4}, {0x8f, 0x5a, 0xf4}} {
		read := uint64(5)
		finish := uint64(8)
		if code[0] == 0x8f {
			read = 7
			finish = 10
		}
		for _, delta := range []int{-1, 0, 1} {
			a := inputFixture(code)
			a.Processor.A = 0x5a
			a.Processor.Y = 0x5a
			a.Processor.X = 0x5a
			var events []TimingEvent
			a.Trace = func(e TimingEvent) { events = append(events, e) }
			a.RunUntilTarget(uint64(int(read)+delta), SyncPortWrite)
			a.WritePort(0, 0x20)
			saved := a.SaveState()
			b := NewAPU()
			if err := b.LoadState(saved); err != nil {
				t.Fatal(err)
			}
			a.RunUntilTarget(finish+1, SyncSafety)
			b.RunUntilTarget(finish+1, SyncSafety)
			if !reflect.DeepEqual(a.SaveState(), b.SaveState()) {
				t.Fatal("dummy read restore diverged")
			}
			reads, publishes := 0, 0
			for _, e := range events {
				switch e.Kind {
				case "input-read":
					reads++
					want := byte(0x10)
					if delta <= 0 {
						want = 0x20
					}
					if e.Cycle != read || e.Value != want {
						t.Fatalf("opcode=%02x delta=%d read=%+v want tick%d value%02x", code[0], delta, e, read, want)
					}
				case "output-port":
					publishes++
					if e.Cycle != finish || e.Value != 0x5a {
						t.Fatalf("publication=%+v", e)
					}
				}
			}
			if reads != 1 || publishes != 1 {
				t.Fatalf("reads=%d publishes=%d", reads, publishes)
			}
		}
	}
}

func TestBeforeCPUResumesWithoutCharging(t *testing.T) {
	a := inputFixture([]byte{0xe4, 0xf4})
	a.RunUntilTarget(5, SyncPortWrite)
	if !a.inputOp.waiting {
		t.Fatal("read not suspended")
	}
	a.RunUntilTarget(5, SyncBeforeCPU)
	if a.GetCycles() != 5 || a.inputOp.waiting {
		t.Fatal("before mode charged a clock or retained read")
	}
	a.WritePort(0, 0x20)
	a.RunUntilTarget(6, SyncSafety)
	if a.Processor.A != 0x10 {
		t.Fatal("completed read changed retroactively")
	}
	b := inputFixture([]byte{0xc4, 0xf4})
	b.Processor.A = 0x5a
	b.RunUntilTarget(8, SyncPostCPU)
	b.RunUntilTarget(8, SyncBeforeCPU)
	if b.GetCycles() != 8 || b.OutPorts[0] != 0x5a {
		t.Fatal("before mode did not publish at target without charging")
	}
}

func TestAPUTargetCyclesIntermediateOverflow(t *testing.T) {
	const max = ^uint64(0)
	if got := apuTargetCycles(max-1, max, 2); got != 2 {
		t.Fatalf("overflowed intermediate target=%d want2", got)
	}
	if got := apuTargetCycles(max, 2, max); got != max {
		t.Fatalf("unrepresentable target=%d", got)
	}
}

func TestPendingPublicationClockUnits(t *testing.T) {
	a := NewAPU()
	a.cycles = 3
	a.pending = 1
	a.pendingOutPortMask = 1
	a.pendingOutPorts[0] = 0x5a
	if !a.pendingOutPortWriteWouldFlushBy(4) || a.pendingOutPortWriteWouldFlushBy(3) {
		t.Fatal("half-cycle remaining duration is incorrect")
	}
	a.cycles = ^uint64(0) - 1
	a.pending = 2
	if a.pendingOutPortWriteWouldFlushBy(^uint64(0)) {
		t.Fatal("overflow made future publication appear due")
	}
}
