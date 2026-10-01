package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputReservationExclusivity(t *testing.T) {
	d := t.TempDir()
	outDir := filepath.Join(d, "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}

	sentinelFile := filepath.Join(outDir, "receipt.json")
	sentinelContent := []byte(`{"sentinel": true, "immutable": "evidence"}`)
	if err := os.WriteFile(sentinelFile, sentinelContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Invoking run with -out pointing to an existing directory must fail.
	args := []string{
		"-candidate", filepath.Join(d, "cand.json"),
		"-fixture", filepath.Join(d, "fix.jsonl"),
		"-capture", filepath.Join(d, "cap.jsonl"),
		"-history", filepath.Join(d, "hist.jsonl"),
		"-rom", filepath.Join(d, "rom.sfc"),
		"-out", outDir,
	}

	// Create dummy inputs so it reaches outDir validation if cand is read.
	_ = os.WriteFile(filepath.Join(d, "cand.json"), []byte(`{"id":"p","kind":"leaf","entry":32768,"instruction_count":1}`), 0644)
	_ = os.WriteFile(filepath.Join(d, "fix.jsonl"), []byte(`{"run":{"engine_revision":"x"}}`), 0644)
	_ = os.WriteFile(filepath.Join(d, "cap.jsonl"), []byte(``), 0644)
	_ = os.WriteFile(filepath.Join(d, "hist.jsonl"), []byte(``), 0644)
	_ = os.WriteFile(filepath.Join(d, "rom.sfc"), make([]byte, 32768), 0644)

	err := run(args)
	if err == nil {
		t.Fatal("expected error when output directory already exists, got nil")
	}

	// Assert sentinel file is byte-identical and untouched.
	afterBytes, err := os.ReadFile(sentinelFile)
	if err != nil {
		t.Fatalf("read sentinel after run: %v", err)
	}
	if !bytes.Equal(afterBytes, sentinelContent) {
		t.Fatalf("sentinel file was overwritten or modified!\ngot:  %s\nwant: %s", afterBytes, sentinelContent)
	}
}
