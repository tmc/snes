package cpu

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func replacementCPU(code []byte) (*CPU, *MockMemory) {
	b := bus.NewBus()
	m := &MockMemory{}
	b.Map(0, 0xffff, m)
	copy(m.Data[0x8000:], code)
	c := NewCPU(b)
	c.PC = 0x8000
	c.P = 0x30
	c.S = 0x1ff
	return c, m
}

func TestInstructionReplacementTiming(t *testing.T) {
	for _, tt := range []struct {
		name string
		code []byte
	}{
		{"load", []byte{0xad, 0x34, 0x12}}, {"store", []byte{0x8d, 0x34, 0x12}}, {"carry", []byte{0x18}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original, m1 := replacementCPU(tt.code)
			changed, m2 := replacementCPU(tt.code)
			original.A = 0xab42
			changed.A = original.A
			original.P |= 1
			changed.P = original.P
			m1.Data[0x1234] = 0x82
			m2.Data[0x1234] = 0x82
			var edges1, edges2 []uint64
			original.BusEdge = func(n uint64) { edges1 = append(edges1, n) }
			changed.BusEdge = func(n uint64) { edges2 = append(edges2, n) }
			changed.ReplaceInstruction = func(address uint32, op uint8) InstructionExecutor {
				if address != 0x8000 {
					t.Fatalf("address %x", address)
				}
				return func(io *InstructionIO) (Snapshot, error) {
					if op == 0x18 {
						if err := io.Idle(6); err != nil {
							return Snapshot{}, err
						}
						s := io.State()
						s.P &^= 1
						return s, nil
					}
					lo, err := io.Fetch()
					if err != nil {
						return Snapshot{}, err
					}
					hi, err := io.Fetch()
					if err != nil {
						return Snapshot{}, err
					}
					addr := uint32(hi)<<8 | uint32(lo)
					s := io.State()
					if op == 0x8d {
						if err := io.Write(addr, uint8(s.A)); err != nil {
							return Snapshot{}, err
						}
						s.Cycles = io.State().Cycles
						return s, nil
					}
					v, err := io.Read(addr)
					if err != nil {
						return Snapshot{}, err
					}
					s = io.State()
					s.A = s.A&0xff00 | uint16(v)
					s.P &^= 0x82
					if v == 0 {
						s.P |= 2
					}
					s.P |= v & 0x80
					return s, nil
				}
			}
			original.Step()
			changed.Step()
			if changed.Fault != nil {
				t.Fatal(changed.Fault)
			}
			if original.Snapshot() != changed.Snapshot() || !reflect.DeepEqual(edges1, edges2) || m1.Data != m2.Data || original.Bus.MDR() != changed.Bus.MDR() {
				t.Fatalf("replacement differs: original=%+v changed=%+v edges %v/%v", original.Snapshot(), changed.Snapshot(), edges1, edges2)
			}
		})
	}
}

func TestInstructionReplacementRefusal(t *testing.T) {
	for _, name := range []string{"invalid_address", "invalid_idle", "fetch_limit", "operation_limit", "wrong_clock", "partial_error", "ignored_error"} {
		t.Run(name, func(t *testing.T) {
			c, m := replacementCPU([]byte{0xa9, 0x55})
			var saved *InstructionIO
			c.ReplaceInstruction = func(uint32, uint8) InstructionExecutor {
				return func(io *InstructionIO) (Snapshot, error) {
					saved = io
					switch name {
					case "invalid_address":
						_, err := io.Read(1 << 24)
						return io.State(), err
					case "invalid_idle":
						err := io.Idle(5)
						return io.State(), err
					case "operation_limit":
						for i := 0; i < 129; i++ {
							if _, err := io.Read(0); err != nil {
								return io.State(), err
							}
						}
					case "fetch_limit":
						for i := 0; i < 4; i++ {
							if _, err := io.Fetch(); err != nil {
								return io.State(), err
							}
						}
					case "wrong_clock":
						s := io.State()
						s.Cycles++
						return s, nil
					case "partial_error":
						if err := io.Write(0x1234, 0x77); err != nil {
							return io.State(), err
						}
						return io.State(), errors.New("transport failed")
					case "ignored_error":
						io.Write(1<<24, 0)
						return io.State(), nil
					}
					return io.State(), nil
				}
			}
			c.Step()
			if c.Fault == nil || !c.Stopped {
				t.Fatal("replacement failure not fatal")
			}
			if c.A == 0x55 {
				t.Fatal("original opcode ran after failure")
			}
			if _, err := saved.Read(0); err == nil {
				t.Fatal("expired executor lease accepted")
			}
			if name == "partial_error" && m.Data[0x1234] != 0x77 {
				t.Fatal("partial effect unexpectedly rolled back")
			}
		})
	}
}

func TestInstructionReplacementDeclineAndInterrupt(t *testing.T) {
	c, _ := replacementCPU([]byte{0xa9, 0x55})
	calls := 0
	c.ReplaceInstruction = func(uint32, uint8) InstructionExecutor { calls++; return nil }
	c.Step()
	if c.A != 0x55 || calls != 1 {
		t.Fatal("decline changed ordinary execution")
	}
	c.NMIPending = true
	c.Step()
	if calls != 1 {
		t.Fatal("replacement intercepted interrupt")
	}
}

func TestInstructionReplacementCopiedLease(t *testing.T) {
	c, m := replacementCPU([]byte{0xea})
	var escaped InstructionIO
	c.ReplaceInstruction = func(uint32, uint8) InstructionExecutor {
		return func(io *InstructionIO) (Snapshot, error) {
			escaped = *io
			if err := escaped.Idle(6); err != nil {
				return Snapshot{}, err
			}
			return io.State(), nil
		}
	}
	c.Step()
	before := c.Cycles
	if err := escaped.Write(0x1234, 0x77); err == nil {
		t.Fatal("copied expired lease accepted")
	}
	if c.Cycles != before || m.Data[0x1234] != 0 {
		t.Fatal("expired copied lease mutated machine")
	}
	c, _ = replacementCPU([]byte{0xea})
	c.ReplaceInstruction = func(uint32, uint8) InstructionExecutor {
		return func(io *InstructionIO) (Snapshot, error) {
			for i := 0; i < 129; i++ {
				copied := *io
				copied.Read(0)
			}
			return io.State(), nil
		}
	}
	c.Step()
	if c.Fault == nil {
		t.Fatal("copied lease bypassed shared operation limit")
	}
}
