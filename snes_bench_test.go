package snes

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func benchmarkROM() []byte {
	rom := make([]byte, 0x8000)
	for i := range rom {
		rom[i] = 0xea
	}
	rom[0x0000] = 0x80
	rom[0x0001] = 0xfe
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func BenchmarkSystemRunFrame(b *testing.B) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(benchmarkROM()); err != nil {
		b.Fatalf("LoadROM: %v", err)
	}
	sys.Power()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := sys.RunFrame(); err != nil {
			b.Fatalf("RunFrame: %v", err)
		}
	}
}

func BenchmarkSystemStateHashes(b *testing.B) {
	sys := NewSystem(nil)
	if err := sys.LoadROM(benchmarkROM()); err != nil {
		b.Fatalf("LoadROM: %v", err)
	}
	sys.Power()
	if err := sys.RunFrame(); err != nil {
		b.Fatalf("RunFrame: %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := sys.StateHashes(); err != nil {
			b.Fatalf("StateHashes: %v", err)
		}
	}
}

func BenchmarkSystemReadWRAMAt(b *testing.B) {
	sys := NewSystem(nil)
	buf := make([]byte, 8192)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := sys.ReadWRAMAt(buf, 0); err != nil {
			b.Fatalf("ReadWRAMAt: %v", err)
		}
	}
}

// benchROM is the ROM named by SNES_BENCH_ROM and a save state taken after
// its first 60 frames, so that each benchmark sample starts past boot
// without re-running it.
var benchROM struct {
	once  sync.Once
	rom   []byte
	state []byte
	err   error
}

// benchBootedSystem returns a system running the SNES_BENCH_ROM ROM,
// positioned 60 frames after power-on. It skips b if the variable is unset.
func benchBootedSystem(b *testing.B) *System {
	path := os.Getenv("SNES_BENCH_ROM")
	if path == "" {
		b.Skip("SNES_BENCH_ROM not set")
	}
	benchROM.once.Do(func() {
		rom, err := os.ReadFile(path)
		if err != nil {
			benchROM.err = err
			return
		}
		sys := NewSystem(nil)
		if err := sys.LoadROM(rom); err != nil {
			benchROM.err = fmt.Errorf("LoadROM: %w", err)
			return
		}
		sys.Power()
		for i := 0; i < 60; i++ { // get past boot
			if err := sys.RunFrame(); err != nil {
				benchROM.err = fmt.Errorf("RunFrame: %w", err)
				return
			}
		}
		benchROM.rom = rom
		benchROM.state, benchROM.err = sys.Serialize()
	})
	if benchROM.err != nil {
		b.Fatal(benchROM.err)
	}
	sys := NewSystem(nil)
	if err := sys.LoadROM(benchROM.rom); err != nil {
		b.Fatalf("LoadROM: %v", err)
	}
	if err := sys.Unserialize(benchROM.state); err != nil {
		b.Fatalf("Unserialize: %v", err)
	}
	return sys
}

// BenchmarkSystemRunFrameROM runs full frames of the ROM named by
// SNES_BENCH_ROM, for example:
//
//	SNES_BENCH_ROM=spc_dsp6.sfc go test -run '^$' -bench RunFrameROM -cpuprofile cpu.out
func BenchmarkSystemRunFrameROM(b *testing.B) {
	sys := benchBootedSystem(b)
	b.ReportAllocs()
	cpu0, apu0 := sys.CPU.GetCycles(), sys.APU.GetCycles()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sys.RunFrame(); err != nil {
			b.Fatalf("RunFrame: %v", err)
		}
	}
	b.StopTimer()
	n := float64(b.N)
	cpu := float64(sys.CPU.GetCycles() - cpu0)
	apu := float64(sys.APU.GetCycles() - apu0)
	sec := b.Elapsed().Seconds()
	b.ReportMetric(cpu/n, "master-clk/frame")
	b.ReportMetric(apu/n, "apu-clk/frame")
	b.ReportMetric(sec*1e9/cpu, "ns/master-clk")
	b.ReportMetric(sec*1e9/apu, "ns/apu-clk")
	b.ReportMetric(cpu/sec/masterClockHz, "x-realtime")
}

// masterClockHz is the NTSC master clock in Hz.
const masterClockHz = 21477272
