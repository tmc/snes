package gsu

// The pixel cache is an 8-entry horizontal row of colour indices. PLOT
// writes into the cache at the column implied by R1; the row is derived
// from R2 via the CBR-based tile address. A commit is issued only when a
// new PLOT targets a *different* row, flushing the current cache into the
// tile row and clearing it. This mirrors bsnes sfc/coprocessor/superfx/
// plot.cpp.

// plotRow returns the CBR-derived tile row VRAM word address for the given
// (x, y) screen coordinates. The upper bits of CBR pin the tile screen
// base; the per-tile offset is (y/8 * tilesPerRow + x/8) * 16 because each
// 8x8 tile occupies 16 bytes regardless of bit depth (we store the low
// plane only and defer bit-plane interleave until flush).
func (d *Device) plotRow(x, y uint16) uint16 {
	tileX := x >> 3
	tileY := y >> 3
	rowInTile := y & 7
	// CBR holds the tile-screen base address in units of 16 bytes.
	base := uint16(d.CBR) &^ 0x000F
	return base + (tileY<<8|tileX)*16 + rowInTile*2
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
// cache value is returned; otherwise the cache is flushed and the call
// returns zero to signal that the caller must consult RAM (there is no
// RAM read-back path yet; games typically rely on the cache for reads that
// follow recent plots).
func (d *Device) rpix(x, y uint16) uint8 {
	row := d.plotRow(x, y)
	if d.cacheHasRow && row == d.cacheRow && d.validMask&(1<<(x&7)) != 0 {
		return d.pixels[x&7]
	}
	d.flushPixelCache()
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
	var row [8]byte
	for i := 0; i < 8; i++ {
		if d.validMask&(1<<i) != 0 {
			row[i] = d.pixels[i]
		}
	}
	if d.vram != nil {
		d.vram.WriteTileRow(d.cacheRow, row)
	} else {
		d.vramShadow = append(d.vramShadow, shadowCommit{Addr: d.cacheRow, Row: row})
	}
	d.commits++
	d.validMask = 0
	d.cacheHasRow = false
}
