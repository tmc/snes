package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/provenance"
)

func TestLoadStateWrites(t *testing.T) {
	h := strings.Repeat("a", 64)
	w := provenance.Window{
		Schema: "snes-observation-window-v1", From: 0, To: 1, Complete: true, Coverage: provenance.WriterCoverage,
		Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"},
		Frames:   []provenance.FrameIdentity{{Frame: 0, PPUFrame: 1, StartCycle: 1, VBlankCycle: 2, EndCycle: 4, StateSHA256: h, BusSHA256: h, PixelSHA256: h}},
		Events:   []provenance.Event{{ID: 0, Kind: "bus", Actor: "cpu", Frame: 0, PPUFrame: 1, Cycle: 2, PC: 0x8000, Op: "write", Addr: 0x10, Value: 3}},
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		data      []byte
		wrongPin  bool
		wantError bool
	}{
		{"indented valid window", b, false, false},
		{"substituted artifact", b, true, true},
		{"unknown field", append([]byte(`{"unexpected":true,`), b[1:]...), false, true},
		{"trailing JSON", append(append([]byte(nil), b...), []byte(` {}`)...), false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "window.json")
			if err := os.WriteFile(path, tt.data, 0600); err != nil {
				t.Fatal(err)
			}
			pin := fmt.Sprintf("%x", sha256.Sum256(tt.data))
			if tt.wrongPin {
				pin = h
			}
			got, err := loadStateWrites(path, pin)
			if (err != nil) != tt.wantError {
				t.Fatalf("load: %v", err)
			}
			if err == nil && (got.Identity.ROMSHA256 != h || len(got.Writes) != 1 || got.Writes[0].Before != nil) {
				t.Fatalf("unexpected timeline: %+v", got)
			}
		})
	}
}
