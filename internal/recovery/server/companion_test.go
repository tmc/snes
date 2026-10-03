package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignedWordCompanion_NaturalProducer(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer(%s): %v", projectDir, err)
	}
	if srv.SignedWords == nil {
		t.Fatalf("expected srv.SignedWords to be populated")
	}

	mux := srv.mux

	// 1. Positive Anchor (09:F88F, retirement 52100)
	t.Run("positive_anchor_09F88F", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/occurrence?addr=09F88F&trace_frame=1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep OccurrenceReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected status available, got %s (reason: %s)", rep.Status, rep.Reason)
		}
		if rep.RetirementID != 52100 {
			t.Errorf("expected retirement 52100, got %d", rep.RetirementID)
		}
		if rep.Seq != 13205 {
			t.Errorf("expected seq 13205, got %d", rep.Seq)
		}

		comp := rep.Companion
		if comp == nil {
			t.Fatalf("expected companion to be attached to 09:F88F")
		}

		if comp.CaseID != "positive" {
			t.Errorf("expected case_id positive, got %s", comp.CaseID)
		}
		if comp.WordHex != "0014" || comp.SignedValue != 20 {
			t.Errorf("expected word 0014 / +20, got %s / %d", comp.WordHex, comp.SignedValue)
		}
		if comp.PhysicalWordAddr != "7E:1F54" {
			t.Errorf("expected physical word addr 7E:1F54, got %s", comp.PhysicalWordAddr)
		}

		// Low-byte store check (prior observed store outside replay)
		if comp.LowByteStore.Address != "7E:1F54" || comp.LowByteStore.Value != 20 || comp.LowByteStore.RecordID != 52086 {
			t.Errorf("unexpected low-byte store: %+v", comp.LowByteStore)
		}

		// High-byte store check (current selected store)
		if comp.HighByteStore.Address != "7E:1F55" || comp.HighByteStore.Value != 0 || comp.HighByteStore.RecordID != 52100 {
			t.Errorf("unexpected high-byte store: %+v", comp.HighByteStore)
		}

		// Architectural registers separate from memory word
		if comp.ExitA != 0xFF00 {
			t.Errorf("expected Exit A $FF00, got $%04X", comp.ExitA)
		}
		if comp.ExitP != 0x32 {
			t.Errorf("expected Exit P $32, got $%02X", comp.ExitP)
		}

		// Walkthrough check (4 rows)
		if len(comp.Walkthrough) != 4 {
			t.Fatalf("expected 4 walkthrough rows, got %d", len(comp.Walkthrough))
		}
		if comp.Walkthrough[0].Mnemonic != "CMP #$80" || comp.Walkthrough[0].RecordID != 52089 {
			t.Errorf("walkthrough row 0: %+v", comp.Walkthrough[0])
		}
		if comp.Walkthrough[1].Mnemonic != "SBC $54" || comp.Walkthrough[1].RecordID != 52093 {
			t.Errorf("walkthrough row 1: %+v", comp.Walkthrough[1])
		}
		if comp.Walkthrough[2].Mnemonic != "EOR #$FF" || comp.Walkthrough[2].RecordID != 52096 {
			t.Errorf("walkthrough row 2: %+v", comp.Walkthrough[2])
		}
		if comp.Walkthrough[3].Mnemonic != "STA $55" || comp.Walkthrough[3].RecordID != 52100 {
			t.Errorf("walkthrough row 3: %+v", comp.Walkthrough[3])
		}

		// Qualification check
		if comp.Qualification.Status != "verified" {
			t.Errorf("expected qualification verified, got %s (reason: %s)", comp.Qualification.Status, comp.Qualification.Reason)
		}
		if !comp.Qualification.DifferentialMatched {
			t.Errorf("expected differential matched")
		}
		if comp.Qualification.RunnerBinaryHash == "" || comp.Qualification.GeneratedCHash == "" {
			t.Errorf("missing hashes in qualification: %+v", comp.Qualification)
		}
	})

	// 2. Negative Anchor (09:F89C, retirement 52123)
	t.Run("negative_anchor_09F89C", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/occurrence?addr=09F89C&trace_frame=1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep OccurrenceReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected status available, got %s (reason: %s)", rep.Status, rep.Reason)
		}
		if rep.RetirementID != 52123 {
			t.Errorf("expected retirement 52123, got %d", rep.RetirementID)
		}
		if rep.Seq != 13211 {
			t.Errorf("expected seq 13211, got %d", rep.Seq)
		}

		comp := rep.Companion
		if comp == nil {
			t.Fatalf("expected companion to be attached to 09:F89C")
		}

		if comp.CaseID != "negative" {
			t.Errorf("expected case_id negative, got %s", comp.CaseID)
		}
		if comp.WordHex != "FFC3" || comp.SignedValue != -61 {
			t.Errorf("expected word FFC3 / -61, got %s / %d", comp.WordHex, comp.SignedValue)
		}
		if comp.PhysicalWordAddr != "7E:1F56" {
			t.Errorf("expected physical word addr 7E:1F56, got %s", comp.PhysicalWordAddr)
		}

		// Low-byte store check
		if comp.LowByteStore.Address != "7E:1F56" || comp.LowByteStore.Value != 195 || comp.LowByteStore.RecordID != 52109 {
			t.Errorf("unexpected low-byte store: %+v", comp.LowByteStore)
		}

		// High-byte store check
		if comp.HighByteStore.Address != "7E:1F57" || comp.HighByteStore.Value != 255 || comp.HighByteStore.RecordID != 52123 {
			t.Errorf("unexpected high-byte store: %+v", comp.HighByteStore)
		}

		// Architectural registers: Exit A $FFFF, Exit P $B1 (177)
		if comp.ExitA != 0xFFFF {
			t.Errorf("expected Exit A $FFFF, got $%04X", comp.ExitA)
		}
		if comp.ExitP != 0xB1 {
			t.Errorf("expected Exit P $B1 (177), got $%02X (%d)", comp.ExitP, comp.ExitP)
		}

		// Walkthrough check (4 rows)
		if len(comp.Walkthrough) != 4 {
			t.Fatalf("expected 4 walkthrough rows, got %d", len(comp.Walkthrough))
		}
		if comp.Walkthrough[0].Mnemonic != "CMP #$80" || comp.Walkthrough[0].RecordID != 52112 {
			t.Errorf("walkthrough row 0: %+v", comp.Walkthrough[0])
		}
		if comp.Walkthrough[1].Mnemonic != "SBC $56" || comp.Walkthrough[1].RecordID != 52116 {
			t.Errorf("walkthrough row 1: %+v", comp.Walkthrough[1])
		}
		if comp.Walkthrough[2].Mnemonic != "EOR #$FF" || comp.Walkthrough[2].RecordID != 52119 {
			t.Errorf("walkthrough row 2: %+v", comp.Walkthrough[2])
		}
		if comp.Walkthrough[3].Mnemonic != "STA $57" || comp.Walkthrough[3].RecordID != 52123 {
			t.Errorf("walkthrough row 3: %+v", comp.Walkthrough[3])
		}

		// Qualification check
		if comp.Qualification.Status != "verified" {
			t.Errorf("expected qualification verified, got %s (reason: %s)", comp.Qualification.Status, comp.Qualification.Reason)
		}
		if !comp.Qualification.DifferentialMatched {
			t.Errorf("expected differential matched")
		}
	})

	// 3. Evidence API Integration
	t.Run("evidence_api_includes_companion", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/evidence?addr=09F88F&trace_frame=1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		occRaw, ok := resp["occurrence"]
		if !ok || occRaw == nil {
			t.Fatalf("missing occurrence in evidence response")
		}
		occBytes, _ := json.Marshal(occRaw)
		var rep OccurrenceReport
		if err := json.Unmarshal(occBytes, &rep); err != nil {
			t.Fatalf("unmarshal occurrence: %v", err)
		}
		if rep.Companion == nil {
			t.Fatalf("expected companion inside evidence response occurrence")
		}
		if rep.Companion.WordHex != "0014" {
			t.Errorf("expected word 0014, got %s", rep.Companion.WordHex)
		}
	})

	// 4. Isolation & Negative Cases (No Stale Companion)
	t.Run("non_anchor_instruction_has_no_companion", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/occurrence?addr=09F882&trace_frame=1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep OccurrenceReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected status available, got %s", rep.Status)
		}
		if rep.Companion != nil {
			t.Errorf("expected non-anchor instruction 09:F882 to have no companion, got %+v", rep.Companion)
		}
	})

	t.Run("different_trace_frame_has_no_stale_companion", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/occurrence?addr=09F88F&trace_frame=2", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep OccurrenceReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		// Frame 2 has no occurrence for 09:F88F, so report is unavailable and companion is nil
		if rep.Companion != nil {
			t.Errorf("expected frame 2 to have no companion, got %+v", rep.Companion)
		}
	})
}

