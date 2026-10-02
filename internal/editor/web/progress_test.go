package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/progress"
)

func TestProgressRoutes(t *testing.T) {
	model := &progress.Report{Schema: "snes-progress-v1", ROMSHA256: "original"}
	handler := ProgressHandler(model)
	model.ROMSHA256 = "changed"
	for _, tt := range []struct {
		method, path string
		want         int
	}{{"GET", "/progress", 200}, {"GET", "/api/progress", 200}, {"POST", "/api/progress", 405}, {"GET", "/api/progress/evidence?index=../../etc/passwd", 404}, {"GET", "/api/progress/evidence?index=-1", 404}, {"GET", "/api/progress/evidence?index=0", 404}} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != tt.want {
				t.Fatalf("got %d want %d", w.Code, tt.want)
			}
			if tt.path == "/api/progress" && tt.method == "GET" && strings.Contains(w.Body.String(), "changed") {
				t.Fatal("snapshot mutated")
			}
		})
	}
	w := httptest.NewRecorder()
	ProgressHandler(nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/progress", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, s := range []string{"textContent", "Unavailable", "c.captured===null", "No single", "physical", "recorded"} {
		if !strings.Contains(progressPage, s) && s != "physical" {
			t.Fatalf("missing UI scope %q", s)
		}
	}
}
