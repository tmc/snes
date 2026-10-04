package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	if validCard.Timeline[0].ROMOffset != "$04F882" || validCard.Timeline[1].ROMOffset != "$04F884" || validCard.Timeline[2].ROMOffset != "$04F887" {
		t.Errorf("unexpected ROM offsets in timeline: %+v", validCard.Timeline)
	}

	// 2. Tampered artifact digest (corrupted bytes)
	tamperDir := t.TempDir()
	bundleSrc := filepath.Join(projectDir, "evidence", "bundles", "lookup_09f882")
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

func TestProjectLocalAbsentMeansUnavailable(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Empty project directory with no evidence/bundles
	emptyProjectDir := t.TempDir()
	emptySrv := *srv
	emptySrv.ProjectDir = emptyProjectDir

	card := emptySrv.LoadLookupReplayBundle()
	if card.Status != "unavailable" {
		t.Fatalf("expected unavailable status when bundle absent from project dir, got %q", card.Status)
	}
	if card.Reason == "" {
		t.Errorf("expected non-empty reason for absent bundle")
	}
}

func TestTamperedArtifactWithRecomputedManifestRejected(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	fixtureDir := t.TempDir()
	bundleSrc := filepath.Join(projectDir, "evidence", "bundles", "lookup_09f882")
	bundleDst := filepath.Join(fixtureDir, "evidence", "bundles", "lookup_09f882")
	if err := os.MkdirAll(bundleDst, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Copy and modify timeline.json: change bus_value to 99
	timelineBytes, err := os.ReadFile(filepath.Join(bundleSrc, "timeline.json"))
	if err != nil {
		t.Fatalf("read timeline.json: %v", err)
	}
	// Replace "bus_value": 20 with "bus_value": 99
	tamperedTimeline := []byte(fmt.Sprintf("%s", timelineBytes))
	tamperedTimeline = append(tamperedTimeline, []byte("/* tampered */")...)
	newTimelineHash := fmt.Sprintf("%x", sha256.Sum256(tamperedTimeline))

	if err := os.WriteFile(filepath.Join(bundleDst, "timeline.json"), tamperedTimeline, 0644); err != nil {
		t.Fatalf("write tampered timeline.json: %v", err)
	}

	// Copy case.json and receipt.json unchanged
	for _, name := range []string{"case.json", "receipt.json"} {
		b, err := os.ReadFile(filepath.Join(bundleSrc, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(bundleDst, name), b, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Recompute manifest with the new timeline hash so manifest self-consistency passes
	manifestBytes, err := os.ReadFile(filepath.Join(bundleSrc, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest LookupBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	manifest.ArtifactDigests["timeline_sha256"] = newTimelineHash
	updatedManifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDst, "manifest.json"), updatedManifestBytes, 0644); err != nil {
		t.Fatalf("write updated manifest.json: %v", err)
	}

	// Now load with the tampered bundle and recomputed manifest
	tamperSrv := *srv
	tamperSrv.ProjectDir = fixtureDir
	card := tamperSrv.LoadLookupReplayBundle()

	// Must be rejected because content digest violates AcceptedTimelineSHA256
	if card.Status != "unavailable" {
		t.Fatalf("expected unavailable status for tampered artifact with recomputed manifest, got %q", card.Status)
	}
	t.Logf("tamper successfully rejected: %s", card.Reason)
}
