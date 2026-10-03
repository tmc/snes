package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	src := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("natural producer project not found: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.CopyFS(tmpDir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}

	packetBytes, err := os.ReadFile(filepath.Join(tmpDir, "signed_words.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packet SignedWordCompanionPacket
	if err := json.Unmarshal(packetBytes, &packet); err != nil {
		t.Fatal(err)
	}
	packet.Cases[0].Qualification.ReceiptPath = "nonexistent_receipt.json"

	newBytes, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "signed_words.json"), newBytes, 0644); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(tmpDir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/occurrence?addr=09F88F&trace_frame=1", nil))
	var rep OccurrenceReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != "available" {
		t.Fatalf("expected status available, got %s", rep.Status)
	}
	c := rep.Companion
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
		"companion-walkthrough-list",
		"companion-walkthrough-row",
		"companion-qualification-box",
		"companion-qual-status",
		"companion-runner-hash",
		"companion-c-hash",
		"companion-c-source",
		"Recorded Memory Word Evidence",
		"Explanatory Interpretation",
		"Isolated Execution Qualification",
		"View Provenance & Compiler Details",
		"accumulator A preserves upper accumulator byte and is separate from assembled memory word",
	}

	uiStr := string(uiHTML)
	for _, s := range requiredSnippets {
		if !strings.Contains(uiStr, s) {
			t.Errorf("uiHTML missing expected snippet %q", s)
		}
	}
}

func TestIndependentCompanionOriginalContract(t *testing.T) {
	src := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("natural producer project not found: %v", err)
	}

	read := func(name string) map[string]any {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err = json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	for _, tt := range []struct {
		name              string
		mutate            func(map[string]any, map[string]any)
		wantCompanion     bool
		wantQualification string
	}{
		{"authentic baseline", nil, true, "verified"},
		{"missing receipt preserves recorded evidence", func(p, r map[string]any) {
			p["cases"].([]any)[0].(map[string]any)["qualification"].(map[string]any)["receipt_path"] = "absent.json"
		}, true, "unavailable"},
		{"wrong canonical packet ID at same address", func(p, r map[string]any) {
			p["cases"].([]any)[0].(map[string]any)["instruction_id"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, false, ""},
		{"inline verified without referenced receipt", func(p, r map[string]any) {
			delete(p["cases"].([]any)[0].(map[string]any)["qualification"].(map[string]any), "receipt_path")
		}, true, "unavailable"},
		{"receipt belongs to different stream and retirements", func(p, r map[string]any) {
			c := r["cases"].([]any)[0].(map[string]any)
			c["source_stream_sha256"] = "other-stream"
			c["source_event_ids"] = []any{1, 2, 3, 4}
		}, true, "unavailable"},
		{"receipt result state and refusal effects mismatch", func(p, r map[string]any) {
			c := r["cases"].([]any)[0].(map[string]any)
			x := c["compiled_c_result"].(map[string]any)
			x["state"].(map[string]any)["a"] = 0
			x["missing_read"] = true
		}, true, "unavailable"},
		{"inline generated source differs from pinned receipt", func(p, r map[string]any) {
			p["cases"].([]any)[0].(map[string]any)["qualification"].(map[string]any)["generated_c_source"] = "void unrelated(void) {}\n"
		}, true, "unavailable"},
		{"compiled X mismatch with A P and effects preserved", func(p, r map[string]any) {
			r["cases"].([]any)[0].(map[string]any)["compiled_c_result"].(map[string]any)["state"].(map[string]any)["x"] = 0
		}, true, "unavailable"},
		{"receipt IR input operand changed with source and results preserved", func(p, r map[string]any) {
			inst := r["cases"].([]any)[0].(map[string]any)["ir"].(map[string]any)["instructions"].([]any)[1].(map[string]any)
			inst["bytes"] = "e555"
		}, true, "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			packet, receipt := read("signed_words.json"), read("sbc_results.json")
			originalID := packet["cases"].([]any)[0].(map[string]any)["instruction_id"].(string)
			if tt.mutate != nil {
				tt.mutate(packet, receipt)
			}
			dir := t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
				t.Fatal(err)
			}
			for name, m := range map[string]map[string]any{"signed_words.json": packet, "sbc_results.json": receipt} {
				path := filepath.Join(dir, name)
				originalBytes, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var original map[string]any
				if err := json.Unmarshal(originalBytes, &original); err != nil {
					t.Fatal(err)
				}
				if reflect.DeepEqual(original, m) {
					continue // Preserve the accepted bytes for unchanged artifacts.
				}
				b, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, b, 0644); err != nil {
					t.Fatal(err)
				}
			}
			srv, err := NewServer(dir)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/instruction/occurrence?instruction="+originalID+"&trace_frame=1", nil))
			var rep OccurrenceReport
			if err = json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || rep.Status != "available" || rep.RetirementID != 52100 || rep.InstructionID != originalID {
				t.Fatalf("ordinary exact selected retirement lost: HTTP=%d %+v", w.Code, rep)
			}
			if (rep.Companion != nil) != tt.wantCompanion {
				t.Errorf("canonical packet mismatch attached companion: got %v, want %v", rep.Companion != nil, tt.wantCompanion)
			}
			if rep.Companion != nil {
				c := rep.Companion
				if c.WordHex != "0014" || c.HighByteStore.RecordID != 52100 || c.ExitA != 0xff00 || c.ExitP != 0x32 {
					t.Error("ordinary recorded companion evidence lost")
				}
				if c.Qualification.Status != tt.wantQualification {
					t.Errorf("qualification=%s want=%s (reason: %s)", c.Qualification.Status, tt.wantQualification, c.Qualification.Reason)
				}
			}
		})
	}
}

