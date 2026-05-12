package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/trace"
)

func BenchmarkHashBGR555Frame(b *testing.B) {
	fb := make([]uint16, 256*224)
	for i := range fb {
		fb[i] = uint16(i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = hashBGR555Frame(fb)
	}
}

func BenchmarkTraceWriterEmit(b *testing.B) {
	var buf bytes.Buffer
	tw := trace.NewWriter(&buf)
	ev := trace.Event{Kind: "bus", Frame: 1, Space: "wram", Addr: 0x20, Op: "write", Value: 0x5a}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := tw.Emit(ev); err != nil {
			b.Fatalf("Emit: %v", err)
		}
	}
}

func BenchmarkReplayWriterArtifactScan(b *testing.B) {
	dir := b.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl.gz")
	writeBenchmarkTrace(b, tracePath, "gzip", 100000)
	ranges := []trace.Range{
		{Space: "wram", Start: 0x10, End: 0x10},
		{Space: "wram", Start: 0x20, End: 0x21},
		{Space: "vram", Start: 0x1000, End: 0x10ff},
		{Space: "oam", Start: 0x40, End: 0x5f},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := collectReplayWriterResults(tracePath, ranges, -1, -1, 120); err != nil {
			b.Fatalf("collectReplayWriterResults: %v", err)
		}
	}
}

func writeBenchmarkTrace(b *testing.B, path, compression string, n int) {
	b.Helper()
	out, _, closeFn, err := createTraceOutput(path, compression)
	if err != nil {
		b.Fatalf("create trace: %v", err)
	}
	tw := trace.NewWriter(out)
	for i := 0; i < n; i++ {
		ev := trace.Event{
			Kind:  "bus",
			Frame: i / 1000,
			Space: "wram",
			Addr:  uint32(i & 0x7f),
			Op:    "read",
			Value: uint64(i & 0xff),
		}
		if i%11 == 0 {
			ev.Op = "write"
		}
		if i%25000 == 0 {
			ev = trace.Event{
				Kind:  "dma",
				Frame: i / 1000,
				Dest:  trace.Range{Space: "vram", Start: 0x1000, End: 0x10ff},
				DMA:   &trace.DMAContext{Channel: 0, Count: 256, Direction: "a_to_b", Target: 0x18, DestRegister: 0x2118},
			}
		}
		if err := tw.Emit(ev); err != nil {
			b.Fatalf("Emit: %v", err)
		}
	}
	if err := closeFn(); err != nil {
		b.Fatalf("close trace: %v", err)
	}
}
