package scheduler

import "testing"

type fakeThread struct {
	cycles    uint64
	step      uint64
	frequency uint64
}

func (t *fakeThread) Run()              { t.cycles += t.step }
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
