package apu

import (
	"fmt"
	"reflect"
	"testing"
)

// The ordering follows bsnes SMP::write: wait, writeRAM, writeIO. writeIO
// calls synchronizeCPU (clock >= 0) before assigning io.cpuN. The retained
// TestAPUDirectReadReferenceArtifactAssignmentResumeClasses fixture witnesses
// both an old-value read inside that suspension and a run-through assignment.
func TestAssignmentResumeBoundary(t *testing.T) {
	for _, opcode := range []byte{0xc4, 0xcb, 0xd8, 0x8f} {
		boundary := uint64(4)
		if opcode == 0x8f {
			boundary = 5
		}
		for _, mode := range []SyncMode{SyncPostCPU, SyncPortRead, SyncPortWrite, SyncSafety} {
			for _, target := range []uint64{0, boundary - 1, boundary, boundary + 1} {
				t.Run(fmt.Sprintf("%02x/mode%d/target%d", opcode, mode, target), func(t *testing.T) {
					a := assignmentAPU(opcode, 0xf4, 0x5a)
					result := a.RunUntilTarget(target, mode)
					wantYield := target == boundary && (mode == SyncPostCPU || mode == SyncPortRead)
					if (result.Yield == YieldAPUPortWrite) != wantYield {
						t.Fatalf("yield = %d", result.Yield)
					}
					wantRAM, wantOut := byte(0), byte(0)
					if target >= boundary {
						wantRAM = 0x5a
						if !wantYield {
							wantOut = 0x5a
						}
					}
					if a.RAM[0xf4] != wantRAM || a.OutPorts[0] != wantOut {
						t.Fatalf("RAM/visible = %02x/%02x, want %02x/%02x", a.RAM[0xf4], a.OutPorts[0], wantRAM, wantOut)
					}
					if wantYield {
						if !a.portAssignmentPending() || a.pendingOutPortMask != 0 || a.pending != 0 {
							t.Fatal("assignment replaced with idle debt or queued publication")
						}
						for range 3 {
							if a.RunUntilTarget(target, SyncPortRead).Yield != YieldAPUPortWrite || a.OutPorts[0] != 0 {
								t.Fatal("repeated read published prematurely")
							}
						}
						a.RunUntilTarget(target+1, SyncPortRead)
						if a.OutPorts[0] != 0x5a || a.portAssignmentPending() {
							t.Fatal("later target did not resume assignment")
						}
						a.OutPorts[0] = 0x99
						a.RunUntilTarget(target+1, SyncPortRead)
						if a.OutPorts[0] != 0x99 {
							t.Fatal("assignment published twice")
						}
					}
				})
			}
		}
	}
}

func assignmentAPU(opcode, port, value byte) *APU {
	a := NewAPU()
	a.Control = 0
	a.Processor.PC = 0x200
	a.Processor.A = value
	a.Processor.X = value
	a.Processor.Y = value
	if opcode != 0x8f {
		copy(a.RAM[0x200:], []byte{opcode, port, 0, 0})
	} else {
		copy(a.RAM[0x200:], []byte{opcode, value, port, 0, 0})
	}
	return a
}

func TestAssignmentResumeState(t *testing.T) {
	for _, opcode := range []byte{0xc4, 0xcb, 0xd8, 0x8f} {
		boundary := uint64(4)
		if opcode == 0x8f {
			boundary = 5
		}
		for at := uint64(0); at <= boundary+1; at++ {
			t.Run(fmt.Sprintf("%02x/cycle%d", opcode, at), func(t *testing.T) {
				a := assignmentAPU(opcode, 0xf6, 0x37)
				a.RunUntilTarget(at, SyncPostCPU)
				state := a.SaveState()
				b := NewAPU()
				b.LoadState(state)
				if !reflect.DeepEqual(state, b.SaveState()) {
					t.Fatal("state changed on restore")
				}
				a.RunUntilTarget(boundary+2, SyncSafety)
				b.RunUntilTarget(boundary+2, SyncSafety)
				if !reflect.DeepEqual(a.SaveState(), b.SaveState()) {
					t.Fatal("restored continuation differs")
				}
				if b.OutPorts[2] != 0x37 {
					t.Fatal("restored assignment missing")
				}
			})
		}
	}
}

func TestAssignmentRepeatedEqualPorts(t *testing.T) {
	a := assignmentAPU(0xc4, 0xf4, 0x5a)
	copy(a.RAM[0x200:], []byte{0xc4, 0xf4, 0xc4, 0xf5, 0xc4, 0xf4, 0})
	for i := uint64(1); i <= 3; i++ {
		if a.RunUntilTarget(i*4, SyncPostCPU).Yield != YieldAPUPortWrite {
			t.Fatalf("write %d did not suspend", i)
		}
		a.RunUntilTarget(i*4, SyncSafety)
		if a.portAssignmentPending() {
			t.Fatal("assignment not retired")
		}
	}
	if a.OutPorts[0] != 0x5a || a.OutPorts[1] != 0x5a {
		t.Fatal(a.OutPorts)
	}
	if a.Processor.Cycles != 12 {
		t.Fatalf("retired cycles = %d", a.Processor.Cycles)
	}
}

func TestAssignmentDispatchDoesNotReadMMIO(t *testing.T) {
	for _, pc := range []uint16{0xfd, 0xfc} {
		a := NewAPU()
		a.Control = 0
		a.Processor.PC = pc
		a.Timers[0].Counter = 0xc4
		a.RAM[0xfc] = 0xe8 // MOV A,#imm; operand is timer counter at FD
		a.RAM[0xfe] = 0    // operand for MOV dp,A executed from FD
		a.Run()
		if pc == 0xfc && a.Processor.A != 0xc4 {
			t.Fatalf("operand timer was consumed by dispatch: A=%02x", a.Processor.A)
		}
		if pc == 0xfd && a.Processor.PC != 0xff {
			t.Fatalf("opcode timer was consumed by dispatch: PC=%04x", a.Processor.PC)
		}
	}
}

func TestAssignmentStateRejectsInvalidPhase(t *testing.T) {
	a := assignmentAPU(0xc4, 0xf4, 0x5a)
	a.RunUntilTarget(4, SyncPostCPU)
	original := a.SaveState()
	for _, change := range []func(*APUState){
		func(s *APUState) { s.MicroOp.Step = 0 },
		func(s *APUState) { s.MicroOp.Step = 9 },
		func(s *APUState) { s.MicroOp.Opcode = 0xff },
		func(s *APUState) { s.MicroOp.Addr = 0x100 },
		func(s *APUState) { s.Pending = 1 },
		func(s *APUState) { s.PendingOutPortMask = 1 },
	} {
		state := original
		change(&state)
		if err := a.LoadState(state); err == nil {
			t.Fatal("invalid phase accepted")
		}
		if !reflect.DeepEqual(a.SaveState(), original) {
			t.Fatal("invalid restore mutated APU")
		}
	}
}
