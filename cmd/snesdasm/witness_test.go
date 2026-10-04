package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/recovery/analysis"
)

func TestWitnessCommand(t *testing.T) {
	const (
		romPath   = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc"
		tracePath = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl"
	)

	if _, err := os.Stat(romPath); err != nil {
		t.Skipf("skipping: ROM not found: %v", err)
	}
	if _, err := os.Stat(tracePath); err != nil {
		t.Skipf("skipping: trace not found: %v", err)
	}

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer

	// Test cumulative edge 2 (default)
	args := []string{
		"witness",
		"-rom", romPath,
		"-trace", tracePath,
		"-edge", "2",
		"-out", outDir,
		"-format", "json",
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run witness failed: %v, stderr: %s", err, stderr.String())
	}

	var report analysis.WitnessRecoveryReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal stdout JSON: %v", err)
	}

	if report.EdgeIndex != 2 {
		t.Errorf("expected EdgeIndex=2, got %d", report.EdgeIndex)
	}
	if report.SourceAddress != "$0087BD" || report.TargetAddress != "$0CC404" {
		t.Errorf("unexpected edge: %s -> %s", report.SourceAddress, report.TargetAddress)
	}
	if report.BaselinePhysicalStarts != 79 || report.WitnessPhysicalStarts != 107 || report.DeltaPhysicalStarts != 28 {
		t.Errorf("unexpected starts: baseline=%d, witness=%d, delta=%d",
			report.BaselinePhysicalStarts, report.WitnessPhysicalStarts, report.DeltaPhysicalStarts)
	}
	if len(report.AddedPhysicalOffsets) != 28 {
		t.Errorf("expected 28 added offsets, got %d", len(report.AddedPhysicalOffsets))
	}

	// Verify artifact files written to outDir
	for _, f := range []string{"receipt.json", "frontier.json"} {
		p := filepath.Join(outDir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output file %s not found: %v", f, err)
		}
	}
}
