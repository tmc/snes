package extractor

import (
	"encoding/json"
	"fmt"

	"github.com/tmc/snes/internal/recovery/dispatchbus"
)

func verifyHelperBus(insns []*RawInsn, events BusEventList, rom []byte) error {
	if len(insns) == 0 {
		return fmt.Errorf("helper bus: no instructions")
	}
	state := func(s CPUState) dispatchbus.State {
		return dispatchbus.State{A: s.A, Y: s.Y, S: s.S, D: s.D, PB: s.PB, DB: s.DB, P: s.P, PC: s.PC, E: s.E, Cycles: s.Cycles}
	}
	var instructions []dispatchbus.Instruction
	for j, in := range insns {
		if j > 0 && insns[j-1].Exit != in.Entry {
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
	for _, ev := range events {
		if ev.Cycle <= insns[0].Entry.Cycles || ev.Cycle > insns[len(insns)-1].Exit.Cycles {
			continue
		}
		var raw struct {
			Schema int   `json:"schema"`
			Width  int   `json:"width"`
			Value  *byte `json:"value"`
			After  *byte `json:"after"`
			CPU    *struct {
				PBR    *byte   `json:"pbr"`
				PC     *uint16 `json:"pc"`
				Opcode *byte   `json:"opcode"`
				Bytes  []byte  `json:"bytes"`
			} `json:"cpu"`
			DMA    json.RawMessage `json:"dma"`
			Source *struct {
				Space string `json:"space"`
				Start uint32 `json:"start"`
			} `json:"source"`
		}
		if err := json.Unmarshal(ev.Raw, &raw); err != nil {
			return fmt.Errorf("helper bus context: %w", err)
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(ev.Raw, &fields)
		_, hasValue := fields["value"]
		known := raw.Value != nil || (!hasValue && raw.Schema == 2 && ev.Op == "read")
		if ev.Op == "write" {
			known = raw.After != nil || raw.Value != nil
		}
		if (hasValue && raw.Value == nil) || (fields["after"] != nil && raw.After == nil) || (raw.Value != nil && raw.After != nil && *raw.Value != *raw.After) {
			known = false
		}
		a := dispatchbus.Access{ID: ev.ID, Cycle: ev.Cycle, Space: ev.Space, Op: ev.Op, Address: ev.Addr, Value: ev.Value, Actor: "unknown", Schema: raw.Schema, Width: raw.Width, ValueKnown: known}
		if ev.Op == "write" {
			var fields map[string]json.RawMessage
			json.Unmarshal(ev.Raw, &fields)
			if _, ok := fields["after"]; ok {
				a.Value = ev.After
			}
		}
		if raw.CPU != nil && raw.CPU.PBR != nil && raw.CPU.PC != nil && raw.CPU.Opcode != nil && (len(raw.DMA) == 0 || string(raw.DMA) == "null") {
			a.Actor = "cpu"
			a.PC = uint32(*raw.CPU.PBR)<<16 | uint32(*raw.CPU.PC)
			a.Opcode = *raw.CPU.Opcode
			a.Bytes = raw.CPU.Bytes
		}
		if raw.Source != nil && raw.Source.Space == "rom" {
			a.ROM = true
			a.ROMOffset = raw.Source.Start
		}
		accesses = append(accesses, a)
	}
	return dispatchbus.Verify(instructions, accesses, rom)
}
