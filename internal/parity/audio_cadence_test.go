package parity

import "testing"

// audioSamplesAtCPUClock derives fresh-boot PCM length from elapsed CPU
// master time (including CPU reset clocks), independently of the APU's counters or buffered sample count.
// The selected NTSC policy is 21,477,272 CPU Hz and 32,040 DSP stereo frames/s.
// Scheduler catch-up rounds to the next SMP input clock; DSP emits a pair
// every 64 input clocks. This helper is bounded to the short fixture runs.
func audioSamplesAtCPUClock(cpuClocks uint64) int {
	const cpuHz = 21477272
	const smpHz = 32040 * 64
	clocks := (cpuClocks*smpHz + cpuHz - 1) / cpuHz
	return int(clocks/64) * 2
}

func TestAudioCadenceClockDerivation(t *testing.T) {
	for _, tt := range []struct {
		clocks uint64
		want   int
	}{{0, 0}, {21477272, 64080}, {21477272 * 2, 128160}} {
		if got := audioSamplesAtCPUClock(tt.clocks); got != tt.want {
			t.Fatalf("CPU clocks %d samples %d want %d", tt.clocks, got, tt.want)
		}
	}
}
