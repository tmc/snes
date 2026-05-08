package apu

import "testing"

func TestMachineCycleFrequency(t *testing.T) {
	apu := NewAPU()
	if got, want := apu.Frequency(), uint64(spcMachineFrequency); got != want {
		t.Fatalf("Frequency = %d, want %d", got, want)
	}
}

// TestSPCInputClockMatchesBsnesReference pins the SMP input-clock frequency
// to the value bsnes/ares feed the SMP crystal. bsnes/sfc/smp/timing.cpp
// derives the SMP machine cycle as 2 ticks of a 2.048 MHz input, scheduled
// at that input rate; Go matches by declaring Frequency() at that input
// rate and advancing one tick per APU.Run(). The CPU master/APU ratio is
// therefore 21,477,272 / 2,048,000 ≈ 10.487 (one APU input tick per ~10.5
// CPU master cycles), not the ~21x that would result from treating the
// SMP as scheduled at its 1.024 MHz op-cycle clock.
func TestSPCInputClockMatchesBsnesReference(t *testing.T) {
	const cpuMasterHz = 21477272
	if got, want := uint64(spcMachineFrequency), uint64(2048000); got != want {
		t.Fatalf("spcMachineFrequency = %d, want %d (bsnes/sfc/smp/timing.cpp input clock)", got, want)
	}
	ratio := float64(cpuMasterHz) / float64(spcMachineFrequency)
	if ratio < 10.45 || ratio > 10.53 {
		t.Fatalf("CPU/APU master-tick ratio = %.4f, want ~10.487 (21,477,272/2,048,000)", ratio)
	}
}

func TestMachineCycleTimerDividers(t *testing.T) {
	apu := NewAPU()
	apu.Write(0x00F1, 0x01)
	apu.Write(0x00FA, 3)

	apu.TickTimers(uint64(timer01Divider * 3))
	if got := apu.Read(0x00FD); got != 1 {
		t.Fatalf("timer0 counter = %d, want 1", got)
	}

	apu = NewAPU()
	apu.Write(0x00F1, 0x04)
	apu.Write(0x00FC, 4)
	apu.TickTimers(uint64(timer2Divider * 4))
	if got := apu.Read(0x00FF); got != 1 {
		t.Fatalf("timer2 counter = %d, want 1", got)
	}
}

func TestResetCyclesClearsDSPPhase(t *testing.T) {
	apu := NewAPU()
	for i := 0; i < dspSampleDivider-1; i++ {
		apu.Run()
	}
	if apu.dspCycles == 0 {
		t.Fatalf("test setup did not leave a partial DSP phase")
	}

	apu.ResetCycles()
	if apu.dspCycles != 0 {
		t.Fatalf("ResetCycles left dspCycles = %d, want 0", apu.dspCycles)
	}

	apu.Run()
	if apu.audioCount != 0 {
		t.Fatalf("DSP sample emitted immediately after ResetCycles; audioCount=%d", apu.audioCount)
	}
}

func TestStoppedSPCStillClocksTimersAndDSP(t *testing.T) {
	apu := NewAPU()
	apu.Control = 0
	apu.Processor.PC = 0x0200
	apu.RAM[0x0200] = 0xEF // SLEEP
	apu.Write(0x00F1, 0x04)
	apu.Write(0x00FC, 2)

	apu.Run()
	pc := apu.Processor.PC
	if !apu.Processor.Stopped {
		t.Fatalf("SLEEP did not stop processor")
	}

	for i := 0; i < dspSampleDivider; i++ {
		apu.Run()
	}
	if got := apu.Processor.PC; got != pc {
		t.Fatalf("stopped processor PC advanced: got %04X want %04X", got, pc)
	}
	if apu.audioCount == 0 {
		t.Fatalf("stopped processor halted DSP cadence")
	}
	if got := apu.Read(0x00FF); got == 0 {
		t.Fatalf("stopped processor halted timer2")
	}
}
