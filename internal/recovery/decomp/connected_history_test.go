package decomp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectedHistoryMetadata(t *testing.T) {
	valid := map[string]any{"schema": 2, "kind": "bus", "id": 1, "cycle": 10, "width": 1, "space": "wram", "op": "write", "addr": 506, "value": 12, "after": 12, "cpu": map[string]any{"pbr": 12, "pc": 50186, "opcode": 32, "bytes": []int{32, 53, 196}, "a": 0, "x": 0, "y": 0, "s": 507, "dp": 0, "dbr": 0, "p": 48, "e": false}}
	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"valid", func(e map[string]any) {}, "cpu"},
		{"missing_a", func(e map[string]any) { delete(e["cpu"].(map[string]any), "a") }, "unknown"},
		{"missing_dp", func(e map[string]any) { delete(e["cpu"].(map[string]any), "dp") }, "unknown"},
		{"missing_e", func(e map[string]any) { delete(e["cpu"].(map[string]any), "e") }, "unknown"},
		{"schema", func(e map[string]any) { e["schema"] = 1 }, "unknown"},
		{"width", func(e map[string]any) { e["width"] = 2 }, "unknown"},
		{"missing_value", func(e map[string]any) { delete(e, "value"); delete(e, "after") }, "unknown"},
		{"conflicting_value", func(e map[string]any) { e["after"] = 13 }, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(valid)
			var e map[string]any
			json.Unmarshal(b, &e)
			tt.change(e)
			b, _ = json.Marshal(e)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "history.jsonl"), append(b, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			v := NewEvidenceVerifier(root)
			h, err := v.loadHistory("history.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			w := h[0x7e01fa]
			if len(w) != 1 || w[0].Actor != tt.want {
				t.Fatalf("history actor: %+v; want %s", w, tt.want)
			}
		})
	}
}

func TestConnectedHistoryMalformedLatest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "history.jsonl")
	data := `{ "kind":"bus", "op":"write", "space":"wram", "addr":506, "id":2, "cycle":11, "value":12, "cpu":{"dp":"bad"} }`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	v := NewEvidenceVerifier(root)
	if _, err := v.loadHistory("history.jsonl"); err == nil {
		t.Fatal("malformed latest write silently skipped")
	}
}

func TestConnectedHistoryContext(t *testing.T) {
	e := cpuStateWithCycles{A: 1, X: 2, Y: 3, D: 4, DB: 5, P: 6, E: false}
	w := historyWrite{CPUA: 1, CPUX: 2, CPUY: 3, CPUD: 4, CPUDB: 5, CPUP: 6, CPUE: false}
	if !connectedHistoryContextMatches(w, e) {
		t.Fatal("matching context refused")
	}
	for _, tt := range []struct {
		name   string
		mutate func(*historyWrite)
	}{{"A", func(w *historyWrite) { w.CPUA++ }}, {"X", func(w *historyWrite) { w.CPUX++ }}, {"Y", func(w *historyWrite) { w.CPUY++ }}, {"D", func(w *historyWrite) { w.CPUD++ }}, {"DB", func(w *historyWrite) { w.CPUDB++ }}, {"P", func(w *historyWrite) { w.CPUP++ }}, {"E", func(w *historyWrite) { w.CPUE = true }}} {
		t.Run(tt.name, func(t *testing.T) {
			bad := w
			tt.mutate(&bad)
			if connectedHistoryContextMatches(bad, e) {
				t.Fatal("changed context accepted")
			}
		})
	}
}

func TestConnectedBoundaryControls(t *testing.T) {
	high := historyWrite{ID: 10, Cycle: 100, CPUBytes: []byte{0x20, 0x35, 0xc4}}
	low := historyWrite{ID: 11, Cycle: 101, CPUBytes: []byte{0x20, 0x35, 0xc4}}
	t.Run("IDs_across_cycles", func(t *testing.T) {
		if !connectedHistoryOrder(high, low) {
			t.Fatal("valid order refused")
		}
		for _, id := range []uint64{9, 10} {
			bad := low
			bad.ID = id
			if connectedHistoryOrder(high, bad) {
				t.Fatal("reversed or duplicate ID accepted")
			}
		}
	})
	t.Run("caller_bytes_HIGH_LOW", func(t *testing.T) {
		rom := []byte{0x20, 0x35, 0xc4}
		if !connectedCallerBytes(high, low, rom) {
			t.Fatal("valid bytes refused")
		}
		for _, side := range []int{0, 1} {
			h, l := high, low
			if side == 0 {
				h.CPUBytes = []byte{0x20, 0x34, 0xc4}
			} else {
				l.CPUBytes = []byte{0x20, 0x34, 0xc4}
			}
			if connectedCallerBytes(h, l, rom) {
				t.Fatal("wrong caller operand accepted")
			}
		}
	})
	t.Run("adjacent_cycles", func(t *testing.T) {
		a := cpuStateWithCycles{Cycles: 10}
		b := a
		if !connectedContinuity(a, b) {
			t.Fatal("exact join refused")
		}
		b.Cycles++
		if connectedContinuity(a, b) {
			t.Fatal("cycle gap accepted")
		}
		b.Cycles -= 2
		if connectedContinuity(a, b) {
			t.Fatal("cycle overlap accepted")
		}
	})
	t.Run("entry_cutoff", func(t *testing.T) {
		hist := []historyWrite{{Cycle: 9, ID: 1}, {Cycle: 11, ID: 2}}
		selected := latestConnectedWrite(hist, 10)
		if selected == nil || selected.ID != 1 {
			t.Fatal("post-entry write selected")
		}
	})
}
