package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/tmc/snes/internal/framecap"
	prov "github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/visualmap"
)

// Provenance coordinates visual and execution provenance tracing.
type Provenance struct {
	Engine                   *visualmap.Engine
	ObservationWindow        *prov.Window
	ObservationWindowPin     string
	ObservationWindowFileSHA string
	Occurrences              *OccurrenceIndex
	FrameCapture             *framecap.Capture
	Document                 *recovery.Document
	Correlator               prov.OccurrenceCorrelator
	server                   *Server
}

// NewProvenance constructs a Provenance service wrapping the given visual engine.
func NewProvenance(e *visualmap.Engine) *Provenance {
	return &Provenance{Engine: e}
}

// EngineInstance returns the underlying visualmap.Engine, resolving dynamically if attached to a server.
func (p *Provenance) EngineInstance() *visualmap.Engine {
	if p == nil {
		return nil
	}
	if p.server != nil {
		return p.server.provenanceEngine()
	}
	return p.Engine
}

// Provenance returns a Provenance wrapper for the server.
func (s *Server) Provenance() *Provenance {
	return &Provenance{
		Engine:                   s.provenanceEngine(),
		ObservationWindow:        s.ObservationWindow,
		ObservationWindowPin:     s.ObservationWindowPin,
		ObservationWindowFileSHA: s.ObservationWindowFileSHA,
		Occurrences:              s.Occurrences,
		FrameCapture:             s.FrameCapture,
		Document:                 s.Document,
		server:                   s,
	}
}

// RegisterPixelTraceRoutes registers the /api/provenance/pixel-trace endpoint on mux.
func RegisterPixelTraceRoutes(mux *http.ServeMux, prov *Provenance) {
	mux.HandleFunc("/api/provenance/pixel-trace", handlePixelTrace(prov))
}

func handlePixelTrace(prov *Provenance) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "unavailable",
				"reason": "method not allowed",
			})
			return
		}

		engine := prov.EngineInstance()
		if engine == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "unavailable",
				"reason": "visual provenance engine unavailable",
			})
			return
		}

		var q visualmap.TraceQuery
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{
					"status": "unavailable",
					"reason": fmt.Sprintf("invalid json body: %v", err),
				})
				return
			}
			if q.Frame == 0 {
				q.Frame = 333
			}
		} else {
			vals := r.URL.Query()
			frameStr := vals.Get("frame")
			if frameStr != "" {
				f, err := strconv.Atoi(frameStr)
				if err != nil || f < 0 {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]any{
						"status": "unavailable",
						"reason": "invalid frame parameter",
					})
					return
				}
				q.Frame = f
			} else {
				q.Frame = 333
			}

			sprStr := vals.Get("sprite")
			if sprStr == "" {
				sprStr = vals.Get("sprite_index")
			}
			if sprStr != "" {
				s, err := strconv.Atoi(sprStr)
				if err != nil || s < 0 || s > 127 {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]any{
						"status": "unavailable",
						"reason": "invalid sprite index (must be 0..127)",
					})
					return
				}
				q.SpriteIndex = &s
			}

			xStr := vals.Get("x")
			yStr := vals.Get("y")
			if xStr != "" && yStr != "" {
				x, errX := strconv.Atoi(xStr)
				y, errY := strconv.Atoi(yStr)
				if errX != nil || errY != nil || x < 0 || x > 255 || y < 0 || y > 239 {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]any{
						"status": "unavailable",
						"reason": "invalid x, y coordinates",
					})
					return
				}
				q.X = &x
				q.Y = &y
			}
		}

		if q.SpriteIndex == nil && (q.X == nil || q.Y == nil) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "unavailable",
				"reason": "query must specify sprite index or screen coordinates (x, y)",
			})
			return
		}

		if !engine.HasFrame(q.Frame) {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "unavailable",
				"reason": fmt.Sprintf("visual provenance unavailable: frame %d has no pre-display OAM evidence", q.Frame),
			})
			return
		}

		res, err := engine.Trace(r.Context(), q)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "unavailable",
				"reason": err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(res)
	}
}
