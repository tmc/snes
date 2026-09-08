package bsnes

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns SNES_QUALIFY_BSNES_CORE when set, otherwise
// the default path to the bsnes libretro core
func DefaultPath() string {
	if path := os.Getenv("SNES_QUALIFY_BSNES_CORE"); path != "" {
		return path
	}
	_, filename, _, _ := runtime.Caller(0)
	// internal/parity/libretro/bsnes/bsnes.go
	// -> ../../../../../../bsnes/bsnes/out/bsnes_libretro.dylib
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	candidates := []string{
		filepath.Join(root, "..", "bsnes", "bsnes", "out", "bsnes_libretro.dylib"),
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil && realRoot != root {
		candidates = append(candidates, filepath.Join(realRoot, "..", "bsnes", "bsnes", "out", "bsnes_libretro.dylib"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0]
}

func New(path string) (*libretro.Bridge, error) {
	if path == "" {
		path = DefaultPath()
	}
	return libretro.New(path)
}
