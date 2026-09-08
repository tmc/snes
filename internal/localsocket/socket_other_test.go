//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package localsocket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsupportedPlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	l, err := Listen(path)
	if l != nil || err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Listen = %v, %v, want unsupported platform", l, err)
	}
	for _, name := range []string{path, path + ".lock"} {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("unsupported listener created %q: %v", name, err)
		}
	}
}
