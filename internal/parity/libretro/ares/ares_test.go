package ares

import (
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Fatal("DefaultPath returned empty path")
	}
	if filepath.Base(path) != "ares_libretro.dylib" {
		t.Fatalf("DefaultPath = %q, want ares_libretro.dylib basename", path)
	}
}
