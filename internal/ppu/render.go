package ppu

// Source bits identify the layer that produced a layerPixel. Bits 0..5
// mirror the SNES CGADSUB layout (bit 0 BG1 ... bit 4 OBJ ... bit 5 BACK)
// so a `source & (CGADSUB & 0x3F)` test gates color-math eligibility
// directly.
//
// bsnes ppu-fast splits OBJ into two source identities (object.cpp:125):
// palettes 0-3 are OBJ1, palettes 4-7 are OBJ2. Color-math hardware only
// gates OBJ2 — io.cpp:570 hardcodes io.col.enable[OBJ1]=0 while bit 4 of
// CGADSUB drives io.col.enable[OBJ2]. We mirror that by giving OBJ2 the
// CGADSUB-bit-4 source (sourceOBJ / sourceOBJ2) and OBJ1 a distinct bit
// outside the 0x3F mask, so OBJ1 is naturally ineligible for color math
// no matter what CGADSUB holds.
const (
	sourceBG1      = 1 << 0
	sourceBG2      = 1 << 1
	sourceBG3      = 1 << 2
	sourceBG4      = 1 << 3
	sourceOBJ      = 1 << 4 // OBJ palettes 4..7 (CGADSUB bit 4)
	sourceOBJ2     = sourceOBJ
	sourceBackdrop = 1 << 5
	sourceOBJ1     = 1 << 6 // OBJ palettes 0..3, never color-math eligible
)

// Exported source-bit constants for parity harnesses that need to
// translate LayerSourceTrace bytes into the snes9x v2 CSV layer-source
// strings. The internal lowercase constants stay the canonical names
// inside the renderer; these mirror them for cross-package use.
const (
	SourceBG1      = sourceBG1
	SourceBG2      = sourceBG2
	SourceBG3      = sourceBG3
	SourceBG4      = sourceBG4
	SourceOBJ      = sourceOBJ
	SourceOBJ2     = sourceOBJ2
	SourceBackdrop = sourceBackdrop
	SourceOBJ1     = sourceOBJ1

	// SourceCOL is a synthesized trace bit set on cells whose final
	// composite color was rewritten by color math. It does not
	// correspond to any plot-time source — color math runs after
	// composite, so the original source bits in pwAbove still reflect
	// the underlying main-screen layer. The trace stamps SourceCOL
	// over those bits for cells where color-math fired and produced a
	// non-passthrough result.
	SourceCOL = 1 << 7
)

type spriteLine struct {
	x      int
	yPos   int
	tile   uint8
	attrs  uint8
	width  int
	height int
}

var (
	smallWidths  = [8]int{8, 8, 8, 16, 16, 32, 16, 16}
	smallHeights = [8]int{8, 8, 8, 16, 16, 32, 32, 32}
	largeWidths  = [8]int{16, 32, 64, 32, 64, 64, 32, 32}
	largeHeights = [8]int{16, 32, 64, 32, 64, 64, 64, 32}
)

func objSize(sizeSel uint8, large bool) (width, height int) {
	if large {
		return largeWidths[sizeSel], largeHeights[sizeSel]
	}
	return smallWidths[sizeSel], smallHeights[sizeSel]
}

func objInterlaceSize(sizeSel uint8, large bool, width, height int) (int, int) {
	if !large && sizeSel >= 6 {
		height = 16
	}
	return width, height
}

func objVisibleHeight(height int, interlace bool) int {
	if interlace {
		return height >> 1
	}
	return height
}

func objRangeBaseX(x int) int {
	if x == 256 {
		return 0
	}
	if (x & 0x100) != 0 {
		return x - 512
	}
	return x
}

func objInRange(x, size int) bool {
	rx := objRangeBaseX(x)
	return rx > -size && rx < 256
}

func objTileInTime(x, col int) bool {
	tx := objRangeBaseX(x) + (col << 3)
	return tx > -8 && tx < 256
}

func clamp5(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 31 {
		return 31
	}
	return uint16(v)
}

func rgb555(c uint16) (r, g, b int) {
	r = int(c & 0x1F)
	g = int((c >> 5) & 0x1F)
	b = int((c >> 10) & 0x1F)
	return
}

func pack555(r, g, b uint16) uint16 {
	return (b << 10) | (g << 5) | r
}

