package ppu

import "testing"

func TestStateRestoresActiveDisplayLatches(t *testing.T) {
	source := NewPPU()
	source.INIDISP = 0x0f
	source.vCounter = 100
	source.latchCGRAMAddr = 0x55
	source.latchOAMAddr = 0x40
	state := source.SaveState()
	for _, target := range []*PPU{source, NewPPU()} {
		target.latchCGRAMAddr = 0x66
		target.latchOAMAddr = 0x60
		target.LoadState(state)
		target.WriteRegister(0x2121, 0)
		target.WriteRegister(0x2122, 0x11)
		target.WriteRegister(0x2122, 0x22)
		target.WriteRegister(0x2102, 0)
		target.WriteRegister(0x2103, 0)
		target.WriteRegister(0x2104, 0x33)
		target.WriteRegister(0x2104, 0x44)
		if target.CGRAM[0xaa] != 0x11 || target.CGRAM[0xab] != 0x22 {
			t.Fatal("CGRAM write missed restored latch")
		}
		if target.OAM[0x40] != 0x33 || target.OAM[0x41] != 0x44 {
			t.Fatal("OAM write missed restored latch")
		}
		if target.CGRAM[0xcc] != 0 || target.OAM[0x60] != 0 {
			t.Fatal("write used mutated latch")
		}
	}
}
