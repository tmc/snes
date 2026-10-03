package ppu

// Phase 2.5 pixel-walk renderer. See docs/planning/phase2.5-pixel-walk.md
// for the design.

// layerPixel carries a single layer's contribution at a given screen-X
// through the compositor. color is the 15-bit BGR555 value (0 is a valid
// color — transparency is tracked by prio == 0). prio is the bsnes
// priority slot number for the (layer, tile-priority) pair in the current
// BG mode; a larger slot beats a smaller one in winner-take-all. source is
// the sourceBG1..sourceOBJ bit used by the color-math mask path.
type layerPixel struct {
	color  uint16
	prio   uint8
	source uint8
}

// Priority slot tables (per BG mode) transcribed from
// bsnes cb6ce0c bsnes/sfc/ppu-fast/io.cpp:597-699 updateVideoMode()
// (fetched 2026-04-24).
//
// Slot index layout:
//
//	0=BG1.0, 1=BG1.1, 2=BG2.0, 3=BG2.1, 4=BG3.0, 5=BG3.1,
//	6=BG4.0, 7=BG4.1, 8=OBJ.0, 9=OBJ.1, 10=OBJ.2, 11=OBJ.3
//
// A slot value of 0 means the layer/priority does not exist in this mode
// (for example BG4 in Mode 1) or did not draw a pixel; those pixels lose
// every compare. Non-zero values are the front-to-back stacking rank —
// larger wins.
//
// Mode 1 has two shapes: $2105 bit 3 clear keeps BG3-high below OBJ.3;
// bit 3 set promotes BG3-high to the top slot. priorityTables[1] covers
// the default; priorityTablesMode1BG3Hi covers the bit-3 override.
var priorityTables = [8][12]uint8{
	0: { // Mode 0: BG1/2/3/4 2bpp
		8, 11, 7, 10, 2, 5, 1, 4, 3, 6, 9, 12,
	},
	1: { // Mode 1, $2105 bit 3 clear: BG1/2 4bpp, BG3 2bpp
		6, 9, 5, 8, 1, 3, 0, 0, 2, 4, 7, 10,
	},
	2: { // Mode 2: BG1/2 4bpp with OPT
		3, 7, 1, 5, 0, 0, 0, 0, 2, 4, 6, 8,
	},
	3: { // Mode 3: BG1 8bpp, BG2 4bpp
		3, 7, 1, 5, 0, 0, 0, 0, 2, 4, 6, 8,
	},
	4: { // Mode 4: BG1 8bpp, BG2 2bpp with OPT
		3, 7, 1, 5, 0, 0, 0, 0, 2, 4, 6, 8,
	},
	5: { // Mode 5: BG1 4bpp, BG2 2bpp (hi-res)
		3, 7, 1, 5, 0, 0, 0, 0, 2, 4, 6, 8,
	},
	6: { // Mode 6: BG1 4bpp (hi-res, OPT)
		2, 5, 0, 0, 0, 0, 0, 0, 1, 3, 4, 6,
	},
	7: { // Mode 7: BG1 only (no tile-priority bit); BG1.0 carries the value
		2, 0, 0, 0, 0, 0, 0, 0, 1, 3, 4, 5,
	},
}

var priorityTableMode7EXTBG = [12]uint8{
	3, 0, 1, 5, 0, 0, 0, 0, 2, 4, 6, 7,
}

// priorityTableMode1BG3Hi overrides Mode 1 when $2105 bit 3 is set. BG3.1
// jumps from slot 3 to slot 10 (above all four OBJ priorities); all other
// OBJ/BG slots shift down by one.
var priorityTableMode1BG3Hi = [12]uint8{
	5, 8, 4, 7, 1, 10, 0, 0, 2, 3, 6, 9,
}

// Priority-table slot indices. Kept as constants to make the inner-loop
// calls readable (plotBG(slot=slotBG1Lo, ...)).
const (
	slotBG1Lo = 0
	slotBG1Hi = 1
	slotBG2Lo = 2
	slotBG2Hi = 3
	slotBG3Lo = 4
	slotBG3Hi = 5
	slotBG4Lo = 6
	slotBG4Hi = 7
	slotOBJ0  = 8
	slotOBJ1  = 9
	slotOBJ2  = 10
	slotOBJ3  = 11
)

// priorityTableFor returns the active priority table for the current PPU
// state: the default table for the BG mode, swapped for the Mode 1 BG3-hi
// variant when $2105 bit 3 is set.
func (p *PPU) priorityTableFor() *[12]uint8 {
	mode := p.BGMode & 0x07
	if mode == 1 && (p.BGMode&0x08) != 0 {
		return &priorityTableMode1BG3Hi
	}
	if mode == 7 && (p.SETINI&0x40) != 0 {
		return &priorityTableMode7EXTBG
	}
	return &priorityTables[mode]
}

