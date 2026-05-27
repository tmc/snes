package parity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/bus"
)

func TestOAMScanlineRefreshTraceGolden(t *testing.T) {
	const (
		// Re-baselined at HEAD 2224143 after PPU/NMI timing work changed
		// the deterministic read cadence. Verified stable across three
		// consecutive runs.
		wantReads = 5507
		wantHash  = "453033fe7bc458474a7dfb870f1b21b84fb75dbe0b20ca50f30e61fbd6b4ac6c"
	)

	trace := goOAMReadTrace(t, syntheticOAMRefreshROM(), 1)
	hash := hashOAMReadTrace(trace)
	if got := len(trace); got != wantReads {
		t.Fatalf("OAM read trace events = %d, want %d; hash=%s", got, wantReads, hash)
	}
	if hash != wantHash {
		t.Fatalf("OAM read trace hash = %s, want %s", hash, wantHash)
	}
}

type oamReadEvent struct {
	Cycles  uint64
	PB      uint8
	PC      uint16
	Value   uint8
	OAMAddr uint16
	H       uint16
	V       uint16
}

type oamReadTraceDevice struct {
	dev   bus.MemoryDevice
	sys   *snes.System
	trace *[]oamReadEvent
}

func (d *oamReadTraceDevice) Read(addr uint32) uint8 {
	value := d.dev.Read(addr)
	if addr&0xffff == 0x2138 {
		state := d.sys.PPU.SaveState()
		*d.trace = append(*d.trace, oamReadEvent{
			Cycles:  d.sys.CPU.Cycles,
			PB:      d.sys.CPU.PB,
			PC:      d.sys.CPU.PC,
			Value:   value,
			OAMAddr: d.sys.PPU.OAMAddr,
			H:       uint16(state.HCounter),
			V:       uint16(state.VCounter),
		})
	}
	return value
}

func (d *oamReadTraceDevice) Write(addr uint32, value uint8) {
	d.dev.Write(addr, value)
}

func (d *oamReadTraceDevice) BlockRead(addr uint32, length int) []byte {
	return d.dev.BlockRead(addr, length)
}

func goOAMReadTrace(t *testing.T, romData []byte, frames int) []oamReadEvent {
	sys := snes.NewSystem(nil)
	mapLoROM(sys, romData)
	var trace []oamReadEvent
	wrapOAMReadPages(sys, &trace)
	if !sys.Load() {
		t.Fatal("Go System Load failed")
	}
	for frame := 0; frame < frames; frame++ {
		if err := sys.Run(); err != nil {
			t.Fatal(err)
		}
	}
	if len(trace) == 0 {
		t.Fatal("no OAM reads recorded")
	}
	return trace
}

func wrapOAMReadPages(sys *snes.System, trace *[]oamReadEvent) {
	for bank := uint32(0); bank < 0x40; bank++ {
		wrapOAMReadPage(sys, bank, trace)
		wrapOAMReadPage(sys, bank|0x80, trace)
	}
}

func wrapOAMReadPage(sys *snes.System, bank uint32, trace *[]oamReadEvent) {
	dev := sys.Bus.GetPage(bank, 0x21)
	sys.Bus.Map(bank<<16|0x2100, bank<<16|0x21ff, &oamReadTraceDevice{
		dev:   dev,
		sys:   sys,
		trace: trace,
	})
}

func syntheticOAMRefreshROM() []byte {
	rom := make([]byte, 0x8000)
	program := []byte{
		0x78,                         // SEI
		0xa9, 0x80, 0x8d, 0x00, 0x21, // LDA #$80; STA $2100
		0xa9, 0x10, 0x8d, 0x02, 0x21, // LDA #$10; STA $2102
		0xa9, 0x00, 0x8d, 0x03, 0x21, // LDA #$00; STA $2103
		0xa9, 0xcc, 0x8d, 0x04, 0x21, // LDA #$CC; STA $2104
		0xa9, 0xdd, 0x8d, 0x04, 0x21, // LDA #$DD; STA $2104
		0xa9, 0x10, 0x8d, 0x02, 0x21, // LDA #$10; STA $2102
		0xa9, 0x00, 0x8d, 0x03, 0x21, // LDA #$00; STA $2103
		0xa9, 0x0f, 0x8d, 0x00, 0x21, // LDA #$0F; STA $2100
	}
	loop := uint16(0x8000 + len(program))
	program = append(program,
		0xad, 0x38, 0x21, // LDA $2138
		0x4c, uint8(loop), uint8(loop>>8),
	)
	copy(rom, program)
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func hashOAMReadTrace(trace []oamReadEvent) string {
	h := sha256.New()
	var buf [17]byte
	for _, event := range trace {
		binary.LittleEndian.PutUint64(buf[0:8], event.Cycles)
		buf[8] = event.PB
		binary.LittleEndian.PutUint16(buf[9:11], event.PC)
		buf[11] = event.Value
		binary.LittleEndian.PutUint16(buf[12:14], event.OAMAddr)
		binary.LittleEndian.PutUint16(buf[14:16], event.H)
		buf[16] = uint8(event.V)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
