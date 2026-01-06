package bsnes

import (
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns the default path to the bsnes libretro core
func DefaultPath() string {
	_, filename, _, _ := runtime.Caller(0)
	// internal/parity/libretro/bsnes/bsnes.go
	// -> ../../../../../../bsnes/bsnes/out/bsnes_libretro.dylib
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	return filepath.Join(root, "..", "bsnes", "bsnes", "out", "bsnes_libretro.dylib")
}

func New(path string) (*libretro.Bridge, error) {
	if path == "" {
		path = DefaultPath()
	}
	return libretro.New(path)
}
