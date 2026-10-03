package scheduler

import (
	"testing"

	"github.com/tmc/snes/internal/apu"
)

var _ YieldingThread = (*apu.APU)(nil)

func TestFutureAPUYieldBoundaryContract(t *testing.T) {
	fixtures := []apuYieldContractFixture{
		{
			name:         "post CPU catch-up stops before visible APU port publish",
			cpuCycles:    8,
			cpuFreq:      1,
			apuFreq:      1,
			yieldCycle:   5,
			publishCycle: 6,
			wantThread: apuYieldContractState{
				cycles:    8,
				runs:      8,
				published: true,
			},
			wantYielding: apuYieldContractState{
				cycles:  5,
				runs:    5,
				yielded: true,
			},
		},
	}

	for _, f := range fixtures {
		t.Run(f.name+"/plain Thread", func(t *testing.T) {
			cpu := &apuYieldContractThread{cycles: f.cpuCycles, frequency: f.cpuFreq}
			apu := f.newThread()
			s := NewScheduler()
			s.RegisterCPU(cpu, cpu.Frequency())
			s.RegisterAPU(apu, apu.Frequency())

			s.syncAll(cpu.GetCycles())
			if got := apu.capture(false); got != f.wantThread {
				t.Fatalf("plain Thread syncAll state = %+v, want %+v", got, f.wantThread)
			}
		})

		t.Run(f.name+"/future yielding Thread", func(t *testing.T) {
			cpu := &apuYieldContractThread{cycles: f.cpuCycles, frequency: f.cpuFreq}
			apu := f.newYieldingThread()
			s := NewScheduler()
			s.RegisterCPU(cpu, cpu.Frequency())
			s.RegisterAPU(apu, apu.Frequency())

			result := s.syncAll(cpu.GetCycles())
			if result.Yield != YieldAPUPortWrite {
				t.Fatalf("post-CPU sync yield = %d, want %d", result.Yield, YieldAPUPortWrite)
			}
			if got := apu.capture(result.Yield != YieldNone); got != f.wantYielding {
				t.Fatalf("future post-CPU sync state = %+v, want %+v", got, f.wantYielding)
			}

			s.Sync(apu)
			if got := apu.capture(false); got != f.wantThread {
				t.Fatalf("safety sync state = %+v, want %+v", got, f.wantThread)
			}
		})
	}
}

type apuYieldContractFixture struct {
	name         string
	cpuCycles    uint64
	cpuFreq      uint64
	apuFreq      uint64
	yieldCycle   uint64
	publishCycle uint64
	wantThread   apuYieldContractState
	wantYielding apuYieldContractState
}

func (f apuYieldContractFixture) newThread() *apuYieldContractThread {
	return &apuYieldContractThread{
		step:         1,
		frequency:    f.apuFreq,
		publishCycle: f.publishCycle,
	}
}

func (f apuYieldContractFixture) newYieldingThread() *apuYieldContractYieldingThread {
	return &apuYieldContractYieldingThread{
		apuYieldContractThread: apuYieldContractThread{
			step:         1,
			frequency:    f.apuFreq,
			publishCycle: f.publishCycle,
		},
		yieldCycle: f.yieldCycle,
	}
}

type apuYieldContractState struct {
	cycles    uint64
	runs      int
	published bool
	yielded   bool
}

type apuYieldContractThread struct {
	cycles       uint64
	step         uint64
	frequency    uint64
	publishCycle uint64
	published    bool
	runs         int
}

func (t *apuYieldContractThread) Run() {
	t.runs++
	t.cycles += t.step
	if t.publishCycle != 0 && t.cycles >= t.publishCycle {
		t.published = true
	}
}

func (t *apuYieldContractThread) GetCycles() uint64 { return t.cycles }
func (t *apuYieldContractThread) ResetCycles()      { t.cycles = 0 }
func (t *apuYieldContractThread) Frequency() uint64 { return t.frequency }

func (t *apuYieldContractThread) capture(yielded bool) apuYieldContractState {
	return apuYieldContractState{
		cycles:    t.cycles,
		runs:      t.runs,
		published: t.published,
		yielded:   yielded,
	}
}

type apuYieldContractYieldingThread struct {
	apuYieldContractThread
	yieldCycle uint64
}

func (t *apuYieldContractYieldingThread) RunUntilTarget(targetCycles uint64, mode SyncMode) SyncResult {
	for t.GetCycles() < targetCycles {
		if mode == SyncPostCPU && t.reachedYieldCycle() {
			return SyncResult{Yield: YieldAPUPortWrite}
		}
		t.Run()
		if mode == SyncPostCPU && t.reachedYieldCycle() {
			return SyncResult{Yield: YieldAPUPortWrite}
		}
	}
	return SyncResult{}
}

func (t *apuYieldContractYieldingThread) reachedYieldCycle() bool {
	return t.yieldCycle != 0 && t.GetCycles() >= t.yieldCycle
}
