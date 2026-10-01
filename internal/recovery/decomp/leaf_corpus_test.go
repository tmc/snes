package decomp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeafCorpusPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cases.jsonl", "negative-controls.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SNESDASM_LEAF_CORPUS", dir)
	if got := findRoutineCasesPath(t); got != filepath.Join(dir, "cases.jsonl") {
		t.Errorf("cases path=%s", got)
	}
	if got := findRoutineNegativeControlsPath(t); got != filepath.Join(dir, "negative-controls.jsonl") {
		t.Errorf("negative path=%s", got)
	}
}

func TestLeafOriginalContradictoryMetadata(t *testing.T) {
	t.Setenv("SNESDASM_LEAF_CORPUS", "")
	cases := loadRoutineCases(t, 1)
	c := cases[0]
	if c.Evidence.Fixture.DecompressedSHA256 != c.Evidence.Fixture.SHA256 {
		t.Skip("producer corpus no longer carries retained contradictory metadata")
	}
	rec, err := NewEvidenceVerifier("").Admit(&c, nil, nil)
	if rec.Admitted || err == nil || !strings.Contains(err.Error(), "embedded decompressed") {
		t.Fatalf("admitted=%v err=%v", rec.Admitted, err)
	}
}
