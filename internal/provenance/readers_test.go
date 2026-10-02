package provenance

import (
	"strings"
	"testing"
)

func readerWindow(events ...Event) Window {
	w := comparisonFixture()
	w.Events = ordered(events)
	w.Frames[0].EndCycle = 100
	for i := range w.Events {
		w.Events[i].Frame = 0
		w.Events[i].PPUFrame = 1
	}
	return w
}

func TestReadFrontier(t *testing.T) {
	write := Event{Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x1f05, Value: 7, PC: 0xcc46e}
	read := Event{Kind: "bus", Actor: "cpu", Op: "read", Addr: 0x801f05, Value: 7, PC: 0xc8000}
	for _, tt := range []struct {
		name   string
		events []Event
		reads  int
		stop   string
		status string
	}{
		{"mirrored CPU read", []Event{write, read}, 1, "window_end", "observed_byte_version"},
		{"DMA read", []Event{write, {Kind: "bus", Actor: "dma_or_hdma", Op: "read", Addr: 0x7e1f05, Value: 7}}, 1, "window_end", "observed_byte_version"},
		{"port read", []Event{write, {Kind: "wram_port", Actor: "cpu", Op: "read", Addr: 0x1f05, Value: 7}}, 1, "window_end", "observed_byte_version"},
		{"same-value replacement", []Event{write, read, write, read}, 1, "overwritten", "observed_byte_version"},
		{"unknown reader", []Event{write, {Kind: "bus", Op: "read", Addr: 0x1f05, Value: 7}}, 1, "window_end", "unknown_actor"},
		{"different cell", []Event{write, {Kind: "bus", Actor: "cpu", Op: "read", Addr: 0x7f1f05, Value: 7}}, 0, "window_end", ""},
		{"no read", []Event{write}, 0, "window_end", ""},
		{"port writer", []Event{{Kind: "wram_port", Actor: "cpu", Op: "write", Addr: 0x1f05, Value: 7}, read}, 1, "window_end", "observed_byte_version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := readerWindow(tt.events...)
			pin, _ := WindowSHA256(w)
			got, err := ReadFrontier(w, pin, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Readers) != tt.reads || got.Termination != tt.stop || got.PhysicalAddress != 0x7e1f05 || got.CapturedProofEligible {
				t.Fatalf("frontier: %+v", got)
			}
			if tt.reads != 0 && got.Readers[0].Status != tt.status {
				t.Fatal(got.Readers[0].Status)
			}
			if (got.Replacement != nil) != (tt.stop == "overwritten") {
				t.Fatal("replacement identity lost")
			}
		})
	}
}

func TestReadFrontierRefusals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Window)
		id     uint64
	}{
		{"missing writer", func(*Window) {}, 100},
		{"read selected", func(*Window) {}, 1},
		{"unknown writer", func(w *Window) { w.Events[0].Actor = "" }, 0},
		{"non-WRAM writer", func(w *Window) { w.Events[0].Addr = 0x2104 }, 0},
		{"conflicting byte", func(w *Window) { w.Events[1].Value++ }, 0},
		{"filtered", func(w *Window) { w.Coverage = "selected_writes" }, 0},
		{"partial", func(w *Window) { w.Complete = false }, 0},
		{"wrong event order", func(w *Window) { w.Events[1].ID++ }, 0},
		{"clock outside frame", func(w *Window) { w.Events[1].Cycle = 1000 }, 0},
		{"invalid bus address", func(w *Window) { w.Events[1].Addr = 1 << 24 }, 0},
		{"invalid port address", func(w *Window) { w.Events[1].Kind = "wram_port"; w.Events[1].Addr = 1 << 17 }, 0},
		{"invalid operation", func(w *Window) { w.Events[1].Op = "fetch" }, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := readerWindow(Event{Kind: "bus", Actor: "cpu", Op: "write", Addr: 1, Value: 7}, Event{Kind: "bus", Actor: "cpu", Op: "read", Addr: 1, Value: 7})
			tt.change(&w)
			pin, _ := WindowSHA256(w)
			got, err := ReadFrontier(w, pin, tt.id)
			if err == nil || got.Schema != "" {
				t.Fatalf("accepted or partially published: %+v, %v", got, err)
			}
		})
	}
	// A malformed event beyond an overwrite must still be rejected.
	w := readerWindow(Event{Kind: "bus", Actor: "cpu", Op: "write", Addr: 1}, Event{Kind: "bus", Actor: "cpu", Op: "write", Addr: 1}, Event{Kind: "bus", Op: "read", Addr: 1 << 24})
	pin, _ := WindowSHA256(w)
	if _, err := ReadFrontier(w, pin, 0); err == nil {
		t.Fatal("accepted malformed event after replacement")
	}
	w = comparisonFixture()
	if _, err := ReadFrontier(w, strings.Repeat("b", 64), 0); err == nil {
		t.Fatal("accepted stale pin")
	}
}
