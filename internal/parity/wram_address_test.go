package parity

// isWRAM reports whether addr maps to WRAM, either directly via bank $7E/$7F
// or via the low-bank $0000-$1FFF mirror.
func isWRAM(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	off := addr & 0xFFFF
	if bank == 0x7E || bank == 0x7F {
		return true
	}
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && off < 0x2000 {
		return true
	}
	return false
}

// wramIndex returns the offset into the 128KB WRAM for an address that isWRAM.
// Only the low 64KB ($7E:0000-$7E:FFFF / mirror) is covered by the parity check;
// we return ^uint32(0) for the upper bank.
func wramIndex(addr uint32) uint32 {
	bank := (addr >> 16) & 0xFF
	off := addr & 0xFFFF
	switch {
	case bank == 0x7E:
		return off
	case bank == 0x7F:
		return 0xFFFFFFFF
	default:
		return off // low-bank mirror covers $0000-$1FFF
	}
}
