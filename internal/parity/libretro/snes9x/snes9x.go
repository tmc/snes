package snes9x

import (
	"path/filepath"
	"runtime"

	"github.com/tmc/snes/internal/parity/libretro"
)

// DefaultPath returns the default path to the snes9x libretro core
func DefaultPath() string {
	_, filename, _, _ := runtime.Caller(0)
	// internal/parity/libretro/snes9x/snes9x.go
	// -> ../../../../../../gosnes/emulators/snes9x/...
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))
	// Stub location
	return filepath.Join(root, "..", "snes9x", "libretro", "snes9x_libretro.dylib")
}

func New(path string) (*libretro.Bridge, error) {
	if path == "" {
		path = DefaultPath()
	}
	return libretro.New(path)
}
