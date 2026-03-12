package snes9x

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns the default path to the snes9x libretro core
func DefaultPath() string {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	candidates := []string{
		filepath.Join(root, "..", "snes9x", "libretro", "snes9x_libretro.dylib"),
		filepath.Join(root, "..", "snes9x", "build", "snes9x_libretro.dylib"),
		filepath.Join(root, "..", "snes9x", "snes9x_libretro.dylib"),
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
