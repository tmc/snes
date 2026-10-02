package statewrites

import (
	"encoding/hex"
	"fmt"

	"github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/trace"
)

// Context describes observed enclosing dispatch ancestry. Current observation
// windows do not carry ancestry, so Build leaves Context unavailable.
type Context struct {
	Entry   uint32 `json:"entry"`
	Ordinal uint64 `json:"ordinal"`
}

// Write is one byte mutation in raw event order. WriterPC is present only for
// CPU actors; a DMA record's incidental CPU PC is not its writer attribution.
type Write struct {
	Kind       string   `json:"kind"`
	Frame      int      `json:"frame"`
	PPUFrame   int      `json:"ppu_frame"`
	Ordinal    uint64   `json:"ordinal"`
	Cycle      uint64   `json:"cycle"`
	Actor      string   `json:"actor"`
	WriterPC   *uint32  `json:"writer_pc"`
	RawAddress uint32   `json:"raw_address"`
	Address    uint32   `json:"address"`
	Before     *uint8   `json:"before"`
	After      uint8    `json:"after"`
	Context    *Context `json:"context"`
}

// Timeline is a snapshot of the selected observation window, not all game runs.
// From and To are host-relative frames and define a half-open interval.
type Timeline struct {
	Schema       string              `json:"schema"`
	WindowSHA256 string              `json:"window_sha256"`
	Identity     provenance.Identity `json:"identity"`
	From         int                 `json:"from"`
	To           int                 `json:"to"`
	Writes       []Write             `json:"writes"`
	Limitations  []string            `json:"limitations"`
}

func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func physical(e provenance.Event) (uint32, bool, error) {
	if e.Kind != "bus" && e.Kind != "wram_port" {
		return 0, false, nil
	}
	if e.Op != "read" && e.Op != "write" {
		return 0, false, fmt.Errorf("event %d: invalid bus operation", e.ID)
	}
	if e.Kind == "wram_port" {
		if e.Addr >= 1<<17 {
			return 0, false, fmt.Errorf("event %d: invalid WRAM port address", e.ID)
		}
		return 0x7e0000 + e.Addr, true, nil
	}
	if e.Addr >= 1<<24 {
		return 0, false, fmt.Errorf("event %d: invalid bus address", e.ID)
	}
	space, offset := trace.CPUSpace(e.Addr)
	return 0x7e0000 + offset, space == "wram", nil
}

