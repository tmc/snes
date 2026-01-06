package ppu

import "testing"

func TestVRAMAccess(t *testing.T) {
	p := NewPPU()

	// Set VRAM Addr to $1000
	p.WriteRegister(0x2116, 0x00) // VMADDL
	p.WriteRegister(0x2117, 0x10) // VMADDH

	if p.VRAMAddr != 0x1000 {
		t.Errorf("VRAM Addr mismatch. Got %04X, expected 1000", p.VRAMAddr)
	}

	// Write Data $AABB
	// 2118 writes Low (AA), no inc (default VMAIN=0)
	// 2119 writes High (BB), inc
	p.WriteRegister(0x2118, 0xAA)
	p.WriteRegister(0x2119, 0xBB)

	// VRAM is word addressed. Index 0x1000 * 2 = 0x2000 / 0x2001.
	if p.VRAM[0x2000] != 0xAA {
		t.Errorf("VRAM Low byte mismatch")
	}
	if p.VRAM[0x2001] != 0xBB {
		t.Errorf("VRAM High byte mismatch")
	}

	if p.VRAMAddr != 0x1001 {
		t.Errorf("VRAM Addr should increment to 1001. Got %04X", p.VRAMAddr)
	}
}

func TestCGRAMAccess(t *testing.T) {
	p := NewPPU()

	// Addr 0
	p.WriteRegister(0x2121, 0x00)

	// Write Color $7FFF (White)
	// Low: FF, High: 7F
	p.WriteRegister(0x2122, 0xFF) // Low
	p.WriteRegister(0x2122, 0x7F) // High

	if p.CGRAM[0] != 0xFF {
		t.Errorf("CGRAM Low mismatch")
	}
	if p.CGRAM[1] != 0x7F {
		t.Errorf("CGRAM High mismatch")
	}

	// Should increment address to 1
	if p.CGRAMAddr != 1 {
		t.Errorf("CGRAM Addr should increment. Got %d", p.CGRAMAddr)
	}
}

func TestOAMAccess(t *testing.T) {
	p := NewPPU()

	p.WriteRegister(0x2102, 0x10) // Addr $10
	p.WriteRegister(0x2103, 0x00)

	p.WriteRegister(0x2104, 0xCC)

	if p.OAM[0x10] != 0xCC {
		t.Errorf("OAM Write failed")
	}
	// Should increment
	if p.OAMAddr != 0x11 {
		t.Errorf("OAM Addr should increment. Got %04X", p.OAMAddr)
	}
}