func applyBrightness(c uint16, b uint8) uint16 {
	// bsnes ppu-fast lightTable: attenuated = round((b/15) * channel),
	// implemented as the integer (b*channel + 7) / 15. b=0 yields exact
	// black; b=15 is identity. Matches bsnes/snes9x at INIDISP brightness=0.
	imgB := (c >> 10) & 0x1F
	imgG := (c >> 5) & 0x1F
	imgR := c & 0x1F

	l := uint16(b & 0x0F)
	imgB = (imgB*l + 7) / 15
	imgG = (imgG*l + 7) / 15
	imgR = (imgR*l + 7) / 15

	return (imgB << 10) | (imgG << 5) | imgR
}

func windowInRange(x int, left, right uint8) bool {
	u := uint8(x)
	return u >= left && u <= right
}

func windowCombine(oneEnable bool, one bool, twoEnable bool, two bool, mask uint8) bool {
	if !oneEnable {
		return two && twoEnable
	}
	if !twoEnable {
		return one
	}
	switch mask & 0x03 {
	case 0:
		return one || two
	case 1:
		return one && two
	case 2:
		return one != two
	default:
		return one == two
	}
}

func colorWindowMaskSelect(value bool, sel uint8) bool {
	switch sel & 0x03 {
	case 0:
		return true
	case 1:
		return value
	case 2:
		return !value
	default:
		return false
	}
}

func (p *PPU) layerWindowParams(sourceBit uint8) (sel uint8, mask uint8, ok bool) {
	switch sourceBit {
	case sourceBG1:
		return p.W12SEL & 0x0F, (p.WBGLOG >> 0) & 0x03, true
	case sourceBG2:
		return (p.W12SEL >> 4) & 0x0F, (p.WBGLOG >> 2) & 0x03, true
	case sourceBG3:
		return p.W34SEL & 0x0F, (p.WBGLOG >> 4) & 0x03, true
	case sourceBG4:
		return (p.W34SEL >> 4) & 0x0F, (p.WBGLOG >> 6) & 0x03, true
	case sourceOBJ, sourceOBJ1:
		return p.WOBJSEL & 0x0F, (p.WOBJLOG >> 0) & 0x03, true
	default:
		return 0, 0, false
	}
}

func (p *PPU) windowValueAt(x int, sel uint8, mask uint8) bool {
	oneEnable := (sel & 0x02) != 0
	one := windowInRange(x, p.WH0, p.WH1)
	if (sel & 0x01) != 0 {
		one = !one
	}
	twoEnable := (sel & 0x08) != 0
	two := windowInRange(x, p.WH2, p.WH3)
	if (sel & 0x04) != 0 {
		two = !two
	}
	return windowCombine(oneEnable, one, twoEnable, two, mask)
}

func (p *PPU) layerMaskedByWindow(x int, sourceBit uint8, below bool) bool {
	enable := p.TMW
	if below {
		enable = p.TSW
	}
	var bit uint8
	switch sourceBit {
	case sourceBG1:
		bit = 1 << 0
	case sourceBG2:
		bit = 1 << 1
	case sourceBG3:
		bit = 1 << 2
	case sourceBG4:
		bit = 1 << 3
	case sourceOBJ, sourceOBJ1:
		bit = 1 << 4
	default:
		return false
	}
	if (enable & bit) == 0 {
		return false
	}
	sel, mask, ok := p.layerWindowParams(sourceBit)
	if !ok {
		return false
	}
	return p.windowValueAt(x, sel, mask)
}

func (p *PPU) colorWindowValueAt(x int) bool {
	sel := (p.WOBJSEL >> 4) & 0x0F
	mask := (p.WOBJLOG >> 2) & 0x03
	return p.windowValueAt(x, sel, mask)
}

func (p *PPU) colorMathWindowEnabledAt(x int, below bool) bool {
	value := p.colorWindowValueAt(x)
	sel := (p.CGWSEL >> 6) & 0x03 // above/main
	if below {
		sel = (p.CGWSEL >> 4) & 0x03
	}
	return colorWindowMaskSelect(value, sel)
}

func (p *PPU) colorMathMainEnabledAt(x int) bool {
	value := p.colorWindowValueAt(x)
	return colorWindowMaskSelect(value, (p.CGWSEL>>6)&0x03)
}

