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

func TestReaderFrontier_StandaloneWindow(t *testing.T) {
	windowPath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project/window.json"
	rawPin := "43d349009685fcc891791133ff846751510ffb3d64a84c48f8fca2e9270710b3"
	canonicalPin := "432255670efb0ae062e7f47d09174f8e04c9ee4494ee0f522423496c73ea36a5"

	if _, err := os.Stat(windowPath); err != nil {
		t.Skipf("observation window file %s not found: %v", windowPath, err)
	}

	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	srv, err := NewServer(projectDir, WithObservationWindow(windowPath, rawPin))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Writer 21601 ($7E:1F05 = 115)
	t.Run("writer_21601_shows_readers_and_replacement", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=21601", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep ReaderFrontierReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected available, got %s (reason: %s)", rep.Status, rep.Reason)
		}
		if rep.RawWindowFileSHA256 != rawPin {
			t.Errorf("expected raw pin %s, got %s", rawPin, rep.RawWindowFileSHA256)
		}
		if rep.WindowSHA256 != canonicalPin {
			t.Errorf("expected canonical pin %s, got %s", canonicalPin, rep.WindowSHA256)
		}
		if rep.CapturedProofEligible {
			t.Errorf("expected captured_proof_eligible: false")
		}
		if rep.Writer == nil || rep.Writer.ID != 21601 {
			t.Fatalf("expected writer 21601, got %+v", rep.Writer)
		}
		if rep.Writer.Value != 115 {
			t.Errorf("expected writer value 115, got %d", rep.Writer.Value)
		}
		if rep.PhysicalAddress != 0x7E1F05 {
			t.Errorf("expected address $7E1F05, got $%06X", rep.PhysicalAddress)
		}
		if len(rep.Readers) != 2 {
			t.Fatalf("expected 2 readers, got %d", len(rep.Readers))
		}
		if rep.Readers[0].Event.ID != 38877 || rep.Readers[0].Event.Value != 115 {
			t.Errorf("reader 0 mismatch: %+v", rep.Readers[0])
		}
		if rep.Readers[1].Event.ID != 103698 || rep.Readers[1].Event.Value != 115 {
			t.Errorf("reader 1 mismatch: %+v", rep.Readers[1])
		}
		if rep.Termination != "overwritten" {
			t.Errorf("expected termination overwritten, got %s", rep.Termination)
		}
		if rep.Replacement == nil || rep.Replacement.ID != 103705 || rep.Replacement.Value != 120 {
			t.Fatalf("expected replacement 103705 with value 120, got %+v", rep.Replacement)
		}
	})

	// 2. Chained Replacement 103705 ($7E:1F05 = 120)
	t.Run("replacement_103705_shows_window_end", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=103705", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep ReaderFrontierReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected available, got %s (reason: %s)", rep.Status, rep.Reason)
		}
		if rep.Writer == nil || rep.Writer.ID != 103705 || rep.Writer.Value != 120 {
			t.Fatalf("expected writer 103705 with value 120, got %+v", rep.Writer)
		}
		if len(rep.Readers) != 0 {
			t.Errorf("expected 0 readers, got %d", len(rep.Readers))
		}
		if rep.Termination != "window_end" {
			t.Errorf("expected termination window_end, got %s", rep.Termination)
		}
		if rep.Replacement != nil {
			t.Errorf("expected nil replacement, got %+v", rep.Replacement)
		}
	})

	// 3. Other generic writer 20725 ($7E:1F3E = 9)
	t.Run("other_writer_20725_shows_equal_value_replacement", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=20725", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}

		var rep ReaderFrontierReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if rep.Status != "available" {
			t.Fatalf("expected available, got %s (reason: %s)", rep.Status, rep.Reason)
		}
		if rep.Writer == nil || rep.Writer.ID != 20725 || rep.Writer.Value != 9 {
			t.Fatalf("expected writer 20725 with value 9, got %+v", rep.Writer)
		}
		if rep.PhysicalAddress != 0x7E1F3E {
			t.Errorf("expected address $7E1F3E, got $%06X", rep.PhysicalAddress)
		}
		if len(rep.Readers) != 1 || rep.Readers[0].Event.ID != 28468 || rep.Readers[0].Event.Value != 9 {
			t.Fatalf("expected reader 28468 with value 9, got %+v", rep.Readers)
		}
		if rep.Termination != "overwritten" {
			t.Errorf("expected termination overwritten, got %s", rep.Termination)
		}
		if rep.Replacement == nil || rep.Replacement.ID != 28482 || rep.Replacement.Value != 9 {
			t.Fatalf("expected replacement 28482 with value 9, got %+v", rep.Replacement)
		}
	})

	// 4. Refusals & boundary controls
	t.Run("refusal_on_non_attributed_event", func(t *testing.T) {
		// Event 38877 is a read, not an attributed write
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=38877", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		var rep ReaderFrontierReport
		json.Unmarshal(rec.Body.Bytes(), &rep)
		if rep.Status != "unavailable" {
			t.Errorf("expected unavailable for read event, got %s", rep.Status)
		}
	})

	t.Run("refusal_on_out_of_bounds_event", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=99999999", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		var rep ReaderFrontierReport
		json.Unmarshal(rec.Body.Bytes(), &rep)
		if rep.Status != "unavailable" {
			t.Errorf("expected unavailable for out-of-bounds ID, got %s", rep.Status)
		}
	})

	t.Run("event_0_preserved_as_legal_query", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=0", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		var rep ReaderFrontierReport
		json.Unmarshal(rec.Body.Bytes(), &rep)
		// Handled by library (e.g. refused if event 0 is not an attributed WRAM write)
		if rep.Status == "" {
			t.Errorf("expected response status to be set")
		}
	})

	t.Run("mismatched_raw_pin_withheld_while_occurrence_available", func(t *testing.T) {
		badPin := "0000000000000000000000000000000000000000000000000000000000000000"
		badSrv, err := NewServer(projectDir, WithObservationWindow(windowPath, badPin))
		if err != nil {
			t.Fatalf("NewServer should not fail on bad window pin: %v", err)
		}

		// Reader frontier should be unavailable
		req := httptest.NewRequest("GET", "/api/provenance/reader-frontier?writer_id=21601", nil)
		rec := httptest.NewRecorder()
		badSrv.mux.ServeHTTP(rec, req)

		var rep ReaderFrontierReport
		json.Unmarshal(rec.Body.Bytes(), &rep)
		if rep.Status != "unavailable" {
			t.Errorf("expected unavailable on mismatched pin, got %s", rep.Status)
		}

		// Ordinary occurrence should remain available
		occReq := httptest.NewRequest("GET", "/api/occurrence?addr=09F88F&trace_frame=1", nil)
		occRec := httptest.NewRecorder()
		badSrv.mux.ServeHTTP(occRec, occReq)
		var occRep OccurrenceReport
		if err := json.Unmarshal(occRec.Body.Bytes(), &occRep); err != nil || occRep.Status != "available" {
			t.Errorf("ordinary occurrence must remain available: %+v", occRep)
		}
	})

	t.Run("project_local_window_config_loading", func(t *testing.T) {
		tempDir := t.TempDir()
		recData, _ := os.ReadFile(filepath.Join(projectDir, "recovery.json"))
		os.WriteFile(filepath.Join(tempDir, "recovery.json"), recData, 0644)
		confData := map[string]string{
			"window_path":   windowPath,
			"window_sha256": rawPin,
		}
		b, _ := json.Marshal(confData)
		os.WriteFile(filepath.Join(tempDir, "window_config.json"), b, 0644)

		cfgSrv, err := NewServer(tempDir)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}

		ts := httptest.NewServer(cfgSrv)
		defer ts.Close()

		res, err := http.Get(ts.URL + "/api/provenance/reader-frontier?writer_id=21601")
		if err != nil {
			t.Fatalf("http.Get: %v", err)
		}
		defer res.Body.Close()

		var rep ReaderFrontierReport
		if err := json.NewDecoder(res.Body).Decode(&rep); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if rep.Status != "available" || rep.Writer == nil || rep.Writer.ID != 21601 {
			t.Errorf("expected available writer 21601, got %+v", rep)
		}
	})
}

func TestReaderFrontier_UIElements(t *testing.T) {
	html := string(uiHTML)
	for _, expected := range []string{
		`data-tab="byteversion"`,
		`id="tabbtn-byteversion"`,
		`id="tab-byteversion"`,
		`id="byteversion-writer-input"`,
		`id="byteversion-query-btn"`,
		`id="byteversion-results"`,
		`id="byteversion-writer-grid"`,
		`id="byteversion-readers-body"`,
		`id="byteversion-replacement-body"`,
		`id="byteversion-limitations-list"`,
		`/api/provenance/reader-frontier?writer_id=`,
		`const TABS = ['cfg', 'refs', 'evidence', 'gamestate', 'timeline', 'byteversion'];`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("ui.html missing expected string: %s", expected)
		}
	}
}