func TestIndependentRecordedCompanionAdmission(t *testing.T) {
	src := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("natural producer project not found: %v", err)
	}

	for _, tt := range []struct {
		name         string
		mutate       func(*SignedWordCompanionPacket)
		wantWithheld bool
	}{
		{name: "authentic positive and negative baseline"},
		{name: "changed positive recorded word", mutate: func(p *SignedWordCompanionPacket) { p.Cases[0].WordHex = "0015" }, wantWithheld: true},
		{name: "wrong packet ROM identity", mutate: func(p *SignedWordCompanionPacket) { p.ROMSHA256 = strings.Repeat("0", 64) }, wantWithheld: true},
		{name: "coherent authored low byte and derived word differ from physical trace", mutate: func(p *SignedWordCompanionPacket) {
			c := &p.Cases[0]
			c.LowByteStore.Value = 21
			c.TableByte = 21
			c.WordHex = "0015"
			c.SignedValue = 21
		}, wantWithheld: true},
		{name: "displayed low store retirement points to CMP not observed STA", mutate: func(p *SignedWordCompanionPacket) {
			p.Cases[0].LowByteStore.RecordID = 52089
		}, wantWithheld: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				path := filepath.Join(dir, "signed_words.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var packet SignedWordCompanionPacket
				if err := json.Unmarshal(raw, &packet); err != nil {
					t.Fatal(err)
				}
				tt.mutate(&packet)
				raw, err = json.Marshal(packet)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			srv, err := NewServer(dir)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			srv.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/occurrence?addr=09F88F&trace_frame=1", nil))
			var rep OccurrenceReport
			if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if rep.Status != "available" || rep.RetirementID != 52100 {
				t.Fatalf("ordinary authentic occurrence lost: %+v", rep)
			}
			if tt.wantWithheld {
				if rep.Companion != nil {
					t.Errorf("mismatched recorded companion promoted while ordinary occurrence remains available")
				}
				return
			}
			if rep.Companion == nil || rep.Companion.WordHex != "0014" || rep.Companion.InstructionID != rep.InstructionID {
				t.Fatalf("authentic positive companion mismatch: %+v", rep)
			}
			wn := httptest.NewRecorder()
			srv.mux.ServeHTTP(wn, httptest.NewRequest("GET", "/api/occurrence?addr=09F89C&trace_frame=1", nil))
			var neg OccurrenceReport
			if err := json.Unmarshal(wn.Body.Bytes(), &neg); err != nil {
				t.Fatal(err)
			}
			if neg.Status != "available" || neg.RetirementID != 52123 || neg.Companion == nil || neg.Companion.WordHex != "FFC3" {
				t.Fatalf("authentic negative companion mismatch: %+v", neg)
			}
		})
	}
}

