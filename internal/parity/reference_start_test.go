package parity

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/tmc/snes/internal/parity/libretro"
)

var referenceWRAMStarts = make(map[*libretro.Bridge][]byte)

func captureReferenceWRAMStart(t *testing.T, core *libretro.Bridge, path string) {
	t.Helper()
	if core.GetMemorySize(2) < 0x10000 {
		t.Fatal("reference lacks 64 KiB WRAM")
	}
	start := make([]byte, 0x10000)
	for i := range start {
		start[i] = core.PeekWRAM(uint32(i))
	}
	referenceWRAMStarts[core] = start
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reference=%s core_sha256=%x initial_wram_sha256=%x phase=post-load-before-first-run", path, sha256.Sum256(image), sha256.Sum256(start))
	t.Cleanup(func() { delete(referenceWRAMStarts, core) })
}

// Unchanged startup bytes cannot establish parity merely because two cores
// happened to choose the same RAM fill. At least one reference must change.
func compareReferenceWRAM(t *testing.T, left, right *libretro.Bridge, addr uint32, got, value byte) (bool, error) {
	t.Helper()
	a, ok := referenceWRAMStarts[left]
	if !ok {
		t.Fatal("missing left reference startup snapshot")
	}
	b, ok := referenceWRAMStarts[right]
	if !ok {
		t.Fatal("missing right reference startup snapshot")
	}
	return compareObservedByte(got, value, a[addr], b[addr])
}

func compareObservedByte(got, reference, initialLeft, initialRight byte) (bool, error) {
	if reference == initialLeft && reference == initialRight {
		return false, nil
	}
	if got != reference {
		return true, fmt.Errorf("Go=%02X Ref=%02X", got, reference)
	}
	return true, nil
}

func TestReferenceStartupObservation(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		got, ref, left, right byte
		observed, fail        bool
	}{
		{"unchanged accidental 55", 0, 0x55, 0x55, 0x55, false, false},
		{"changed agreeing match", 0x55, 0x55, 0, 0x55, true, false},
		{"changed agreeing mismatch", 0, 0x55, 0, 0x55, true, true},
		{"both changed mismatch", 0, 0x55, 0, 1, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			observed, err := compareObservedByte(tt.got, tt.ref, tt.left, tt.right)
			if observed != tt.observed || (err != nil) != tt.fail {
				t.Fatalf("observed=%v error=%v", observed, err)
			}
		})
	}
}

func closeReference(t *testing.T, core *libretro.Bridge) {
	t.Helper()
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Errorf("close reference: %v", err)
		}
	})
}
