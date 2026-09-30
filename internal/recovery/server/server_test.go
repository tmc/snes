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
	if err := covIdx.AddRun(coverage.RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom-sha",
		Outcome:    "complete",
		StreamSHA:  "test-stream-sha",
		IsComplete: true,
	}, []coverage.Site{{
		InstructionID: "inst-1",
		Address:       0x008000,
		HasROMOffset:  true,
		Hits:          1,
		FirstSeq:      1,
		LastSeq:       1,
		Frames:        []uint64{0},
		FrameHits:     []uint64{1},
	}}); err != nil {
		t.Fatalf("AddRun: %v", err)
	}
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

	// 13a. GET /api/pseudoc?addr=008000 (read-only)
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/pseudoc returned code %d: %s", w.Code, w.Body.String())
	}
	var pseudoResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &pseudoResp); err != nil {
		t.Fatalf("unmarshal /api/pseudoc: %v", err)
	}
	if pseudoResp["block_id"] == nil || pseudoResp["pseudoc"] == nil {
		t.Errorf("expected block_id and pseudoc in /api/pseudoc response")
	}

	// 13b. GET /api/pseudoc with validate=true must be rejected as read-only violation
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000&validate=true", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected GET /api/pseudoc?validate=true to return 400, got %d", w.Code)
	}

	// 13c. POST /api/pseudoc/validate
	req = httptest.NewRequest(http.MethodPost, "/api/pseudoc/validate?addr=008000", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/pseudoc/validate returned code %d: %s", w.Code, w.Body.String())
	}
	var valResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &valResp); err != nil {
		t.Fatalf("unmarshal /api/pseudoc/validate: %v", err)
	}
	if valResp["validation_receipt"] == nil {
		t.Errorf("expected validation_receipt in /api/pseudoc/validate response")
	}

	// 14. GET /api/pseudoc?addr=008000&receipt=true (read saved receipt)
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000&receipt=true", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/pseudoc?receipt=true returned code %d: %s", w.Code, w.Body.String())
	}
	var receiptResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &receiptResp); err != nil {
		t.Fatalf("unmarshal /api/pseudoc receipt: %v", err)
	}
	rec, ok := receiptResp["validation_receipt"].(map[string]any)
	if !ok || rec == nil {
		t.Fatalf("expected saved validation_receipt in /api/pseudoc?receipt=true response")
	}
	meta, _ := rec["metadata"].(map[string]any)
	if isStale, ok := meta["is_stale"].(bool); ok && isStale {
		t.Errorf("expected initial saved receipt not to be stale")
	}

	// 15. Modifying project revision causes saved receipt to become stale and ineligible
	srv.Revision = "stale_rev_from_new_edit"
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000&receipt=true", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/pseudoc?receipt=true returned code %d: %s", w.Code, w.Body.String())
	}
	var staleResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &staleResp); err != nil {
		t.Fatalf("unmarshal stale /api/pseudoc receipt: %v", err)
	}
	staleRec, _ := staleResp["validation_receipt"].(map[string]any)
	if eligible, ok := staleRec["eligible"].(bool); ok && eligible {
		t.Errorf("expected receipt with mismatched revision to be ineligible")
	}
	staleMeta, _ := staleRec["metadata"].(map[string]any)
	if isStale, ok := staleMeta["is_stale"].(bool); !ok || !isStale {
		t.Errorf("expected receipt with mismatched revision to have is_stale=true")
	}
}