// plot writes a layer's pixel into the given compositor buffer iff the
// new slot beats the existing slot. Matches bsnes line.cpp plotAbove /
// plotBelow: strict > compare, ties lose. A slot of 0 is a no-op
// (transparent or nonexistent layer), matching bsnes's initial state.
func plot(buf *[512]layerPixel, x int, slot uint8, color uint16, source uint8) {
	if slot == 0 {
		return
	}
	if slot > buf[x].prio {
		buf[x].color = color
		buf[x].prio = slot
		buf[x].source = source
	}
}

// pixelWalkResetBuffers clears the above/below layerPixel buffers to the
// backdrop state — prio=0 so every non-zero slot wins. Width is the
// active scanline width (256 in standard, 512 in hi-res).
func (p *PPU) pixelWalkResetBuffers(width int, backColor uint16) {
	for x := 0; x < width; x++ {
		p.pwAbove[x] = layerPixel{color: backColor, prio: 0, source: sourceBackdrop}
		p.pwBelow[x] = layerPixel{color: backColor, prio: 0, source: sourceBackdrop}
	}
	if p.LayerTraceActive {
		for x := 0; x < width && x < len(p.pwAbovePal); x++ {
			p.pwAbovePal[x] = 0
		}
	}
}

// renderScanlinePixelWalk is the Phase 2.5 scanline renderer. It walks
// pixels (0..Width-1) rather than tile columns, computes each enabled
// layer's contribution, and resolves the final color via a winner-take-
// all priority compositor.
//
// Mode 5/6 hi-res (Slice 5): the BG walker steps 512 sub-pixels, writing
// odd sub-pixels to pwAbove[x>>1] and even sub-pixels to pwBelow[x>>1].
// This matches bsnes ppu-fast/background.cpp:129 (even->plotBelow,
// odd->plotAbove). OBJ still renders at 256 columns and mirrors into
// both buffers so sprites appear on both sub-pixels (bsnes
// object.cpp:123). The TS subscreen pass is not re-run in hi-res — the
// hi-res walk fills pwBelow itself via the even sub-pixels. For the
// 256-wide FrontBuffer we emit pwAbove[x] (the odd sub-pixel, bsnes's
// main-screen value); pwBelow[x] serves as the color-math operand via
// pixelWalkApplyColorMath, same as a normal subscreen.
//
// Mode 7 (Phase 3) is also routed through here even though its BG
// renderer is not yet per-pixel.
func (p *PPU) renderScanlinePixelWalk(y int) {
	if (p.INIDISP & 0x80) != 0 {
		start := y * p.Width
		for x := 0; x < p.Width; x++ {
			p.FrontBuffer[start+x] = 0x0000
		}
		hiresStart := y * 512
		for x := 0; x < 512 && hiresStart+x < len(p.hiresFrontBuffer); x++ {
			p.hiresFrontBuffer[hiresStart+x] = 0
		}
		// Force-blank scanlines emit no pixels. Clear their trace rows
		// so the previous frame's layer and palette stamps do not persist.
		if p.LayerTraceActive && y >= 0 && y < 240 {
			for x := 0; x < 256; x++ {
				p.LayerSourceTrace[y][x] = 0
				p.LayerPaletteTrace[y][x] = 0
			}
		}
		return
	}

	brightness := p.INIDISP & 0x0F
	// pwAbove/pwBelow carry raw BGR555 values — brightness attenuation
	// happens per-pixel at the final FrontBuffer copy so that the backdrop
	// doesn't pay double brightness. (Double-applying brightness=0 rounds
	// non-black backdrops to black — broke TestVideoStartup.)
	rawBack := uint16(p.CGRAM[0]) | (uint16(p.CGRAM[1]) << 8)
	width := p.Width
	start := y * width

	mode := p.BGMode & 0x07
	hires := mode == 5 || mode == 6
	walkWidth := 256
	if hires {
		walkWidth = 512
	}

	p.pixelWalkResetBuffers(walkWidth, rawBack)

	// Pre-pass the per-line OAM scan so STAT77 RangeOver/TimeOver flags
	// update before evaluateOBJ (called from pixelWalkDrawOBJ) re-scans
	// internally.
	p.updateOBJFlags(y)

	table := p.priorityTableFor()

	if hires {
		// Single hi-res walk fills pwAbove (odd sub-pixels) and pwBelow
		// (even sub-pixels). TM gates the above stream and TS gates the
		// below stream, matching bsnes ppu-fast's separate above/below
		// layer enables.
		p.pixelWalkDrawLayersHires(y, p.TM, p.TS, table)
		if (p.TM|p.TS)&0x10 != 0 {
			p.pixelWalkDrawOBJHires(y, table)
		}
	} else {
		p.pixelWalkDrawLayers(y, p.TM, table, &p.pwAbove, false)
		if p.TM&0x10 != 0 {
			p.pixelWalkDrawOBJ(y, table, &p.pwAbove, false)
		}

		// Subscreen pass: same BG/OBJ code against pwBelow under TS.
		// Only runs when color math's CGWSEL bit 1 is set — otherwise
		// the below buffer stays at the backdrop and applyColorMathLine
		// reads nothing.
		useSub := (p.CGWSEL & 0x02) != 0
		if useSub {
			p.pixelWalkDrawLayers(y, p.TS, table, &p.pwBelow, true)
			if p.TS&0x10 != 0 {
				p.pixelWalkDrawOBJ(y, table, &p.pwBelow, true)
			}
		}
	}

	// Final compositor: copy the winning raw color from pwAbove into
	// FrontBuffer. In hi-res pwAbove[x] already holds the odd sub-pixel's
	// winner (bsnes main-screen). Apply color math on raw BGR555, then
	// apply brightness once to the final blended color.
	useSubscreen := (p.CGWSEL & 0x02) != 0
	if hires {
		p.finalizeHiresScanline(y, start, width, useSubscreen, brightness)
		return
	}

	for x := 0; x < width; x++ {
		p.FrontBuffer[start+x] = p.pwAbove[x].color
	}
	if p.LayerTraceActive && y >= 0 && y < 240 {
		for x := 0; x < 256 && x < width; x++ {
			p.LayerSourceTrace[y][x] = p.pwAbove[x].source
			p.LayerPaletteTrace[y][x] = p.pwAbovePal[x]
		}
	}
	p.pixelWalkApplyColorMath(start, width, useSubscreen)
	for x := 0; x < width; x++ {
		p.FrontBuffer[start+x] = applyBrightness(p.FrontBuffer[start+x], brightness)
	}
}

