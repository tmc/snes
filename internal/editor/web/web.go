// Package web serves a read-only view of pinned editor target evidence.
package web

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/editor/experiment"
)

// Model contains observation metadata, not an admission grant.
type Model struct {
	Frames           *Frames           `json:"frames,omitempty"`
	ManifestSHA256   string            `json:"manifest_sha256"`
	Target           experiment.Target `json:"target"`
	Observation      json.RawMessage   `json:"observation"`
	Baseline         string            `json:"baseline"`
	Frame            string            `json:"frame"`
	Sprites          *Sprites          `json:"sprites,omitempty"`
	SpriteProvenance string            `json:"sprite_provenance"`
}

// Load measures a manifest and verifies every named artifact before serving it.
// The caller selects the manifest explicitly; no producer policy is trusted.
func Load(path string) (*Model, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m struct {
		Schema       string            `json:"schema"`
		Entries      *int              `json:"handler_entries"`
		Frames       []int             `json:"observed_frames"`
		Instructions *int              `json:"contiguous_instructions_per_handler"`
		Writes       *int              `json:"ordered_wram_writes_per_handler"`
		Scope        string            `json:"observed_scope"`
		Target       string            `json:"target"`
		Artifacts    map[string]string `json:"artifacts"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Schema != "editor-target-observation-v1" || m.Entries == nil || *m.Entries < 0 || len(m.Frames) != 2 || m.Frames[0] < 0 || m.Frames[1] < m.Frames[0] || m.Instructions == nil || *m.Instructions <= 0 || m.Writes == nil || *m.Writes < 0 || m.Scope == "" {
		return nil, fmt.Errorf("incomplete observation metadata")
	}
	if len(m.Artifacts) == 0 || m.Artifacts[m.Target] == "" {
		return nil, fmt.Errorf("missing pinned target")
	}
	dir := filepath.Dir(path)
	for name, want := range m.Artifacts {
		if filepath.Base(name) != name || name == "." || len(want) != 64 {
			return nil, fmt.Errorf("invalid artifact reference")
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("open artifact %s: %w", name, err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("measure artifact %s: %w", name, err)
		}
		if fmt.Sprintf("%x", h.Sum(nil)) != want {
			return nil, fmt.Errorf("artifact %s differs from manifest", name)
		}
	}
	t, err := os.ReadFile(filepath.Join(dir, m.Target))
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(t)) != m.Artifacts[m.Target] {
		return nil, fmt.Errorf("target changed during load")
	}
	var target experiment.Target
	if err := json.Unmarshal(t, &target); err != nil {
		return nil, fmt.Errorf("decode target: %w", err)
	}
	if err := target.ValidateValue(target.Parameter.Minimum); err != nil {
		return nil, err
	}
	return &Model{ManifestSHA256: fmt.Sprintf("%x", sha256.Sum256(b)), Target: target, Observation: append(json.RawMessage(nil), b...), Baseline: "unavailable: observation manifest grants no captured proof", Frame: "unavailable: no edited frame capture", SpriteProvenance: "unavailable: sprite provenance not integrated"}, nil
}

// Handler returns a GET-only local editor. Drafts stay in the browser and never execute.
func Handler(m *Model) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/target", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "read-only endpoint", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/api/frame", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read-only endpoint", 405)
			return
		}
		n, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil || m.Frames == nil {
			http.NotFound(w, r)
			return
		}
		b, ok := m.Frames.images[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "read-only endpoint", 405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self'")
		io.Copy(w, strings.NewReader(page))
	})
	return mux
}
