package decomp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompositionCorpusPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corrected")
	t.Setenv("SNESDASM_COMPOSITION_CORPUS", dir)
	for _, name := range []string{"cases.jsonl", "negative-controls.jsonl", "swap-controls.jsonl"} {
		if got := compositionCorpusPath(t, name); got != filepath.Join(dir, name) {
			t.Errorf("path=%s", got)
		}
	}
	t.Setenv("SNESDASM_COMPOSITION_CORPUS", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := compositionCorpusPath(t, "cases.jsonl"); got != filepath.Join(home, "tmp", "agent-collab", "snes", "replay-cases", "composition-099f91", "cases.jsonl") {
		t.Errorf("default path=%s", got)
	}
}

func TestCompositionOriginalContradictoryMetadata(t *testing.T) {
	t.Setenv("SNESDASM_COMPOSITION_CORPUS", "")
	cases := load099F91Cases(t, "cases.jsonl")
	if len(cases) == 0 {
		t.Fatal("empty original corpus")
	}
	c := cases[0]
	if c.Evidence.Fixture.DecompressedSHA256 != c.Evidence.Fixture.SHA256 {
		t.Skip("producer corpus no longer carries the retained contradictory metadata")
	}
	rec, err := NewEvidenceVerifier("").Admit(&c, nil, nil)
	if rec.Admitted || err == nil || !strings.Contains(err.Error(), "embedded decompressed") {
		t.Fatalf("admitted=%v err=%v, want contradictory metadata rejection", rec.Admitted, err)
	}
}