func (p *PPU) finalizeHiresScanline(y, start, width int, useSubscreen bool, brightness uint8) {
	hiresStart := y * 512
	if p.LayerTraceActive && y >= 0 && y < 240 {
		for x := 0; x < 256 && x < width; x++ {
			p.LayerSourceTrace[y][x] = p.pwAbove[x].source
			p.LayerPaletteTrace[y][x] = p.pwAbovePal[x]
		}
	}
	for x := 0; x < width; x++ {
		below := p.pwBelow[x]
		above := p.pwAbove[x]

		even := p.pixelWalkColorMathPixel(x, below.color, below.source, above.color, useSubscreen)
		odd := p.pixelWalkColorMathPixel(x, above.color, above.source, below.color, useSubscreen)
		even = applyBrightness(even, brightness)
		odd = applyBrightness(odd, brightness)

		if i := hiresStart + x*2; i+1 < len(p.hiresFrontBuffer) {
			p.hiresFrontBuffer[i] = even
			p.hiresFrontBuffer[i+1] = odd
		}
		p.FrontBuffer[start+x] = odd
	}
}

// pixelWalkDrawLayers dispatches BG rendering per BGMode into buf using
// the given TM/TS mask. Layers absent from a mode are simply not drawn,
// and their priority-table slots are zero so any stray calls would no-op
// anyway.
func (p *PPU) pixelWalkDrawLayers(y int, mask uint8, table *[12]uint8, buf *[512]layerPixel, below bool) {
	mode := p.BGMode & 0x07
	switch mode {
	case 0:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 2, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 2, 32, table, buf, below)
		}
		if mask&0x04 != 0 {
			p.pixelWalkDrawBG(y, 2, 2, 64, table, buf, below)
		}
		if mask&0x08 != 0 {
			p.pixelWalkDrawBG(y, 3, 2, 96, table, buf, below)
		}
	case 1:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 4, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 4, 0, table, buf, below)
		}
		if mask&0x04 != 0 {
			p.pixelWalkDrawBG(y, 2, 2, 0, table, buf, below)
		}
	case 2:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 4, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 4, 0, table, buf, below)
		}
	case 3:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 8, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 4, 0, table, buf, below)
		}
	case 4:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 8, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 2, 0, table, buf, below)
		}
	case 5:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 4, 0, table, buf, below)
		}
		if mask&0x02 != 0 {
			p.pixelWalkDrawBG(y, 1, 2, 0, table, buf, below)
		}
	case 6:
		if mask&0x01 != 0 {
			p.pixelWalkDrawBG(y, 0, 4, 0, table, buf, below)
		}
	case 7:
		if mask&0x01 != 0 {
			p.pixelWalkDrawMode7(y, table, buf, below)
		}
		if mask&0x02 != 0 && (p.SETINI&0x40) != 0 {
			p.pixelWalkDrawMode7EXTBG(y, table, buf, below)
		}
	}
}

func (p *PPU) pixelWalkDrawMode7(y int, table *[12]uint8, buf *[512]layerPixel, below bool) {
	slot := table[slotBG1Lo]
	directColor := p.CGWSEL&0x01 != 0
	mosaicSize, mosaicOn := p.mosaicParams(0)
	if mosaicOn {
		y = p.mosaicSnapY(y, mosaicSize)
	}
	for x := 0; x < p.Width; x++ {
		sx := x
		if mosaicOn {
			sx -= sx % mosaicSize
		}
		sy := y
		if p.M7XFlip {
			sx = 255 - sx
		}
		if p.M7YFlip {
			sy = 255 - sy
		}
		tx, ty := p.mode7TexelCoord(sx, sy)
		c, ok := p.mode7Sample(tx, ty)
		if !ok || c == 0 {
			continue
		}
		if p.layerMaskedByWindow(x, sourceBG1, below) {
			continue
		}

		color := uint16(0)
		if directColor {
			color = directColor555(c, 0)
		} else {
			color = uint16(p.CGRAM[int(c)*2]) | uint16(p.CGRAM[int(c)*2+1])<<8
			p.latchCGRAMAddr = c
		}
		plot(buf, x, slot, color, sourceBG1)
		p.tracePalAbove(buf, x, sourceBG1, c)
	}
}