func TestServer_ReceiptAdmissionAndStaleRevision(t *testing.T) {
	dir := t.TempDir()
	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: "test-rom-sha-match",
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
			Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
		},
		{
			ID:       "inst-2",
			Address:  0x008001,
			Offset:   1,
			Bytes:    "18",
			Opcode:   0x18,
			Mnemonic: "clc",
			Mode:     "implied",
			Context:  recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
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
		{
			ID:          "edge-2",
			Kind:        "fallthrough",
			Source:      "inst-2",
			Destination: 0x008002,
			Evidence:    []string{"ev-2"},
		},
	}
	docFile, err := os.Create(filepath.Join(dir, "recovery.json"))
	if err != nil {
		t.Fatalf("create recovery.json: %v", err)
	}
	if err := json.NewEncoder(docFile).Encode(doc); err != nil {
		t.Fatalf("encode recovery.json: %v", err)
	}
	docFile.Close()

	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. POST /api/pseudoc/validate to run verification and save receipt
	req := httptest.NewRequest(http.MethodPost, "/api/pseudoc/validate?addr=008000", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/pseudoc/validate failed: %d (%s)", w.Code, w.Body.String())
	}
	var valResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &valResp); err != nil {
		t.Fatalf("unmarshal validate response: %v", err)
	}
	rec, ok := valResp["validation_receipt"].(map[string]any)
	if !ok || rec == nil {
		t.Fatalf("missing validation_receipt in POST response")
	}
	if matched, _ := rec["matched"].(bool); !matched {
		t.Fatalf("expected matched=true, got false (discrepancy: %v)", rec["discrepancy"])
	}
	if eligible, _ := rec["eligible"].(bool); !eligible {
		t.Fatalf("expected eligible=true on fresh matched receipt")
	}

	// 2. Read saved receipt over GET /api/pseudoc?receipt=true
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000&receipt=true", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/pseudoc?receipt=true failed: %d (%s)", w.Code, w.Body.String())
	}
	var getResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal GET response: %v", err)
	}
	getRec, _ := getResp["validation_receipt"].(map[string]any)
	if eligible, _ := getRec["eligible"].(bool); !eligible {
		t.Errorf("expected GET saved receipt to be eligible")
	}

	// 3. Stale revision between CLI/server invalidates eligibility
	srv.Revision = "updated_git_or_project_rev"
	req = httptest.NewRequest(http.MethodGet, "/api/pseudoc?addr=008000&receipt=true", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/pseudoc?receipt=true failed: %d (%s)", w.Code, w.Body.String())
	}
	var staleResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &staleResp); err != nil {
		t.Fatalf("unmarshal stale GET response: %v", err)
	}
	staleRec, _ := staleResp["validation_receipt"].(map[string]any)
	if eligible, _ := staleRec["eligible"].(bool); eligible {
		t.Errorf("expected stale revision receipt to have eligible=false")
	}
	staleMeta, _ := staleRec["metadata"].(map[string]any)
	if isStale, _ := staleMeta["is_stale"].(bool); !isStale {
		t.Errorf("expected stale revision receipt to have is_stale=true")
	}
	if reason, _ := staleMeta["stale_reason"].(string); !strings.Contains(reason, "project revision mismatch") {
		t.Errorf("expected 'project revision mismatch' in stale_reason, got: %s", reason)
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

func TestServer_Revision_ContentBased(t *testing.T) {
	dir := createTestProject(t)
	srv1, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Same-count change in watches.json
	watchPath := filepath.Join(dir, "watches.json")
	data, err := os.ReadFile(watchPath)
	if err != nil {
		t.Fatalf("read watches: %v", err)
	}
	modifiedWatches := strings.Replace(string(data), `"coins"`, `"rupees"`, 1)
	if err := os.WriteFile(watchPath, []byte(modifiedWatches), 0644); err != nil {
		t.Fatalf("write watches: %v", err)
	}
	srv2, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer 2 failed: %v", err)
	}
	if srv2.Revision == srv1.Revision {
		t.Errorf("expected revision to change after watches.json edit, got same revision %s", srv1.Revision)
	}

	// 2. Same-count change in coverage.json
	covPath := filepath.Join(dir, "coverage.json")
	covData, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatalf("read coverage: %v", err)
	}
	modifiedCov := strings.Replace(string(covData), `"first_seq":1`, `"first_seq":2`, 1)
	if err := os.WriteFile(covPath, []byte(modifiedCov), 0644); err != nil {
		t.Fatalf("write coverage: %v", err)
	}
	srv3, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer 3 failed: %v", err)
	}
	if srv3.Revision == srv2.Revision {
		t.Errorf("expected revision to change after coverage.json edit, got same revision %s", srv2.Revision)
	}

	// 3. Same-count change in recovery.json
	recPath := filepath.Join(dir, "recovery.json")
	recData, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read recovery: %v", err)
	}
	modifiedRec := strings.Replace(string(recData), `"sei"`, `"cli"`, 1)
	if err := os.WriteFile(recPath, []byte(modifiedRec), 0644); err != nil {
		t.Fatalf("write recovery: %v", err)
	}
	srv4, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer 4 failed: %v", err)
	}
	if srv4.Revision == srv3.Revision {
		t.Errorf("expected revision to change after recovery.json edit, got same revision %s", srv3.Revision)
	}
}

