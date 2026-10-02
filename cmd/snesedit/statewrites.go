package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tmc/snes/internal/editor/statewrites"
	"github.com/tmc/snes/internal/provenance"
)

func loadStateWrites(path, pin string) (*statewrites.Timeline, error) {
	expected, err := hex.DecodeString(pin)
	if path == "" || err != nil || len(expected) != sha256.Size || hex.EncodeToString(expected) != pin {
		return nil, fmt.Errorf("state writes require a path and lowercase SHA-256")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open state writes: %w", err)
	}
	defer f.Close()
	const limit = 64 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read state writes: %w", err)
	}
	if len(b) > limit {
		return nil, fmt.Errorf("state writes exceed 64 MiB")
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != pin {
		return nil, fmt.Errorf("state writes artifact identity mismatch")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var w provenance.Window
	if err := d.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode state writes: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("state writes contain trailing JSON")
	}
	canonical, err := provenance.WindowSHA256(w)
	if err != nil {
		return nil, fmt.Errorf("hash state writes window: %w", err)
	}
	return statewrites.Build(w, canonical)
}