func (p *PPU) pixelWalkDrawMode7EXTBG(y int, table *[12]uint8, buf *[512]layerPixel, below bool) {
	slotLo := table[slotBG2Lo]
	slotHi := table[slotBG2Hi]
	mosaicSize, mosaicOn := p.mosaicParams(1)
	if mosaicOn {
		y = p.mosaicSnapY(y, mosaicSize)
	}
	for x := 0; x < p.Width; x++ {
		sx := x
		if mosaicOn {
			sx -= sx % mosaicSize
		}
		sy := y
		if p.M7XFlip {
			sx = 255 - sx
		}
		if p.M7YFlip {
			sy = 255 - sy
		}
		tx, ty := p.mode7TexelCoord(sx, sy)
		c, ok := p.mode7Sample(tx, ty)
		prio := c & 0x80
		c &= 0x7F
		if !ok || c == 0 {
			continue
		}
		if p.layerMaskedByWindow(x, sourceBG2, below) {
			continue
		}

		slot := slotLo
		if prio != 0 {
			slot = slotHi
		}
		color := uint16(p.CGRAM[int(c)*2]) | uint16(p.CGRAM[int(c)*2+1])<<8
		p.latchCGRAMAddr = c
		plot(buf, x, slot, color, sourceBG2)
		p.tracePalAbove(buf, x, sourceBG2, c)
	}
}

// pixelWalkDrawOBJ is the thin OBJ adapter: calls evaluateOBJ to fill
// objColor/objPrio, then plots each non-transparent, non-window-masked
// sprite pixel into buf using the mode's OBJ priority slots.
func (p *PPU) pixelWalkDrawOBJ(y int, table *[12]uint8, buf *[512]layerPixel, below bool) {
	p.evaluateOBJ(y)
	objColor := p.objColor[:256]
	objPrio := p.objPrio[:256]
	objSource := p.objSource[:256]
	objPalette := p.objPalette[:256]
	objSlots := [4]uint8{table[slotOBJ0], table[slotOBJ1], table[slotOBJ2], table[slotOBJ3]}
	for x := 0; x < 256; x++ {
		prio := objPrio[x]
		if prio < 0 {
			continue
		}
		if p.layerMaskedByWindow(x, sourceOBJ, below) {
			continue
		}
		plot(buf, x, objSlots[prio&3], objColor[x], objSource[x])
		p.tracePalAbove(buf, x, objSource[x], objPalette[x])
	}
}

// pixelWalkDrawLayersHires dispatches BG rendering for Mode 5 (4bpp BG1
// + 2bpp BG2) and Mode 6 (4bpp BG1 + OPT) to the hi-res draw function,
// which walks 512 sub-pixels and splits them into pwAbove (odd) and
// pwBelow (even). TM gates the above stream and TS gates the below stream.
func (p *PPU) pixelWalkDrawLayersHires(y int, aboveMask, belowMask uint8, table *[12]uint8) {
	mode := p.BGMode & 0x07
	switch mode {
	case 5:
		if (aboveMask|belowMask)&0x01 != 0 {
			p.pixelWalkDrawBGHires(y, 0, 4, 0, aboveMask&0x01 != 0, belowMask&0x01 != 0, table)
		}
		if (aboveMask|belowMask)&0x02 != 0 {
			p.pixelWalkDrawBGHires(y, 1, 2, 0, aboveMask&0x02 != 0, belowMask&0x02 != 0, table)
		}
	case 6:
		if (aboveMask|belowMask)&0x01 != 0 {
			p.pixelWalkDrawBGHires(y, 0, 4, 0, aboveMask&0x01 != 0, belowMask&0x01 != 0, table)
		}
	}
}

// pixelWalkDrawOBJHires plots sprites into the hi-res streams selected by
// TM/TS. bsnes object.cpp builds separate above/below window masks and only
// plots OBJ to streams whose enable bit is set.
func (p *PPU) pixelWalkDrawOBJHires(y int, table *[12]uint8) {
	p.evaluateOBJ(y)
	objColor := p.objColor[:256]
	objPrio := p.objPrio[:256]
	objSource := p.objSource[:256]
	objPalette := p.objPalette[:256]
	objSlots := [4]uint8{table[slotOBJ0], table[slotOBJ1], table[slotOBJ2], table[slotOBJ3]}
	for x := 0; x < 256; x++ {
		prio := objPrio[x]
		if prio < 0 {
			continue
		}
		slot := objSlots[prio&3]
		color := objColor[x]
		source := objSource[x]
		if p.TM&0x10 != 0 && !p.layerMaskedByWindow(x, sourceOBJ, false) {
			plot(&p.pwAbove, x, slot, color, source)
			p.tracePalAbove(&p.pwAbove, x, source, objPalette[x])
		}
		if p.TS&0x10 != 0 && !p.layerMaskedByWindow(x, sourceOBJ, true) {
			plot(&p.pwBelow, x, slot, color, source)
		}
	}
}

