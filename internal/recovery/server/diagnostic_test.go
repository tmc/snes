package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/framecap"
	"github.com/tmc/snes/internal/trace"
)

func createOverlapSceneCapture(t *testing.T, variant string) (*framecap.Capture, string) {
	t.Helper()
	fixturePath := filepath.Join("/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/overlap-scene", variant, "fixture.sfc")
	rom, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("overlap scene fixture %s not available: %v", fixturePath, err)
		return nil, ""
	}

	dir := t.TempDir()
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("load rom: %v", err)
	}
	sys.Power()
	sys.PPU.EnableLayerTrace(true)

	romSum := sha256.Sum256(rom)
	fw, err := framecap.Create(framecap.Options{
		Dir:        dir,
		LayerTrace: true,
		Run: &trace.RunInfo{
			ROMSHA256:      hex.EncodeToString(romSum[:]),
			EngineRevision: "a019ea3d5727c4cc68fc6650a56dec9c76ed1e3b",
			Mapper:         "lorom",
		},
	})
	if err != nil {
		t.Fatalf("framecap create: %v", err)
	}
	stop, err := sys.CaptureFrames(fw.Keep, fw.Frame)
	if err != nil {
		t.Fatalf("capture frames: %v", err)
	}
	defer stop()

	for i := 0; i < 3; i++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("run sys: %v", err)
		}
	}
	if _, err := fw.Close("complete"); err != nil {
		t.Fatalf("close framecap: %v", err)
	}

	cap, err := framecap.Open(dir)
	if err != nil {
		t.Fatalf("open framecap: %v", err)
	}
	return cap, dir
}

func TestFrameDiagnosticEndpoint_Gates(t *testing.T) {
	cap, dir := createOverlapSceneCapture(t, "baseline")
	if cap == nil {
		return
	}

	srv := &Server{
		ProjectDir:   dir,
		FrameCapture: cap,
	}

	type diagResponse struct {
		Frame      int    `json:"frame"`
		X          int    `json:"x"`
		Y          int    `json:"y"`
		ContentID  string `json:"content_id"`
		Status     string `json:"status"`
		IsKnown    bool   `json:"is_known"`
		Supported  bool   `json:"supported"`
		Source     uint8  `json:"source"`
		SourceName string `json:"source_name"`
		Palette    uint8  `json:"palette"`
		Reason     string `json:"reason"`
	}

	queryDiag := func(frame, x, y int) diagResponse {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/frame/diagnostic?frame=%d&x=%d&y=%d", frame, x, y), nil)
		w := httptest.NewRecorder()
		srv.handleFrameDiagnostic(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("handleFrameDiagnostic returned HTTP %d: %s", w.Code, w.Body.String())
		}
		var resp diagResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return resp
	}

	// 1. Gate: Pixel (101, 51) -> OBJ1 (64), Palette 129
	r101 := queryDiag(1, 101, 51)
	if !r101.IsKnown || r101.Status != "known" {
		t.Errorf("expected (101,51) known, got status=%s is_known=%v", r101.Status, r101.IsKnown)
	}
	if r101.Source != 64 || r101.Palette != 129 {
		t.Errorf("expected (101,51) OBJ1 (64)/129, got source=%d palette=%d", r101.Source, r101.Palette)
	}

	// 2. Gate: Pixel (105, 51) -> OBJ1 (64), Palette 145
	r105 := queryDiag(1, 105, 51)
	if !r105.IsKnown || r105.Status != "known" {
		t.Errorf("expected (105,51) known, got status=%s is_known=%v", r105.Status, r105.IsKnown)
	}
	if r105.Source != 64 || r105.Palette != 145 {
		t.Errorf("expected (105,51) OBJ1 (64)/145, got source=%d palette=%d", r105.Source, r105.Palette)
	}

	// 3. Gate: Pixel (109, 51) -> Backdrop (32), Palette 0
	r109 := queryDiag(1, 109, 51)
	if !r109.IsKnown || r109.Status != "known" {
		t.Errorf("expected (109,51) known, got status=%s is_known=%v", r109.Status, r109.IsKnown)
	}
	if r109.Source != 32 || r109.Palette != 0 {
		t.Errorf("expected (109,51) Backdrop (32)/0, got source=%d palette=%d", r109.Source, r109.Palette)
	}

	// 4. Gate: Frame 0 unknown cell negative control
	rF0 := queryDiag(0, 0, 0)
	if rF0.IsKnown || rF0.Status != "unknown" {
		t.Errorf("expected frame 0 unknown, got status=%s is_known=%v", rF0.Status, rF0.IsKnown)
	}
}

