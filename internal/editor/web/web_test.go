package web

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	target := []byte(`{"id":"test","parameter":{"field":{"bytes":1},"minimum":0,"maximum":255}}`)
	os.WriteFile(filepath.Join(dir, "target.json"), target, 0600)
	manifest := map[string]any{"target": "target.json", "artifacts": map[string]string{"target.json": fmt.Sprintf("%x", sha256.Sum256(target))}}
	b, _ := json.Marshal(manifest)
	path := filepath.Join(dir, "manifest.json")
	os.WriteFile(path, b, 0600)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Baseline, "unavailable") {
		t.Fatal("observation acquired baseline proof")
	}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			Handler(m).ServeHTTP(w, httptest.NewRequest(method, "/api/target", nil))
			want := 405
			if method == "GET" {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	os.WriteFile(filepath.Join(dir, "target.json"), []byte("changed"), 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("tamper accepted")
	}
	manifest["target"] = "../target.json"
	manifest["artifacts"] = map[string]string{"../target.json": strings.Repeat("0", 64)}
	b, _ = json.Marshal(manifest)
	os.WriteFile(path, b, 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("path escape accepted")
	}
}
func TestRoutes(t *testing.T) {
	for _, path := range []string{"/", "/missing"} {
		w := httptest.NewRecorder()
		Handler(&Model{}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := 200
		if path != "/" {
			want = 404
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
}

func TestRetainedTarget(t *testing.T) {
	path := os.Getenv("SNES_EDITOR_MANIFEST")
	if path == "" {
		t.Skip("retained manifest not configured")
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Target.Start != 0x0cc45b || m.Target.End != 0x0cc47b {
		t.Fatal("unexpected retained target")
	}
	if strings.Contains(m.Baseline, "qualified") {
		t.Fatal("observation acquired proof")
	}
	w := httptest.NewRecorder()
	Handler(m).ServeHTTP(w, httptest.NewRequest("GET", "/api/target", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var got Model
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ManifestSHA256 != m.ManifestSHA256 {
		t.Fatal("identity lost")
	}
}
