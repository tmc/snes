package scheduler

import "testing"

type fakeThread struct {
	cycles    uint64
	step      uint64
	frequency uint64
	runs      int
}

func (t *fakeThread) Run()              { t.runs++; t.cycles += t.step }
func (t *fakeThread) GetCycles() uint64 { return t.cycles }
func (t *fakeThread) ResetCycles()      { t.cycles = 0 }
func (t *fakeThread) Frequency() uint64 { return t.frequency }
func (t *fakeThread) AddCycles(cycles uint64) {
	t.cycles += cycles
}

type irqThread struct {
	fakeThread
	irqCount int
	irqSet   bool
}

func (t *irqThread) TriggerIRQ() {
	t.irqCount++
	t.irqSet = true
}

func (t *irqThread) ClearIRQ() {
	t.irqSet = false
}

type nmiThread struct {
	fakeThread
	nmiCount int
}

func (t *nmiThread) TriggerNMI() {
	t.nmiCount++
}

func TestSyncUsesThreadFrequency(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{cycles: 40, frequency: 100}
	apu := &fakeThread{step: 1, frequency: 25}

	s.RegisterCPU(cpu, cpu.Frequency())
	s.RegisterAPU(apu, apu.Frequency())

	s.Sync(apu)

	if got, want := apu.GetCycles(), uint64(10); got != want {
		t.Fatalf("apu cycles = %d, want %d", got, want)
	}
}

func TestSyncRunsTargetUntilCaughtUpInOneCall(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{cycles: 40, frequency: 100}
	apu := &fakeThread{step: 1, frequency: 25}

	s.RegisterCPU(cpu, cpu.Frequency())
	s.RegisterAPU(apu, apu.Frequency())

	s.Sync(apu)

	if got, want := apu.runs, 10; got != want {
		t.Fatalf("apu Run calls = %d, want %d", got, want)
	}
	if got, want := apu.GetCycles(), uint64(10); got != want {
		t.Fatalf("apu cycles = %d, want %d", got, want)
	}
}

func TestSyncBeforeStopsBeforeExactBoundary(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{cycles: 40, frequency: 100}
	apu := &fakeThread{step: 1, frequency: 25}

	s.RegisterCPU(cpu, cpu.Frequency())
	s.RegisterAPU(apu, apu.Frequency())

	s.SyncBefore(apu)
	if got, want := apu.GetCycles(), uint64(9); got != want {
		t.Fatalf("apu cycles = %d, want %d", got, want)
	}

	s.Sync(apu)
	if got, want := apu.GetCycles(), uint64(10); got != want {
		t.Fatalf("apu cycles after Sync = %d, want %d", got, want)
	}
}

func TestSyncBeforeUsesYieldingThreadTarget(t *testing.T) {
	tests := []struct {
		name       string
		cpuCycles  uint64
		wantBefore uint64
		wantSync   uint64
	}{
		{name: "exact", cpuCycles: 40, wantBefore: 9, wantSync: 10},
		{name: "fractional", cpuCycles: 41, wantBefore: 10, wantSync: 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewScheduler()
			cpu := &fakeThread{cycles: tt.cpuCycles, frequency: 100}
			apu := &yieldingFakeThread{fakeThread: fakeThread{step: 1, frequency: 25}}

			s.RegisterCPU(cpu, cpu.Frequency())
			s.RegisterAPU(apu, apu.Frequency())

			s.SyncBefore(apu)
			if got := apu.GetCycles(); got != tt.wantBefore {
				t.Fatalf("apu cycles = %d, want %d", got, tt.wantBefore)
			}
			if got, want := apu.runUntilCalls, 1; got != want {
				t.Fatalf("RunUntilTarget calls = %d, want %d", got, want)
			}
			if got := apu.lastTarget; got != tt.wantBefore {
				t.Fatalf("RunUntilTarget target = %d, want %d", got, tt.wantBefore)
			}
			if got, want := apu.lastMode, SyncSafety; got != want {
				t.Fatalf("RunUntilTarget mode = %d, want %d", got, want)
			}

			s.Sync(apu)
			if got := apu.GetCycles(); got != tt.wantSync {
				t.Fatalf("apu cycles after Sync = %d, want %d", got, tt.wantSync)
			}
			if got := apu.lastTarget; got != tt.wantSync {
				t.Fatalf("RunUntilTarget target after Sync = %d, want %d", got, tt.wantSync)
			}
		})
	}
}

