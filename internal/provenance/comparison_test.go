package provenance

import (
	"strings"
	"testing"
)

func comparisonFixture() Window {
	events := ordered(fixture())
	for i := range events {
		events[i].Frame = 0
		events[i].PPUFrame = 1
	}
	h := strings.Repeat("a", 64)
	return Window{Schema: "snes-observation-window-v1", Identity: Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"}, From: 0, To: 1, Complete: true, Coverage: WriterCoverage, Events: events, Frames: []FrameIdentity{{Frame: 0, PPUFrame: 1, StartCycle: 1, VBlankCycle: 2, EndCycle: 10, StateSHA256: h, BusSHA256: h, PixelSHA256: h}}}
}

func TestCompare(t *testing.T) {
	for _, tt := range []struct {
		name    string
		change  func(*Window)
		direct  int
		refused bool
	}{
		{"unchanged", func(w *Window) {}, 1, false},
		{"changed version", func(w *Window) {
			for i := range w.Events {
				if w.Events[i].Kind != "dma" {
					w.Events[i].Value = 8
				}
			}
			w.Frames[0].PixelSHA256 = strings.Repeat("b", 64)
		}, 1, false},
		{"intervening writer", func(w *Window) {
			e := w.Events[0]
			e.PC = 0x9000
			w.Events = ordered(append([]Event{w.Events[0], e}, w.Events[1:]...))
		}, 0, false},
		{"missing writer actor", func(w *Window) { w.Events[0].Actor = "" }, 0, false},
		{"missing DMA actor", func(w *Window) { w.Events[2].Actor = "" }, 0, false},
		{"partial", func(w *Window) { w.Complete = false }, 0, true},
		{"filtered", func(w *Window) { w.Coverage = "selected_writes" }, 0, true},
		{"stale ROM", func(w *Window) { w.Identity.ROMSHA256 = strings.Repeat("b", 64) }, 0, true},
		{"stale frame", func(w *Window) { w.Frames[0].PPUFrame++ }, 0, true},
		{"unordered", func(w *Window) { w.Events[2].ID++ }, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, b := comparisonFixture(), comparisonFixture()
			tt.change(&b)
			apin, _ := WindowSHA256(a)
			bpin, _ := WindowSHA256(b)
			out, err := Compare(a, b, apin, bpin, Selection{Frame: 0, RoutineStart: 0x85fc, RoutineEnd: 0x8781, Sprite: 0})
			if (err != nil) != tt.refused {
				t.Fatalf("refusal=%v error=%v", tt.refused, err)
			}
			if err != nil {
				return
			}
			if len(out.Edited.SourceConsumed) != tt.direct || out.CapturedProofEligible {
				t.Fatalf("direct links=%d proof=%v", len(out.Edited.SourceConsumed), out.CapturedProofEligible)
			}
			if !strings.Contains(out.Dependency, "unavailable") {
				t.Fatal("transitive dependency granted")
			}
			if tt.name == "missing writer actor" && out.Edited.OAMLinks[0].Writer != nil {
				t.Fatal("unknown actor promoted")
			}
			if tt.name == "changed version" && (len(out.SourceWriteDifferences) != 1 || len(out.PPUWriteDifferences) != 1 || !out.Frames[0].PixelsChanged) {
				t.Fatal("observed differences missing")
			}
		})
	}
}

func TestCompareStalePin(t *testing.T) {
	w := comparisonFixture()
	pin, _ := WindowSHA256(w)
	w.Events[0].Value++
	if _, err := Compare(w, w, pin, pin, Selection{Frame: 0, RoutineStart: 0x85fc, RoutineEnd: 0x8781}); err == nil {
		t.Fatal("stale event pin accepted")
	}
}

func TestCompareEventFrameBinding(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Window)
	}{
		{"clock before frame", func(w *Window) {
			for i := range w.Events {
				w.Events[i].Cycle = 0
			}
		}},
		{"clock after frame", func(w *Window) {
			for i := range w.Events {
				w.Events[i].Cycle += 100
			}
		}},
		{"wrong PPU frame", func(w *Window) {
			for i := range w.Events {
				w.Events[i].PPUFrame = 99
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, b := comparisonFixture(), comparisonFixture()
			tt.change(&b)
			ap, _ := WindowSHA256(a)
			bp, _ := WindowSHA256(b)
			if _, err := Compare(a, b, ap, bp, Selection{Frame: 0, RoutineStart: 0x85fc, RoutineEnd: 0x8781, Sprite: 0}); err == nil {
				t.Fatal("accepted events outside their declared frame")
			}
		})
	}
}
