package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

func TestCPUConfigDefaultMatchesPreStage6Literals(t *testing.T) {
	if DefaultSCPUConfig.FrequencyHz != 21477272 {
		t.Errorf("DefaultSCPUConfig.FrequencyHz = %d, want 21477272", DefaultSCPUConfig.FrequencyHz)
	}
	if DefaultSCPUConfig.MDRRestoreMask != 0x40FC00 {
		t.Errorf("DefaultSCPUConfig.MDRRestoreMask = %X, want 40FC00", DefaultSCPUConfig.MDRRestoreMask)
	}
	if DefaultSCPUConfig.MDRRestoreVal != 0x4000 {
		t.Errorf("DefaultSCPUConfig.MDRRestoreVal = %X, want 4000", DefaultSCPUConfig.MDRRestoreVal)
	}
	if !DefaultSCPUConfig.DRAMRefreshEnabled {
		t.Errorf("DefaultSCPUConfig.DRAMRefreshEnabled = false, want true")
	}
}

func TestNewCPUUsesDefaultSCPUConfig(t *testing.T) {
	c := NewCPU(bus.NewBus())
	if got := c.Frequency(); got != 21477272 {
		t.Errorf("NewCPU(b).Frequency() = %d, want 21477272 (DefaultSCPUConfig)", got)
	}
}

func TestNewCPUWithConfigHonorsCustomFrequency(t *testing.T) {
	cfg := DefaultSCPUConfig
	cfg.FrequencyHz = 10737272 // SA-1 master clock
	c := NewCPUWithConfig(bus.NewBus(), cfg)
	if got := c.Frequency(); got != 10737272 {
		t.Errorf("NewCPUWithConfig(...FrequencyHz=10737272).Frequency() = %d, want 10737272", got)
	}
}

func TestDRAMRefreshEnabledDefaultStillStallsAcrossScanline(t *testing.T) {
	// Sanity that the default S-CPU path still inserts the 40-cycle
	// refresh stall. Drive AddCycles past one scanline boundary (1364)
	// then a small amount past the refresh-position threshold; expect
	// Cycles to gain an extra 40 beyond what was added directly.
	c := NewCPU(bus.NewBus())
	// Burn cycles to land just past one full scanline + the refresh
	// position. The refresh fires on the second addBusCycles call
	// after the scanline boundary; drive enough cycles that the
	// trigger is unambiguous.
	c.AddCycles(2000)
	if c.Cycles < 2000 {
		t.Fatalf("Cycles=%d after AddCycles(2000); want >= 2000", c.Cycles)
	}
	// With DRAMRefreshEnabled, an additional 40 cycles get charged
	// when the refresh window is crossed. We don't assert exact
	// counts (cycle accounting is precise but layout-dependent);
	// instead, compare against the same workload with refresh disabled.
	cfg := DefaultSCPUConfig
	cfg.DRAMRefreshEnabled = false
	c2 := NewCPUWithConfig(bus.NewBus(), cfg)
	c2.AddCycles(2000)
	if c2.Cycles >= c.Cycles {
		t.Errorf("DRAMRefreshEnabled=false produced Cycles=%d, expected strictly less than enabled=true Cycles=%d", c2.Cycles, c.Cycles)
	}
}

func TestDRAMRefreshDisabledLeavesRefreshStateUntouched(t *testing.T) {
	cfg := DefaultSCPUConfig
	cfg.DRAMRefreshEnabled = false
	c := NewCPUWithConfig(bus.NewBus(), cfg)
	c.AddCycles(5000) // far past any refresh trigger
	// Refresh fields should stay zero — maybeDRAMRefresh was never called.
	if c.DRAMRefreshLine != 0 {
		t.Errorf("DRAMRefreshLine=%d, want 0 (refresh disabled should not advance)", c.DRAMRefreshLine)
	}
	if c.DRAMRefreshScanline != 0 {
		t.Errorf("DRAMRefreshScanline=%d, want 0", c.DRAMRefreshScanline)
	}
	if c.DRAMRefreshLineStart != 0 {
		t.Errorf("DRAMRefreshLineStart=%d, want 0", c.DRAMRefreshLineStart)
	}
	if c.DRAMRefreshPosition != 0 {
		t.Errorf("DRAMRefreshPosition=%d, want 0 (latch never ran)", c.DRAMRefreshPosition)
	}
}

func TestMDRRestoreMaskZeroDisablesPredicate(t *testing.T) {
	// With MDRRestoreMask=0 the read path should never invoke
	// Bus.SetMDR(mdr) regardless of address. We assert this by
	// observing that a counting BusIO sees zero SetMDR calls after
	// reads in the would-be-restored window.
	cfg := DefaultSCPUConfig
	cfg.MDRRestoreMask = 0
	cb := &countingBus{}
	c := &CPU{Bus: cb, config: cfg}
	_ = c.read(0x004200) // would normally hit the restore path
	_ = c.read(0x004212)
	if cb.setMDRCount != 0 {
		t.Errorf("MDRRestoreMask=0 produced %d SetMDR calls, want 0", cb.setMDRCount)
	}
}

func TestMDRRestoreDefaultStillRestoresInWindow(t *testing.T) {
	// Sanity: the default S-CPU config still calls SetMDR when the
	// address falls in the $4000-$43FF window.
	cb := &countingBus{}
	c := &CPU{Bus: cb, config: DefaultSCPUConfig}
	_ = c.read(0x004200) // $00:4200 — in window
	if cb.setMDRCount != 1 {
		t.Errorf("DefaultSCPUConfig produced %d SetMDR calls for $004200 read, want 1", cb.setMDRCount)
	}
	cb2 := &countingBus{}
	c2 := &CPU{Bus: cb2, config: DefaultSCPUConfig}
	_ = c2.read(0x008000) // ROM, out of window
	if cb2.setMDRCount != 0 {
		t.Errorf("DefaultSCPUConfig produced %d SetMDR calls for $008000 read, want 0 (out of window)", cb2.setMDRCount)
	}
}

// countingBus is a minimal BusIO used only by Stage 6 tests to
// observe SetMDR invocation count.
type countingBus struct {
	setMDRCount int
	mdr         uint8
}

func (b *countingBus) Read(addr uint32) uint8           { return 0 }
func (b *countingBus) Write(addr uint32, val uint8)     {}
func (b *countingBus) GetWaitStates(addr uint32) uint64 { return 0 }
func (b *countingBus) MDR() uint8                       { return b.mdr }
func (b *countingBus) SetMDR(v uint8)                   { b.mdr = v; b.setMDRCount++ }
