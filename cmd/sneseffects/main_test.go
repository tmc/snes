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

func TestPinnedComparison(t *testing.T) {
	h := strings.Repeat("a", 64)
	w := provenance.Window{Schema: "snes-observation-window-v1", Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"}, From: 0, To: 1, Complete: true, Coverage: provenance.WriterCoverage, Frames: []provenance.FrameIdentity{{Frame: 0, PPUFrame: 1, StartCycle: 1, VBlankCycle: 2, EndCycle: 3, StateSHA256: h, BusSHA256: h, PixelSHA256: h}}}
	for _, kind := range []string{"valid", "wrong pin", "partial", "trailing", "unknown field"} {
		t.Run(kind, func(t *testing.T) {
			v := w
			if kind == "partial" {
				v.Complete = false
			}
			b, _ := json.Marshal(v)
			if kind == "trailing" {
				b = append(b, []byte(" {}")...)
			}
			if kind == "unknown field" {
				b = append([]byte(`{"borrowed_proof":true,`), b[1:]...)
			}
			path := filepath.Join(t.TempDir(), "window.json")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			pin := fmt.Sprintf("%x", sha256.Sum256(b))
			if kind == "wrong pin" {
				pin = h
			}
			var out bytes.Buffer
			err := run([]string{"-original", path, "-original-sha256", pin, "-edited", path, "-edited-sha256", pin, "-frame", "0", "-routine-start", "0x8000", "-routine-end", "0x8010"}, &out)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, err)
			}
			if err != nil && out.Len() != 0 {
				t.Fatal("refused comparison published partial JSON")
			}
			if err == nil {
				var report provenance.Comparison
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.CapturedProofEligible {
					t.Fatal("proof granted")
				}
			}
		})
	}
}
