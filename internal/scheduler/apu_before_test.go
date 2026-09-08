package scheduler

import (
	"github.com/tmc/snes/internal/apu"
	"testing"
)

func TestSyncBeforeCPUInputReadRationalBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		cpuTicks, cpuFrequency, apuFrequency uint64
		want                                 byte
	}{
		{"before", 83985, 10, 1, 0},
		{"equal", 8399, 1, 1, 0},
		{"after-reference", 87978, 21477272, 2050560, 0x65},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := apu.NewAPU()
			a.Control = 0
			a.Processor.PC = 0x200
			a.InPorts[0] = 0x65
			copy(a.RAM[0x200:], []byte{0xe4, 0xf4, 0x2f, 0xfe})
			state := a.SaveState()
			state.Cycles = 8394
			if err := a.LoadState(state); err != nil {
				t.Fatal(err)
			}
			cpu := &fakeThread{cycles: tc.cpuTicks, frequency: tc.cpuFrequency}
			s := NewScheduler()
			s.RegisterCPU(cpu, tc.cpuFrequency)
			s.RegisterAPU(a, tc.apuFrequency)
			s.SyncBefore(a)
			before := a.GetCycles()
			if before >= 8400 {
				t.Fatalf("charged beyond before-write boundary: %d", before)
			}
			a.WritePort(0, 0)
			a.RunUntilTarget(8400, apu.SyncSafety)
			if a.Processor.A != tc.want {
				t.Fatalf("input read=%02x want=%02x (before target%d)", a.Processor.A, tc.want, before)
			}
		})
	}
}