// Build verifies the window's canonical JSON identity and complete producer
// coverage declaration. It never infers coverage from absent accesses. Word
// writes remain separate bus bytes; reads and writes through aliases share the
// same physical byte. A conflicting subsequent read refuses the whole result.
func Build(w provenance.Window, pin string) (*Timeline, error) {
	got, err := provenance.WindowSHA256(w)
	if err != nil {
		return nil, fmt.Errorf("hash window: %w", err)
	}
	if !validSHA(pin) || got != pin {
		return nil, fmt.Errorf("window identity mismatch")
	}
	if w.Schema != "snes-observation-window-v1" || !w.Complete || w.Coverage != provenance.WriterCoverage || w.From < 0 || w.To <= w.From || w.To-w.From > 16 || len(w.Frames) != w.To-w.From || len(w.Events) > 2000000 {
		return nil, fmt.Errorf("unsupported or partial observation coverage")
	}
	for _, s := range []string{w.Identity.ROMSHA256, w.Identity.StateSHA256, w.Identity.InputsSHA256, w.Identity.RunSHA256} {
		if !validSHA(s) {
			return nil, fmt.Errorf("invalid observation identity")
		}
	}
	switch w.Identity.Mode {
	case "original_interpreter", "generated_c", "sprite_data":
	case "recovered_c":
		for _, s := range []string{w.Identity.SourceSHA256, w.Identity.IRSHA256, w.Identity.EditedIRSHA256, w.Identity.PlanSHA256, w.Identity.EditSHA256} {
			if !validSHA(s) {
				return nil, fmt.Errorf("missing recovered source identity")
			}
		}
	default:
		return nil, fmt.Errorf("unsupported observation mode")
	}
	for i, f := range w.Frames {
		if f.Frame != w.From+i || f.StartCycle >= f.VBlankCycle || f.EndCycle < f.VBlankCycle || !validSHA(f.StateSHA256) || !validSHA(f.BusSHA256) || !validSHA(f.PixelSHA256) || i > 0 && f.PPUFrame != w.Frames[i-1].PPUFrame+1 {
			return nil, fmt.Errorf("invalid observation frame")
		}
	}
	out := &Timeline{Schema: "snes-state-writes-v1", WindowSHA256: pin, Identity: w.Identity, From: w.From, To: w.To, Writes: []Write{}, Limitations: []string{
		"writer completeness is a declared producer contract within this window",
		"initial byte values and dispatch ancestry are unavailable",
		"byte accesses do not establish arithmetic, sprite ownership, or pixel causality",
		"frame ordinals are host-relative; ordinal is the raw event ID, not a CPU instruction sequence",
	}}
	values := make(map[uint32]uint8)
	for i, e := range w.Events {
		switch e.Kind {
		case "bus", "wram_port", "ppu", "dma", "hdma":
		default:
			return nil, fmt.Errorf("event %d: unsupported event kind", i)
		}
		if e.ID != uint64(i) || e.Frame < w.From || e.Frame >= w.To || i > 0 && (e.Frame < w.Events[i-1].Frame || e.Cycle < w.Events[i-1].Cycle) {
			return nil, fmt.Errorf("event %d: invalid order", i)
		}
		f := w.Frames[e.Frame-w.From]
		if e.Cycle < f.StartCycle || e.Cycle > f.EndCycle || e.PPUFrame < int(f.PPUFrame) || uint64(e.PPUFrame)-f.PPUFrame > 1 {
			return nil, fmt.Errorf("event %d: outside declared frame", i)
		}
		a, ok, err := physical(e)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if e.Op == "read" {
			if v, exists := values[a]; exists && v != e.Value {
				return nil, fmt.Errorf("event %d: read conflicts with observed byte", i)
			}
			values[a] = e.Value
			continue
		}
		row := Write{Kind: e.Kind, Frame: e.Frame, PPUFrame: e.PPUFrame, Ordinal: e.ID, Cycle: e.Cycle, Actor: e.Actor, RawAddress: e.Addr, Address: a, After: e.Value}
		if v, exists := values[a]; exists {
			before := v
			row.Before = &before
		}
		if e.Actor == "cpu" {
			if e.PC >= 1<<24 {
				return nil, fmt.Errorf("event %d: invalid writer PC", i)
			}
			pc := e.PC
			row.WriterPC = &pc
		}
		out.Writes = append(out.Writes, row)
		if e.Actor == "cpu" || e.Actor == "dma_or_hdma" {
			values[a] = e.Value
		} else {
			delete(values, a)
		}
	}
	return out, nil
}

func clone(w Write) Write {
	if w.Before != nil {
		v := *w.Before
		w.Before = &v
	}
	if w.WriterPC != nil {
		v := *w.WriterPC
		w.WriterPC = &v
	}
	if w.Context != nil {
		v := *w.Context
		w.Context = &v
	}
	return w
}

// Select returns owned writes to one physical byte in [from, to), preserving raw
// event order. Address accepts physical WRAM or a CPU low-RAM mirror. Empty
// results mean measured zero writes in this complete window, not unreachable.
func (t *Timeline) Select(address uint32, from, to int) ([]Write, error) {
	if t == nil || from < t.From || to > t.To || to <= from {
		return nil, fmt.Errorf("selection outside window")
	}
	a, ok, err := physical(provenance.Event{Kind: "bus", Op: "read", Addr: address})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("selection is not WRAM")
	}
	out := []Write{}
	for _, w := range t.Writes {
		if w.Address == a && w.Frame >= from && w.Frame < to {
			out = append(out, clone(w))
		}
	}
	return out, nil
}
