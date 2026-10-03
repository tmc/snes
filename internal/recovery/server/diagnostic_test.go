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

	fw, err := framecap.Create(framecap.Options{
		Dir:        dir,
		LayerTrace: true,
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

	// Control D: Unsupported format controls
	// D1: Width 512
	cap.Records[1].Width = 512
	respHires := queryDiag(1, 101, 51)
	if respHires["status"] != "unsupported" || respHires["supported"] != false {
		t.Errorf("expected unsupported for Width=512, got %v", respHires)
	}
	cap.Records[1].Width = 256

	// D2: Interlace = true
	cap.Records[1].Interlace = true
	respInterlace := queryDiag(1, 101, 51)
	if respInterlace["status"] != "unsupported" || respInterlace["supported"] != false {
		t.Errorf("expected unsupported for Interlace=true, got %v", respInterlace)
	}
	cap.Records[1].Interlace = false

	// D3: PseudoHires = true
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
}
