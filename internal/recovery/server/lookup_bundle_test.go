package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupBundleTamperAndMismatch(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Valid bundle load
	validCard := srv.LoadLookupReplayBundle()
	if validCard.Status != "available" {
		t.Fatalf("expected valid bundle status available, got %q (reason: %s)", validCard.Status, validCard.Reason)
	}
	if validCard.BaselineInput != 115 || validCard.BaselineOutput != 20 {
		t.Errorf("unexpected baseline values: %+v", validCard)
	}
	if len(validCard.Timeline) != 3 || len(validCard.Cases) != 3 {
		t.Errorf("unexpected counts: timeline=%d cases=%d", len(validCard.Timeline), len(validCard.Cases))
	}

	// 2. Tampered artifact digest
	tamperDir := t.TempDir()
	bundleSrc := filepath.Join("..", "..", "..", "evidence", "bundles", "lookup_09f882")
	bundleDst := filepath.Join(tamperDir, "evidence", "bundles", "lookup_09f882")
	if err := os.MkdirAll(bundleDst, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	for _, name := range []string{"manifest.json", "case.json", "timeline.json", "receipt.json"} {
		content, err := os.ReadFile(filepath.Join(bundleSrc, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if name == "case.json" {
			// Alter 1 byte in case.json to trigger tamper detection
			content = append(content, ' ')
		}
		if err := os.WriteFile(filepath.Join(bundleDst, name), content, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	tamperSrv := *srv
	tamperSrv.ProjectDir = tamperDir
	tamperCard := tamperSrv.LoadLookupReplayBundle()
	if tamperCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for tampered case.json, got %q", tamperCard.Status)
	}

	// 3. Mismatched ROM SHA
	mismatchedROMSrv := *srv
	mismatchedDoc := *srv.Document
	mismatchedDoc.ROM.NormalizedSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	mismatchedROMSrv.Document = &mismatchedDoc

	romMismatchCard := mismatchedROMSrv.LoadLookupReplayBundle()
	if romMismatchCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for mismatched ROM SHA, got %q", romMismatchCard.Status)
	}
}
