package statewrites

import (
	"github.com/tmc/snes/internal/provenance"
	"strings"
	"testing"
)

func window(events []provenance.Event) provenance.Window {
	h := strings.Repeat("a", 64)
	return provenance.Window{Schema: "snes-observation-window-v1", Complete: true, Coverage: provenance.WriterCoverage, To: 2, Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"}, Events: events, Frames: []provenance.FrameIdentity{
		{Frame: 0, PPUFrame: 1, StartCycle: 0, VBlankCycle: 8, EndCycle: 10, StateSHA256: h, BusSHA256: h, PixelSHA256: h},
		{Frame: 1, PPUFrame: 2, StartCycle: 11, VBlankCycle: 18, EndCycle: 20, StateSHA256: h, BusSHA256: h, PixelSHA256: h}}}
}
func event(id uint64, frame int, kind, op, actor string, addr uint32, v uint8) provenance.Event {
	return provenance.Event{ID: id, Frame: frame, PPUFrame: frame + 1, Cycle: uint64(frame*11) + id, Kind: kind, Op: op, Actor: actor, Addr: addr, Value: v, PC: 0x0cc45b}
}
func build(t *testing.T, w provenance.Window) *Timeline {
	t.Helper()
	pin, _ := provenance.WindowSHA256(w)
	out, err := Build(w, pin)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestOrderAliasesAndWords(t *testing.T) {
	w := window([]provenance.Event{
		event(0, 0, "bus", "write", "cpu", 0x001000, 5),
		event(1, 0, "bus", "write", "cpu", 0x801000, 5),
		event(2, 0, "bus", "write", "cpu", 0x7e1001, 8),
		event(3, 0, "wram_port", "write", "dma_or_hdma", 0x1000, 9),
		event(4, 1, "bus", "write", "cpu", 0x7e1000, 10),
	})
	out := build(t, w)
	rows, err := out.Select(0x3f1000, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Ordinal != 0 || rows[1].Ordinal != 1 || rows[2].Ordinal != 3 {
		t.Fatalf("order: %+v", rows)
	}
	if rows[0].Before != nil || rows[1].Before == nil || *rows[1].Before != 5 || rows[2].WriterPC != nil || rows[2].Context != nil {
		t.Fatalf("unknowns: %+v", rows)
	}
	if rows[2].Address != 0x7e1000 || rows[2].RawAddress != 0x1000 {
		t.Fatal("port binding")
	}
	*rows[1].Before = 99
	*rows[1].WriterPC = 0
	again, _ := out.Select(0x7e1000, 0, 2)
	if *again[1].Before != 5 || *again[1].WriterPC != 0x0cc45b || len(again) != 4 {
		t.Fatal("selection leaked mutable pointers")
	}
	high, _ := out.Select(0x7e1001, 0, 2)
	if len(high) != 1 || high[0].Before != nil {
		t.Fatal("word byte collapsed")
	}
}
func TestBeforeReadAndUnknownActor(t *testing.T) {
	out := build(t, window([]provenance.Event{
		event(0, 0, "bus", "read", "cpu", 0x81, 1),
		event(1, 0, "bus", "write", "cpu", 0x7e0081, 2),
		event(2, 0, "bus", "write", "", 0x81, 3),
		event(3, 0, "bus", "write", "cpu", 0x81, 4),
	}))
	if *out.Writes[0].Before != 1 || *out.Writes[1].Before != 2 || out.Writes[1].WriterPC != nil || out.Writes[2].Before != nil {
		t.Fatalf("before: %+v", out.Writes)
	}
}
func TestRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*provenance.Window)
	}{
		{"unsupported gap", func(w *provenance.Window) { w.Events[1].Kind = "restore" }},
		{"partial", func(w *provenance.Window) { w.Complete = false }},
		{"coverage", func(w *provenance.Window) { w.Coverage = "cpu_only" }},
		{"identity", func(w *provenance.Window) { w.Identity.RunSHA256 = "" }},
		{"order", func(w *provenance.Window) { w.Events[1].ID = 0 }},
		{"frame", func(w *provenance.Window) { w.Events[1].Frame = 2 }},
		{"cycle", func(w *provenance.Window) { w.Events[1].Cycle = 21 }},
		{"port", func(w *provenance.Window) { w.Events[1].Kind = "wram_port"; w.Events[1].Addr = 1 << 17 }},
		{"address", func(w *provenance.Window) { w.Events[1].Addr = 1 << 24 }},
		{"conflict", func(w *provenance.Window) { w.Events[1].Op = "read"; w.Events[1].Value = 9 }},
		{"writerpc", func(w *provenance.Window) { w.Events[1].PC = 1 << 24 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window([]provenance.Event{event(0, 0, "bus", "write", "cpu", 0x81, 1), event(1, 0, "bus", "write", "cpu", 0x81, 2)})
			tt.change(&w)
			pin, _ := provenance.WindowSHA256(w)
			if got, err := Build(w, pin); err == nil || got != nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	w := window(nil)
	if _, err := Build(w, strings.Repeat("b", 64)); err == nil {
		t.Fatal("accepted wrong pin")
	}
	out := build(t, w)
	for _, tt := range []struct {
		addr     uint32
		from, to int
	}{{0x2100, 0, 1}, {0x81, -1, 1}, {0x81, 0, 3}, {0x81, 1, 1}} {
		if _, err := out.Select(tt.addr, tt.from, tt.to); err == nil {
			t.Fatal("accepted invalid selection")
		}
	}
	rows, err := out.Select(0x81, 0, 1)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("measured zero")
	}
}
