package provenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tmc/snes/internal/trace"
)

// Reader records a read of an observed byte version. It does not describe how
// the instruction uses the value or which later writes depend on that value.
type Reader struct {
	Event  Event  `json:"event"`
	Status string `json:"status"`
}

// Frontier describes the readers of one physical WRAM byte version, bounded by
// the next write or the end of the observation window. A write of the same value
// creates a new version. Window-end termination says nothing about future reads.
type Frontier struct {
	Schema                string   `json:"schema"`
	WindowSHA256          string   `json:"window_sha256"`
	Identity              Identity `json:"identity"`
	Writer                Event    `json:"writer"`
	PhysicalAddress       uint32   `json:"physical_wram_address"`
	Readers               []Reader `json:"readers"`
	Replacement           *Event   `json:"replacement,omitempty"`
	Termination           string   `json:"termination"`
	CapturedProofEligible bool     `json:"captured_proof_eligible"`
	Limitations           []string `json:"limitations"`
}

func wramEvent(e Event) (uint32, bool, error) {
	if e.Kind != "bus" && e.Kind != "wram_port" {
		return 0, false, nil
	}
	if e.Op != "read" && e.Op != "write" {
		return 0, false, fmt.Errorf("event %d: invalid bus operation", e.ID)
	}
	if e.Kind == "wram_port" {
		if e.Addr >= 1<<17 {
			return 0, false, fmt.Errorf("event %d: invalid WRAM port address", e.ID)
		}
		return 0x7e0000 + e.Addr, true, nil
	}
	if e.Addr >= 1<<24 {
		return 0, false, fmt.Errorf("event %d: invalid CPU bus address", e.ID)
	}
	space, offset := trace.CPUSpace(e.Addr)
	return 0x7e0000 + offset, space == "wram", nil
}

// ReadFrontier joins a selected WRAM write to subsequent reads of its exact byte
// version in a pinned complete window. WriterID is the raw event ID, including
// zero. Unknown reader actors remain unknown; a conflicting read value refuses
// the result because the recorded writer coverage cannot explain that read.
// The result grants no arithmetic, pixel ownership, or captured-proof claims.
func ReadFrontier(w Window, pin string, writerID uint64) (Frontier, error) {
	var out Frontier
	if err := validateWindow(w, pin); err != nil {
		return out, err
	}
	if writerID >= uint64(len(w.Events)) {
		return out, fmt.Errorf("writer event is outside window")
	}
	writer := w.Events[writerID]
	address, ok, err := wramEvent(writer)
	if err != nil {
		return out, err
	}
	if !ok || writer.Op != "write" || writer.Actor != "cpu" && writer.Actor != "dma_or_hdma" {
		return out, fmt.Errorf("selected event is not an attributed WRAM write")
	}
	// Validate every bus event before publishing even when the selected version
	// is overwritten early. Partial validation would hide malformed coverage.
	for _, e := range w.Events {
		if _, _, err := wramEvent(e); err != nil {
			return out, err
		}
	}
	out = Frontier{
		Schema: "snes-reader-frontier-v1", WindowSHA256: pin,
		Identity: w.Identity, Writer: writer, PhysicalAddress: address,
		Readers: []Reader{}, Termination: "window_end",
		Limitations: []string{
			"writer completeness is the declared producer contract, not independently established by absence of events",
			"byte reads do not establish arithmetic propagation or a dependency on subsequent writes",
			"pixel ownership and readers outside the recorded window are unknown",
		},
	}
	for _, e := range w.Events[writerID+1:] {
		a, memory, _ := wramEvent(e)
		if !memory || a != address {
			continue
		}
		if e.Op == "write" {
			copy := e
			out.Replacement = &copy
			out.Termination = "overwritten"
			break
		}
		if e.Value != writer.Value {
			return Frontier{}, fmt.Errorf("event %d: read differs from selected WRAM byte version", e.ID)
		}
		status := "unknown_actor"
		if e.Actor == "cpu" || e.Actor == "dma_or_hdma" {
			status = "observed_byte_version"
		}
		out.Readers = append(out.Readers, Reader{Event: e, Status: status})
	}
	return out, nil
}

// ReadFrontierFile loads a complete observation window from path, verifies its
// SHA-256 against filePin, and calls ReadFrontier for the specified writerID.
func ReadFrontierFile(path, filePin string, writerID uint64) (Frontier, error) {
	if path == "" || len(filePin) != 64 {
		return Frontier{}, fmt.Errorf("window path and 64-character SHA-256 are required")
	}
	f, err := os.Open(path)
	if err != nil {
		return Frontier{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (512<<20)+1))
	if err != nil {
		return Frontier{}, err
	}
	if len(data) > 512<<20 || fmt.Sprintf("%x", sha256.Sum256(data)) != filePin {
		return Frontier{}, fmt.Errorf("window file identity or size differs")
	}
	var w Window
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return Frontier{}, fmt.Errorf("window JSON: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return Frontier{}, fmt.Errorf("trailing window data")
	}
	pin, err := WindowSHA256(w)
	if err != nil {
		return Frontier{}, err
	}
	return ReadFrontier(w, pin, writerID)
}
