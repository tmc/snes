package ares

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathExplicitOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "missing-core.dylib")
	t.Setenv("SNES_QUALIFY_ARES_CORE", want)
	if got := DefaultPath(); got != want {
		t.Fatalf("path = %q, want explicit %q", got, want)
	}
	if _, err := New(""); err == nil {
		t.Fatal("missing explicit core silently accepted")
	}
}
