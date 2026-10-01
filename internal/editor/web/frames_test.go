package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedFrames(t *testing.T) {
	p := os.Getenv("SNES_FRAME_MANIFEST")
	if p == "" {
		t.Skip("retained frames not configured")
	}
	f, err := LoadFrames(p, os.Getenv("SNES_FRAME_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Records) != 255 || f.Mode != "original_interpreter" {
		t.Fatal("wrong frame inventory")
	}
	for _, r := range f.Records {
		if fmt.Sprintf("%x", sha256.Sum256(f.images[r.Index])) != r.PNGSHA256 {
			t.Fatal("image pin differs")
		}
	}
	h := Handler(&Model{Frames: f})
	for _, q := range []struct {
		path string
		want int
	}{{"/api/frame?index=225", 200}, {"/api/frame?index=../../etc/passwd", 404}, {"/api/frame?index=0", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", q.path, nil))
		if w.Code != q.want {
			t.Fatal(w.Code)
		}
	}
	if _, err := LoadFrames(p, fmt.Sprintf("%064x", 2)); err == nil {
		t.Fatal("manifest substitution")
	}
}

func TestFrameArtifactControls(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "png"), 0700)
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 256, 224)))
	path := filepath.Join(dir, "png", "000001.png")
	os.WriteFile(path, imageBytes.Bytes(), 0600)
	header := `{"kind":"frame_run","schema":1,"run":{"rom_sha256":"` + strings.Repeat("1", 64) + `","engine_revision":"test","engine_dirty":false}}`
	rec := map[string]any{"kind": "frame", "index": 1, "number": 1, "trace_frame": 1, "stored": true, "width": 256, "height": 224, "format": "bgr555le", "content_id": strings.Repeat("2", 64), "png": "png/000001.png", "png_sha256": fmt.Sprintf("%x", sha256.Sum256(imageBytes.Bytes()))}
	load := func() error {
		b, _ := json.Marshal(rec)
		data := []byte(header + "\n" + string(b) + "\n")
		manifest := filepath.Join(dir, "frames.jsonl")
		os.WriteFile(manifest, data, 0600)
		_, err := LoadFrames(manifest, fmt.Sprintf("%x", sha256.Sum256(data)))
		return err
	}
	if err := load(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		key   string
		value any
	}{{"format", "rgba"}, {"width", 512}, {"png", "../../outside.png"}, {"interlace", true}, {"png_sha256", strings.Repeat("3", 64)}} {
		old, exists := rec[q.key]
		rec[q.key] = q.value
		if err := load(); err == nil {
			t.Fatalf("accepted %s mutation", q.key)
		}
		if exists {
			rec[q.key] = old
		} else {
			delete(rec, q.key)
		}
	}
	large := make([]byte, (2<<20)+1)
	if err := os.WriteFile(path, large, 0600); err != nil {
		t.Fatal(err)
	}
	rec["png_sha256"] = fmt.Sprintf("%x", sha256.Sum256(large))
	if err := load(); err == nil {
		t.Fatal("oversized pinned PNG accepted")
	}
	os.Remove(path)
	if err := load(); err == nil {
		t.Fatal("missing PNG accepted")
	}
}
