package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestDRAMRefreshLatchesShortScanlinePhase(t *testing.T) {
	c := NewCPU(bus.NewBus())
	const (
		scanlinesPerFrame = 262
		shortScanline     = 240
	)
	line := uint64(scanlinesPerFrame + shortScanline + 8)
	gotLine, lineStart := ntscScanlineStart(line * 1364)
	if gotLine != line {
		t.Fatalf("scanline = %d, want %d", gotLine, line)
	}

	c.Cycles = lineStart + 533
	c.AddCycles(1)

	if c.DRAMRefreshPosition != 534 {
		t.Fatalf("refresh position = %d, want 534", c.DRAMRefreshPosition)
	}
	if got, want := c.Cycles-lineStart, uint64(574); got != want {
		t.Fatalf("line cycle after refresh = %d, want %d", got, want)
	}
}

func TestDRAMRefreshStateRoundTripPreservesScanlineLatch(t *testing.T) {
	c := NewCPU(bus.NewBus())
	_, lineStart := ntscScanlineStart((262 + 240 + 8) * 1364)
	c.Cycles = lineStart + 533
	c.AddCycles(1)

	state := c.SaveState()
	restored := NewCPU(bus.NewBus())
	restored.LoadState(state)

	if restored.DRAMRefreshScanline != c.DRAMRefreshScanline ||
		restored.DRAMRefreshLineStart != c.DRAMRefreshLineStart ||
		restored.DRAMRefreshPosition != c.DRAMRefreshPosition ||
		restored.DRAMRefreshLine != c.DRAMRefreshLine {
		t.Fatalf("restored refresh state = line %d start %d pos %d refreshed %d, want line %d start %d pos %d refreshed %d",
			restored.DRAMRefreshScanline, restored.DRAMRefreshLineStart, restored.DRAMRefreshPosition, restored.DRAMRefreshLine,
			c.DRAMRefreshScanline, c.DRAMRefreshLineStart, c.DRAMRefreshPosition, c.DRAMRefreshLine)
	}
}
