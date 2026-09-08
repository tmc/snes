package ares

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns SNES_QUALIFY_ARES_CORE when set, otherwise
// the default path to the ares libretro core.
func DefaultPath() string {
	if path := os.Getenv("SNES_QUALIFY_ARES_CORE"); path != "" {
		return path
	}
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	candidates := []string{
		filepath.Join(root, "..", "ares", "ares_libretro.dylib"),
		filepath.Join(root, "..", "ares", "out", "ares_libretro.dylib"),
		filepath.Join(root, "..", "ares", "mia", "ares_libretro.dylib"),
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil && realRoot != root {
		candidates = append(candidates,
			filepath.Join(realRoot, "..", "ares", "ares_libretro.dylib"),
			filepath.Join(realRoot, "..", "ares", "out", "ares_libretro.dylib"),
			filepath.Join(realRoot, "..", "ares", "mia", "ares_libretro.dylib"),
		)
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
