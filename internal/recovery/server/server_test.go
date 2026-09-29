package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
)

func createTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: "test-rom-sha",
		NormalizedSize:   32 * 1024,
		Mapper:           "lorom",
	})
	doc.Instructions = []recovery.Instruction{
		{
			ID:       "inst-1",
			Address:  0x008000,
			Offset:   0,
			Bytes:    "78",
			Opcode:   0x78,
			Mnemonic: "sei",
			Mode:     "implied",
			Context:  recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
		},
		{
			ID:       "inst-2",
			Address:  0x008001,
			Offset:   1,
			Bytes:    "9c0021",
			Opcode:   0x9C,
			Mnemonic: "stz",
			Mode:     "absolute",
			Context:  recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
		},
	}
	doc.Edges = []recovery.Edge{
		{
			ID:          "edge-1",
			Kind:        "fallthrough",
			Source:      "inst-1",
			Destination: 0x008001,
			Evidence:    []string{"ev-1"},
		},
	}

	docFile, err := os.Create(filepath.Join(dir, "recovery.json"))
	if err != nil {
		t.Fatalf("create recovery.json: %v", err)
	}
	defer docFile.Close()
	if err := recovery.Encode(docFile, doc); err != nil {
		t.Fatalf("encode recovery.json: %v", err)
	}

	// Create coverage.json
	covIdx := coverage.NewIndex("test-rom-sha")
	covIdx.AddRun(coverage.RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom-sha",
		Outcome:    "complete",
		IsComplete: true,
	})
	covIdx.AddEvent(coverage.Event{
		RunID:         "run-1",
		Seq:           1,
		Frame:         0,
		Address:       0x008000,
		Offset:        0,
		HasROMOffset:  true,
		InstructionID: "inst-1",
	})
	covFile, err := os.Create(filepath.Join(dir, "coverage.json"))
	if err != nil {
		t.Fatalf("create coverage.json: %v", err)
	}
	defer covFile.Close()
	if err := covIdx.Encode(covFile); err != nil {
		t.Fatalf("encode coverage.json: %v", err)
	}

	return dir
}

func TestServer_Endpoints(t *testing.T) {
	dir := createTestProject(t)
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET / (UI)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / returned code %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "snesdasm") {
		t.Errorf("expected HTML to contain snesdasm")
	}

	// 2. GET /api/project
	req = httptest.NewRequest(http.MethodGet, "/api/project", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/project returned code %d", w.Code)
	}
	var proj map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &proj); err != nil {
		t.Fatalf("unmarshal /api/project: %v", err)
	}
	if proj["project_rom_hash"] != "test-rom-sha" {
		t.Errorf("expected rom hash test-rom-sha, got %v", proj["project_rom_hash"])
	}

	// 3. GET /api/routines
	req = httptest.NewRequest(http.MethodGet, "/api/routines", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/routines returned code %d", w.Code)
	}
	var routines []any
	if err := json.Unmarshal(w.Body.Bytes(), &routines); err != nil {
		t.Fatalf("unmarshal /api/routines: %v", err)
	}
	if len(routines) == 0 {
		t.Errorf("expected at least 1 routine")
	}

	// 4. GET /api/refs
	req = httptest.NewRequest(http.MethodGet, "/api/refs", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/refs returned code %d", w.Code)
	}
	var refs []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &refs); err != nil {
		t.Fatalf("unmarshal /api/refs: %v", err)
	}
	if len(refs) != 1 || refs[0]["hardware_name"] != "INIDISP" {
		t.Errorf("expected INIDISP ref, got %v", refs)
	}

	// 5. GET /api/graph?format=dot
	req = httptest.NewRequest(http.MethodGet, "/api/graph?format=dot", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/graph returned code %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "digraph CFG") {
		t.Errorf("expected DOT format, got:\n%s", w.Body.String())
	}

	// 6. GET /api/coverage
	req = httptest.NewRequest(http.MethodGet, "/api/coverage", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/coverage returned code %d", w.Code)
	}
	var covRes coverage.CoverageResult
	if err := json.Unmarshal(w.Body.Bytes(), &covRes); err != nil {
		t.Fatalf("unmarshal /api/coverage: %v", err)
	}
	if covRes.TotalHits != "1" {
		t.Errorf("expected total hits 1, got %s", covRes.TotalHits)
	}
}

func TestServer_CLIWebAgreement(t *testing.T) {
	dir := createTestProject(t)
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Direct query from Coverage index
	directRes, err := srv.Coverage.Query(coverage.Filter{})
	if err != nil {
		t.Fatalf("direct Query failed: %v", err)
	}

	// Web query
	req := httptest.NewRequest(http.MethodGet, "/api/coverage", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var webRes coverage.CoverageResult
	if err := json.Unmarshal(w.Body.Bytes(), &webRes); err != nil {
		t.Fatalf("unmarshal web response: %v", err)
	}

	if directRes.TotalHits != webRes.TotalHits {
		t.Errorf("agreement failure: direct hits %s != web hits %s", directRes.TotalHits, webRes.TotalHits)
	}
	if directRes.Quality != webRes.Quality {
		t.Errorf("agreement failure: direct quality %s != web quality %s", directRes.Quality, webRes.Quality)
	}
	if len(directRes.ByOffset) != len(webRes.ByOffset) {
		t.Errorf("agreement failure: direct offsets %d != web offsets %d", len(directRes.ByOffset), len(webRes.ByOffset))
	}
}
