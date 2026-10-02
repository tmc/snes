package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/parity/libretro"
)

func hashBytes(buf []byte) string {
	h := sha256.Sum256(buf)
	return hex.EncodeToString(h[:])
}

func checkFile(t *testing.T, path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("Missing required file: %s", path)
	}
}

func compareSnapshot(got, want []byte) error {
	if len(got) == 0 || len(got) != len(want) {
		return fmt.Errorf("invalid snapshot lengths %d and %d", len(got), len(want))
	}
	for i, b := range got {
		if b != want[i] {
			return fmt.Errorf("offset %#x: got %#02x, want %#02x", i, b, want[i])
		}
	}
	return nil
}

func initializedWRAMROM() []byte {
	rom := makeIdleLoROM()
	copy(rom, []byte{0xa9, 0x5a, 0x8f, 0x00, 0x20, 0x7e, 0xa9, 0xa5, 0x8f, 0x01, 0x20, 0x7e, 0x80, 0xfe})
	return rom
}

func TestParityComparisonRejectsMismatch(t *testing.T) {
	for _, tt := range []struct {
		name      string
		got, want []byte
		fail      bool
	}{
		{"equal", []byte{1, 2}, []byte{1, 2}, false}, {"mutant", []byte{1, 3}, []byte{1, 2}, true},
		{"missing", nil, []byte{1}, true}, {"empty", nil, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := compareSnapshot(tt.got, tt.want); (err != nil) != tt.fail {
				t.Fatalf("error = %v, want failure %v", err, tt.fail)
			}
		})
	}
	fmt.Println("QUALIFY comparisons=4")
}

func TestPublicLoaderSmoke(t *testing.T) {
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(initializedWRAMROM()); err != nil {
		t.Fatal(err)
	}
	sys.Power()
	if err := sys.RunFrame(); err != nil {
		t.Fatal(err)
	}
	got := []byte{sys.Bus.Read(0x7e2000), sys.Bus.Read(0x7e2001)}
	if err := compareSnapshot(got, []byte{0x5a, 0xa5}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("QUALIFY comparisons=2")
}

func referenceFrameBGR555Width(core *libretro.Bridge) uint32 {
	if core.FramePitch != 0 {
		return core.FramePitch / 2
	}
	return core.FrameWidth
}

type simpleROM struct {
	data []byte
}

func (d *simpleROM) Read(addr uint32) uint8 {
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	romAddr := (uint32(bank) * 0x8000) + (offset - 0x8000)
	if romAddr < uint32(len(d.data)) {
		return d.data[romAddr]
	}
	return 0
}
func (d *simpleROM) Write(addr uint32, val uint8)             {}
func (d *simpleROM) BlockRead(addr uint32, length int) []byte { return nil }

func mapLoROM(sys *snes.System, rom []byte) {
	dev := &simpleROM{data: rom}
	for bank := uint32(0); bank <= 0x3F; bank++ {
		sys.Bus.Map(bank<<16|0x8000, bank<<16|0xFFFF, dev)
	}
	for bank := uint32(0x80); bank <= 0xBF; bank++ {
		sys.Bus.Map(bank<<16|0x8000, bank<<16|0xFFFF, dev)
	}
}

func observeDMACompletion(sys *snes.System, after func()) func() {
	prev := sys.CPU.BusEdge
	sys.CPU.BusEdge = func(n uint64) {
		if prev != nil {
			prev(n)
		}
		state := sys.DMA.SaveState().Execution
		if !sys.DMA.Busy() && !state.Armed && !state.Pending {
			after()
		}
	}
	return func() { sys.CPU.BusEdge = prev }
}

// dmaPreviewByte avoids device reads and observer-induced open-bus changes.
func dmaPreviewByte(sys *snes.System, rom []byte, addr uint32) (uint8, bool) {
	if ram, ok := sys.Bus.GetPage(addr>>16, (addr>>8)&0xff).(*bus.RAMDevice); ok {
		return ram.Read(addr), true
	}
	if len(rom) > 512 && len(rom)&0x7fff == 512 {
		rom = rom[512:]
	}
	if off, ok := sys.ROMAddress(addr); ok && uint64(off) < uint64(len(rom)) {
		return rom[off], true
	}
	return 0, false
}