func (p *PPU) colorMathOperandEnabledAt(x int) bool {
	value := p.colorWindowValueAt(x)
	return colorWindowMaskSelect(value, (p.CGWSEL>>4)&0x03)
}

func (p *PPU) applyColorMathLine(start int, source []uint8, second []uint16, useSubscreen bool) {
	if p.CGADSUB == 0 {
		return
	}
	fixed := pack555(uint16(p.FixedR), uint16(p.FixedG), uint16(p.FixedB))
	subtract := (p.CGADSUB & 0x80) != 0
	half := (p.CGADSUB & 0x40) != 0
	mathMask := p.CGADSUB & 0x3F

	for x := 0; x < p.Width; x++ {
		if source[x]&mathMask == 0 {
			continue
		}
		c := p.FrontBuffer[start+x]
		mainEnabled := p.colorMathMainEnabledAt(x)
		if !p.colorMathOperandEnabledAt(x) {
			if !mainEnabled {
				p.FrontBuffer[start+x] = 0
			}
			continue
		}
		r, g, b := rgb555(c)
		if !mainEnabled {
			r, g, b = 0, 0, 0
		}
		operand := fixed
		if useSubscreen && second != nil {
			operand = second[x]
		}
		fr, fg, fb := rgb555(operand)

		if subtract {
			r = int(clamp5(r - fr))
			g = int(clamp5(g - fg))
			b = int(clamp5(b - fb))
		} else {
			r = int(clamp5(r + fr))
			g = int(clamp5(g + fg))
			b = int(clamp5(b + fb))
		}
		if half {
			r >>= 1
			g >>= 1
			b >>= 1
		}
		p.FrontBuffer[start+x] = pack555(uint16(r), uint16(g), uint16(b))
	}
}

func (p *PPU) sourceBitForLayer(layer int) uint8 {
	switch layer {
	case 0:
		return sourceBG1
	case 1:
		return sourceBG2
	case 2:
		return sourceBG3
	case 3:
		return sourceBG4
	default:
		return 0
	}
}

// mosaicParams returns (size, enabled) for a given BG layer index (0..3).
// $2106 layout: bits 7..4 hold size-1 (0000 => 1x1 => no mosaic), bits 3..0
// hold per-layer enable (bit 0 = BG1 ... bit 3 = BG4). Size of 1 is a
// no-op regardless of the enable bit.
func (p *PPU) mosaicParams(layer int) (size int, enabled bool) {
	size = int((p.MOSAIC>>4)&0x0F) + 1
	enabled = size > 1 && (p.MOSAIC&(1<<uint(layer))) != 0
	return
}

// mosaicSnapY implements the vertical-mosaic snap used by every BG layer.
// The caller already passes y as the visible-frame-relative scanline index
// (0 on the first visible line), so the hardware invariant "mosaic counter
// resets to 0 at frame start and increments by 1 per visible scanline" is
// just "y itself." Games rewriting MOSAIC mid-frame therefore don't shift
// the block grid, matching bsnes.
func (p *PPU) mosaicSnapY(y, size int) int {
	if size <= 1 {
		return y
	}
	return y - (y % size)
}

// RenderScanline draws a single scanline (y) via the Phase 2.5 pixel-walk
// renderer.
func (p *PPU) RenderScanline(y int) {
	if !p.ForceBlank() {
		p.OAMAddr = p.OAMBaseAddr
	}
	p.noteMode7Scanline(y)
	p.noteFrameLine(y)
	p.renderScanlinePixelWalk(y)
}

func (p *PPU) noteMode7Scanline(y int) {
	if p.Mode7ScanlineHook == nil || p.BGMode&0x07 != 7 {
		return
	}
	points := [3]Mode7TracePoint{
		{X: 0},
		{X: 128},
		{X: 255},
	}
	for i := range points {
		points[i].TexelX, points[i].TexelY = p.mode7TexelCoord(points[i].X, y)
	}
	p.Mode7ScanlineHook(Mode7ScanlineEvent{
		Y:          y,
		Matrix:     [4]uint16{p.M7A, p.M7B, p.M7C, p.M7D},
		Center:     [2]uint16{p.M7X, p.M7Y},
		Scroll:     [2]uint16{p.M7HOFS, p.M7VOFS},
		M7SEL:      p.M7SEL,
		FrameCount: p.FrameCount,
		HCounter:   p.hCounter,
		VCounter:   p.vCounter,
		Points:     points,
		LastPair:   p.lastM7Pair,
	})
}