func TestSyncPortAccessUsesExplicitMode(t *testing.T) {
	tests := []struct {
		name string
		sync func(*Scheduler, Thread) SyncResult
		want SyncMode
	}{
		{
			name: "read",
			sync: func(s *Scheduler, target Thread) SyncResult {
				return s.SyncPortRead(target)
			},
			want: SyncPortRead,
		},
		{
			name: "write",
			sync: func(s *Scheduler, target Thread) SyncResult {
				return s.SyncPortWrite(target)
			},
			want: SyncPortWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewScheduler()
			cpu := &fakeThread{cycles: 40, frequency: 100}
			apu := &yieldingFakeThread{fakeThread: fakeThread{step: 1, frequency: 25}}

			s.RegisterCPU(cpu, cpu.Frequency())
			s.RegisterAPU(apu, apu.Frequency())

			tt.sync(s, apu)
			if got := apu.GetCycles(); got != 10 {
				t.Fatalf("apu cycles = %d, want 10", got)
			}
			if got := apu.lastMode; got != tt.want {
				t.Fatalf("RunUntilTarget mode = %d, want %d", got, tt.want)
			}
		})
	}
}

type yieldingFakeThread struct {
	fakeThread
	runUntilCalls int
	lastTarget    uint64
	lastMode      SyncMode
}

func (t *yieldingFakeThread) RunUntilTarget(targetCycles uint64, mode SyncMode) SyncResult {
	t.runUntilCalls++
	t.lastTarget = targetCycles
	t.lastMode = mode
	for t.GetCycles() < targetCycles {
		t.Run()
	}
	return SyncResult{}
}

func TestAddCyclesSynchronizesTargets(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{frequency: 100}
	apu := &fakeThread{step: 1, frequency: 25}
	ppu := &fakeThread{step: 4, frequency: 100}

	s.RegisterCPU(cpu, cpu.Frequency())
	s.RegisterAPU(apu, apu.Frequency())
	s.RegisterPPU(ppu, ppu.Frequency())

	s.AddCycles(40)

	if got, want := cpu.GetCycles(), uint64(40); got != want {
		t.Fatalf("cpu cycles = %d, want %d", got, want)
	}
	if got, want := apu.GetCycles(), uint64(10); got != want {
		t.Fatalf("apu cycles = %d, want %d", got, want)
	}
	if got, want := ppu.GetCycles(), uint64(40); got != want {
		t.Fatalf("ppu cycles = %d, want %d", got, want)
	}
}

func TestRunFrameTriggersHIRQMode(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 1364, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(1)
	s.SetIRQTimer(0, 0)

	s.RunFrame()
	if cpu.irqCount != 262 {
		t.Fatalf("irqCount = %d, want 262", cpu.irqCount)
	}
}

func TestReadTIMEUPReturnsAndClearsIRQFlag(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 1364, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(2)
	s.SetIRQTimer(0, 3)

	s.RunFrame()
	if !cpu.irqSet {
		t.Fatalf("cpu irq pending = false, want true")
	}
	if got := s.ReadTIMEUP(); got != 0x80 {
		t.Fatalf("TIMEUP first read = %02X, want 80", got)
	}
	if cpu.irqSet {
		t.Fatalf("cpu irq pending = true after TIMEUP read, want false")
	}
	if got := s.ReadTIMEUP(); got != 0x00 {
		t.Fatalf("TIMEUP second read = %02X, want 00", got)
	}
}

func TestSetIRQModeOffClearsTIMEUPAndPendingIRQ(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 1364, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(2)
	s.SetIRQTimer(0, 3)

	s.RunFrame()
	if got := s.ReadTIMEUP(); got != 0x80 {
		t.Fatalf("TIMEUP before disable = %02X, want 80", got)
	}

	s.SetIRQMode(2)
	s.RunFrame()
	s.SetIRQMode(0)
	if cpu.irqSet {
		t.Fatalf("cpu irq pending = true after IRQ disable, want false")
	}
	if got := s.ReadTIMEUP(); got != 0x00 {
		t.Fatalf("TIMEUP after IRQ disable = %02X, want 00", got)
	}
}

func TestRunFrameTriggersHVIRQMode(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 4, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(3)
	s.SetIRQTimer(10, 3) // dot 10, line 3

	s.RunFrame()
	if cpu.irqCount != 1 {
		t.Fatalf("irqCount = %d, want 1", cpu.irqCount)
	}
}

func TestRunDisplayFrameTriggersHVIRQMode(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 4, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(3)
	s.SetIRQTimer(10, 3)

	s.RunDisplayFrame()
	if cpu.irqCount != 1 {
		t.Fatalf("irqCount = %d, want 1", cpu.irqCount)
	}
	if got := s.ReadTIMEUP(); got != 0x80 {
		t.Fatalf("TIMEUP = %02X, want 80", got)
	}
}

