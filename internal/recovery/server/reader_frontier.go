package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	prov "github.com/tmc/snes/internal/provenance"
)

// ServerOption configures optional server settings.
type ServerOption func(*ServerConfig)

// ServerConfig contains optional configuration for the recovery server.
type ServerConfig struct {
	ObservationWindowPath   string
	ObservationWindowSHA256 string
}

// WithObservationWindow configures an explicit observation window file and its expected raw file SHA-256.
func WithObservationWindow(path, expectedRawSHA string) ServerOption {
	return func(cfg *ServerConfig) {
		cfg.ObservationWindowPath = path
		cfg.ObservationWindowSHA256 = expectedRawSHA
	}
}

// ReaderFrontierReport represents the standalone byte-version inspection result.
type ReaderFrontierReport struct {
	Status                string         `json:"status"` // "available" or "unavailable"
	Reason                string         `json:"reason,omitempty"`
	RawWindowFileSHA256   string         `json:"raw_window_file_sha256,omitempty"`
	WindowSHA256          string         `json:"window_sha256,omitempty"`
	Schema                string         `json:"schema,omitempty"`
	Identity              *prov.Identity `json:"identity,omitempty"`
	Writer                *prov.Event    `json:"writer,omitempty"`
	PhysicalAddress       uint32         `json:"physical_wram_address,omitempty"`
	PhysicalAddressHex    string         `json:"physical_wram_hex,omitempty"`
	Readers               []prov.Reader  `json:"readers,omitempty"`
	Replacement           *prov.Event    `json:"replacement,omitempty"`
	Termination           string         `json:"termination,omitempty"`
	CapturedProofEligible bool           `json:"captured_proof_eligible"`
	Limitations           []string       `json:"limitations,omitempty"`
}

// LoadObservationWindowFile loads, bounds-checks, and validates a complete observation window file,
// verifying the caller-supplied expected raw file SHA-256 and computing the canonical window pin.
func LoadObservationWindowFile(path string, expectedRawSHA string, activeROMSHA string) (*prov.Window, string, string, error) {
	if path == "" {
		return nil, "", "", fmt.Errorf("no observation window file specified")
	}
	if len(expectedRawSHA) != 64 {
		return nil, "", "", fmt.Errorf("invalid expected raw file SHA-256: must be 64 hex characters")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("open window file %s: %w", path, err)
	}
	defer f.Close()

	// Enforce 512MB max limit + 1 byte probe
	data, err := io.ReadAll(io.LimitReader(f, (512<<20)+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read window file %s: %w", path, err)
	}
	if len(data) > 512<<20 {
		return nil, "", "", fmt.Errorf("window file %s exceeds 512MB limit", path)
	}

	actualRawSHA := fmt.Sprintf("%x", sha256.Sum256(data))
	if actualRawSHA != expectedRawSHA {
		return nil, "", "", fmt.Errorf("window file identity differs: expected %s, got %s", expectedRawSHA, actualRawSHA)
	}

	var w prov.Window
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return nil, "", "", fmt.Errorf("window JSON decode %s: %w", path, err)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, "", "", fmt.Errorf("trailing window data in %s", path)
	}

	// Protect source context: if project has an active ROM and window identity has a ROM, verify same ROM
	if activeROMSHA != "" && w.Identity.ROMSHA256 != "" && !strings.EqualFold(activeROMSHA, w.Identity.ROMSHA256) {
		return nil, "", "", fmt.Errorf("window ROM mismatch: window has %s, project has %s", w.Identity.ROMSHA256, activeROMSHA)
	}

	canonicalPin, err := prov.WindowSHA256(w)
	if err != nil {
		return nil, "", "", fmt.Errorf("compute canonical window pin: %w", err)
	}

	return &w, canonicalPin, actualRawSHA, nil
}

func (s *Server) handleReaderFrontier(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ReaderFrontierReport{
			Status: "unavailable",
			Reason: "method not allowed",
		})
		return
	}

	if s.ObservationWindow == nil {
		reason := "no observation window configured"
		if s.ObservationWindowLoadReason != "" {
			reason = s.ObservationWindowLoadReason
		}
		json.NewEncoder(w).Encode(ReaderFrontierReport{
			Status: "unavailable",
			Reason: reason,
		})
		return
	}

	writerParam := r.URL.Query().Get("writer_id")
	if writerParam == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ReaderFrontierReport{
			Status:              "unavailable",
			Reason:              "writer_id query parameter is required (e.g. ?writer_id=21601)",
			RawWindowFileSHA256: s.ObservationWindowFileSHA,
			WindowSHA256:        s.ObservationWindowPin,
		})
		return
	}

	writerID, err := strconv.ParseUint(writerParam, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ReaderFrontierReport{
			Status:              "unavailable",
			Reason:              fmt.Sprintf("invalid writer_id %q: %v", writerParam, err),
			RawWindowFileSHA256: s.ObservationWindowFileSHA,
			WindowSHA256:        s.ObservationWindowPin,
		})
		return
	}

	frontier, err := prov.ReadFrontier(*s.ObservationWindow, s.ObservationWindowPin, writerID)
	if err != nil {
		json.NewEncoder(w).Encode(ReaderFrontierReport{
			Status:              "unavailable",
			Reason:              err.Error(),
			RawWindowFileSHA256: s.ObservationWindowFileSHA,
			WindowSHA256:        s.ObservationWindowPin,
		})
		return
	}

	rep := ReaderFrontierReport{
		Status:                "available",
		RawWindowFileSHA256:   s.ObservationWindowFileSHA,
		WindowSHA256:          s.ObservationWindowPin,
		Schema:                frontier.Schema,
		Identity:              &frontier.Identity,
		Writer:                &frontier.Writer,
		PhysicalAddress:       frontier.PhysicalAddress,
		PhysicalAddressHex:    fmt.Sprintf("$%06X", frontier.PhysicalAddress),
		Readers:               frontier.Readers,
		Replacement:           frontier.Replacement,
		Termination:           frontier.Termination,
		CapturedProofEligible: false,
		Limitations:           frontier.Limitations,
	}
	json.NewEncoder(w).Encode(rep)
}
