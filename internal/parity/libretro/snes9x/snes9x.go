package snes9x

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns SNES_QUALIFY_SNES9X_CORE when set, otherwise
// the default path to the snes9x libretro core
func DefaultPath() string {
	if path := os.Getenv("SNES_QUALIFY_SNES9X_CORE"); path != "" {
		return path
	}
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	candidates := []string{
		filepath.Join(root, "..", "snes9x", "libretro", "snes9x_libretro.dylib"),
		filepath.Join(root, "..", "snes9x", "build", "snes9x_libretro.dylib"),
		filepath.Join(root, "..", "snes9x", "snes9x_libretro.dylib"),
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil && realRoot != root {
		candidates = append(candidates,
			filepath.Join(realRoot, "..", "snes9x", "libretro", "snes9x_libretro.dylib"),
			filepath.Join(realRoot, "..", "snes9x", "build", "snes9x_libretro.dylib"),
			filepath.Join(realRoot, "..", "snes9x", "snes9x_libretro.dylib"),
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
