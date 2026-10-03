package cworkbench

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func capturedFixture(t *testing.T) Config {
	t.Helper()
	c := fixture(t)
	sha := func(s string) string { return hash([]byte(s)) }
	write := func(name string, value any) Input {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(filepath.Dir(c.NotesPath), name)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return Input{Path: path, SHA256: hash(b)}
	}
	rom, runner, rev, digest, caseHash := sha("rom"), sha("runner"), "revision", sha("admission"), sha("case")
	c.Receipt = write("report.json", map[string]any{
		"schema": "snes-connected-queue-v1", "status": "qualified", "source_sha256": c.Source.SHA256, "region_sha256": c.IR.SHA256,
		"rom_sha256": rom, "revision": rev, "routine_id": "sub_008000", "runner_hash": runner, "profile_sha256": sha("profile"),
		"cases": 1, "admitted": 1, "matched": 1, "refused": 0, "mismatched": 0, "unexecuted": 0,
	})
	writes := []Write{{Address: 0x7e0001, Value: 7}, {Address: 0x7e0002, Value: 8}}
	caseFile := write("cases.json", []any{map[string]any{
		"case_id": "case-1", "case_hash": caseHash, "admission_digest": digest, "routine_id": "sub_008000", "rom_sha256": rom,
		"run_id": "run-1", "stream_sha256": sha("stream"), "frame": 3, "entry_seq": 100, "exit_seq": 104,
		"instruction_count": 4, "return_insn_pc": 0x008008, "observed_next_pc": 0x008009, "observed_writes": writes,
	}})
	receiptFile := write("receipts.json", []any{map[string]any{
		"case_id": "case-1", "case_hash": caseHash, "admission_digest": digest, "block_id": "sub_008000",
		"matched": true, "eligible": true, "captured_proof_eligible": true, "effects_match": true,
		"observed_match": true, "emulator_match": true, "c_match": true,
		"case_identity":  map[string]any{"case_id": "case-1", "run_id": "run-1", "stream_sha256": sha("stream"), "rom_sha256": rom, "frame": 3, "entry_seq": 100, "exit_seq": 104, "observed_next_pc": 0x008009},
		"metadata":       map[string]any{"runner_hash": runner, "generated_c_hash": c.Source.SHA256, "rom_sha256": rom, "project_revision": rev},
		"trace_observed": map[string]any{"next_pc": 0x008009, "writes": writes}, "compiled_c": map[string]any{"next_pc": 0x008009, "writes": writes},
	}})
	c.CapturedCases = []CaseFiles{{ReportSHA256: c.Receipt.SHA256, Cases: caseFile, Receipts: receiptFile}}
	return c
}

func mutateJSON(t *testing.T, input *Input, change func(any)) {
	t.Helper()
	b, err := os.ReadFile(input.Path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	change(value)
	b, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input.Path, b, 0600); err != nil {
		t.Fatal(err)
	}
	input.SHA256 = hash(b)
}

func TestCapturedCases(t *testing.T) {
	c := capturedFixture(t)
	w, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.model.CapturedCases) != 1 || len(w.model.CapturedCases[0].Cases) != 1 {
		t.Fatalf("cases: %+v", w.model.CapturedCases)
	}
	got := w.model.CapturedCases[0].Cases[0]
	if got.Frame != 3 || got.EntrySeq != 100 || got.ReturnPC != 0x008008 || !sameWrites(got.ObservedWrites, got.CWrites) {
		t.Fatalf("case: %+v", got)
	}
	if w.model.Reports[0].ROMSHA256 == "" || w.model.Reports[0].Revision != "revision" || w.model.Reports[0].ProfileSHA256 == "" {
		t.Fatal("report pins missing")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/model", nil)
	out := httptest.NewRecorder()
	w.Handler().ServeHTTP(out, r)
	if out.Code != 200 || !json.Valid(out.Body.Bytes()) {
		t.Fatalf("model: %d %s", out.Code, out.Body.String())
	}
}

func TestCapturedCaseRefusals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*testing.T, *Config)
	}{
		{"wrong report", func(t *testing.T, c *Config) { c.CapturedCases[0].ReportSHA256 = hash([]byte("other")) }},
		{"duplicate report", func(t *testing.T, c *Config) { c.CapturedCases = append(c.CapturedCases, c.CapturedCases[0]) }},
		{"case count", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.Receipt, func(v any) { v.(map[string]any)["cases"] = 2 })
			c.CapturedCases[0].ReportSHA256 = c.Receipt.SHA256
		}},
		{"wrong source", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.Receipt, func(v any) { v.(map[string]any)["source_sha256"] = hash([]byte("other")) })
			c.CapturedCases[0].ReportSHA256 = c.Receipt.SHA256
		}},
		{"missing profile pin", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.Receipt, func(v any) { delete(v.(map[string]any), "profile_sha256") })
			c.CapturedCases[0].ReportSHA256 = c.Receipt.SHA256
		}},
		{"wrong revision", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Receipts, func(v any) { v.([]any)[0].(map[string]any)["metadata"].(map[string]any)["project_revision"] = "other" })
		}},
		{"case identity", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Receipts, func(v any) { v.([]any)[0].(map[string]any)["case_id"] = "other" })
		}},
		{"runner identity", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Receipts, func(v any) {
				v.([]any)[0].(map[string]any)["metadata"].(map[string]any)["runner_hash"] = hash([]byte("other"))
			})
		}},
		{"stale receipt", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Receipts, func(v any) { v.([]any)[0].(map[string]any)["metadata"].(map[string]any)["is_stale"] = true })
		}},
		{"ordered writes", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Receipts, func(v any) {
				w := v.([]any)[0].(map[string]any)["compiled_c"].(map[string]any)["writes"].([]any)
				w[0], w[1] = w[1], w[0]
			})
		}},
		{"unknown return marker", func(t *testing.T, c *Config) {
			mutateJSON(t, &c.CapturedCases[0].Cases, func(v any) { v.([]any)[0].(map[string]any)["return_insn_pc"] = 0x008020 })
		}},
		{"tampered cases", func(t *testing.T, c *Config) { os.WriteFile(c.CapturedCases[0].Cases.Path, []byte("[]"), 0600) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := capturedFixture(t)
			tt.change(t, &c)
			if _, err := Open(c); err == nil {
				t.Fatal("accepted inconsistent captured case")
			}
		})
	}
}
