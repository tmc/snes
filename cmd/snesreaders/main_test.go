package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/provenance"
)

func TestRun(t *testing.T) {
	h := strings.Repeat("a", 64)
	w := provenance.Window{Schema: "snes-observation-window-v1", Complete: true, Coverage: provenance.WriterCoverage, To: 1,
		Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"},
		Frames:   []provenance.FrameIdentity{{PPUFrame: 1, VBlankCycle: 2, EndCycle: 3, StateSHA256: h, BusSHA256: h, PixelSHA256: h}},
		Events:   []provenance.Event{{Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x1f05, PPUFrame: 1}},
	}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "window.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	pin := fmt.Sprintf("%x", sha256.Sum256(data))
	for _, tt := range []struct {
		name string
		args []string
		ok   bool
	}{
		{"zero event", []string{"-window", path, "-window-sha256", pin, "-writer", "0"}, true},
		{"stale file", []string{"-window", path, "-window-sha256", h, "-writer", "0"}, false},
		{"missing event", []string{"-window", path, "-window-sha256", pin}, false},
		{"overflow", []string{"-window", path, "-window-sha256", pin, "-writer", "18446744073709551616"}, false},
		{"negative", []string{"-window", path, "-window-sha256", pin, "-writer", "-1"}, false},
		{"outside", []string{"-window", path, "-window-sha256", pin, "-writer", "1"}, false},
		{"positional", []string{"extra"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(tt.args, &out, &bytes.Buffer{})
			if (err == nil) != tt.ok || !tt.ok && out.Len() != 0 {
				t.Fatalf("output %q, error %v", out.String(), err)
			}
			if tt.ok {
				var got provenance.Frontier
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.PhysicalAddress != 0x7e1f05 || got.CapturedProofEligible {
					t.Fatalf("frontier %+v, error %v", got, err)
				}
			}
		})
	}
}
