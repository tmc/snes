package gsu

// The pixel cache is an 8-entry horizontal row of colour indices. PLOT
// writes into the cache at the column implied by R1; the row is derived
// from R2 via the CBR-based tile address. A commit is issued only when a
// new PLOT targets a *different* row, flushing the current cache into the
// tile row and clearing it. This mirrors bsnes sfc/coprocessor/superfx/
// plot.cpp.

// plotRow returns the SCMR/SCBR-derived tile row VRAM word address for the
// given (x, y) screen coordinates.
func (d *Device) plotRow(x, y uint16) uint16 {
	xx := uint8(x)
	yy := uint8(y)
	var cn uint16
	switch d.screenHT() {
	case 0:
		cn = (uint16(xx&0xf8) << 1) + (uint16(yy&0xf8) >> 3)
	case 1:
		cn = (uint16(xx&0xf8) << 1) + (uint16(xx&0xf8) >> 1) + (uint16(yy&0xf8) >> 3)
	case 2:
		cn = (uint16(xx&0xf8) << 1) + uint16(xx&0xf8) + (uint16(yy&0xf8) >> 3)
	case 3:
		cn = (uint16(yy&0x80) << 2) + (uint16(xx&0x80) << 1) + (uint16(yy&0x78) << 1) + (uint16(xx&0x78) >> 3)
	}
	return uint16(uint32(cn)*(uint32(d.screenBPP())<<3)+uint32(d.SCBR)<<10+uint32(yy&7)*2) & 0xffff
}

func (d *Device) screenHT() uint8 {
	return ((d.SCMR >> 4) & 2) | ((d.SCMR >> 2) & 1)
}

func (d *Device) screenBPP() uint8 {
	switch d.SCMR & 3 {
	case 0:
		return 2
	case 1, 2:
		return 4
	default:
		return 8
	}
}

// plot stores a colour index at (x, y). If this PLOT targets a different
// tile row than the cache currently holds, the current cache is flushed
// first. The cache only commits on that transition: successive plots
// inside the same row accumulate without touching VRAM.
func (d *Device) plot(x, y uint16, color uint8) {
	if d.POR&porTransparent == 0 && color&0x0F == 0 {
		return
	}
	if d.POR&porDither != 0 {
		if (x^y)&1 != 0 {
			color >>= 4
		}
		color &= 0x0F
	}

	row := d.plotRow(x, y)
	if d.cacheHasRow && row != d.cacheRow {
		d.flushPixelCache()
	}
	d.cacheRow = row
	d.cacheHasRow = true
	d.pixels[x&7] = color
	d.validMask |= 1 << (x & 7)
}

func (d *Device) color(source uint8) uint8 {
	switch {
	case d.POR&porHighNibble != 0:
		return (d.COLR & 0xF0) | (source >> 4)
	case d.POR&porFreezeHigh != 0:
		return (d.COLR & 0xF0) | (source & 0x0F)
	default:
		return source
	}
}

// rpix returns the colour at (x, y). If it lies in the current cache, the
// cache value is returned; otherwise the cache is flushed before reading
// the last committed row.
func (d *Device) rpix(x, y uint16) uint8 {
	row := d.plotRow(x, y)
	if d.cacheHasRow && row == d.cacheRow && d.validMask&(1<<(x&7)) != 0 {
		return d.pixels[x&7]
	}
	d.flushPixelCache()
	if pixels, ok := d.vramRows[row]; ok {
		return pixels[x&7]
	}
	return 0
}

// flushPixelCache commits the 8-pixel cache. It increments the commit
// counter whenever any pixel had been written since the last flush; an
// empty cache is a no-op.
func (d *Device) flushPixelCache() {
	if !d.cacheHasRow || d.validMask == 0 {
		d.validMask = 0
		d.cacheHasRow = false
		return
	}
	row := d.vramRows[d.cacheRow]
	for i := 0; i < 8; i++ {
		if d.validMask&(1<<i) != 0 {
			row[i] = d.pixels[i]
		}
	}
	if d.vramRows == nil {
		d.vramRows = make(map[uint16][8]byte)
	}
	d.vramRows[d.cacheRow] = row
	if d.vram != nil {
		d.vram.WriteTileRow(d.cacheRow, row)
	} else {
		d.vramShadow = append(d.vramShadow, shadowCommit{Addr: d.cacheRow, Row: row})
	}
	d.commits++
	d.validMask = 0
	d.cacheHasRow = false
}
