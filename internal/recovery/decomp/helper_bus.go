package decomp

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery/dispatchbus"
)

func (v *EvidenceVerifier) verifyHelperBus(insns []captureCPUInsn, events []busEvent) error {
	if len(insns) == 0 {
		return fmt.Errorf("helper bus: no instructions")
	}
	state := func(s cpuStateWithCycles) dispatchbus.State {
		return dispatchbus.State{A: s.A, Y: s.Y, S: s.S, D: s.D, PB: s.PB, DB: s.DB, P: s.P, PC: s.PC, E: s.E, Cycles: s.Cycles}
	}
	var instructions []dispatchbus.Instruction
	for j, in := range insns {
		if j > 0 && !cpuStateEqualWithCycles(insns[j-1].Exit, in.Entry) {
			return fmt.Errorf("helper bus: CPU continuity break")
		}
		i := dispatchbus.Instruction{Entry: state(in.Entry), Exit: state(in.Exit)}
		for _, f := range in.Fetches {
			i.Bytes = append(i.Bytes, f.Value)
			i.Fetches = append(i.Fetches, f.Addr)
		}
		instructions = append(instructions, i)
	}
	var accesses []dispatchbus.Access
	for _, e := range events {
		if e.Cycle <= insns[0].Entry.Cycles || e.Cycle > insns[len(insns)-1].Exit.Cycles {
			continue
		}
		value := byte(0)
		if e.Value != nil {
			value = *e.Value
		}
		if e.Op == "write" && e.After != nil {
			value = *e.After
		}
		accesses = append(accesses, dispatchbus.Access{ID: e.ID, Cycle: e.Cycle, Space: e.Space, Op: e.Op, Address: e.Addr, Value: value, Actor: e.Actor, PC: e.CPUPC, Opcode: e.CPUOpcode, Bytes: e.CPUBytes, ROM: e.ROM, ROMOffset: e.ROMOffset, Schema: e.Schema, Width: e.Width, ValueKnown: e.ValueKnown})
	}
	return dispatchbus.Verify(instructions, accesses, v.policy.rom)
}
