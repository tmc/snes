package parity

func cpuAPUPort(addr uint32) (port int, ok bool) {
	off := addr & 0xFFFF
	bank := (addr >> 16) & 0xFF
	ppuReachable := bank <= 0x3F || bank >= 0x80 && bank <= 0xBF
	if !ppuReachable || off < 0x2140 || off > 0x2143 {
		return 0, false
	}
	return int(off - 0x2140), true
}