func TestFrameDiagnosticEndpoint_NegativeAndTamperControls(t *testing.T) {
	cap, dir := createOverlapSceneCapture(t, "baseline")
	if cap == nil {
		return
	}

	srv := &Server{
		ProjectDir:   dir,
		FrameCapture: cap,
	}

	queryDiag := func(frame, x, y int) map[string]any {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/frame/diagnostic?frame=%d&x=%d&y=%d", frame, x, y), nil)
		w := httptest.NewRecorder()
		srv.handleFrameDiagnostic(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("handleFrameDiagnostic returned HTTP %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return resp
	}

	// Control A: Missing sidecar file -> unavailable
	origSidecar := cap.Records[1].Sidecar
	cap.Records[1].Sidecar = "sidecars/nonexistent.sidecar.json"
	respMissing := queryDiag(1, 101, 51)
	if respMissing["status"] != "unavailable" || respMissing["is_known"] != false {
		t.Errorf("expected unavailable for missing sidecar, got %v", respMissing)
	}
	cap.Records[1].Sidecar = origSidecar

	// Control B: Tampered sidecar SHA -> unavailable
	origSHA := cap.Records[1].SidecarSHA256
	cap.Records[1].SidecarSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	respTampered := queryDiag(1, 101, 51)
	if respTampered["status"] != "unavailable" || respTampered["is_known"] != false {
		t.Errorf("expected unavailable for tampered sidecar SHA, got %v", respTampered)
	}
	cap.Records[1].SidecarSHA256 = origSHA

	// Control C: Mismatched content ID in sidecar -> unavailable
	sidecarPath := filepath.Join(dir, filepath.FromSlash(origSidecar))
	origBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sc framecap.Sidecar
	if err := json.Unmarshal(origBytes, &sc); err != nil {
		t.Fatalf("unmarshal sidecar: %v", err)
	}
	sc.ContentID = "tampered-content-id"
	tamperedBytes, _ := json.Marshal(sc)
	if err := os.WriteFile(sidecarPath, tamperedBytes, 0644); err != nil {
		t.Fatalf("write tampered sidecar: %v", err)
	}
	// Also update record SHA so it tests the content ID mismatch specifically
	h := sha256.Sum256(tamperedBytes)
	cap.Records[1].SidecarSHA256 = hex.EncodeToString(h[:])

	respContentMismatch := queryDiag(1, 101, 51)
	if respContentMismatch["status"] != "unavailable" || respContentMismatch["is_known"] != false {
		t.Errorf("expected unavailable for content ID mismatch, got %v", respContentMismatch)
	}

	// Restore original sidecar
	if err := os.WriteFile(sidecarPath, origBytes, 0644); err != nil {
		t.Fatalf("restore sidecar: %v", err)
	}
	cap.Records[1].SidecarSHA256 = origSHA

	// Control D: Format controls
	// D1: Mismatched Width (record 512 vs sidecar 256) -> unavailable
	cap.Records[1].Width = 512
	respHiresMismatch := queryDiag(1, 101, 51)
	if respHiresMismatch["status"] != "unavailable" || respHiresMismatch["supported"] != false {
		t.Errorf("expected unavailable for mismatched Width=512, got %v", respHiresMismatch)
	}
	cap.Records[1].Width = 256

	// D2: Mismatched Interlace (record true vs sidecar false) -> unavailable
	cap.Records[1].Interlace = true
	respInterlaceMismatch := queryDiag(1, 101, 51)
	if respInterlaceMismatch["status"] != "unavailable" || respInterlaceMismatch["supported"] != false {
		t.Errorf("expected unavailable for mismatched Interlace=true, got %v", respInterlaceMismatch)
	}
	cap.Records[1].Interlace = false

	// D3: Authentic unsupported format (PseudoHires = true on record) -> unsupported
	cap.Records[1].PseudoHires = true
	respPseudo := queryDiag(1, 101, 51)
	if respPseudo["status"] != "unsupported" || respPseudo["supported"] != false {
		t.Errorf("expected unsupported for PseudoHires=true, got %v", respPseudo)
	}
	cap.Records[1].PseudoHires = false
}

func TestUIStaleResponseGuardContract(t *testing.T) {
	uiPath := filepath.Join("ui.html")
	content, err := os.ReadFile(uiPath)
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	html := string(content)

	// Verify generation guard token exists
	if !strings.Contains(html, "provenanceRequestGen") {
		t.Errorf("ui.html missing provenanceRequestGen generation token")
	}

	// Verify generation check exists on fetch completion
	if !strings.Contains(html, "reqGen !== provenanceRequestGen") {
		t.Errorf("ui.html missing generation token check to discard stale responses")
	}

	// Verify frame match check exists
	if !strings.Contains(html, "data.frame !== currentTimelineFrame") && !strings.Contains(html, "currentTimelineFrame !== frameAtReq") {
		t.Errorf("ui.html missing frame check against currentTimelineFrame to discard stale frame responses")
	}

	// Verify diagnostic endpoint is queried
	if !strings.Contains(html, "/api/frame/diagnostic") {
		t.Errorf("ui.html does not query /api/frame/diagnostic")
	}

	// Verify Renderer Layer/Palette Diagnostic section exists
	if !strings.Contains(html, "Renderer Layer/Palette Diagnostic") {
		t.Errorf("ui.html missing 'Renderer Layer/Palette Diagnostic' section")
	}

	// Verify timeline uses trace_frame for coverage filtering
	if !strings.Contains(html, "rec.trace_frame != null") || !strings.Contains(html, "rec.trace_frame + 1") {
		t.Errorf("ui.html missing trace_frame coverage filtering")
	}

	// Verify missing trace mapping is disabled/unavailable rather than guessed
	if !strings.Contains(html, "trace unavailable") || !strings.Contains(html, "Coverage mapping unavailable") {
		t.Errorf("ui.html does not report trace coverage mapping unavailable when trace_frame is null")
	}

	// Verify loadTimeline joins coverage filter range to PPU frame via trace_frame
	if !strings.Contains(html, "f.trace_frame === filterFrom") {
		t.Errorf("ui.html loadTimeline does not map coverage filter to timeline frame via trace_frame")
	}
}

func TestIndependentSidecarValidation(t *testing.T) {
	cap, dir := createOverlapSceneCapture(t, "baseline")
	if cap == nil {
		t.Fatal("owned fixture missing")
	}
	srv := &Server{ProjectDir: dir, FrameCapture: cap}
	origRec := cap.Records[1]
	path := filepath.Join(dir, origRec.Sidecar)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var original framecap.Sidecar
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	query := func(t *testing.T) map[string]any {
		w := httptest.NewRecorder()
		srv.handleFrameDiagnostic(w, httptest.NewRequest("GET", "/api/frame/diagnostic?frame=1&x=101&y=51", nil))
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		t.Logf("HTTP %d response: %s", w.Code, w.Body.String())
		return result
	}
	t.Run("baseline", func(t *testing.T) {
		r := query(t)
		if r["status"] != "known" || r["palette"] != float64(129) {
			t.Fatal(r)
		}
	})
	cases := []struct {
		name   string
		mutate func(*framecap.Sidecar, *framecap.Record)
	}{
		{"unrelated_run", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Run = &trace.RunInfo{} }},
		{"missing_content_id", func(sc *framecap.Sidecar, r *framecap.Record) { sc.ContentID = "" }},
		{"wrong_frame_number", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Number = 999 }},
		{"wrong_frame_index", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Index = 999 }},
		{"wrong_cycles", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Start++; sc.VBlank++ }},
		{"wrong_field", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Field = 1 - r.Field }},
		{"wrong_schema_kind", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Schema = 999; sc.Kind = "unrelated" }},
		{"missing_known_mask", func(sc *framecap.Sidecar, r *framecap.Record) { sc.KnownMask = nil }},
		{"missing_palettes", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Palettes = nil }},
		{"sidecar_wrong_dimensions", func(sc *framecap.Sidecar, r *framecap.Record) { sc.Height = 240 }},
		{"record_wrong_height", func(sc *framecap.Sidecar, r *framecap.Record) { r.Height = 240 }},
		{"missing_manifest_sha", func(sc *framecap.Sidecar, r *framecap.Record) { r.SidecarSHA256 = "" }},
		{"unlisted_sidecar_filename_fallback", func(sc *framecap.Sidecar, r *framecap.Record) { r.Sidecar = ""; r.SidecarSHA256 = "" }},
		{"mismatched_dirty_sha", func(sc *framecap.Sidecar, r *framecap.Record) {
			sc.Run.EngineDirty = true
			sc.Run.EngineDirtySHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var sc framecap.Sidecar
			if err := json.Unmarshal(raw, &sc); err != nil {
				t.Fatal(err)
			}
			rec := origRec
			tt.mutate(&sc, &rec)
			body, err := json.Marshal(sc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0644); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			if rec.SidecarSHA256 != "" {
				rec.SidecarSHA256 = hex.EncodeToString(sum[:])
			}
			cap.Records[1] = rec
			result := query(t)
			if result["status"] != "unavailable" || result["is_known"] != false {
				t.Errorf("invalid/misbound sidecar accepted: %v", result)
			}
		})
	}
}

