package extractor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProducerCoverageExplicitGzipSink(t *testing.T) {
	for _, tt := range []struct {
		name        string
		compression string
		container   string
		receipt     string
		logical     string
		ok          bool
	}{
		{"raw sink", "gzip", "raw", "raw", "decoded", true},
		{"logical receipt", "gzip", "raw", "decoded", "decoded", true},
		{"missing container pin", "gzip", "", "raw", "decoded", false},
		{"wrong container pin", "gzip", "other", "raw", "decoded", false},
		{"wrong receipt", "gzip", "raw", "other", "decoded", false},
		{"wrong logical stream", "gzip", "raw", "raw", "raw", false},
		{"no declared encoding", "", "raw", "raw", "decoded", false},
		{"unsupported encoding", "zip", "raw", "raw", "decoded", false},
		{"plain but encoded bytes", "none", "", "decoded", "decoded", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			summary := map[string]any{
				"rom_hash": "rom", "emulator": "engine", "trace_hash": tt.logical,
				"trace_compression": tt.compression, "trace_compressed_hash": tt.container,
				"event_kinds": []string{"bus", "mmio", "cpu_insn", "cpu_transition", "dma", "hdma"},
			}
			sp, rp := filepath.Join(dir, "summary.json"), filepath.Join(dir, "receipt.json")
			for path, value := range map[string]any{sp: summary, rp: ReceiptData{Schema: 2, Outcome: "complete", StreamSHA256: tt.receipt}} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadProducerCoverage(sp, rp, "raw", "decoded", "rom", true)
			if (err == nil) != tt.ok {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, tt.ok, err)
			}
		})
	}
}