func (p *PPU) updateOBJFlags(y int) {
	sizeSel := (p.OBSEL >> 5) & 0x7
	interlace := p.SETINI&0x02 != 0

	firstSprite := 0
	if p.OAMPriority {
		firstSprite = int((p.OAMBaseAddr >> 2) & 0x7F)
	}

	lineSpriteCount := 0
	storedCount := 0
	for n := 0; n < 128; n++ {
		i := (firstSprite + n) & 0x7F
		addr := i * 4
		xLow := int(p.OAM[addr])
		yPos := int(p.OAM[addr+1])

		highByte := p.OAM[512+(i/4)]
		xHigh := (highByte >> ((i % 4) * 2)) & 1
		sizeBit := (highByte >> ((i%4)*2 + 1)) & 1

		x := xLow | (int(xHigh) << 8)
		large := sizeBit != 0
		spriteWidth, spriteHeight := objSize(sizeSel, large)
		if interlace {
			spriteWidth, spriteHeight = objInterlaceSize(sizeSel, large, spriteWidth, spriteHeight)
		}
		if !objInRange(x, spriteWidth) {
			continue
		}

		relY := (y - yPos) & 0xFF
		if relY >= objVisibleHeight(spriteHeight, interlace) {
			continue
		}

		lineSpriteCount++
		if lineSpriteCount > 32 {
			p.RangeOver = true
			break
		}
		p.lineSprites[storedCount] = spriteLine{
			x:      x,
			yPos:   yPos,
			tile:   p.OAM[addr+2],
			attrs:  p.OAM[addr+3],
			width:  spriteWidth,
			height: spriteHeight,
		}
		storedCount++
	}

	tilesLeft := 34
	for i := storedCount - 1; i >= 0; i-- {
		s := p.lineSprites[i]
		numCols := s.width / 8
		counted := 0
		for col := 0; col < numCols; col++ {
			if !objTileInTime(s.x, col) {
				continue
			}
			counted++
		}
		if counted > tilesLeft {
			p.TimeOver = true
		}
		if counted < tilesLeft {
			tilesLeft -= counted
		} else {
			tilesLeft = 0
		}
	}
}

func bgTilemapEntryAddr(sc uint8, col, row int) uint32 {
	base := uint32(sc&0xFC) << 9
	hScreens := 1 + int(sc&0x01)
	vScreens := 1 + int((sc>>1)&0x01)
	screenCol := 0
	if hScreens == 2 {
		screenCol = (col >> 5) & 1
	}
	screenRow := 0
	if vScreens == 2 {
		screenRow = (row >> 5) & 1
	}
	screen := screenRow*hScreens + screenCol
	idx := (row&0x1F)*32 + (col & 0x1F)
	return base + uint32(screen*0x800+idx*2)
}

// optBG3Entry reads a BG3 tilemap entry by tile (col, row) for use as an
// Offset-per-Tile lookup.
func (p *PPU) optBG3Entry(baseAddr uint32, col, row int) uint16 {
	idx := (row&0x1F)*32 + (col & 0x1F)
	mask := uint32(VRAMSize - 1)
	a := baseAddr + uint32(idx*2)
	lo := p.VRAM[a&mask]
	hi := p.VRAM[(a+1)&mask]
	return uint16(lo) | uint16(hi)<<8
}