func TestFrameDiagnosticEndpoint_AuthenticHiresStatus(t *testing.T) {
	cap, dir := createOverlapSceneCapture(t, "baseline")
	if cap == nil {
		return
	}
	srv := &Server{
		ProjectDir:   dir,
		FrameCapture: cap,
	}

	origRec := cap.Records[1]
	path := filepath.Join(dir, filepath.FromSlash(origRec.Sidecar))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sc framecap.Sidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}

	// Authentic 512-wide producer sets Supported=false, Width=512, retaining 256-col trace arrays.
	sc.Width = 512
	sc.Supported = false
	sc.UnsupportedReason = "unsupported frame dimensions 512x224"
	origRec.Width = 512

	body, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	origRec.SidecarSHA256 = hex.EncodeToString(sum[:])
	cap.Records[1] = origRec

	req := httptest.NewRequest("GET", "/api/frame/diagnostic?frame=1&x=100&y=50", nil)
	w := httptest.NewRecorder()
	srv.handleFrameDiagnostic(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "unsupported" {
		t.Errorf("expected status 'unsupported', got %q (%v)", resp["status"], resp["reason"])
	}
	if resp["is_known"] != false || resp["supported"] != false {
		t.Errorf("expected is_known=false, supported=false, got %v, %v", resp["is_known"], resp["supported"])
	}
	if resp["reason"] != "unsupported frame dimensions 512x224" {
		t.Errorf("expected reason 'unsupported frame dimensions 512x224', got %q", resp["reason"])
	}
}
