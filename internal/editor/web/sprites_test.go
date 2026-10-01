package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/tmc/snes/internal/provenance"
	"os"
	"path/filepath"
	"testing"
)

func spriteCapture(t *testing.T, events []provenance.Event) (string, string) {
	t.Helper()
	for i := range events {
		events[i].ID = uint64(i)
	}
	c := map[string]any{"schema": 1, "complete": true, "rom_sha256": fmt.Sprintf("%064x", 1), "writer_coverage": "all_cpu_dma_hdma_wram_and_wram_port", "from": 0, "to": 1, "events": events}
	b, _ := json.Marshal(c)
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	z.Write(b)
	z.Close()
	path := filepath.Join(t.TempDir(), "capture.gz")
	os.WriteFile(path, buf.Bytes(), 0600)
	return path, fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
}
func TestSpriteArtifact(t *testing.T) {
	path, pin := spriteCapture(t, []provenance.Event{{Kind: "ppu", Space: "oam", Op: "write", Addr: 0, Value: 7}})
	s, err := LoadSprites(path, pin, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 128 || s.Entries[0].ID != pin+":0:oam:0" {
		t.Fatal("unstable selection")
	}
	if s.Entries[0].Explanation.Bytes[0].Link != nil || s.Entries[127].Explanation.X != nil {
		t.Fatal("invented origin or missing value")
	}
	for _, frame := range []int{-1, 1} {
		if _, err := LoadSprites(path, pin, frame); err == nil {
			t.Fatal("unbounded frame")
		}
	}
	if _, err := LoadSprites(path, fmt.Sprintf("%064x", 2), 0); err == nil {
		t.Fatal("capture substitution")
	}
	bad, badpin := spriteCapture(t, []provenance.Event{{Kind: "ppu", Space: "oam", Addr: 544}})
	if _, err := LoadSprites(bad, badpin, 0); err == nil {
		t.Fatal("OAM overflow")
	}
}
func TestRetainedSprites(t *testing.T) {
	path := os.Getenv("SNES_SPRITE_CAPTURE")
	pin := os.Getenv("SNES_SPRITE_SHA256")
	if path == "" {
		t.Skip("retained sprite capture not configured")
	}
	s, err := LoadSprites(path, pin, 82)
	if err != nil {
		t.Fatal(err)
	}
	b := s.Entries[0].Explanation.Bytes[4]
	if b.Link == nil || b.Link.Writer == nil || b.Link.Status != "observed_last_writer" {
		t.Fatalf("missing retained high-table chain: %+v", b)
	}
	if s.Entries[0].Explanation.Bytes[0].Link != nil {
		t.Fatal("invented lowtable origin")
	}
	if out := os.Getenv("SNES_SPRITE_OUTPUT"); out != "" {
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("sprite0 highbyte512 writer event%d read%d register%d physical%d", b.Link.Writer.ID, b.Link.Read.ID, b.Link.RegisterWrite.ID, b.Link.OAMWrite.ID)
}
