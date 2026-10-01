package extractor

import (
	"encoding/json"
	"testing"
)

func TestCoverageIdentityDirtyFiltered(t *testing.T) {
	c := &producerCoverage{summary: producerSummary{ROM: "rom", Engine: "engine", PC: []coverageRange{{Space: "cpu", Start: 0x8000, End: 0x8001}}, Events: []string{"bus", "cpu_insn", "cpu_transition"}}}
	var h producerHeader
	if err := json.Unmarshal([]byte(`{"kind":"run","run":{"rom_sha256":"rom","engine_revision":"engine","engine_dirty":true,"events":["bus","cpu_insn","cpu_transition"],"filters":{"pc_ranges":[{"space":"cpu","start":32768,"end":32769}]}}}`), &h); err != nil {
		t.Fatal(err)
	}
	if err := c.checkHeader(&h); err == nil {
		t.Fatal("dirty PC-filtered header accepted")
	}
	h.Run.Dirty = false
	if err := c.checkHeader(&h); err != nil {
		t.Fatalf("clean filtered header refused: %v", err)
	}
}

func TestCoverageIdentityEngine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame int
		hash  string
	}{{"same_frames", 10, "hash"}, {"shifted_frames", 11, "hash"}, {"empty_hash", 10, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &producerCoverage{summary: producerSummary{Engine: "capture-engine", FrameSummary: []frameStateSummary{{Frame: 10, StateHash: "hash"}}}}
			h := &producerCoverage{summary: producerSummary{Engine: "other-engine", FrameSummary: []frameStateSummary{{Frame: tc.frame, StateHash: tc.hash}}}}
			if err := c.checkEngine(h); err == nil {
				t.Fatal("different engines accepted via frame hashes")
			}
			h.summary.Engine = c.summary.Engine
			if err := c.checkEngine(h); err != nil {
				t.Fatalf("matching engines refused: %v", err)
			}
		})
	}
}
