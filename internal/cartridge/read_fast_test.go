package cartridge

import "testing"

// TestReadPlainROM checks that reads of plain carts agree with the full
// mapping, which Read bypasses for $8000-$FFFF of LoROM and HiROM carts.
func TestReadPlainROM(t *testing.T) {
	for _, mode := range []MappingMode{LoROM, HiROM, ExLoROM, ExHiROM} {
		for _, size := range []int{0x8000, 0x60000, 0x100000, 0x300000, 0x400000} {
			for _, ramSize := range []int{0, 0x2000} {
				c := &Cartridge{ROM: make([]byte, size), RAM: make([]byte, 0x2000), RAMSize: ramSize, Mode: mode}
				for i := range c.ROM {
					c.ROM[i] = byte(uint32(i) * 2654435761 >> 24)
				}
				var addrs []uint32
				for addr := uint32(0); addr < 1<<24; addr += 0x3F {
					addrs = append(addrs, addr)
				}
				for bank := uint32(0); bank < 0x100; bank++ {
					// ROM mirror boundaries fall at bank starts.
					addrs = append(addrs, bank<<16|0x8000, bank<<16|0xFFFF)
				}
				for _, addr := range addrs {
					bank, offset := addr>>16, addr&0xFFFF
					var want uint8
					if c.isSRAMAddress(bank, offset) {
						want = c.RAM[c.sramAddress(bank, offset)]
					} else if pc, ok := c.romAddress(addr); ok {
						want = c.ROM[pc]
					}
					if got := c.Read(addr); got != want {
						t.Fatalf("mode %d, ROM %#x, RAM %#x: Read(%06x) = %02x, want %02x", mode, size, ramSize, addr, got, want)
					}
				}
			}
		}
	}
}
