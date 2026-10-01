package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tmc/snes/internal/provenance"
)

// Sprites contains versioned OAM evidence from one explicitly pinned capture.
// It describes OAM entries, never visible pixel ownership.
type Sprites struct {
	CaptureSHA256  string            `json:"capture_sha256"`
	ROMSHA256      string            `json:"rom_sha256"`
	ThroughFrame   int               `json:"through_host_frame"`
	WriterCoverage string            `json:"writer_coverage"`
	Entries        []SpriteSelection `json:"entries"`
}

// SpriteSelection binds an index to a capture and inclusive host-frame boundary.
type SpriteSelection struct {
	ID          string                       `json:"id"`
	Explanation provenance.SpriteExplanation `json:"explanation"`
}

// LoadSprites verifies capture bytes before deriving explanations through frame.
// Complete writer coverage is an explicit producer contract, not a proof from absence.
func LoadSprites(path, pin string, frame int) (*Sprites, error) {
	if path == "" || len(pin) != 64 {
		return nil, fmt.Errorf("capture path and SHA-256 required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 || fmt.Sprintf("%x", sha256.Sum256(b)) != pin {
		return nil, fmt.Errorf("capture bytes differ from pin or exceed limit")
	}
	z, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	decoded, err := io.ReadAll(io.LimitReader(z, 128<<20+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > 128<<20 {
		return nil, fmt.Errorf("capture exceeds decoded limit")
	}
	var c struct {
		Schema   int                `json:"schema"`
		ROM      string             `json:"rom_sha256"`
		Complete bool               `json:"complete"`
		Coverage string             `json:"writer_coverage"`
		From     int                `json:"from"`
		To       int                `json:"to"`
		Events   []provenance.Event `json:"events"`
	}
	if err := json.Unmarshal(decoded, &c); err != nil {
		return nil, err
	}
	if c.Schema != 1 || !c.Complete || len(c.ROM) != 64 || c.Coverage != "all_cpu_dma_hdma_wram_and_wram_port" || c.From < 0 || c.To <= c.From || frame < c.From || frame >= c.To || len(c.Events) == 0 {
		return nil, fmt.Errorf("unsupported or incomplete capture contract")
	}
	for _, e := range c.Events {
		if e.Frame < c.From || e.Frame >= c.To {
			return nil, fmt.Errorf("event outside capture bounds")
		}
	}
	out := &Sprites{CaptureSHA256: pin, ROMSHA256: c.ROM, ThroughFrame: frame, WriterCoverage: c.Coverage}
	for i := 0; i < 128; i++ {
		e, err := provenance.ExplainSprite(c.Events, i, frame, 0, 0)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, SpriteSelection{fmt.Sprintf("%s:%d:oam:%d", pin, frame, i), e})
	}
	return out, nil
}
