package libretro

import (
	"bytes"
	"fmt"
	"testing"
	"unsafe"
)

func ExampleBridge_SetInitialWRAM() {
	var core Bridge
	fmt.Println(core.SetInitialWRAM(0xaa))
	// Output: initial wram requires a loaded game before first run
}

func TestSetInitialWRAM(t *testing.T) {
	for _, tt := range []struct {
		name          string
		loaded, ran   bool
		size          uint64
		missing, fail bool
	}{
		{"loaded", true, false, 128 * 1024, false, false},
		{"not loaded", false, false, 128 * 1024, false, true},
		{"already run", true, true, 128 * 1024, false, true},
		{"wrong size", true, false, 128*1024 - 1, false, true},
		{"missing", true, false, 128 * 1024, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ram := bytes.Repeat([]byte{0x12}, 128*1024)
			core := Bridge{gameLoaded: tt.loaded, hasRun: tt.ran, retroGetMemorySize: func(uint32) uint64 { return tt.size }, retroGetMemoryData: func(uint32) unsafe.Pointer {
				if tt.missing {
					return nil
				}
				return unsafe.Pointer(&ram[0])
			}}
			err := core.SetInitialWRAM(0xaa)
			if (err != nil) != tt.fail {
				t.Fatalf("error=%v", err)
			}
			want := byte(0xaa)
			if tt.fail {
				want = 0x12
			}
			if !bytes.Equal(ram, bytes.Repeat([]byte{want}, len(ram))) {
				t.Fatal("memory changed incorrectly")
			}
		})
	}
}

func TestSetInitialWRAMAfterRun(t *testing.T) {
	ram := make([]byte, 128*1024)
	core := Bridge{gameLoaded: true, retroRun: func() {}, retroGetMemorySize: func(uint32) uint64 { return uint64(len(ram)) }, retroGetMemoryData: func(uint32) unsafe.Pointer { return unsafe.Pointer(&ram[0]) }}
	core.Run()
	if err := core.SetInitialWRAM(0xaa); err == nil {
		t.Fatal("startup mutation accepted after Run")
	}
	if !bytes.Equal(ram, make([]byte, len(ram))) {
		t.Fatal("rejection changed memory")
	}
}