func directColor555(c byte, palette uint16) uint16 {
	pr := (uint16(c)&0x07)<<2 | (palette&0x01)<<1
	pg := ((uint16(c)>>3)&0x07)<<2 | (palette & 0x02)
	pb := ((uint16(c)>>6)&0x03)<<3 | (palette & 0x04)
	return (pb << 10) | (pg << 5) | pr
}

// pixelWalkApplyColorMath bridges the existing applyColorMathLine path
// to the layerPixel buffers. It extracts the winning layer's source bit
// per pixel into a scratch uint8 slice, builds a 256-wide uint16 view of
// pwBelow.color, and invokes the canonical color-math code so the
// sub/fixed operand, add/subtract, half, and CGADSUB/CGWSEL mask
// handling stay unified across both renderers.
func (p *PPU) pixelWalkApplyColorMath(start, width int, useSubscreen bool) {
	if p.CGADSUB == 0 {
		return
	}
	traceY := -1
	if p.LayerTraceActive {
		traceY = (start / p.Width)
	}
	for x := 0; x < width; x++ {
		src := p.pwAbove[x].source
		before := p.FrontBuffer[start+x]
		p.FrontBuffer[start+x] = p.pixelWalkColorMathPixel(
			x,
			before,
			src,
			p.pwBelow[x].color,
			useSubscreen,
		)
		// Color-math source attribution:
		// COL replaces the trace's source label *only* when no
		// underlying main-screen BG/OBJ layer wrote the pixel. If the
		// pixel was already attributed to BG1..BG4 or OBJ, that
		// attribution wins -- color math is a transformation on top,
		// not a layer identity, and erasing BG1 with COL throws away
		// the diagnostic signal we actually want for parity. Concretely
		// we only stamp COL when src == sourceBackdrop, math fired
		// (source bit is in CGADSUB and the output differs from the
		// pre-math color), and the sub-screen contributed something
		// (pwBelow.source != sourceBackdrop). The sub-screen check
		// matches snes9x's `szrow[x] != 0` guard at the dumper site.
		if traceY >= 0 && traceY < 240 && x < 256 {
			if src == sourceBackdrop &&
				src&(p.CGADSUB&0x3F) != 0 &&
				p.FrontBuffer[start+x] != before &&
				p.pwBelow[x].source != sourceBackdrop {
				p.LayerSourceTrace[traceY][x] = SourceCOL
			}
		}
	}
}

func (p *PPU) pixelWalkColorMathPixel(x int, color uint16, source uint8, operand uint16, useSubscreen bool) uint16 {
	if p.CGADSUB == 0 {
		return color
	}
	if source&(p.CGADSUB&0x3F) == 0 {
		return color
	}
	mainEnabled := p.colorMathMainEnabledAt(x)
	if !p.colorMathOperandEnabledAt(x) {
		if !mainEnabled {
			return 0
		}
		return color
	}
	r, g, b := rgb555(color)
	if !mainEnabled {
		r, g, b = 0, 0, 0
	}
	second := pack555(uint16(p.FixedR), uint16(p.FixedG), uint16(p.FixedB))
	if useSubscreen {
		second = operand
	}
	fr, fg, fb := rgb555(second)
	if p.CGADSUB&0x80 != 0 {
		r = int(clamp5(r - fr))
		g = int(clamp5(g - fg))
		b = int(clamp5(b - fb))
	} else {
		r = int(clamp5(r + fr))
		g = int(clamp5(g + fg))
		b = int(clamp5(b + fb))
	}
	if p.CGADSUB&0x40 != 0 {
		r >>= 1
		g >>= 1
		b >>= 1
	}
	return pack555(uint16(r), uint16(g), uint16(b))
}

