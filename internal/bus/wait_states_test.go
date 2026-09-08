package bus

import "testing"

func TestWaitStateAddressClasses(t *testing.T) {
	// Expected costs come from bsnes sfc/cpu/memory.cpp, not from the table
	// under test. Unmapped addresses still incur their address-bus timing.
	tests := []struct {
		name       string
		addr       uint32
		slow, fast uint64
	}{
		{"wram", 0x7e4016, 8, 8},
		{"wram-high", 0x7fffff, 8, 8},
		{"low-ram", 0x001fff, 8, 8},
		{"ppu-start", 0x002000, 6, 6},
		{"ppu-end", 0x003fff, 6, 6},
		{"joy-start", 0x004000, 12, 12},
		{"joy-mirror", 0xbf4016, 12, 12},
		{"joy-end", 0x8041ff, 12, 12},
		{"cpu-io-start", 0x004200, 6, 6},
		{"cpu-io-end", 0xbf5fff, 6, 6},
		{"ram-window", 0x006000, 8, 8},
		{"low-rom", 0x008000, 8, 8},
		{"high-rom", 0x808000, 8, 6},
		{"high-rom-end", 0xbfffff, 8, 6},
		{"full-bank-rom", 0xc00000, 8, 6},
		{"full-bank-joy-offset", 0xc04016, 8, 6},
		{"rom-end", 0xffffff, 8, 6},
		{"non-io-bank", 0x404016, 8, 8},
	}
	b := NewBus()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []uint8{0, 1, 0} {
				b.WriteMEMSEL(mode)
				want := tt.slow
				if mode == 1 {
					want = tt.fast
				}
				if got := b.GetWaitStates(tt.addr); got != want {
					t.Fatalf("%06x MEMSEL=%d: cycles=%d, want %d", tt.addr, mode, got, want)
				}
			}
		})
	}
}