func TestSignedWordCompanion_MissingOrMismatchedQualification(t *testing.T) {
	// Build a temporary project dir with signed_words.json referencing a missing receipt
	tmpDir := t.TempDir()

	packet := SignedWordCompanionPacket{
		StreamSHA256: "test-stream-hash",
		Cases: []SignedWordCompanionCase{
			{
				CaseID:        "positive",
				TraceFrame:    1,
				RetirementID:  52100,
				Address:       0x09F88F,
				WordHex:       "0014",
				SignedValue:   20,
				PhysicalWordAddr: "7E:1F54",
				LowByteStore: ByteStoreWitness{
					Address:  "7E:1F54",
					Value:    20,
					RecordID: 52086,
				},
				HighByteStore: ByteStoreWitness{
					Address:  "7E:1F55",
					Value:    0,
					RecordID: 52100,
				},
				ExitA: 0xFF00,
				ExitP: 0x32,
				Qualification: CompanionQualification{
					ReceiptPath: "nonexistent_receipt.json",
				},
			},
		},
	}

	packetBytes, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "signed_words.json"), packetBytes, 0644); err != nil {
		t.Fatal(err)
	}

	idx, err := LoadSignedWordCompanion(tmpDir)
	if err != nil {
		t.Fatalf("LoadSignedWordCompanion: %v", err)
	}

	c := idx.Lookup("test-stream-hash", 1, 52100, "", 0x09F88F)
	if c == nil {
		t.Fatalf("expected companion case to be found")
	}

	// Recorded evidence must remain available!
	if c.WordHex != "0014" || c.SignedValue != 20 {
		t.Errorf("expected recorded evidence to remain intact")
	}
	if c.LowByteStore.Address != "7E:1F54" || c.HighByteStore.Address != "7E:1F55" {
		t.Errorf("expected store witnesses to remain intact")
	}

	// But qualification status must be "unavailable" with concrete reason!
	if c.Qualification.Status != "unavailable" {
		t.Errorf("expected qualification status unavailable, got %s", c.Qualification.Status)
	}
	if c.Qualification.Reason == "" {
		t.Errorf("expected non-empty reason for unavailable qualification")
	}
	t.Logf("confirmed qualification withheld with reason: %s", c.Qualification.Reason)
}

func TestSignedWordCompanion_UIElements(t *testing.T) {
	requiredSnippets := []string{
		"Signed Word Explanation Companion",
		"occurrence-companion-section",
		"companion-qual-badge",
		"companion-assembled-word",
		"companion-low-store",
		"companion-high-store",
		"companion-exit-a",
		"companion-exit-p",
		"companion-walkthrough-box",
		"companion-walkthrough-table",
		"companion-qualification-box",
		"companion-qual-status",
		"companion-runner-hash",
		"companion-c-hash",
		"companion-c-source",
		"Recorded Memory Word Evidence",
		"Explanatory Interpretation",
		"Isolated Execution Qualification",
		"accumulator A preserves upper byte and is separate from assembled memory word",
	}

	uiStr := string(uiHTML)
	for _, s := range requiredSnippets {
		if !strings.Contains(uiStr, s) {
			t.Errorf("uiHTML missing expected snippet %q", s)
		}
	}
}