// evaluateOBJ runs the OAM scan, line-sprite selection, and per-sprite
// drawing for scanline y, populating p.objColor[] and p.objPrio[].
// Does not write to any dst buffer — the pixel-walk OBJ adapter consumes
// objColor/objPrio in its own compositor step.
func (p *PPU) evaluateOBJ(y int) {
	// OBJ Registers
	// OBSEL ($2101) - Size, Base Address.
	// OAM data in p.OAM.

	// Scan OAM (128 sprites)
	// Entry format (4 bytes):
	// xxxxxxxx yyyyyyyy tttttttt vhoopppn
	// x: X pos (low 8 bits)
	// y: Y pos
	// t: Tile index
	// n: Name table (0/1)
	// p: Palette (0-7) + 8 = (8-15)
	// o: Priority
	// h: H-Flip
	// v: V-Flip

	// High Table (32 bytes = 2 bits per sprite for X-high and Size)
	// Not implemented yet (assume X < 256, Size small).
	// Default size 8x8 or 16x16.

	// Default Size: 8x8 ( Small) / 16x16 (Large) if Bit 5 of OBSEL=0
	// 8x8 / 32x32 if Bit 5=1?
	// Let's assume 8x8 for now.

	// Bits 0-2: Name Base (Address of Table 0) in 8K-Word steps (16KB)
	// Bits 3-4: Name Select (Offset of Table 1) in 4K-Word steps (8KB)

	nameBase := uint32(p.OBSEL&0x7) << 13   // Base tile data address (word address)
	nameSel := uint32((p.OBSEL >> 3) & 0x3) // 0-3
	table1Offset := (nameSel + 1) << 12     // Additional offset when nameselect=1 (word address)

	sizeSel := (p.OBSEL >> 5) & 0x7
	interlace := p.SETINI&0x02 != 0

	/*
		// DEBUG: OAM Dump (Scanline 138)
		if y == 138 && p.FrameCount == 600 { // Check End of Test
			fmt.Println("DEBUG: OAM Dump Frame 600")
			for i := 0; i < 128; i++ {
				oamX := p.OAM[i*4]
				oamY := p.OAM[i*4+1]
				attr := p.OAM[i*4+3]
				tile := p.OAM[i*4+2]
				if oamY < 240 { // Visible only
					fmt.Printf("OAM[%d]: X=%d Y=%d T=%02X A=%02X\n", i, oamX, oamY, tile, attr)
				}
			}
		}
	*/

	lineSprites := p.lineSprites[:]
	lineSpriteCount := 0
	objColor := p.objColor[:256]
	objPrio := p.objPrio[:256]
	objSource := p.objSource[:256]
	objPalette := p.objPalette[:256]
	objOrder := p.objOrder[:256]
	for i := 0; i < 256; i++ {
		objPrio[i] = -1
		objSource[i] = 0
		objPalette[i] = 0
		objOrder[i] = 1 << 30
	}

	firstSprite := 0
	if p.OAMPriority {
		firstSprite = int((p.OAMBaseAddr >> 2) & 0x7F)
	}

	// Scan OAM in evaluation order. Hardware keeps up to 32 sprites per line.
	for n := 0; n < 128; n++ {
		i := (firstSprite + n) & 0x7F
		addr := i * 4
		// latchOAMAddr mirrors bsnes's latch.oamAddress: most-recent
		// per-line OAM byte address sampled by the renderer.
		// $2104 active-display writes redirect here.
		p.latchOAMAddr = uint16(addr)
		xLow := int(p.OAM[addr])
		yPos := int(p.OAM[addr+1])
		tile := p.OAM[addr+2]
		attrs := p.OAM[addr+3]

		// High Bits
		highByte := p.OAM[512+(i/4)]
		xHigh := (highByte >> ((i % 4) * 2)) & 1
		sizeBit := (highByte >> ((i%4)*2 + 1)) & 1

		x := xLow | (int(xHigh) << 8) // 9-bit: 0..511

		large := sizeBit != 0
		spriteWidth, spriteHeight := objSize(sizeSel, large)
		if interlace {
			spriteWidth, spriteHeight = objInterlaceSize(sizeSel, large, spriteWidth, spriteHeight)
		}
		if !objInRange(x, spriteWidth) {
			continue
		}

		relY := (y - yPos) & 0xFF
		if relY < objVisibleHeight(spriteHeight, interlace) {
			if lineSpriteCount < len(lineSprites) {
				lineSprites[lineSpriteCount] = spriteLine{
					x:      x,
					yPos:   yPos,
					tile:   tile,
					attrs:  attrs,
					width:  spriteWidth,
					height: spriteHeight,
				}
				lineSpriteCount++
			}
		}
	}

	// Render from highest index to lowest so low OAM indices win on overlap.
	tilesLeft := 34
	for i := lineSpriteCount - 1; i >= 0; i-- {
		s := lineSprites[i]
		tableOffset := uint32(0)
		if (s.attrs & 1) != 0 {
			tableOffset = table1Offset
		}
		relY := (y - s.yPos) & 0xFF
		if interlace {
			relY <<= 1
		}
		if tilesLeft > 0 {
			tilesLeft -= p.drawSprite(relY, s.x, s.tile, s.attrs, s.width, s.height, interlace, p.FrameCount&1, nameBase, tableOffset, tilesLeft, i, objColor, objPrio, objSource, objPalette, objOrder)
		}
	}
}

