package parity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/ppu"
)

func TestMode7LatchTraceGolden(t *testing.T) {
	const (
		wantLatchEvents = 12
		wantLatchHash   = "098ebf0fa083c9ae282849422fa91dce8bf02ed4a1b3386fd0e3e839b19e0c9c"
		wantPairEvents  = 2
		wantPairHash    = "03ed4cba0823749417726d14ad06b4075faabf7fe98514c48df24049df6fc290"
	)

	latches, pairs := goMode7LatchTrace(t, syntheticMode7LatchROM())
	latchHash := hashMode7LatchEvents(latches)
	if got := len(latches); got != wantLatchEvents {
		t.Fatalf("Mode 7 latch events = %d, want %d; hash=%s", got, wantLatchEvents, latchHash)
	}
	if latchHash != wantLatchHash {
		t.Fatalf("Mode 7 latch hash = %s, want %s", latchHash, wantLatchHash)
	}

	pairHash := hashMode7PairEvents(pairs)
	if got := len(pairs); got != wantPairEvents {
		t.Fatalf("Mode 7 pair events = %d, want %d; hash=%s", got, wantPairEvents, pairHash)
	}
	if pairHash != wantPairHash {
		t.Fatalf("Mode 7 pair hash = %s, want %s", pairHash, wantPairHash)
	}
}

func goMode7LatchTrace(t *testing.T, romData []byte) ([]ppu.Mode7LatchEvent, []ppu.Mode7MatrixPairEvent) {
	sys := snes.NewSystem(nil)
	mapLoROM(sys, romData)
	var latches []ppu.Mode7LatchEvent
	var pairs []ppu.Mode7MatrixPairEvent
	sys.PPU.Mode7LatchEventHook = func(e ppu.Mode7LatchEvent) {
		latches = append(latches, e)
	}
	sys.PPU.Mode7MatrixPairHook = func(e ppu.Mode7MatrixPairEvent) {
		pairs = append(pairs, e)
	}
	if !sys.Load() {
		t.Fatal("Go System Load failed")
	}
	if err := sys.Run(); err != nil {
		t.Fatal(err)
	}
	if len(latches) == 0 {
		t.Fatal("no Mode 7 latch events recorded")
	}
	if len(pairs) == 0 {
		t.Fatal("no Mode 7 matrix pair events recorded")
	}
	return latches, pairs
}

func syntheticMode7LatchROM() []byte {
	rom := make([]byte, 0x8000)
	program := []byte{0x78} // SEI
	for _, write := range []struct {
		addr uint16
		val  uint8
	}{
		{0x211b, 0x34}, {0x211b, 0x12},
		{0x211c, 0x78}, {0x211c, 0x56},
		{0x211d, 0xbc}, {0x211d, 0x9a},
		{0x211e, 0xf0}, {0x211e, 0xde},
		{0x211f, 0x11}, {0x211f, 0x22},
		{0x2120, 0x33}, {0x2120, 0x44},
	} {
		program = append(program,
			0xa9, write.val,
			0x8d, uint8(write.addr), uint8(write.addr>>8),
		)
	}
	loop := uint16(0x8000 + len(program))
	program = append(program, 0x4c, uint8(loop), uint8(loop>>8))
	copy(rom, program)
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func hashMode7LatchEvents(events []ppu.Mode7LatchEvent) string {
	h := sha256.New()
	var buf [12]byte
	for _, event := range events {
		binary.LittleEndian.PutUint16(buf[0:2], event.Addr)
		binary.LittleEndian.PutUint16(buf[2:4], event.Value)
		binary.LittleEndian.PutUint32(buf[4:8], uint32(event.FrameCount))
		binary.LittleEndian.PutUint16(buf[8:10], uint16(event.HCounter))
		binary.LittleEndian.PutUint16(buf[10:12], uint16(event.VCounter))
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashMode7PairEvents(events []ppu.Mode7MatrixPairEvent) string {
	h := sha256.New()
	var buf [16]byte
	for _, event := range events {
		binary.LittleEndian.PutUint16(buf[0:2], event.FirstAddr)
		binary.LittleEndian.PutUint16(buf[2:4], event.FirstValue)
		binary.LittleEndian.PutUint16(buf[4:6], event.NextAddr)
		binary.LittleEndian.PutUint16(buf[6:8], event.NextValue)
		binary.LittleEndian.PutUint32(buf[8:12], uint32(event.FrameCount))
		binary.LittleEndian.PutUint16(buf[12:14], uint16(event.HCounter))
		binary.LittleEndian.PutUint16(buf[14:16], uint16(event.VCounter))
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
