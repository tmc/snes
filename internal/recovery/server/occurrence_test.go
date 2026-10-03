package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOccurrenceCard_NaturalProducerWalkthrough(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if srv.Occurrences == nil {
		t.Fatalf("expected srv.Occurrences to be populated from admitted trace")
	}

	tests := []struct {
		name         string
		addr         string
		traceFrame   string
		ppuFrame     string
		wantStatus   string
		wantRetID    uint64
		wantSeq      uint64
		wantBusID    uint64
		wantEff      string
		wantPhys     string
		wantBusVal   uint8
		checkChanges func(t *testing.T, rep *OccurrenceReport)
	}{
		{
			name:       "09F882_trace1_ppu333",
			addr:       "09F882",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "available",
			wantRetID:  52077,
			wantSeq:    13199,
			wantBusID:  52076,
			wantEff:    "$1F05",
			wantPhys:   "7E:1F05",
			wantBusVal: 115, // $73
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.Entry.D != 0x1F00 || rep.Exit.D != 0x1F00 {
					t.Errorf("expected D=$1F00, got entry=%X, exit=%X", rep.Entry.D, rep.Exit.D)
				}
				if rep.Entry.Y != 0x0045 || rep.Exit.Y != 0x0073 {
					t.Errorf("expected Y $0045 -> $0073, got entry=%X, exit=%X", rep.Entry.Y, rep.Exit.Y)
				}
				if rep.Entry.P != 0xB1 || rep.Exit.P != 0x31 {
					t.Errorf("expected P $B1 -> $31, got entry=%X, exit=%X", rep.Entry.P, rep.Exit.P)
				}
				if rep.Entry.PB != 0x09 || rep.Entry.DB != 0x09 {
					t.Errorf("expected PB=09, DB=09, got PB=%X, DB=%X", rep.Entry.PB, rep.Entry.DB)
				}
			},
		},
		{
			name:       "09F884_trace1_ppu333",
			addr:       "09F884",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "available",
			wantRetID:  52082,
			wantSeq:    13200,
			wantBusID:  52081,
			wantEff:    "$09:FBE0",
			wantPhys:   "ROM $04FBE0",
			wantBusVal: 20, // $14
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.Entry.DB != 0x09 || rep.Entry.Y != 0x0073 {
					t.Errorf("expected DB=09, Y=0073, got DB=%X, Y=%X", rep.Entry.DB, rep.Entry.Y)
				}
				if rep.Entry.A != 0xFFFF || rep.Exit.A != 0xFF14 {
					t.Errorf("expected A $FFFF -> $FF14, got entry=%X, exit=%X", rep.Entry.A, rep.Exit.A)
				}
				if rep.Entry.P != 0x31 || rep.Exit.P != 0x31 {
					t.Errorf("expected P $31 unchanged, got entry=%X, exit=%X", rep.Entry.P, rep.Exit.P)
				}
			},
		},
		{
			name:       "09F887_trace1_ppu333",
			addr:       "09F887",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "available",
			wantRetID:  52086,
			wantSeq:    13201,
			wantBusID:  52085,
			wantEff:    "$1F54",
			wantPhys:   "7E:1F54",
			wantBusVal: 20, // $14
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.Entry.D != 0x1F00 {
					t.Errorf("expected D=$1F00, got %X", rep.Entry.D)
				}
				if rep.Entry.A != 0xFF14 || rep.Exit.A != 0xFF14 {
					t.Errorf("expected A $FF14 unchanged, got entry=%X, exit=%X", rep.Entry.A, rep.Exit.A)
				}
				if rep.Entry.P != 0x31 || rep.Exit.P != 0x31 {
					t.Errorf("expected P $31 unchanged, got entry=%X, exit=%X", rep.Entry.P, rep.Exit.P)
				}
				if rep.OperandBus.Before == nil || *rep.OperandBus.Before != 27 {
					t.Errorf("expected bus before=27 ($1B), got %v", rep.OperandBus.Before)
				}
				if rep.OperandBus.After == nil || *rep.OperandBus.After != 20 {
					t.Errorf("expected bus after=20 ($14), got %v", rep.OperandBus.After)
				}
			},
		},
		{
			name:       "09F882_trace0_unavailable",
			addr:       "09F882",
			traceFrame: "0",
			wantStatus: "unavailable",
		},
		{
			name:       "0CC46E_trace0_writer115",
			addr:       "0CC46E",
			traceFrame: "0",
			ppuFrame:   "332",
			wantStatus: "available",
			wantRetID:  30148,
			wantSeq:    8546,
			wantBusID:  30147,
			wantEff:    "$1F05",
			wantPhys:   "7E:1F05",
			wantBusVal: 115,
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.OperandBus == nil || rep.OperandBus.Before == nil || *rep.OperandBus.Before != 110 {
					t.Errorf("expected bus before=110, got %v", rep.OperandBus)
				}
				if rep.OperandBus == nil || rep.OperandBus.After == nil || *rep.OperandBus.After != 115 {
					t.Errorf("expected bus after=115, got %v", rep.OperandBus)
				}
			},
		},
		{
			name:       "0CC46E_trace1_unavailable",
			addr:       "0CC46E",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "unavailable",
		},
		{
			name:       "0CC46E_trace2_writer120",
			addr:       "0CC46E",
			traceFrame: "2",
			ppuFrame:   "334",
			wantStatus: "available",
			wantRetID:  139220,
			wantSeq:    35514,
			wantBusID:  139219,
			wantEff:    "$1F05",
			wantPhys:   "7E:1F05",
			wantBusVal: 120,
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.OperandBus == nil || rep.OperandBus.Before == nil || *rep.OperandBus.Before != 115 {
					t.Errorf("expected bus before=115, got %v", rep.OperandBus)
				}
				if rep.OperandBus == nil || rep.OperandBus.After == nil || *rep.OperandBus.After != 120 {
					t.Errorf("expected bus after=120, got %v", rep.OperandBus)
				}
			},
		},
		{
			name:       "0CC468_trace0_read110",
			addr:       "0CC468",
			traceFrame: "0",
			ppuFrame:   "332",
			wantStatus: "available",
			wantRetID:  30138,
			wantSeq:    8543,
			wantBusID:  30137,
			wantEff:    "$1F05",
			wantPhys:   "7E:1F05",
			wantBusVal: 110,
		},
		{
			name:       "0CC468_trace1_unavailable",
			addr:       "0CC468",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "unavailable",
		},
		{
			name:       "0CC468_trace2_read115",
			addr:       "0CC468",
			traceFrame: "2",
			ppuFrame:   "334",
			wantStatus: "available",
			wantRetID:  139210,
			wantSeq:    35511,
			wantBusID:  139209,
			wantEff:    "$1F05",
			wantPhys:   "7E:1F05",
			wantBusVal: 115,
		},
		{
			name:       "09F81D_trace1_first_of_4040",
			addr:       "09F81D",
			traceFrame: "1",
			ppuFrame:   "333",
			wantStatus: "available",
			wantRetID:  61752,
			wantSeq:    15723,
			checkChanges: func(t *testing.T, rep *OccurrenceReport) {
				if rep.TotalMatches != 4040 {
					t.Errorf("expected total matches 4040, got %d", rep.TotalMatches)
				}
				if rep.MatchIndex != 1 {
					t.Errorf("expected match index 1, got %d", rep.MatchIndex)
				}
				if rep.GlobalMatchingRetirements != 12332 {
					t.Errorf("expected global matching retirements 12332, got %d", rep.GlobalMatchingRetirements)
				}
				if rep.GlobalFirstSeq != 1 {
					t.Errorf("expected global first seq 1, got %d", rep.GlobalFirstSeq)
				}
				if rep.OperandBus != nil {
					t.Errorf("expected OperandBus to be unavailable for 09F81D, got %v", rep.OperandBus)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/evidence?addr=%s", tt.addr)
			if tt.traceFrame != "" {
				url += "&trace_frame=" + tt.traceFrame
			}
			if tt.ppuFrame != "" {
				url += "&frame=" + tt.ppuFrame
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
			}

			var body struct {
				Occurrence *OccurrenceReport `json:"occurrence"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			if body.Occurrence == nil {
				t.Fatalf("expected occurrence in response")
			}

			if body.Occurrence.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (reason: %s)", body.Occurrence.Status, tt.wantStatus, body.Occurrence.Reason)
			}

			if tt.wantStatus == "available" {
				if body.Occurrence.RetirementID != tt.wantRetID {
					t.Errorf("retirement ID = %d, want %d", body.Occurrence.RetirementID, tt.wantRetID)
				}
				if body.Occurrence.Seq != tt.wantSeq {
					t.Errorf("seq = %d, want %d", body.Occurrence.Seq, tt.wantSeq)
				}
				if tt.wantBusID != 0 {
					if body.Occurrence.OperandBus == nil {
						t.Fatalf("expected OperandBus to be present")
					}
					if body.Occurrence.OperandBus.ID != tt.wantBusID {
						t.Errorf("bus ID = %d, want %d", body.Occurrence.OperandBus.ID, tt.wantBusID)
					}
					if body.Occurrence.OperandBus.EffectiveHex != tt.wantEff {
						t.Errorf("effective = %q, want %q", body.Occurrence.OperandBus.EffectiveHex, tt.wantEff)
					}
					if body.Occurrence.OperandBus.PhysicalHex != tt.wantPhys {
						t.Errorf("physical = %q, want %q", body.Occurrence.OperandBus.PhysicalHex, tt.wantPhys)
					}
					if body.Occurrence.OperandBus.Value != tt.wantBusVal {
						t.Errorf("bus value = %d, want %d", body.Occurrence.OperandBus.Value, tt.wantBusVal)
					}
				}
				if tt.checkChanges != nil {
					tt.checkChanges(t, body.Occurrence)
				}
			}
		})
	}
}

func TestOccurrenceCard_CanonicalInstructionIDQuery(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	// Query by canonical ID: 3c34e9a7b3c58e42ba4fd3a3368d195c5e049db0e2fe0748fa2026ff2e6aec21 (09:F882)
	req := httptest.NewRequest(http.MethodGet, "/api/instruction/occurrence?instruction=3c34e9a7b3c58e42ba4fd3a3368d195c5e049db0e2fe0748fa2026ff2e6aec21&trace_frame=1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	var rep OccurrenceReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rep.Status != "available" {
		t.Fatalf("status = %q, want available (reason: %s)", rep.Status, rep.Reason)
	}
	if rep.RetirementID != 52077 {
		t.Errorf("retirement ID = %d, want 52077", rep.RetirementID)
	}
	if rep.Seq != 13199 {
		t.Errorf("seq = %d, want 13199", rep.Seq)
	}
	if rep.InstructionID != "3c34e9a7b3c58e42ba4fd3a3368d195c5e049db0e2fe0748fa2026ff2e6aec21" {
		t.Errorf("instruction_id = %q, want canonical id", rep.InstructionID)
	}
}

