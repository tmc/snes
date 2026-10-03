package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentHeaderBindingControls(t *testing.T) {
	base, err := os.ReadFile(filepath.Join(reviewAuthenticBase, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported trace schema", func(h map[string]any) { h["schema"] = float64(1) }},
		{"same ROM foreign execution header", func(h map[string]any) {
			r := h["run"].(map[string]any)
			r["engine_revision"] = "foreign-engine"
			r["start"] = "foreign-savestate"
			r["initial_state_sha256"] = "different-state"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := bytes.IndexByte(base, '\n')
			var h map[string]any
			if err := json.Unmarshal(base[:i], &h); err != nil {
				t.Fatal(err)
			}
			tt.mutate(h)
			head, _ := json.Marshal(h)
			raw := append(append(head, '\n'), base[i+1:]...)
			project := reviewFixture(t, raw, false, true)
			status, body := reviewHTTP(t, project)
			if status == 200 {
				t.Errorf("unqualified header accepted200: %.300s", body)
			} else {
				t.Logf("rejected%d", status)
			}
		})
	}
}