// pixelWalkDrawBG plots one BG layer into buf at pixel granularity with
// Phase 2 features threaded through. Mosaic snaps screen-x and sample-y
// before the scroll math; OPT (Mode 2/4, BG1/BG2) replaces the scroll
// offsets using bsnes's offsetX >= 8 carve-out and (hlookup & ~7) mask;
// Direct Color overrides the palette lookup for 8bpp Mode 3/4 BG1 under
// $2130 bit 0.
func (p *PPU) pixelWalkDrawBG(y, layer, bpp, palOffset int, table *[12]uint8, buf *[512]layerPixel, below bool) {
	var sc, nba uint8
	var hofs, vofs uint16
	switch layer {
	case 0:
		sc = p.BG1SC
		nba = p.BG12NBA & 0x0F
		hofs = p.BG1HOFS
		vofs = p.BG1VOFS
	case 1:
		sc = p.BG2SC
		nba = (p.BG12NBA >> 4) & 0x0F
		hofs = p.BG2HOFS
		vofs = p.BG2VOFS
	case 2:
		sc = p.BG3SC
		nba = p.BG34NBA & 0x0F
		hofs = p.BG3HOFS
		vofs = p.BG3VOFS
	case 3:
		sc = p.BG4SC
		nba = (p.BG34NBA >> 4) & 0x0F
		hofs = p.BG4HOFS
		vofs = p.BG4VOFS
	}

	tileBase := uint32(nba) << 13
	tileSize := uint32(32)
	switch bpp {
	case 2:
		tileSize = 16
	case 8:
		tileSize = 64
	}

	scrollX := int(hofs)
	scrollY := int(vofs)
	mask := uint32(VRAMSize - 1)
	tileSize16 := (p.BGMode & (0x10 << uint(layer))) != 0

	slotLo := table[slotBG1Lo+layer*2]
	slotHi := table[slotBG1Hi+layer*2]
	sourceBit := p.sourceBitForLayer(layer)

	// Mosaic params apply to this layer's sample-y snap and its screen-x
	// anchor-pixel replication. y was passed as the raw scanline index;
	// pre-snap it here so the tilemap row derivation below already sees
	// the anchor row.
	mosaicSize, mosaicOn := p.mosaicParams(layer)
	if mosaicOn {
		y = p.mosaicSnapY(y, mosaicSize)
	}

	// OPT is Mode 2/4, BG1/BG2 only. Bit (13+layer) gates per-layer
	// validity in the BG3 lookup; mode 2 issues two fetches (one at
	// bg3.voffset+0 for H, one at +8 for V), mode 4 issues one with
	// bit 15 routing H vs V.
	ppuMode := p.BGMode & 0x07
	optEnabled := (ppuMode == 2 || ppuMode == 4) && layer < 2
	var bg3TilemapAddr uint32
	var bg3ScrollX, bg3ScrollY int
	var optValidBit uint16
	if optEnabled {
		bg3TilemapAddr = uint32(p.BG3SC&0xFC) << 9
		bg3ScrollX = int(p.BG3HOFS)
		bg3ScrollY = int(p.BG3VOFS)
		optValidBit = uint16(0x2000) << uint(layer)
	}

	directColor := bpp == 8 && layer == 0 && (p.CGWSEL&0x01) != 0

	for x := 0; x < p.Width; x++ {
		// Mosaic horizontal snap: pixels inside a block sample the
		// anchor's scroll/opt state, which transparently replicates the
		// anchor's pixel across the block without needing a post-pass.
		effX := x
		if mosaicOn {
			effX = x - (x % mosaicSize)
		}

		hoffset := effX + scrollX
		voffset := y + scrollY

		if optEnabled {
			offsetX := effX + (scrollX & 7)
			if offsetX >= 8 {
				bg3Col := ((offsetX - 8) + (bg3ScrollX &^ 7)) >> 3
				bg3HRow := bg3ScrollY >> 3
				hlookup := p.optBG3Entry(bg3TilemapAddr, bg3Col, bg3HRow)
				if ppuMode == 4 {
					if hlookup&optValidBit != 0 {
						if hlookup&0x8000 == 0 {
							hoffset = offsetX + int(hlookup&^7)
						} else {
							voffset = y + int(hlookup)
						}
					}
				} else {
					bg3VRow := (bg3ScrollY + 8) >> 3
					vlookup := p.optBG3Entry(bg3TilemapAddr, bg3Col, bg3VRow)
					if hlookup&optValidBit != 0 {
						hoffset = offsetX + int(hlookup&^7)
					}
					if vlookup&optValidBit != 0 {
						voffset = y + int(vlookup)
					}
				}
			}
		}

		col := hoffset >> 3
		row := voffset >> 3
		if tileSize16 {
			col >>= 1
			row >>= 1
		}
		fineX := hoffset & 7
		fineY := voffset & 7

		entAddr := bgTilemapEntryAddr(sc, col, row)
		t0 := p.VRAM[entAddr&mask]
		t1 := p.VRAM[(entAddr+1)&mask]
		entry := uint16(t0) | uint16(t1)<<8

		tileIdx := uint32(entry & 0x3FF)
		palette := (entry >> 10) & 0x7
		prioBit := (entry >> 13) & 1
		mirrorX := (entry >> 14) & 1
		mirrorY := (entry >> 15) & 1
		if tileSize16 {
			if ((hoffset>>3)&1)^int(mirrorX) != 0 {
				tileIdx++
			}
			if ((voffset>>3)&1)^int(mirrorY) != 0 {
				tileIdx += 16
			}
		}
		if mirrorX != 0 {
			fineX ^= 7
		}
		if mirrorY != 0 {
			fineY ^= 7
		}

		tileDataAddr := tileBase + tileIdx*tileSize
		offset := uint32(fineY) * 2
		shift := 7 - uint(fineX)
		bit := byte(1 << shift)

		p0 := p.VRAM[(tileDataAddr+offset)&mask]
		p1 := p.VRAM[(tileDataAddr+offset+1)&mask]
		c := ((p0 & bit) >> shift) | (((p1 & bit) >> shift) << 1)
		if bpp == 4 || bpp == 8 {
			p2 := p.VRAM[(tileDataAddr+offset+16)&mask]
			p3 := p.VRAM[(tileDataAddr+offset+17)&mask]
			c |= ((p2 & bit) >> shift) << 2
			c |= ((p3 & bit) >> shift) << 3
		}
		if bpp == 8 {
			p4 := p.VRAM[(tileDataAddr+offset+32)&mask]
			p5 := p.VRAM[(tileDataAddr+offset+33)&mask]
			p6 := p.VRAM[(tileDataAddr+offset+48)&mask]
			p7 := p.VRAM[(tileDataAddr+offset+49)&mask]
			c |= ((p4 & bit) >> shift) << 4
			c |= ((p5 & bit) >> shift) << 5
			c |= ((p6 & bit) >> shift) << 6
			c |= ((p7 & bit) >> shift) << 7
		}
		if c == 0 {
			continue
		}

		var color uint16
		if directColor {
			// Direct Color encoding (CGWSEL bit 0): the BG pixel index
			// is interpreted as a packed BGR color rather than a CGRAM
			// index.
			color = directColor555(c, uint16(palette))
		} else {
			var palIdx uint16
			switch bpp {
			case 2:
				palIdx = uint16(palOffset) + (uint16(palette) * 4) + uint16(c)
			case 4:
				palIdx = (uint16(palette) * 16) + uint16(c)
			case 8:
				palIdx = uint16(c)
			}
			color = uint16(p.CGRAM[palIdx*2]) | uint16(p.CGRAM[palIdx*2+1])<<8
			p.latchCGRAMAddr = uint8(palIdx)
		}

		if p.layerMaskedByWindow(x, sourceBit, below) {
			continue
		}

		slot := slotLo
		if prioBit != 0 {
			slot = slotHi
		}
		plot(buf, x, slot, color, sourceBit)
		if p.LayerTraceActive {
			var traceIdx uint16
			switch bpp {
			case 2:
				traceIdx = uint16(palOffset) + (uint16(palette) * 4) + uint16(c)
			case 4:
				traceIdx = (uint16(palette) * 16) + uint16(c)
			case 8:
				traceIdx = uint16(c)
			}
			p.tracePalAbove(buf, x, sourceBit, byte(traceIdx))
		}
	}
}