func TestControlTarget(t *testing.T) {
	tests := []struct {
		name     string
		addr     uint32
		bytes    string
		want     uint32
		wantKind string
		wantOK   bool
	}{
		{"bcc forward", 0x0CC124, "900d", 0x0CC133, "branch", true},
		{"bne backward", 0x008010, "d0fe", 0x008010, "branch", true},
		{"bra wraps up within bank", 0x01FFF0, "8020", 0x010012, "branch", true},
		{"bpl wraps down within bank", 0x020002, "10f0", 0x02FFF4, "branch", true},
		{"brl wraps within bank", 0x03FFF0, "822000", 0x030013, "branch", true},
		{"jsr uses program bank", 0x0CC135, "200189", 0x0C8901, "call", true},
		{"jmp uses program bank", 0x818000, "4c3412", 0x811234, "jump", true},
		{"jsl long", 0x0CC135, "229c8700", 0x00879C, "call", true},
		{"jml long", 0x008000, "5c20c10c", 0x0CC120, "jump", true},
		{"jml indirect has no static target", 0x0080C6, "dc0300", 0, "", false},
		{"lda is not control flow", 0x008000, "a910", 0, "", false},
		{"truncated branch", 0x008000, "90", 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := recovery.Instruction{Address: tt.addr, Bytes: tt.bytes}
			got, kind, ok := controlTarget(inst)
			if got != tt.want || kind != tt.wantKind || ok != tt.wantOK {
				t.Errorf("controlTarget(%06X %s) = %06X, %q, %v; want %06X, %q, %v",
					tt.addr, tt.bytes, got, kind, ok, tt.want, tt.wantKind, tt.wantOK)
			}
		})
	}
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		in      string
		want    uint32
		wantErr bool
	}{
		{"$80B5", 0x0080B5, false},
		{"0080b5", 0x0080B5, false},
		{"0x0CC120", 0x0CC120, false},
		{"00:80B5", 0x0080B5, false},
		{"7E:03", 0x7E0003, false},
		{"", 0, true},
		{"zz", 0, true},
		{"1000000", 0, true},
	}
	for _, tt := range tests {
		got, err := parseAddress(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseAddress(%q) = %06X, %v; want %06X, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseFrameRange(t *testing.T) {
	tests := []struct {
		in         string
		start, end uint64
		wantErr    bool
	}{
		{"883:884", 883, 884, false},
		{"0:0", 0, 0, false},
		{"884:883", 0, 0, true},
		{"883", 0, 0, true},
		{"a:b", 0, 0, true},
		{"-1:3", 0, 0, true},
	}
	for _, tt := range tests {
		start, end, err := parseFrameRange(tt.in)
		if (err != nil) != tt.wantErr || start != tt.start || end != tt.end {
			t.Errorf("parseFrameRange(%q) = %d, %d, %v; want %d, %d, err=%v",
				tt.in, start, end, err, tt.start, tt.end, tt.wantErr)
		}
	}
}

func get(t *testing.T, srv *Server, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func TestServer_Locate(t *testing.T) {
	srv, err := NewServer(createTestProject(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	tests := []struct {
		url       string
		wantCode  int
		wantID    string
		wantExact bool
	}{
		{"/api/locate?addr=8000", http.StatusOK, "inst-1", true},
		{"/api/locate?addr=00:8001", http.StatusOK, "inst-2", true},
		{"/api/locate?addr=$8003", http.StatusOK, "inst-2", false},
		{"/api/locate?offset=0", http.StatusOK, "inst-1", true},
		{"/api/locate?offset=2", http.StatusOK, "inst-2", false},
		{"/api/locate?addr=8004", http.StatusNotFound, "", false},
		{"/api/locate?addr=7fff", http.StatusNotFound, "", false},
		{"/api/locate?addr=zz", http.StatusBadRequest, "", false},
		{"/api/locate", http.StatusBadRequest, "", false},
	}
	for _, tt := range tests {
		w := get(t, srv, tt.url)
		if w.Code != tt.wantCode {
			t.Errorf("GET %s: code %d, want %d", tt.url, w.Code, tt.wantCode)
			continue
		}
		if tt.wantCode != http.StatusOK {
			continue
		}
		var resp struct {
			Instruction DisasmItem   `json:"instruction"`
			Exact       bool         `json:"exact"`
			Routines    []routineRef `json:"routines"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET %s: unmarshal: %v", tt.url, err)
		}
		if resp.Instruction.ID != tt.wantID || resp.Exact != tt.wantExact {
			t.Errorf("GET %s = %s exact=%v; want %s exact=%v", tt.url, resp.Instruction.ID, resp.Exact, tt.wantID, tt.wantExact)
		}
		if len(resp.Routines) == 0 || resp.Routines[0].EntryAddress != 0x008000 {
			t.Errorf("GET %s: routines = %+v, want entry $008000 first", tt.url, resp.Routines)
		}
	}
}

func TestServer_EvidenceScoped(t *testing.T) {
	srv, err := NewServer(createTestProject(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	w := get(t, srv, "/api/evidence")
	var unscoped map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &unscoped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := unscoped["instructions"]; ok {
		t.Errorf("unscoped /api/evidence returned instructions")
	}

	tests := []struct {
		url          string
		wantCode     int
		wantID       string
		wantEdgesOut int
		wantEdgesIn  int
	}{
		{"/api/evidence?addr=8000", http.StatusOK, "inst-1", 1, 0},
		{"/api/evidence?instruction=inst-2", http.StatusOK, "inst-2", 0, 1},
		{"/api/evidence?addr=9000", http.StatusNotFound, "", 0, 0},
		{"/api/evidence?addr=zz", http.StatusBadRequest, "", 0, 0},
	}
	for _, tt := range tests {
		w := get(t, srv, tt.url)
		if w.Code != tt.wantCode {
			t.Errorf("GET %s: code %d, want %d", tt.url, w.Code, tt.wantCode)
			continue
		}
		if tt.wantCode != http.StatusOK {
			continue
		}
		var resp struct {
			Instructions []instructionEvidence `json:"instructions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET %s: unmarshal: %v", tt.url, err)
		}
		if len(resp.Instructions) != 1 {
			t.Fatalf("GET %s: %d instructions, want 1", tt.url, len(resp.Instructions))
		}
		ie := resp.Instructions[0]
		if ie.ID != tt.wantID || len(ie.EdgesOut) != tt.wantEdgesOut || len(ie.EdgesIn) != tt.wantEdgesIn {
			t.Errorf("GET %s = %s out=%d in=%d; want %s out=%d in=%d", tt.url,
				ie.ID, len(ie.EdgesOut), len(ie.EdgesIn), tt.wantID, tt.wantEdgesOut, tt.wantEdgesIn)
		}
		if len(ie.EdgesIn) > 0 && (ie.EdgesIn[0].From == nil || *ie.EdgesIn[0].From != 0x008000) {
			t.Errorf("GET %s: edge in from = %v, want $008000", tt.url, ie.EdgesIn[0].From)
		}
	}
}

func TestServer_FrameRangeValidation(t *testing.T) {
	srv, err := NewServer(createTestProject(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	tests := []struct {
		url      string
		wantCode int
	}{
		{"/api/coverage?frames=0:1", http.StatusOK},
		{"/api/coverage?frames=5:3", http.StatusBadRequest},
		{"/api/coverage?frames=5", http.StatusBadRequest},
		{"/api/watch?id=coins&frames=x:1", http.StatusBadRequest},
		{"/api/watch?id=coins&frames=10:20", http.StatusOK},
	}
	for _, tt := range tests {
		if w := get(t, srv, tt.url); w.Code != tt.wantCode {
			t.Errorf("GET %s: code %d, want %d", tt.url, w.Code, tt.wantCode)
		}
	}
}

func TestServer_RoutineAddresses(t *testing.T) {
	srv, err := NewServer(createTestProject(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	var routines []struct {
		EntryAddress uint32   `json:"entry_address"`
		Addresses    []uint32 `json:"addresses"`
		Bytes        int      `json:"bytes"`
	}
	if err := json.Unmarshal(get(t, srv, "/api/routines").Body.Bytes(), &routines); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(routines) != 1 {
		t.Fatalf("got %d routines, want 1", len(routines))
	}
	r := routines[0]
	if len(r.Addresses) != 2 || r.Addresses[0] != 0x008000 || r.Addresses[1] != 0x008001 || r.Bytes != 4 {
		t.Errorf("routine = %+v, want addresses [$8000 $8001] and 4 bytes", r)
	}
}
