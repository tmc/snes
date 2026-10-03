package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	prov "github.com/tmc/snes/internal/provenance"
)

func TestIntervalBridge(t *testing.T) {
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

	tests := []struct {
		name                 string
		query                string
		method               string
		wantHTTPCode         int
		wantStatus           string
		wantReasonContains   string
		wantAddr             uint32
		wantAddrHex          string
		wantVal              uint8
		wantReaders          int
		wantTerm             string
		wantHostStart        int
		wantHostEnd          int
		wantPPUStart         int
		wantPPUEnd           int
		wantCyclesStart      uint64
		wantCyclesEnd        uint64
		wantInitialStore     bool
		wantInitialBusID     uint64
		wantInitialRetID     uint64
		wantInitialSeq       uint64
		wantInitialPrecRetID uint64
		wantHasReplacement   bool
		wantReplVal          uint8
		wantReplBusID        uint64
		wantReplRetID        uint64
		wantReplSeq          uint64
		wantReplPrecRetID    uint64
		wantCorrStatus       string
	}{
		{
			name:                 "writer_21601_shows_interval_readers_and_replacement",
			query:                "/api/provenance/byte-interval?writer_id=21601",
			method:               http.MethodGet,
			wantHTTPCode:         http.StatusOK,
			wantStatus:           "available",
			wantAddr:             0x7E1F05,
			wantAddrHex:          "$7E1F05",
			wantVal:              115,
			wantReaders:          2,
			wantTerm:             "overwritten",
			wantHostStart:        108,
			wantHostEnd:          109,
			wantPPUStart:         332,
			wantPPUEnd:           333,
			wantCyclesStart:      118888482,
			wantCyclesEnd:        119603210,
			wantInitialStore:     true,
			wantInitialBusID:     30147,
			wantInitialRetID:     30148,
			wantInitialSeq:       8546,
			wantInitialPrecRetID: 30143,
			wantHasReplacement:   true,
			wantReplVal:          120,
			wantReplBusID:        139219,
			wantReplRetID:        139220,
			wantReplSeq:          35514,
			wantReplPrecRetID:    139215,
			wantCorrStatus:       "complete",
		},
		{
			name:                 "writer_21601_with_matching_addr",
			query:                "/api/provenance/byte-interval?writer_id=21601&addr=7E1F05",
			method:               http.MethodGet,
			wantHTTPCode:         http.StatusOK,
			wantStatus:           "available",
			wantAddr:             0x7E1F05,
			wantAddrHex:          "$7E1F05",
			wantVal:              115,
			wantReaders:          2,
			wantTerm:             "overwritten",
			wantHostStart:        108,
			wantHostEnd:          109,
			wantPPUStart:         332,
			wantPPUEnd:           333,
			wantCorrStatus:       "complete",
			wantInitialStore:     true,
			wantInitialRetID:     30148,
			wantInitialPrecRetID: 30143,
			wantHasReplacement:   true,
			wantReplVal:          120,
			wantReplRetID:        139220,
		},
		{
			name:                 "query_by_addr_only_finds_writer_21601",
			query:                "/api/provenance/byte-interval?addr=7E1F05",
			method:               http.MethodGet,
			wantHTTPCode:         http.StatusOK,
			wantStatus:           "available",
			wantAddr:             0x7E1F05,
			wantVal:              115,
			wantReaders:          2,
			wantTerm:             "overwritten",
			wantCorrStatus:       "complete",
			wantInitialStore:     true,
			wantInitialRetID:     30148,
			wantInitialPrecRetID: 30143,
			wantHasReplacement:   true,
			wantReplVal:          120,
			wantReplRetID:        139220,
		},
		{
			name:                 "replacement_103705_shows_window_end",
			query:                "/api/provenance/byte-interval?writer_id=103705",
			method:               http.MethodGet,
			wantHTTPCode:         http.StatusOK,
			wantStatus:           "available",
			wantAddr:             0x7E1F05,
			wantVal:              120,
			wantReaders:          0,
			wantTerm:             "window_end",
			wantHostStart:        110,
			wantHostEnd:          110,
			wantPPUStart:         334,
			wantPPUEnd:           334,
			wantHasReplacement:   false,
			wantInitialStore:     true,
			wantInitialBusID:     139219,
			wantInitialRetID:     139220,
			wantInitialSeq:       35514,
			wantInitialPrecRetID: 139215,
		},
		{
			name:               "mismatched_addr_returns_bad_request",
			query:              "/api/provenance/byte-interval?writer_id=21601&addr=7E1F06",
			method:             http.MethodGet,
			wantHTTPCode:       http.StatusBadRequest,
			wantStatus:         "unavailable",
			wantReasonContains: "does not match",
		},
		{
			name:               "missing_parameters_returns_bad_request",
			query:              "/api/provenance/byte-interval",
			method:             http.MethodGet,
			wantHTTPCode:       http.StatusBadRequest,
			wantStatus:         "unavailable",
			wantReasonContains: "required",
		},
		{
			name:               "invalid_writer_id_returns_bad_request",
			query:              "/api/provenance/byte-interval?writer_id=abc",
			method:             http.MethodGet,
			wantHTTPCode:       http.StatusBadRequest,
			wantStatus:         "unavailable",
			wantReasonContains: "invalid writer_id",
		},
		{
			name:               "non_attributed_event_returns_unavailable",
			query:              "/api/provenance/byte-interval?writer_id=38877",
			method:             http.MethodGet,
			wantHTTPCode:       http.StatusOK,
			wantStatus:         "unavailable",
			wantReasonContains: "not an attributed WRAM write",
		},
		{
			name:               "out_of_bounds_writer_id_returns_unavailable",
			query:              "/api/provenance/byte-interval?writer_id=99999999",
			method:             http.MethodGet,
			wantHTTPCode:       http.StatusOK,
			wantStatus:         "unavailable",
			wantReasonContains: "outside window",
		},
		{
			name:         "method_not_allowed",
			query:        "/api/provenance/byte-interval?writer_id=21601",
			method:       http.MethodPost,
			wantHTTPCode: http.StatusMethodNotAllowed,
			wantStatus:   "unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.query, nil)
			rec := httptest.NewRecorder()
			srv.mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantHTTPCode {
				t.Fatalf("HTTP %d, want %d: %s", rec.Code, tt.wantHTTPCode, rec.Body.String())
			}

			var rep ByteIntervalReport
			if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if rep.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q (reason: %s)", rep.Status, tt.wantStatus, rep.Reason)
			}

			if tt.wantReasonContains != "" {
				if rep.Reason == "" || !containsSubstring(rep.Reason, tt.wantReasonContains) {
					t.Errorf("Reason = %q, want containing %q", rep.Reason, tt.wantReasonContains)
				}
			}

			if tt.wantStatus != "available" {
				return
			}

			if rep.RawWindowFileSHA256 != rawPin {
				t.Errorf("RawWindowFileSHA256 = %s, want %s", rep.RawWindowFileSHA256, rawPin)
			}
			if rep.WindowSHA256 != canonicalPin {
				t.Errorf("WindowSHA256 = %s, want %s", rep.WindowSHA256, canonicalPin)
			}
			if rep.PhysicalAddress != tt.wantAddr {
				t.Errorf("PhysicalAddress = $%06X, want $%06X", rep.PhysicalAddress, tt.wantAddr)
			}
			if tt.wantAddrHex != "" && rep.PhysicalAddressHex != tt.wantAddrHex {
				t.Errorf("PhysicalAddressHex = %s, want %s", rep.PhysicalAddressHex, tt.wantAddrHex)
			}
			if rep.Value != tt.wantVal {
				t.Errorf("Value = %d, want %d", rep.Value, tt.wantVal)
			}
			if len(rep.Readers) != tt.wantReaders {
				t.Errorf("Readers = %d, want %d", len(rep.Readers), tt.wantReaders)
			}
			if rep.Termination != tt.wantTerm {
				t.Errorf("Termination = %s, want %s", rep.Termination, tt.wantTerm)
			}
			if tt.wantHostStart != 0 && (rep.HostFrames.Start != tt.wantHostStart || rep.HostFrames.End != tt.wantHostEnd) {
				t.Errorf("HostFrames = %d..%d, want %d..%d", rep.HostFrames.Start, rep.HostFrames.End, tt.wantHostStart, tt.wantHostEnd)
			}
			if tt.wantPPUStart != 0 && (rep.PPUFrames.Start != tt.wantPPUStart || rep.PPUFrames.End != tt.wantPPUEnd) {
				t.Errorf("PPUFrames = %d..%d, want %d..%d", rep.PPUFrames.Start, rep.PPUFrames.End, tt.wantPPUStart, tt.wantPPUEnd)
			}
			if tt.wantCyclesStart != 0 && (rep.Cycles.Start != tt.wantCyclesStart || rep.Cycles.End != tt.wantCyclesEnd) {
				t.Errorf("Cycles = %d..%d, want %d..%d", rep.Cycles.Start, rep.Cycles.End, tt.wantCyclesStart, tt.wantCyclesEnd)
			}

			if tt.wantInitialStore {
				if rep.InitialStore == nil {
					t.Fatalf("InitialStore is nil")
				}
				if tt.wantInitialBusID != 0 && rep.InitialStore.TraceBusID != tt.wantInitialBusID {
					t.Errorf("InitialStore.TraceBusID = %d, want %d", rep.InitialStore.TraceBusID, tt.wantInitialBusID)
				}
				if tt.wantInitialRetID != 0 && rep.InitialStore.RetirementID != tt.wantInitialRetID {
					t.Errorf("InitialStore.RetirementID = %d, want %d", rep.InitialStore.RetirementID, tt.wantInitialRetID)
				}
				if tt.wantInitialSeq != 0 && rep.InitialStore.Seq != tt.wantInitialSeq {
					t.Errorf("InitialStore.Seq = %d, want %d", rep.InitialStore.Seq, tt.wantInitialSeq)
				}
				if tt.wantInitialPrecRetID != 0 && rep.InitialStore.PrecedingRetirementID != tt.wantInitialPrecRetID {
					t.Errorf("InitialStore.PrecedingRetirementID = %d, want %d", rep.InitialStore.PrecedingRetirementID, tt.wantInitialPrecRetID)
				}
			}

			if tt.wantHasReplacement {
				if rep.Replacement == nil {
					t.Fatalf("Replacement is nil")
				}
				if rep.Replacement.Value != tt.wantReplVal {
					t.Errorf("Replacement.Value = %d, want %d", rep.Replacement.Value, tt.wantReplVal)
				}
				if tt.wantReplBusID != 0 && rep.Replacement.TraceBusID != tt.wantReplBusID {
					t.Errorf("Replacement.TraceBusID = %d, want %d", rep.Replacement.TraceBusID, tt.wantReplBusID)
				}
				if tt.wantReplRetID != 0 && rep.Replacement.RetirementID != tt.wantReplRetID {
					t.Errorf("Replacement.RetirementID = %d, want %d", rep.Replacement.RetirementID, tt.wantReplRetID)
				}
				if tt.wantReplSeq != 0 && rep.Replacement.Seq != tt.wantReplSeq {
					t.Errorf("Replacement.Seq = %d, want %d", rep.Replacement.Seq, tt.wantReplSeq)
				}
				if tt.wantReplPrecRetID != 0 && rep.Replacement.PrecedingRetirementID != tt.wantReplPrecRetID {
					t.Errorf("Replacement.PrecedingRetirementID = %d, want %d", rep.Replacement.PrecedingRetirementID, tt.wantReplPrecRetID)
				}
			} else if rep.Replacement != nil && !tt.wantHasReplacement {
				t.Errorf("expected nil Replacement, got %+v", rep.Replacement)
			}

			if tt.wantCorrStatus != "" && rep.CorrespondenceStatus != tt.wantCorrStatus {
				t.Errorf("CorrespondenceStatus = %q, want %q", rep.CorrespondenceStatus, tt.wantCorrStatus)
			}

			// Validate Readers correspondence
			if len(rep.Readers) == 2 {
				// Reader 0: 38877 -> LDY dp ($09:F882), seq 13199, ret 52077, prec 52073
				r0 := rep.Readers[0]
				if r0.Event.ID != 38877 {
					t.Errorf("Reader 0 WindowEventID = %d, want 38877", r0.Event.ID)
				}
				if r0.TraceBusID != 52076 {
					t.Errorf("Reader 0 TraceBusID = %d, want 52076", r0.TraceBusID)
				}
				if r0.RetirementID != 52077 {
					t.Errorf("Reader 0 RetirementID = %d, want 52077", r0.RetirementID)
				}
				if r0.Seq != 13199 {
					t.Errorf("Reader 0 Seq = %d, want 13199", r0.Seq)
				}
				if r0.PrecedingRetirementID != 52073 {
					t.Errorf("Reader 0 PrecedingRetirementID = %d, want 52073", r0.PrecedingRetirementID)
				}
				if r0.Instruction != "09:F882" {
					t.Errorf("Reader 0 Instruction = %s, want 09:F882", r0.Instruction)
				}
				if len(r0.RegisterChanges) == 0 || !containsSubstring(r0.RegisterChanges[0], "0073") {
					t.Errorf("Reader 0 RegisterChanges = %+v, want containing 0073", r0.RegisterChanges)
				}

				// Reader 1: 103698 -> LDA abs ($0C:C468), seq 35511, ret 139210, prec 139205
				r1 := rep.Readers[1]
				if r1.Event.ID != 103698 {
					t.Errorf("Reader 1 WindowEventID = %d, want 103698", r1.Event.ID)
				}
				if r1.TraceBusID != 139209 {
					t.Errorf("Reader 1 TraceBusID = %d, want 139209", r1.TraceBusID)
				}
				if r1.RetirementID != 139210 {
					t.Errorf("Reader 1 RetirementID = %d, want 139210", r1.RetirementID)
				}
				if r1.Seq != 35511 {
					t.Errorf("Reader 1 Seq = %d, want 35511", r1.Seq)
				}
				if r1.PrecedingRetirementID != 139205 {
					t.Errorf("Reader 1 PrecedingRetirementID = %d, want 139205", r1.PrecedingRetirementID)
				}
				if r1.Instruction != "0C:C468" {
					t.Errorf("Reader 1 Instruction = %s, want 0C:C468", r1.Instruction)
				}
				if len(r1.RegisterChanges) == 0 || !containsSubstring(r1.RegisterChanges[0], "C473") {
					t.Errorf("Reader 1 RegisterChanges = %+v, want containing C473", r1.RegisterChanges)
				}
			}
		})
	}
}

