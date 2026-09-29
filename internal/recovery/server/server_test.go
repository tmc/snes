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
	"github.com/tmc/snes/internal/recovery/watches"
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

	// Create watches.json
	wf := &watches.File{
		Format:        watches.FileFormatWatches,
		SchemaVersion: 1,
		ROMSHA256:     "test-rom-sha",
		Watches: []watches.WatchDefinition{
			{
				ID:          "coins",
				Name:        "Coins",
				MemorySpace: "wram",
				Offset:      0x10,
				Width:       1,
			},
		},
	}
	if err := watches.SaveWatches(filepath.Join(dir, "watches.json"), wf); err != nil {
		t.Fatalf("save watches: %v", err)
	}

	// Create snapshots.json
	snapData1 := make([]byte, 64)
	snapData1[0x10] = 5
	snapData2 := make([]byte, 64)
	snapData2[0x10] = 10
	snaps := []*watches.Snapshot{
		{
			Format:        watches.FileFormatSnapshot,
			SchemaVersion: 1,
			RunID:         "run-1",
			ROMSHA256:     "test-rom-sha",
			MemorySpace:   "wram",
			BaseOffset:    0,
			Length:        64,
			Sequence:      1,
			Frame:         10,
			Data:          snapData1,
		},
		{
			Format:        watches.FileFormatSnapshot,
			SchemaVersion: 1,
			RunID:         "run-1",
			ROMSHA256:     "test-rom-sha",
			MemorySpace:   "wram",
			BaseOffset:    0,
			Length:        64,
			Sequence:      2,
			Frame:         20,
			Data:          snapData2,
		},
	}
	snapRaw, _ := json.Marshal(snaps)
	if err := os.WriteFile(filepath.Join(dir, "snapshots.json"), snapRaw, 0644); err != nil {
		t.Fatalf("write snapshots.json: %v", err)
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

	// 5b. GET /api/graph?format=svg
	req = httptest.NewRequest(http.MethodGet, "/api/graph?format=svg", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/graph?format=svg returned code %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" {
		t.Errorf("expected SVG Content-Type, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "<svg") || !strings.Contains(w.Body.String(), "</svg>") {
		t.Errorf("expected SVG output, got:\n%s", w.Body.String())
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

	// 7. GET /api/watches
	req = httptest.NewRequest(http.MethodGet, "/api/watches", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/watches returned code %d", w.Code)
	}
	var watchFile watches.File
	if err := json.Unmarshal(w.Body.Bytes(), &watchFile); err != nil {
		t.Fatalf("unmarshal /api/watches: %v", err)
	}
	if len(watchFile.Watches) != 1 || watchFile.Watches[0].ID != "coins" {
		t.Errorf("expected coins watch, got %v", watchFile.Watches)
	}

	// 8. GET /api/snapshots
	req = httptest.NewRequest(http.MethodGet, "/api/snapshots", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/snapshots returned code %d", w.Code)
	}
	var snapMetas []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snapMetas); err != nil {
		t.Fatalf("unmarshal /api/snapshots: %v", err)
	}
	if len(snapMetas) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snapMetas))
	}

	// 9. GET /api/watch?id=coins
	req = httptest.NewRequest(http.MethodGet, "/api/watch?id=coins", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/watch?id=coins returned code %d", w.Code)
	}
	var history []watches.HistoryEntry
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal /api/watch: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	// 10. GET /api/watch?id=coins&changes=1
	req = httptest.NewRequest(http.MethodGet, "/api/watch?id=coins&changes=1", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/watch?id=coins&changes=1 returned code %d", w.Code)
	}
	var changes []watches.ChangeInterval
	if err := json.Unmarshal(w.Body.Bytes(), &changes); err != nil {
		t.Fatalf("unmarshal /api/watch changes: %v", err)
	}
	if len(changes) != 1 || changes[0].IntervalLabel != "[10, 20)" {
		t.Errorf("expected change interval [10, 20), got %v", changes)
	}

	// 11. GET /api/disasm
	req = httptest.NewRequest(http.MethodGet, "/api/disasm", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/disasm returned code %d", w.Code)
	}
	var disasmResp struct {
		Instructions []DisasmItem `json:"instructions"`
		Total        int          `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &disasmResp); err != nil {
		t.Fatalf("unmarshal /api/disasm: %v", err)
	}
	if len(disasmResp.Instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(disasmResp.Instructions))
	}
	if disasmResp.Instructions[1].Assembly != "stz.w $2100" {
		t.Errorf("expected assembly 'stz.w $2100', got %q", disasmResp.Instructions[1].Assembly)
	}
	if disasmResp.Instructions[0].Provenance != "static" {
		t.Errorf("expected provenance 'static', got %q", disasmResp.Instructions[0].Provenance)
	}

	// 12. GET /api/evidence
	req = httptest.NewRequest(http.MethodGet, "/api/evidence", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/evidence returned code %d", w.Code)
	}
	var evResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &evResp); err != nil {
		t.Fatalf("unmarshal /api/evidence: %v", err)
	}
	if evResp["rom"] == nil {
		t.Errorf("expected rom in /api/evidence response")
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

	// Watches agreement: direct BuildHistory vs web /api/watch
	directHist, err := watches.BuildHistory(&srv.Watches.Watches[0], srv.Snapshots, srv.Watches)
	if err != nil {
		t.Fatalf("direct BuildHistory failed: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/watch?id=coins", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var webHist []watches.HistoryEntry
	if err := json.Unmarshal(w.Body.Bytes(), &webHist); err != nil {
		t.Fatalf("unmarshal web watch history: %v", err)
	}

	if len(directHist) != len(webHist) {
		t.Fatalf("watches agreement failure: direct len %d != web len %d", len(directHist), len(webHist))
	}
	for i := range directHist {
		if directHist[i].Evaluation.DecodedString != webHist[i].Evaluation.DecodedString {
			t.Errorf("watches agreement failure at index %d: direct %s != web %s",
				i, directHist[i].Evaluation.DecodedString, webHist[i].Evaluation.DecodedString)
		}
	}
}