func TestRunDisplayFrameTriggersHVIRQWhenStepCrossesDot(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 8, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(3)
	s.SetIRQTimer(1, 3)

	s.RunDisplayFrame()
	if cpu.irqCount != 1 {
		t.Fatalf("irqCount = %d, want 1", cpu.irqCount)
	}
}

func TestRunDisplayFrameTriggersVIRQWhenStepCrossesLine(t *testing.T) {
	s := NewScheduler()
	cpu := &irqThread{fakeThread: fakeThread{step: 8, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetIRQMode(2)
	s.SetIRQTimer(0, 3)

	s.RunDisplayFrame()
	if cpu.irqCount != 1 {
		t.Fatalf("irqCount = %d, want 1", cpu.irqCount)
	}
}

func TestSetPALChangesFrameLength(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{step: 1, frequency: 21477272}
	s.RegisterCPU(cpu, cpu.Frequency())

	s.RunFrame()
	if got, want := cpu.GetCycles(), uint64(357366); got != want {
		t.Fatalf("ntsc frame cycles = %d, want %d", got, want)
	}

	s.Reset()
	s.SetPAL(true)
	s.RunFrame()
	if got, want := cpu.GetCycles(), uint64(425568); got != want {
		t.Fatalf("pal frame cycles = %d, want %d", got, want)
	}
}

func TestRunDisplayFrameReturnsAtVBlankThenFullPeriod(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{step: 2, frequency: 21477272}
	s.RegisterCPU(cpu, cpu.Frequency())

	s.RunDisplayFrame()
	if got, want := cpu.GetCycles(), uint64(225*1364); got != want {
		t.Fatalf("first display frame cycles = %d, want %d", got, want)
	}

	s.RunDisplayFrame()
	if got, want := cpu.GetCycles(), uint64(225*1364+357366); got != want {
		t.Fatalf("second display frame cycles = %d, want %d", got, want)
	}
}

func TestResetThenSetPALUsesPALDisplayFrameBoundary(t *testing.T) {
	s := NewScheduler()
	cpu := &fakeThread{step: 2, frequency: 21477272}
	s.RegisterCPU(cpu, cpu.Frequency())

	s.Reset()
	s.SetPAL(true)
	s.RunDisplayFrame()
	if got, want := cpu.GetCycles(), uint64(240*1364); got != want {
		t.Fatalf("first PAL display frame after reset = %d, want %d", got, want)
	}
}

func TestLoadStatePreservesFrameEvent(t *testing.T) {
	s := NewScheduler()
	s.LoadState(SchedulerState{PAL: true, FrameEvent: 123456})
	if got := s.SaveState().FrameEvent; got != 123456 {
		t.Fatalf("FrameEvent after LoadState = %d, want 123456", got)
	}
}

func TestRunDisplayFrameKeepsNMITriggeredUntilPendingNMIHasRun(t *testing.T) {
	s := NewScheduler()
	cpu := &nmiThread{fakeThread: fakeThread{step: 2, frequency: 21477272}}
	s.RegisterCPU(cpu, cpu.Frequency())
	s.SetNMI(true)

	s.RunDisplayFrame()
	if got := cpu.nmiCount; got != 1 {
		t.Fatalf("first display frame NMI count = %d, want 1", got)
	}
	if !s.NMITriggered() {
		t.Fatalf("NMITriggered after display frame = false, want true")
	}

	s.SetNMI(false)
	if got := s.SetNMI(true); !got {
		t.Fatalf("NMI re-enable rising edge = false, want true")
	}
	if !s.NMITriggered() {
		t.Fatalf("NMITriggered after re-enable = false, want true")
	}

	s.RunDisplayFrame()
	if got := cpu.nmiCount; got != 2 {
		t.Fatalf("second display frame NMI count = %d, want 2", got)
	}
}

func TestSetNMIReturnsRisingEdge(t *testing.T) {
	s := NewScheduler()
	if got := s.SetNMI(false); got {
		t.Fatalf("SetNMI(false) initial = true, want false")
	}
	if got := s.SetNMI(true); !got {
		t.Fatalf("SetNMI(true) after false = false, want true (rising)")
	}
	if got := s.SetNMI(true); got {
		t.Fatalf("SetNMI(true) when already true = true, want false")
	}
	if got := s.SetNMI(false); got {
		t.Fatalf("SetNMI(false) falling edge = true, want false")
	}
	if !s.SetNMI(true) {
		t.Fatalf("SetNMI(true) after disable = false, want true (rising)")
	}
}
