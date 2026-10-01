// Package provenance joins observed WRAM writers to bounded DMA OAM uploads.
package provenance

import (
	"fmt"

	"github.com/tmc/snes/internal/trace"
)

// Event preserves raw bus addresses; physical addresses are separate.
type Event struct {
	ID       uint64 `json:"id"`
	Kind     string `json:"kind"`
	Actor    string `json:"actor,omitempty"`
	Frame    int    `json:"frame"`
	PPUFrame int    `json:"ppu_frame"`
	Cycle    uint64 `json:"cycle"`
	PC       uint32 `json:"pc,omitempty"`
	Op       string `json:"op,omitempty"`
	Addr     uint32 `json:"addr,omitempty"`
	Value    uint8  `json:"value,omitempty"`
	Space    string `json:"space,omitempty"`
	Channel  int    `json:"channel,omitempty"`
	Mode     uint8  `json:"mode,omitempty"`
	Count    int    `json:"count,omitempty"`
	Target   uint8  `json:"target,omitempty"`
}

// Link records a byte version consumed by a DMA transfer and physical OAM write.
type Link struct {
	Writer        *Event `json:"writer,omitempty"`
	Read          Event  `json:"read"`
	RegisterWrite Event  `json:"register_write"`
	OAMWrite      Event  `json:"oam_write"`
	Canonical     uint32 `json:"canonical"`
	Channel       int    `json:"channel"`
	Routine       bool   `json:"routine_last_writer"`
	Status        string `json:"status"`
}

// Result contains bounded observed provenance, never replacement timing claims.
type Result struct {
	RoutineWrites           []Event  `json:"routine_writes"`
	UnconsumedRoutineWrites []Event  `json:"unconsumed_routine_writes"`
	Links                   []Link   `json:"links"`
	Unknown                 []string `json:"unknown"`
}

// Analyze accepts a complete ordered event window produced by the capture CLI.
// Every bus writer and physical WRAM-port write must be present. This is a
// producer contract, not something that absence of records can establish.
// High-table links require adjacent DMA bus read, $2104 write, and physical
// mutation records. Each pair is consumed once. Low-table latch mutations are
// not linked; missing actor metadata or intervening events refuse the join.
func Analyze(events []Event, frame int, start, end uint32) (Result, error) {
	var out Result
	last := map[uint32]Event{}
	var dma *Event
	var read, reg *Event
	var writerAtRead *Event
	remaining := 0
	lastCycle := uint64(0)
	for i, e := range events {
		if e.ID != uint64(i) || (i > 0 && e.Cycle < lastCycle) {
			return out, fmt.Errorf("event %d: invalid order", i)
		}
		lastCycle = e.Cycle
		if e.Kind == "bus" && e.Op == "write" || e.Kind == "wram_port" && e.Op == "write" {
			space, addr := trace.CPUSpace(e.Addr)
			if e.Kind == "wram_port" {
				space = "wram"
				addr = e.Addr
			}
			if space == "wram" {
				last[addr] = e
				if e.Actor == "cpu" && e.Frame == frame && e.PC >= start && e.PC < end {
					out.RoutineWrites = append(out.RoutineWrites, e)
				}
			}
		}
		if e.Kind == "dma" || e.Kind == "hdma" {
			if remaining > 0 {
				out.Unknown = append(out.Unknown, "overlapping transfer")
				dma = nil
				remaining = 0
			}
			dma = nil
			read, reg, writerAtRead = nil, nil, nil
			remaining = 0
			if e.Kind == "dma" && e.Mode == 0 && e.Target == 4 && e.Count > 0 {
				d := e
				dma = &d
				remaining = e.Count
				read = nil
				reg = nil
			} else {
				out.Unknown = append(out.Unknown, "unsupported transfer shape")
			}
			continue
		}
		if dma == nil {
			continue
		}
		if e.Kind == "bus" {
			if e.Actor != "dma_or_hdma" || e.Channel != dma.Channel {
				out.Unknown = append(out.Unknown, "DMA actor/channel mismatch")
				dma = nil
				continue
			}
			if remaining == 0 {
				dma = nil
				continue
			}
			if e.Op == "read" {
				if read != nil && reg == nil {
					out.Unknown = append(out.Unknown, "multiple reads before OAM write")
					dma = nil
					continue
				}
				expected := dma.Addr&0xff0000 | uint32(uint16(dma.Addr+uint32(dma.Count-remaining)))
				if e.Addr != expected {
					out.Unknown = append(out.Unknown, "unexpected DMA source address")
					dma = nil
					continue
				}
				writerAtRead = nil
				space, a := trace.CPUSpace(e.Addr)
				if space == "wram" {
					if w, ok := last[a]; ok {
						v := w
						writerAtRead = &v
					}
				}
				reg = nil
				v := e
				read = &v
			} else if e.Op == "write" {
				_, a := trace.CPUSpace(e.Addr)
				if a != 0x2104 || read == nil || reg != nil || read.Value != e.Value || e.ID != read.ID+1 || e.Frame != read.Frame || e.PPUFrame != read.PPUFrame {
					out.Unknown = append(out.Unknown, "DMA bus pair mismatch")
					dma = nil
					continue
				}
				v := e
				reg = &v
				remaining--
			}
		}
		if e.Kind == "ppu" && e.Space == "oam" && e.Addr >= 512 {
			if read == nil || reg == nil || reg.Value != e.Value || e.Op != "write" || e.Addr >= 544 || e.ID != reg.ID+1 || e.Frame != reg.Frame || e.PPUFrame != reg.PPUFrame {
				out.Unknown = append(out.Unknown, "missing DMA register/read pair")
				dma = nil
				continue
			}
			space, a := trace.CPUSpace(read.Addr)
			link := Link{Read: *read, RegisterWrite: *reg, OAMWrite: e, Canonical: a, Channel: dma.Channel, Status: "unknown"}
			if space == "wram" {
				if w := writerAtRead; w != nil && w.Value == read.Value && last[a].ID == w.ID {
					v := *w
					link.Writer = &v
					link.Status = "observed_last_writer"
					link.Routine = w.Actor == "cpu" && w.Frame == frame && w.PC >= start && w.PC < end
				} else {
					out.Unknown = append(out.Unknown, "missing or mismatched WRAM writer")
				}
			}
			out.Links = append(out.Links, link)
			read, reg, writerAtRead = nil, nil, nil

		}
	}
	if remaining > 0 {
		out.Unknown = append(out.Unknown, "incomplete DMA transfer")
	}
	consumed := map[uint64]bool{}
	for _, l := range out.Links {
		if l.Routine && l.Writer != nil {
			consumed[l.Writer.ID] = true
		}
	}
	for _, w := range out.RoutineWrites {
		if !consumed[w.ID] {
			out.UnconsumedRoutineWrites = append(out.UnconsumedRoutineWrites, w)
		}
	}
	if len(out.Links) == 0 {
		out.Unknown = append(out.Unknown, "no observed high-OAM consumption")
	}
	return out, nil
}
