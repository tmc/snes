package libretro

import (
	"os"
	"testing"
	"unsafe"
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

func TestParityBridgeCoreVariables(t *testing.T) {
	bridge := &Bridge{}
	bridge.SetCoreVariable("bsnes_ppu_fast", "OFF")
	bridge.SetCoreVariable("bsnes_entropy", "None")
	for _, tt := range []struct {
		key  string
		want string
	}{
		{"bsnes_ppu_fast", "OFF"},
		{"bsnes_entropy", "None"},
	} {
		key := cString(tt.key)
		variable := retroVariable{Key: &key[0]}
		if !bridge.getVariable(unsafe.Pointer(&variable)) {
			t.Fatalf("getVariable(%q) returned false", tt.key)
		}
		if got := cStringValue(variable.Value); got != tt.want {
			t.Fatalf("getVariable(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestParityBridgeUnknownCoreVariable(t *testing.T) {
	bridge := &Bridge{}
	key := cString("unknown_option")
	variable := retroVariable{Key: &key[0], Value: &key[0]}
	if bridge.getVariable(unsafe.Pointer(&variable)) {
		t.Fatal("getVariable(unknown_option) returned true")
	}
	if variable.Value != nil {
		t.Fatalf("unknown variable Value = %v, want nil", variable.Value)
	}
}

func TestParityBridgeDeleteCoreVariable(t *testing.T) {
	bridge := &Bridge{}
	bridge.SetCoreVariable("bsnes_entropy", "None")
	bridge.SetCoreVariable("bsnes_entropy", "")
	key := cString("bsnes_entropy")
	variable := retroVariable{Key: &key[0], Value: &key[0]}
	if bridge.getVariable(unsafe.Pointer(&variable)) {
		t.Fatal("getVariable(bsnes_entropy) returned true after delete")
	}
	if variable.Value != nil {
		t.Fatalf("deleted variable Value = %v, want nil", variable.Value)
	}
}