func (p *PPU) drawSprite(relY int, x int, tileIdxBase uint8, attrs uint8, width, height int, interlace bool, field int, nameBase, tableOffset uint32, tileBudget int, orderRank int, objColor []uint16, objPrio []int, objSource []uint8, objPalette []uint8, objOrder []int) int {
	// ...
	vFlip := (attrs & 0x80) != 0
	hFlip := (attrs & 0x40) != 0
	palette := (attrs & 0x0E) >> 1
	priority := int((attrs >> 4) & 0x03)
	// bsnes ppu-fast/object.cpp:125 - palette[x] < 192 ? OBJ1 : OBJ2.
	// OBJ palette base sits at CGRAM index 128, 16 entries per palette,
	// so palette[x] < 192 corresponds to palette groups 0..3 (OBJ1) and
	// >= 192 corresponds to groups 4..7 (OBJ2).
	source := uint8(sourceOBJ1)
	if palette >= 4 {
		source = sourceOBJ2
	}
	tilesDrawn := 0

	if vFlip {
		if width == height {
			relY = height - 1 - relY
		} else if relY < width {
			relY = width - 1 - relY
		} else {
			relY = width + (width - 1) - (relY - width)
		}
	}
	if interlace {
		if vFlip {
			relY -= field
		} else {
			relY += field
		}
	}

	numCols := width / 8
	y := relY & 0xFF
	character := uint32(tileIdxBase)
	tileDataAddress := nameBase + tableOffset
	chrx := character & 0x0F
	chry := ((character >> 4) + uint32((y>>3)&0x0F)) & 0x0F
	chry <<= 4

	readWord := func(wordAddr uint32) uint16 {
		wordAddr &= 0x7FFF
		idx := int(wordAddr) * 2
		return uint16(p.VRAM[idx]) | (uint16(p.VRAM[idx+1]) << 8)
	}

	for col := 0; col < numCols; col++ {
		if !objTileInTime(x, col) {
			continue
		}
		if tilesDrawn >= tileBudget {
			break
		}
		mx := col
		if hFlip {
			mx = numCols - 1 - col
		}

		tilesDrawn++
		pos := tileDataAddress + ((chry + ((chrx + uint32(mx)) & 0x0F)) << 4)
		addr := (pos & 0xFFF0) + uint32(y&7)
		word01 := readWord(addr + 0)
		word23 := readWord(addr + 8)

		// Draw 8 pixels
		for px := 0; px < 8; px++ {
			screenPx := px
			if hFlip {
				screenPx = 7 - px
			}

			bit := uint(7 - screenPx)
			c0 := uint8((word01 >> bit) & 1)
			c1 := uint8((word01 >> (bit + 8)) & 1)
			c2 := uint8((word23 >> bit) & 1)
			c3 := uint8((word23 >> (bit + 8)) & 1)
			c := c0 | (c1 << 1) | (c2 << 2) | (c3 << 3)

			if c > 0 {
				relX := (col * 8) + px
				destX := (x + relX) & 0x1FF

				if destX < 256 {
					// Palette Offset 128
					palIdx := (128 + (uint16(palette) * 16) + uint16(c))
					p.latchCGRAMAddr = uint8(palIdx)
					cLow := p.CGRAM[palIdx*2]
					cHigh := p.CGRAM[palIdx*2+1]
					color := uint16(cLow) | (uint16(cHigh) << 8)
					if priority > objPrio[destX] || (priority == objPrio[destX] && orderRank < objOrder[destX]) {
						objPrio[destX] = priority
						objSource[destX] = source
						objPalette[destX] = byte(palIdx)
						objOrder[destX] = orderRank
						objColor[destX] = color
					}
				}
			}
		}
	}

	return tilesDrawn
}
