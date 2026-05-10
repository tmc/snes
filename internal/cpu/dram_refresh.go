package cpu

// S-CPU DRAM refresh. The 5A22 inserts a 40-cycle DRAM refresh stall
// once per scanline, starting at cycle 530+8-(start%8) within the
// scanline (bsnes/sfc/cpu/timing.cpp). Five 8-cycle blocks tick the
// math ALU. The fields driving this state (DRAMRefreshLine,
// DRAMRefreshScanline, DRAMRefreshLineStart, DRAMRefreshPosition)
// remain on *CPU; only the methods live here so the file boundary
// reflects the future Core/SA-1 split (a SA-1 CPU instance leaves
// the refresh fields zero and these methods become no-ops on it
// because its scheduler does not feed S-CPU-shaped scanline cycles).

func (c *CPU) maybeDRAMRefresh() {
	c.latchDRAMRefreshScanline()
	refreshLine := c.DRAMRefreshScanline + 1
	if c.DRAMRefreshLine == refreshLine || c.Cycles-c.DRAMRefreshLineStart < c.DRAMRefreshPosition {
		return
	}
	c.DRAMRefreshLine = refreshLine
	for i := 0; i < 5; i++ {
		c.Cycles += 8
		c.mathALUEdge()
	}
}

func (c *CPU) latchDRAMRefreshScanline() {
	line, start := ntscScanlineStart(c.Cycles)
	if c.DRAMRefreshScanline == line && c.DRAMRefreshLineStart == start && c.DRAMRefreshPosition != 0 {
		return
	}
	c.DRAMRefreshScanline = line
	c.DRAMRefreshLineStart = start
	c.DRAMRefreshPosition = 530 + 8 - start%8
}

func ntscScanlineStart(cycles uint64) (line, start uint64) {
	const (
		lineCycles         uint64 = 1364
		scanlinesPerFrame  uint64 = 262
		shortScanline      uint64 = 240
		shortScanlineDelta uint64 = 4
		evenFrameCycles           = scanlinesPerFrame * lineCycles
		oddFrameCycles            = evenFrameCycles - shortScanlineDelta
		fieldPairCycles           = evenFrameCycles + oddFrameCycles
	)

	pair := cycles / fieldPairCycles
	rem := cycles % fieldPairCycles
	line = pair * scanlinesPerFrame * 2
	start = pair * fieldPairCycles

	if rem < evenFrameCycles {
		line += rem / lineCycles
		start += (rem / lineCycles) * lineCycles
		return line, start
	}

	rem -= evenFrameCycles
	line += scanlinesPerFrame
	start += evenFrameCycles

	shortStart := shortScanline * lineCycles
	if rem < shortStart {
		line += rem / lineCycles
		start += (rem / lineCycles) * lineCycles
		return line, start
	}
	if rem < shortStart+lineCycles-shortScanlineDelta {
		line += shortScanline
		start += shortStart
		return line, start
	}

	rem -= shortStart + lineCycles - shortScanlineDelta
	line += shortScanline + 1 + rem/lineCycles
	start += shortStart + lineCycles - shortScanlineDelta + (rem/lineCycles)*lineCycles
	return line, start
}
