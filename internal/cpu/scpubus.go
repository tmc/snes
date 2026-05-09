package cpu

import "github.com/tmc/snes/internal/bus"

// scpuBus adapts *bus.Bus to BusIO. The MDR field on *bus.Bus is
// exposed via accessor methods so the core can save and restore it
// across $4000-$43FF reads without depending on the concrete struct
// layout.
type scpuBus struct {
	*bus.Bus
}

func (b scpuBus) MDR() uint8       { return b.Bus.MDR }
func (b scpuBus) SetMDR(val uint8) { b.Bus.MDR = val }

// NewSCPUBus wraps a *bus.Bus as a BusIO suitable for the S-CPU.
func NewSCPUBus(b *bus.Bus) BusIO { return scpuBus{b} }
