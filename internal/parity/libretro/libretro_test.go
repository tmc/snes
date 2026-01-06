package libretro

import (
	"os"
	"testing"
)

func TestParityBridge_Load(t *testing.T) {
	// Locate the dylib
	// It should be in .../bsnes/bsnes/out/bsnes_libretro.dylib
	// We are in internal/parity

	// Assume run from module root
	libPath := "../../../../bsnes/bsnes/out/bsnes_libretro.dylib"
	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		t.Skipf("bsnes_libretro.dylib not found at %s. Build it first.", libPath)
	}

	bridge, err := New(libPath)
	if err != nil {
		t.Fatalf("Failed to load bridge: %v", err)
	}

	bridge.Init()

	version := bridge.retroApiVersion()
	t.Logf("Retro API Version: %d", version)
	if version != 1 {
		t.Errorf("Expected version 1, got %d", version)
	}
}
