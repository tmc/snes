package extractor

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectedHistoryMetadata(t *testing.T) {
	valid := map[string]any{"schema": 2, "kind": "bus", "id": 1, "cycle": 10, "width": 1, "space": "wram", "op": "write", "addr": 506, "value": 12, "after": 12, "cpu": map[string]any{"pbr": 12, "pc": 50186, "opcode": 32, "bytes": []int{32, 53, 196}, "a": 0, "x": 0, "y": 0, "s": 507, "dp": 0, "dbr": 0, "p": 48, "e": false}}
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong_type_dp", func(e map[string]any) { e["cpu"].(map[string]any)["dp"] = "bad" }},
		{"missing_a", func(e map[string]any) { delete(e["cpu"].(map[string]any), "a") }},
		{"missing_e", func(e map[string]any) { delete(e["cpu"].(map[string]any), "e") }},
		{"schema", func(e map[string]any) { e["schema"] = 1 }},
		{"width", func(e map[string]any) { e["width"] = 2 }},
		{"missing_value", func(e map[string]any) { delete(e, "value"); delete(e, "after") }},
		{"conflicting_value", func(e map[string]any) { e["after"] = 13 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(valid)
			var e map[string]any
			json.Unmarshal(b, &e)
			tt.change(e)
			b, _ = json.Marshal(e)
			h, err := NewWriteHistory(bufio.NewScanner(strings.NewReader(string(b))))
			if err != nil {
				return
			}
			w, ok := h.latestWriteBefore(0x7e01fa, 10)
			if ok && w.Actor == "cpu" {
				t.Fatal("malformed history retained as complete CPU actor")
			}
		})
	}
}
