package parity

import (
	"testing"

	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

// TestControlledWRAMObservation runs the same controls in both core orders.
// Agreement from deliberately different starting bytes proves a change in at
// least one reference; it does not claim to trace every write in both cores.
func TestControlledWRAMObservation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, writes := range []bool{false, true} {
			name := "no-write"
			if writes {
				name = "writes"
			}
			if reverse {
				name += "/reverse"
			}
			t.Run(name, func(t *testing.T) {
				rom := makeIdleLoROM()
				if writes {
					rom = initializedWRAMROM()
				}
				path := writeTempROM(t, "startup.sfc", rom)
				var left, right *libretro.Bridge
				if reverse {
					right = runHiganReferenceWithWRAM(t, snes9x.DefaultPath(), path, 1, 0x55)
					left = runHiganReferenceWithWRAM(t, bsnes.DefaultPath(), path, 1, 0xaa)
				} else {
					left = runHiganReferenceWithWRAM(t, bsnes.DefaultPath(), path, 1, 0xaa)
					right = runHiganReferenceWithWRAM(t, snes9x.DefaultPath(), path, 1, 0x55)
				}
				for _, addr := range []uint32{0x2000, 0x2001} {
					l, r := left.PeekWRAM(addr), right.PeekWRAM(addr)
					if !writes {
						if l != 0xaa || r != 0x55 {
							t.Fatalf("no-write bytes=%02x/%02x", l, r)
						}
						continue
					}
					if l != r {
						t.Fatalf("references disagree: %02x/%02x", l, r)
					}
					want := byte(0x5a)
					if addr == 0x2001 {
						want = 0xa5
					}
					if l != want {
						t.Fatalf("reference result=%02x want %02x", l, want)
					}
					if observed, err := compareReferenceWRAM(t, left, right, addr, want, l); !observed || err != nil {
						t.Fatalf("positive observed=%v error=%v", observed, err)
					}
					if observed, err := compareReferenceWRAM(t, left, right, addr, want^1, l); !observed || err == nil {
						t.Fatalf("mismatch control observed=%v error=%v", observed, err)
					}
				}
			})
		}
	}
}