func TestIntervalBridge_StandaloneRegistration(t *testing.T) {
	mux := http.NewServeMux()
	p := &Provenance{
		ObservationWindow: &prov.Window{
			Schema:   "snes-observation-window-v1",
			From:     108,
			To:       109,
			Complete: true,
			Coverage: prov.WriterCoverage,
			Events: []prov.Event{
				{ID: 0, Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1000, Frame: 108, PPUFrame: 332},
			},
			Frames: []prov.FrameIdentity{
				{Frame: 108, PPUFrame: 332, StartCycle: 0, VBlankCycle: 5000, EndCycle: 10000, StateSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BusSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PixelSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
			Identity: prov.Identity{
				ROMSHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				StateSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				InputsSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				RunSHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Mode:         "original_interpreter",
			},
		},
	}
	pin, _ := prov.WindowSHA256(*p.ObservationWindow)
	p.ObservationWindowPin = pin

	RegisterIntervalBridgeRoutes(mux, p)

	req := httptest.NewRequest(http.MethodGet, "/api/provenance/byte-interval?writer_id=0", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}

	var rep ByteIntervalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rep.Status != "available" {
		t.Fatalf("Status = %q, want available", rep.Status)
	}
	if rep.PhysicalAddress != 0x7E1F05 {
		t.Errorf("PhysicalAddress = $%06X, want $7E1F05", rep.PhysicalAddress)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
