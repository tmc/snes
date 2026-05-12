package snes

import "testing"

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
