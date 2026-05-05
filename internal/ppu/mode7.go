package ppu

func m7Signed13(v uint16) int {
	v &= 0x1FFF
	if v&0x1000 != 0 {
		return int(int16(v | 0xE000))
	}
	return int(v)
}

func (p *PPU) mode7TexelCoord(x, y int) (int, int) {
	a := int(int16(p.M7A))
	b := int(int16(p.M7B))
	c := int(int16(p.M7C))
	d := int(int16(p.M7D))

	tx := ((a * x) + (b * y)) >> 8
	ty := ((c * x) + (d * y)) >> 8
	return tx + m7Signed13(p.M7HOFS), ty + m7Signed13(p.M7VOFS)
}

func (p *PPU) mode7Sample(tx, ty int) byte {
	tx &= 0x3FF
	ty &= 0x3FF

	tileX := tx >> 3
	tileY := ty >> 3
	fineX := tx & 7
	fineY := ty & 7

	mask := VRAMSize - 1
	mapAddr := ((tileY*128 + tileX) << 1) & mask
	tile := p.VRAM[mapAddr]
	charAddr := (int(tile)<<7 + fineY*16 + fineX*2 + 1) & mask
	return p.VRAM[charAddr]
}
