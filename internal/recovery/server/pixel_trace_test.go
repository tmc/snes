package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/recovery/visualmap"
	"github.com/tmc/snes/internal/trace"
)

func setupTestProvenanceEngine(t *testing.T) *visualmap.Engine {
	t.Helper()
	block := &structure.BasicBlock{
		ID:           "bb-trace-oam",
		StartAddress: 0x008000,
		EndAddress:   0x008008,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a978",
				Opcode:   0xA9,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
			{
				ID:       "inst:8002",
				Address:  0x008002,
				Bytes:    "8d0002",
				Opcode:   0x8D,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
		},
	}

	engine := visualmap.NewEngine(&recovery.Document{}, []*structure.BasicBlock{block})
	engine.SetFrameBounds(333, visualmap.FrameBounds{
		StartCycle:  3330000,
		VBlankCycle: 3320000,
		EndCycle:    3340000,
	})

	var oam [544]uint8
	oam[0] = 120                      // X low
	oam[1] = 80                       // Y
	oam[2] = 42                       // Tile
	oam[3] = (3 << 1) | (2 << 4) | 0x80 // Palette=3, Priority=2, VFlip=1
	oam[512] = 0x02                   // Sprite 0: Large=1

	engine.SetOAMSnapshot(333, oam)

	// Ingest CPU instruction retirement
	engine.IngestEvent(trace.Event{
		ID:    100,
		Kind:  "cpu_insn",
		Cycle: 3300060,
		Frame: 332,
		Insn: &trace.Insn{
			Seq: 501,
			Entry: trace.Registers{
				PC: 0x8002, PB: 0x00, A: 0x0078, Cycles: 3300010,
			},
			Exit: trace.Registers{
				PC: 0x8005, PB: 0x00, Cycles: 3300060,
			},
			Fetches: []trace.FetchRecord{
				{Addr: 0x8002, Value: 0x8D, Role: "opcode"},
			},
			Status: "retired",
		},
	})

	// Ingest CPU write
	engine.IngestEvent(trace.Event{
		ID:    101,
		Kind:  "bus",
		Op:    "write",
		Addr:  0x7E0200,
		Value: 120,
		Cycle: 3300050,
		Frame: 332,
		PC:    &trace.PC{Bank: 0x00, Addr: 0x8002},
	})

	// Ingest DMA transfer
	engine.IngestEvent(trace.Event{
		ID:    102,
		Kind:  "dma",
		Cycle: 3325000,
		Frame: 332,
		DMA: &trace.DMAContext{
			Channel: 0,
			Target:  0x04,
			Count:   544,
		},
		Source: trace.Range{Space: "wram", Start: 0x0200, End: 0x041F},
		Dest:   trace.Range{Space: "oam", Start: 0x0000, End: 0x021F},
	})

	return engine
}

func TestPixelTrace_StandaloneRegistration(t *testing.T) {
	engine := setupTestProvenanceEngine(t)
	prov := NewProvenance(engine)

	mux := http.NewServeMux()
	RegisterPixelTraceRoutes(mux, prov)

	tests := []struct {
		name       string
		method     string
		url        string
		body       string
		wantCode   int
		wantStatus string
		wantSprite int
	}{
		{
			name:       "GET by sprite at frame 333",
			method:     http.MethodGet,
			url:        "/api/provenance/pixel-trace?frame=333&sprite=0",
			wantCode:   http.StatusOK,
			wantStatus: "candidate_correlated",
			wantSprite: 0,
		},
		{
			name:       "GET by coordinate hit at frame 333",
			method:     http.MethodGet,
			url:        "/api/provenance/pixel-trace?frame=333&x=122&y=82",
			wantCode:   http.StatusOK,
			wantStatus: "candidate_correlated",
			wantSprite: 0,
		},
		{
			name:       "GET by coordinate miss at frame 333",
			method:     http.MethodGet,
			url:        "/api/provenance/pixel-trace?frame=333&x=200&y=200",
			wantCode:   http.StatusOK,
			wantStatus: "candidate_unmatched",
		},
		{
			name:       "POST with JSON body",
			method:     http.MethodPost,
			url:        "/api/provenance/pixel-trace",
			body:       `{"frame": 333, "sprite_index": 0}`,
			wantCode:   http.StatusOK,
			wantStatus: "candidate_correlated",
			wantSprite: 0,
		},
		{
			name:     "GET missing parameters",
			method:   http.MethodGet,
			url:      "/api/provenance/pixel-trace?frame=333",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "GET invalid sprite index",
			method:   http.MethodGet,
			url:      "/api/provenance/pixel-trace?frame=333&sprite=200",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "GET unpopulated frame fails closed",
			method:   http.MethodGet,
			url:      "/api/provenance/pixel-trace?frame=999&sprite=0",
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "Method Not Allowed",
			method:   http.MethodDelete,
			url:      "/api/provenance/pixel-trace",
			wantCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.url, bytes.NewBufferString(tt.body))
			} else {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("HTTP code = %d, want %d (body: %s)", rec.Code, tt.wantCode, rec.Body.String())
			}

			if tt.wantCode == http.StatusOK {
				var res visualmap.TraceResult
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("unmarshal TraceResult: %v", err)
				}
				if res.Status != tt.wantStatus {
					t.Errorf("status = %q, want %q", res.Status, tt.wantStatus)
				}
				if tt.wantStatus == "candidate_correlated" {
					if res.CandidateType == "" {
						t.Errorf("expected CandidateType, got empty")
					}
					if res.OwnershipQualification == "" {
						t.Errorf("expected OwnershipQualification, got empty")
					}
					if res.WinningPixelWitness == "" {
						t.Errorf("expected WinningPixelWitness, got empty")
					}
					if res.OAM.Index != tt.wantSprite {
						t.Errorf("OAM.Index = %d, want %d", res.OAM.Index, tt.wantSprite)
					}
					if res.DMARegisters == nil || res.DMARegisters.BaseRegister != "$4300" {
						t.Errorf("expected DMARegisters $4300, got %+v", res.DMARegisters)
					}
					if res.ShadowBuffer == nil || res.ShadowBuffer.BaseAddressHex != "$7E:0200" {
						t.Errorf("expected ShadowBuffer $7E:0200, got %+v", res.ShadowBuffer)
					}
					if res.CPUWrite == nil || res.CPUWrite.StoredValue != 120 {
						t.Errorf("expected CPUWrite 120, got %+v", res.CPUWrite)
					}
				}
			}
		})
	}
}

func TestPixelTrace_NilEngineFailsClosed(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPixelTraceRoutes(mux, NewProvenance(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/provenance/pixel-trace?frame=333&sprite=0", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "visual provenance engine unavailable") {
		t.Errorf("body missing failure reason: %s", rec.Body.String())
	}
}
