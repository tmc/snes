package cpu

import "github.com/tmc/snes/internal/bus"

// CPUConfig parameterizes the cycle-counted bus path for instances
// that share the WDC65816 core but differ on host-system specifics.
// The S-CPU instance uses DefaultSCPUConfig; a future SA-1 CPU
// instance will supply its own configuration with FrequencyHz set
// to the SA-1 master clock and DRAMRefreshEnabled cleared.
//
// Other S-CPU-isms — the multiply ALU at $4202-$4206, the interrupt
// vectors at $FFEA/$FFEE/$FFFA/$FFFE/$FFFC, and the RESET cycle
// profile in scpu_interrupts.go — are intentionally not in this
// struct. The ALU is already self-gating (mathALUEdge returns when
// MultiplyCounter == 0; an SA-1 instance never writes $4203 so the
// counter stays zero), and the S-CPU-specific files live alongside
// scpu_*.go where an SA-1 instance will not call them.
type CPUConfig struct {
	// FrequencyHz is the host-system master clock the scheduler
	// uses to size cothread budgets. Bsnes computes the SA-1 clock
	// as system.cpuFrequency() * (1.0..4.0 overclock); for the
	// S-CPU it is fixed at 21.477 MHz.
	FrequencyHz uint64

	// MDRRestoreMask + MDRRestoreVal define the address window
	// where a CPU read does not clobber the bus MDR latch. Bsnes
	// documents this for S-CPU MMIO at $4000-$43FF (mask 0x40FC00,
	// val 0x4000). An instance that does not need MDR-restore can
	// leave MDRRestoreMask zero, which short-circuits the predicate.
	MDRRestoreMask uint32
	MDRRestoreVal  uint32

	// DRAMRefreshEnabled gates the once-per-scanline 40-cycle DRAM
	// refresh stall. The S-CPU inserts this stall via maybeDRAMRefresh
	// from inside addBusCycles and AddCycles. SA-1 has no DRAM
	// refresh hardware; setting this false makes both call sites
	// skip the refresh hook entirely.
	DRAMRefreshEnabled bool
}

// DefaultSCPUConfig matches the literal values used by the S-CPU
// before Stage 6 — preserves behavior for every existing caller of
// NewCPU(b *bus.Bus).
var DefaultSCPUConfig = CPUConfig{
	FrequencyHz:        21477272,
	MDRRestoreMask:     0x40FC00,
	MDRRestoreVal:      0x4000,
	DRAMRefreshEnabled: true,
}

// NewCPUWithConfig constructs a CPU instance with caller-provided
// configuration. Used by future non-S-CPU instances; current callers
// continue to use NewCPU(b), which delegates here with
// DefaultSCPUConfig.
func NewCPUWithConfig(b *bus.Bus, cfg CPUConfig) *CPU {
	return &CPU{
		Bus:    NewSCPUBus(b),
		E:      true, // Standard 65c816 reset state is Emulation Mode
		D:      0,
		config: cfg,
	}
}
