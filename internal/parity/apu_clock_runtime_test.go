package parity

import (
	"testing"

	"github.com/tmc/snes"
)

// TestAPUClockRatioRuntime is the runtime complement to
// internal/apu/machine_cycle_test.go TestSPCInputClockMatchesBsnesReference.
// The unit test pins the configured constants. This test pins the actual
// scheduler delivery: after running a fixed number of frames, the ratio
// of CPU master cycles consumed to APU input cycles consumed must match
// the configured 21,477,272 / 2,048,000 ≈ 10.487 ratio.
//
// A regression in apu.Run() per-call work (e.g. accidentally advancing
// a.cycles by 2 instead of 1, or scheduler.syncToMaster using the wrong
// frequency) would fail this test even if the constant in apu.go is
// unchanged.
func TestAPUClockRatioRuntime(t *testing.T) {
	rom := makeIdleLoROM()
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("load rom: %v", err)
	}
	sys.Power()

	const frames = 10
	cpuStart := sys.CPU.GetCycles()
	apuStart := sys.APU.GetCycles()
	for i := 0; i < frames; i++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	cpuDelta := sys.CPU.GetCycles() - cpuStart
	apuDelta := sys.APU.GetCycles() - apuStart

	if apuDelta == 0 {
		t.Fatalf("APU did not advance: cpuDelta=%d apuDelta=0", cpuDelta)
	}

	ratio := float64(cpuDelta) / float64(apuDelta)
	const want = 21477272.0 / 2048000.0 // ≈ 10.487
	const tol = 0.05
	if ratio < want-tol || ratio > want+tol {
		t.Fatalf("CPU/APU runtime ratio = %.4f (cpuDelta=%d apuDelta=%d), want %.4f ± %.2f",
			ratio, cpuDelta, apuDelta, want, tol)
	}

	cyclesPerFrame := cpuDelta / frames
	const ntscCyclesPerFrame = 357366
	if cyclesPerFrame < ntscCyclesPerFrame*9/10 || cyclesPerFrame > ntscCyclesPerFrame*11/10 {
		t.Fatalf("CPU master cycles/frame = %d, want ~%d (NTSC)", cyclesPerFrame, ntscCyclesPerFrame)
	}

	t.Logf("frames=%d cpu_cycles=%d apu_cycles=%d ratio=%.4f cpu/frame=%d",
		frames, cpuDelta, apuDelta, ratio, cyclesPerFrame)
}

// makeIdleLoROM builds a minimal 32 KiB LoROM whose reset vector points
// at a tight `BRA $-2` infinite loop. Sufficient to exercise the
// scheduler without any DMA, IRQ, or APU traffic.
func makeIdleLoROM() []byte {
	rom := make([]byte, 0x8000)
	// Reset entry at bank $00:$8000.
	// 0x80, 0xFE = BRA -2 (loops to itself).
	rom[0x0000] = 0x80
	rom[0x0001] = 0xFE
	// Internal header at $FFC0..$FFFF (LoROM header at file offset 0x7FC0).
	for i := 0x7FC0; i < 0x7FD5; i++ {
		rom[i] = ' '
	}
	rom[0x7FD5] = 0x20 // LoROM, slow
	rom[0x7FD6] = 0x00 // ROM only
	rom[0x7FD7] = 0x05 // 32 KiB
	rom[0x7FD8] = 0x00
	rom[0x7FD9] = 0x00
	rom[0x7FDA] = 0x33
	// Native vectors at $FFE0..$FFEF; emulation at $FFF0..$FFFF.
	// Reset vector (emulation mode) at $FFFC/$FFFD.
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80
	return rom
}
