package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery/analysis/extractor"
)

func TestExtractCommand(t *testing.T) {
	const (
		romPath    = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc"
		tracePath  = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl"
		projectDir = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	)

	if _, err := os.Stat(romPath); err != nil {
		t.Skipf("skipping: ROM not found: %v", err)
	}
	if _, err := os.Stat(tracePath); err != nil {
		t.Skipf("skipping: trace not found: %v", err)
	}
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("skipping: project dir not found: %v", err)
	}

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	args := []string{
		"extract",
		"-project", projectDir,
		"-rom", romPath,
		"-trace", tracePath,
		"-max-spans", "20",
		"-candidate-budget", "150",
		"-out", outDir,
		"-format", "json",
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run extract failed: %v, stderr: %s", err, stderr.String())
	}

	var acct extractor.Accounting
	if err := json.Unmarshal(stdout.Bytes(), &acct); err != nil {
		t.Fatalf("unmarshal stdout JSON: %v, raw stdout: %s", err, stdout.String())
	}

	if acct.StreamSHA256 != extractor.PinnedStreamSHA256 {
		t.Errorf("stream sha256 mismatch: got %s, want %s", acct.StreamSHA256, extractor.PinnedStreamSHA256)
	}
	if acct.ROMSHA256 != extractor.PinnedROMSHA256 {
		t.Errorf("rom sha256 mismatch: got %s, want %s", acct.ROMSHA256, extractor.PinnedROMSHA256)
	}

	// Verify Phase 1: Machine-select and qualify 1 straight-line span of at least 2 distinct physical starts.
	if len(acct.QualifiedSpans) < 1 {
		t.Fatalf("Phase 1 prerequisite failed: want >= 1 qualified span, got %d", len(acct.QualifiedSpans))
	}
	if len(acct.QualifiedSpans[0].PhysicalStarts) < 2 {
		t.Fatalf("Phase 1 prerequisite failed: first span has %d physical starts, want >= 2", len(acct.QualifiedSpans[0].PhysicalStarts))
	}

	// Verify Phase 2: Target of 3 supported spans across 2 physical ROM banks and >= 20 newly qualified physical starts.
	if len(acct.QualifiedSpans) < 3 {
		t.Fatalf("Phase 2 failed: want >= 3 qualified spans, got %d", len(acct.QualifiedSpans))
	}
	if len(acct.UniqueROMBanks) < 2 {
		t.Fatalf("Phase 2 failed: want >= 2 unique ROM banks, got %d (%v)", len(acct.UniqueROMBanks), acct.UniqueROMBanks)
	}
	if len(acct.UniquePhysicalStarts) < 20 {
		t.Fatalf("Phase 2 failed: want >= 20 physical starts, got %d", len(acct.UniquePhysicalStarts))
	}

	// Check exported accounting.json on disk
	acctFile := filepath.Join(outDir, "accounting.json")
	if _, err := os.Stat(acctFile); err != nil {
		t.Errorf("expected accounting.json on disk: %v", err)
	}

	// Check that each qualified span has generated.c and case.json on disk
	for _, q := range acct.QualifiedSpans {
		spanDir := filepath.Join(outDir, "spans", q.BlockID)
		genC := filepath.Join(spanDir, "generated.c")
		caseJSON := filepath.Join(spanDir, "case.json")
		if _, err := os.Stat(genC); err != nil {
			t.Errorf("expected %s on disk: %v", genC, err)
		}
		if _, err := os.Stat(caseJSON); err != nil {
			t.Errorf("expected %s on disk: %v", caseJSON, err)
		}
	}
}
