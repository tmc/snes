package scheduler

import "testing"

// stepIRQThread advances by a varying step size to mimic real CPU
// instruction timings (6, 8, 10, 12 master cycles per instruction). Most
// instructions are not multiples of 4 from the H-counter perspective, so
// the scheduler's IRQ matcher must tolerate sample misses on the exact
// equality boundary.
type stepIRQThread struct {
	irqThread
	steps  []uint64
	cursor int
}

func (t *stepIRQThread) Run() {
	t.cycles += t.steps[t.cursor%len(t.steps)]
	t.cursor++
}

// TestHVIRQHitsWithRealisticCPUSteps checks that the IRQ matcher detects
// a scanline boundary crossed between CPU instruction samples.
func TestHVIRQHitsWithRealisticCPUSteps(t *testing.T) {
	s := NewScheduler()
	cpu := &stepIRQThread{
		irqThread: irqThread{fakeThread: fakeThread{frequency: 21477272}},
		steps:     []uint64{8, 4, 6},
	}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(3)
	s.SetIRQTimer(0, 207)
	s.RunFrame()

	if cpu.irqCount == 0 {
		t.Fatalf("HIRQ never fired with H+V mode (irqH=0, irqV=207) under realistic CPU step pattern; matcher misses the exact-equality window. step pattern: %v", cpu.steps)
	}
}