// pixelWalkDrawBGHires is the 512-sub-pixel walker for Mode 5/6. Matches
// bsnes ppu-fast/background.cpp cb6ce0c semantics: the BG walker steps
// one screen sub-pixel at a time, hscroll is pre-shifted left by 1 so
// the walker moves in sub-pixel units, tile-column addressing strides
// 16 sub-pixels (log2=4) per cell, and within a 16-wide tile pair the
// `(hoffset & 8)` bit selects the right-half cell (+1 tile index).
// Plot routing: even sub-pixel (x & 1 == 0) -> pwBelow[x>>1]; odd
// (x & 1 == 1) -> pwAbove[x>>1]. Mosaic cell doubles horizontally in
// hi-res (bsnes background.cpp:115). OPT applies only to Mode 6 (and
// Mode 2/4 which route through the non-hires path); the BG3 lookup
// operates at 256-scale since BG3 itself is not hi-res.
func (p *PPU) pixelWalkDrawBGHires(y, layer, bpp, palOffset int, aboveEnable, belowEnable bool, table *[12]uint8) {
	var sc, nba uint8
	var hofs, vofs uint16
	switch layer {
	case 0:
		sc = p.BG1SC
		nba = p.BG12NBA & 0x0F
		hofs = p.BG1HOFS
		vofs = p.BG1VOFS
	case 1:
		sc = p.BG2SC
		nba = (p.BG12NBA >> 4) & 0x0F
		hofs = p.BG2HOFS
		vofs = p.BG2VOFS
	}

	tileBase := uint32(nba) << 13
	tileSize := uint32(32)
	switch bpp {
	case 2:
		tileSize = 16
	case 8:
		tileSize = 64
	}

	// hscroll in sub-pixel units. scrollXRaw preserved for OPT which
	// operates at 256-column granularity.
	scrollXRaw := int(hofs)
	scrollX := scrollXRaw << 1
	scrollY := int(vofs)
	mask := uint32(VRAMSize - 1)

	slotLo := table[slotBG1Lo+layer*2]
	slotHi := table[slotBG1Hi+layer*2]
	sourceBit := p.sourceBitForLayer(layer)
	tileSize16 := (p.BGMode & (0x10 << uint(layer))) != 0

	// Mosaic snap-y and horizontal-cell-doubling.
	mosaicSize, mosaicOn := p.mosaicParams(layer)
	if mosaicOn {
		y = p.mosaicSnapY(y, mosaicSize)
	}
	if p.SETINI&0x01 != 0 || (p.BGMode&0x07) == 5 || (p.BGMode&0x07) == 6 {
		y <<= 1
		if !mosaicOn {
			y |= p.FrameCount & 1
		}
	}
	// Hi-res mosaic cell widens to 2*size sub-pixels.
	mosaicCell := mosaicSize * 2

	// OPT is Mode 6 only in hi-res (Mode 5 has no OPT); operates on
	// the 256-column BG3 domain. Validity bit matches bsnes's
	// (0x2000 << layer) encoding.
	ppuMode := p.BGMode & 0x07
	optEnabled := ppuMode == 6 && layer < 2
	var bg3TilemapAddr uint32
	var bg3ScrollX, bg3ScrollY int
	var optValidBit uint16
	if optEnabled {
		bg3TilemapAddr = uint32(p.BG3SC&0xFC) << 9
		bg3ScrollX = int(p.BG3HOFS)
		bg3ScrollY = int(p.BG3VOFS)
		optValidBit = uint16(0x2000) << uint(layer)
	}

	for x := 0; x < 512; x++ {
		// Mosaic horizontal snap (in sub-pixel units).
		effX := x
		if mosaicOn {
			effX = x - (x % mosaicCell)
		}

		// Sub-pixel coordinates. hoffset is sub-pixel-absolute; col
		// indexes the tilemap row at 16-sub-pixel stride; subCol = 0
		// picks the left cell of a 16-wide tile pair, subCol = 1 picks
		// the right cell (tileIdx+1 per bsnes background.cpp:84).
		hoffset := effX + scrollX
		voffset := y + scrollY

		if optEnabled {
			// OPT operates on the 256-column base domain. Translate
			// the sub-pixel effX into a 256-column index then apply
			// the standard carve-out.
			opX := (effX >> 1) + (scrollXRaw & 7)
			if opX >= 8 {
				bg3Col := ((opX - 8) + (bg3ScrollX &^ 7)) >> 3
				bg3HRow := bg3ScrollY >> 3
				hlookup := p.optBG3Entry(bg3TilemapAddr, bg3Col, bg3HRow)
				if hlookup&optValidBit != 0 {
					// Replace the 256-domain hoffset with hlookup's
					// &~7-aligned value, then re-expand back to
					// sub-pixel units (shift left by 1). Keep the
					// walker's fine sub-pixel offset (effX & 15) so
					// pixel-granular carve-out semantics survive.
					hoffset = (opX+int(hlookup&^7))<<1 + (effX & 1)
				}
			}
		}

		col := hoffset >> 4
		row := voffset >> 3
		if tileSize16 {
			row >>= 1
		}
		// Within the 16-sub-pixel-wide tilemap entry, subCol picks the
		// left/right 8-pixel cell; fineX indexes the pixel inside
		// that cell.
		subCol := (hoffset >> 3) & 1
		fineX := (hoffset >> 1) & 7
		fineY := uint32(voffset & 7)

		entAddr := bgTilemapEntryAddr(sc, col, row)
		t0 := p.VRAM[entAddr&mask]
		t1 := p.VRAM[(entAddr+1)&mask]
		entry := uint16(t0) | uint16(t1)<<8

		tileIdx := uint32(entry & 0x3FF)
		palette := (entry >> 10) & 0x7
		prioBit := (entry >> 13) & 1
		mirrorX := (entry >> 14) & 1
		mirrorY := (entry >> 15) & 1
		// Mirror-X flips the sub-cell selection: bsnes XORs
		// (hoffset & 8) with mirrorX to decide the +1 stride.
		if subCol^int(mirrorX) != 0 {
			tileIdx++
		}
		if tileSize16 && (((voffset>>3)&1)^int(mirrorY) != 0) {
			tileIdx += 16
		}
		if mirrorY != 0 {
			fineY ^= 7
		}

		tileDataAddr := tileBase + tileIdx*tileSize
		offset := fineY * 2
		shift := 7 - uint(fineX)
		bit := byte(1 << shift)

		p0 := p.VRAM[(tileDataAddr+offset)&mask]
		p1 := p.VRAM[(tileDataAddr+offset+1)&mask]
		c := ((p0 & bit) >> shift) | (((p1 & bit) >> shift) << 1)
		if bpp == 4 {
			p2 := p.VRAM[(tileDataAddr+offset+16)&mask]
			p3 := p.VRAM[(tileDataAddr+offset+17)&mask]
			c |= ((p2 & bit) >> shift) << 2
			c |= ((p3 & bit) >> shift) << 3
		}
		if c == 0 {
			continue
		}

		var palIdx uint16
		switch bpp {
		case 2:
			palIdx = uint16(palOffset) + (uint16(palette) * 4) + uint16(c)
		case 4:
			palIdx = (uint16(palette) * 16) + uint16(c)
		}
		color := uint16(p.CGRAM[palIdx*2]) | uint16(p.CGRAM[palIdx*2+1])<<8
		p.latchCGRAMAddr = uint8(palIdx)

		// Window test: 256-column domain. layerMaskedByWindow takes
		// the screen column (sub-pixel X >> 1).
		X := x >> 1

		slot := slotLo
		if prioBit != 0 {
			slot = slotHi
		}
		if x&1 == 0 {
			if belowEnable && !p.layerMaskedByWindow(X, sourceBit, true) {
				plot(&p.pwBelow, X, slot, color, sourceBit)
			}
		} else {
			if aboveEnable && !p.layerMaskedByWindow(X, sourceBit, false) {
				plot(&p.pwAbove, X, slot, color, sourceBit)
			}
		}
	}
}
