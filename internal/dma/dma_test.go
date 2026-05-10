package dma

import (
	"bytes"
	"encoding/gob"
	"testing"
)

type testBus struct {
	mem    map[uint32]uint8
	writes []busWrite
}

type busWrite struct {
	addr uint32
	val  uint8
}

func newTestBus() *testBus {
	return &testBus{mem: make(map[uint32]uint8)}
}

func (b *testBus) Read(addr uint32) uint8 {
	return b.mem[addr]
}

func (b *testBus) Write(addr uint32, val uint8) {
	b.writes = append(b.writes, busWrite{addr: addr, val: val})
	b.mem[addr] = val
}

type testScheduler struct {
	cycles uint64
}

func (s *testScheduler) AddCycles(cycles uint64) { s.cycles += cycles }

func TestDMAMode5Pattern(t *testing.T) {
	b := newTestBus()
	s := &testScheduler{}
	d := NewDMA(b, s)

	c := &d.Channels[0]
	c.Control = 0x05 // mode 5
	c.Target = 0x10
	c.SrcBank = 0x7E
	c.SrcAddr = 0x1000
	c.Size = 4

	b.mem[0x7E1000] = 0x11
	b.mem[0x7E1001] = 0x22
	b.mem[0x7E1002] = 0x33
	b.mem[0x7E1003] = 0x44

	d.Execute(0)

	if got := len(b.writes); got != 4 {
		t.Fatalf("writes = %d, want 4", got)
	}
	wantAddrs := []uint32{0x2110, 0x2111, 0x2110, 0x2111}
	wantVals := []uint8{0x11, 0x22, 0x33, 0x44}
	for i := range wantAddrs {
		if b.writes[i].addr != wantAddrs[i] || b.writes[i].val != wantVals[i] {
			t.Fatalf("write %d = [%04X]=%02X, want [%04X]=%02X", i, b.writes[i].addr, b.writes[i].val, wantAddrs[i], wantVals[i])
		}
	}
	if c.Size != 0 {
		t.Fatalf("size = %04X, want 0000", c.Size)
	}
	if got, want := s.cycles, uint64(32); got != want {
		t.Fatalf("scheduler cycles = %d, want %d", got, want)
	}
}

func TestDMATriggerChargesPreambleAndAlignment(t *testing.T) {
	b := newTestBus()
	s := &testScheduler{}
	d := NewDMA(b, s)

	c := &d.Channels[0]
	c.Control = 0x00
	c.Target = 0x00
	c.SrcBank = 0x7E
	c.SrcAddr = 0x1000
	c.Size = 1
	b.mem[0x7E1000] = 0x8F

	d.Trigger(0x01)

	if got, want := s.cycles, uint64(32); got != want {
		t.Fatalf("scheduler cycles = %d, want %d", got, want)
	}
	if got := d.Enable; got != 0 {
		t.Fatalf("enable = %02X, want 00", got)
	}
	if got := b.mem[0x2100]; got != 0x8F {
		t.Fatalf("B-bus write = %02X, want 8F", got)
	}
}

func TestDMAMode6And7Patterns(t *testing.T) {
	b := newTestBus()
	d := NewDMA(b, nil)

	c := &d.Channels[0]
	c.Target = 0x20
	c.SrcBank = 0x7E
	c.SrcAddr = 0x2000

	// Mode 6: 0,0 pattern for two bytes.
	c.Control = 0x06
	c.Size = 2
	b.mem[0x7E2000] = 0xAA
	b.mem[0x7E2001] = 0xBB
	d.Execute(0)

	if got := b.writes[0].addr; got != 0x2120 {
		t.Fatalf("mode6 write0 addr = %04X, want 2120", got)
	}
	if got := b.writes[1].addr; got != 0x2120 {
		t.Fatalf("mode6 write1 addr = %04X, want 2120", got)
	}

	// Mode 7: 0,0,1,1 pattern.
	b.writes = nil
	c.Control = 0x07
	c.SrcAddr = 0x2010
	c.Size = 4
	for i := 0; i < 4; i++ {
		b.mem[0x7E2010+uint32(i)] = uint8(0x10 + i)
	}
	d.Execute(0)

	want := []uint32{0x2120, 0x2120, 0x2121, 0x2121}
	for i, addr := range want {
		if got := b.writes[i].addr; got != addr {
			t.Fatalf("mode7 write%d addr = %04X, want %04X", i, got, addr)
		}
	}
}

func TestDMAInvalidABusAddressSkipsTransfer(t *testing.T) {
	b := newTestBus()
	s := &testScheduler{}
	d := NewDMA(b, s)

	c := &d.Channels[0]
	c.Control = 0x00
	c.Target = 0x18
	c.SrcBank = 0x00
	c.SrcAddr = 0x2100
	c.Size = 2

	b.mem[0x002100] = 0x11
	b.mem[0x002101] = 0x22

	d.Execute(0)

	if got := len(b.writes); got != 0 {
		t.Fatalf("writes = %d, want 0", got)
	}
	if got := c.SrcAddr; got != 0x2102 {
		t.Fatalf("source addr = %04X, want 2102", got)
	}
	if got, want := s.cycles, uint64(16); got != want {
		t.Fatalf("scheduler cycles = %d, want %d", got, want)
	}
	if got := c.Size; got != 0 {
		t.Fatalf("size = %04X, want 0000", got)
	}
}

// TestHDMASeedsFromSrcAddrNotTableAddr pins bsnes cpu/dma.cpp:146
// hdmaSetup semantics: the HDMA table pointer is seeded from
// sourceAddress ($43x2/3, Go SrcAddr), not from $43x8/9 (Go TableAddr).
// Real games configure HDMA tables via $43x2/3; $43x8/9 are read-back
// of the running pointer and are overwritten at frame start.
func TestHDMASeedsFromSrcAddrNotTableAddr(t *testing.T) {
	b := newTestBus()
	d := NewDMA(b, nil)

	// Configure via $43x2/3 (A1TxL/H) as a real game would.
	d.Write(0x4302, 0x00) // A1T0L
	d.Write(0x4303, 0x30) // A1T0H -> SrcAddr=0x3000
	d.Write(0x4304, 0x40) // A1T0B -> SrcBank=0x40
	if got := d.Channels[0].SrcAddr; got != 0x3000 {
		t.Fatalf("src addr = %04X, want 3000", got)
	}

	// Park a stale (and wrong) value in TableAddr to prove it is NOT the
	// frame-start source.
	d.Write(0x4308, 0xFF) // A2AxL
	d.Write(0x4309, 0xFF) // A2AxH
	if got := d.Channels[0].TableAddr; got != 0xFFFF {
		t.Fatalf("table addr stage = %04X, want FFFF", got)
	}

	c := &d.Channels[0]
	c.Control = 0x00 // mode 0, direct
	c.Target = 0x18
	d.HDMAEnable = 0x01

	// HDMA table at SrcBank:SrcAddr = $40:3000 -> [count=1][data=0x5A]
	b.mem[0x403000] = 0x01
	b.mem[0x403001] = 0x5A

	d.ResetHDMA()
	d.ExecuteHDMA()

	if got := len(b.writes); got == 0 {
		t.Fatal("no HDMA writes, want one (seeded from SrcAddr)")
	}
	if got := b.writes[0].addr; got != 0x2118 {
		t.Fatalf("hdma dest addr = %04X, want 2118", got)
	}
	if got := b.writes[0].val; got != 0x5A {
		t.Fatalf("hdma value = %02X, want 5A", got)
	}
	// $43x8/9 read-back should reflect the post-seed running pointer
	// (bsnes io.cpp:110-111 returns hdmaAddress).
	if got := c.TableAddr; got == 0xFFFF {
		t.Fatalf("TableAddr still %04X after ResetHDMA: $43x8/9 was used as source", got)
	}
}

func TestHDMACompletionPreservesEnableForNextFrame(t *testing.T) {
	b := newTestBus()
	d := NewDMA(b, nil)
	c := &d.Channels[0]
	c.Control = 0x00
	c.Target = 0x2C
	c.SrcBank = 0x7E
	c.SrcAddr = 0x2000
	d.HDMAEnable = 0x01

	b.mem[0x7E2000] = 0x01
	b.mem[0x7E2001] = 0x11
	b.mem[0x7E2002] = 0x00

	d.ResetHDMA()
	d.ExecuteHDMA()
	if d.HDMAEnable != 0x01 {
		t.Fatalf("HDMAEnable after completed table = %02X, want 01", d.HDMAEnable)
	}
	if c.Active {
		t.Fatal("completed HDMA channel still active for current frame")
	}

	d.ResetHDMA()
	if !c.Active {
		t.Fatal("enabled HDMA channel did not reactivate on next frame")
	}
}

// TestDMAGPWRAMtoWRAMViaB80IsNoOp pins the bsnes-aligned WRAM-to-WRAM
// guard from sfc/cpu/dma.cpp:119-129: GP-DMA with B-bus target = $80
// (i.e. destination $2180 WMDATA) and an A-bus source pointing into
// WRAM ($7E/$7F or the low/high mirrors at $00..$3F:$0000-$1FFF /
// $80..$BF:$0000-$1FFF) is a hardware-invalid pairing. bsnes drops
// writeB while keeping addressA stepping and cycles elapsing; the
// matching guard in Go's DMA suppresses the bus write at the same
// addressB without affecting the source-side read, address auto-
// increment, or scheduler accounting.
//
// We test the four standard A-bus regions that select WRAM:
//   - $7E:0000 (canonical low-WRAM bank)
//   - $7F:0000 (canonical high-WRAM bank)
//   - $00:0000 (low-bank mirror window 0..1FFF)
//   - $80:0000 (high-bank mirror window 0..1FFF)
//
// And one negative case: $00:8000 (LoROM A-bus, NOT WRAM) — the
// guard must NOT fire there even with target=$80; the write should
// still go to $2180 (where the IODevice WMDATA handler processes it).
func TestDMAGPWRAMtoWRAMViaB80IsNoOp(t *testing.T) {
	cases := []struct {
		name        string
		srcBank     uint8
		srcAddr     uint16
		expectGuard bool
	}{
		{"7E_canonical", 0x7E, 0x0000, true},
		{"7F_canonical", 0x7F, 0x0000, true},
		{"low_bank_mirror_00", 0x00, 0x0000, true},
		{"low_bank_mirror_3F", 0x3F, 0x0000, true},
		{"high_bank_mirror_80", 0x80, 0x0000, true},
		{"high_bank_mirror_BF", 0xBF, 0x0000, true},
		{"00_8000_LoROM_not_WRAM", 0x00, 0x8000, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBus()
			s := &testScheduler{}
			d := NewDMA(b, s)
			c := &d.Channels[0]
			c.Control = 0x00 // mode 0, A->B, no decrement, not fixed
			c.Target = 0x80  // -> destBase = $2180 (WMDATA)
			c.SrcBank = tc.srcBank
			c.SrcAddr = tc.srcAddr
			c.Size = 4
			b.mem[uint32(tc.srcBank)<<16|uint32(tc.srcAddr)+0] = 0xAA
			b.mem[uint32(tc.srcBank)<<16|uint32(tc.srcAddr)+1] = 0xBB
			b.mem[uint32(tc.srcBank)<<16|uint32(tc.srcAddr)+2] = 0xCC
			b.mem[uint32(tc.srcBank)<<16|uint32(tc.srcAddr)+3] = 0xDD

			d.Execute(0)

			writes2180 := 0
			for _, w := range b.writes {
				if (w.addr & 0xFFFF) == 0x2180 {
					writes2180++
				}
			}
			if tc.expectGuard {
				if writes2180 != 0 {
					t.Fatalf("WRAM->WRAM via B=$80 produced %d writes to $2180; want 0 (bsnes-aligned guard)", writes2180)
				}
			} else {
				if writes2180 != 4 {
					t.Fatalf("non-WRAM A-bus -> $2180 produced %d writes; want 4 (guard must NOT fire here)", writes2180)
				}
			}

			// Address stepping and timing must elapse regardless of
			// the guard, matching the existing validA path.
			if got := c.SrcAddr; got != tc.srcAddr+4 {
				t.Fatalf("SrcAddr after Execute = %04X, want %04X (auto-increment must run unconditionally)",
					got, tc.srcAddr+4)
			}
			if got := s.cycles; got != 32 { // 4 transfers * 8 cycles each
				t.Fatalf("scheduler cycles = %d, want 32 (timing must elapse unconditionally)", got)
			}
		})
	}
}

// TestHDMAChannelStateRoundTrip pins that the per-channel HDMA-internal
// state survives a gob-mediated SaveState/LoadState round-trip. Six
// fields drive per-line HDMA execution (hdmaAddr, hdmaIndirectAddr,
// hdmaLines, hdmaRepeat, hdmaDoTransfer, hdmaCompleted); on HEAD pre-
// fix they were unexported and were silently zeroed by gob encoding,
// so a save mid-frame would resume with zero hdmaLines and
// hdmaDoTransfer=false -- next ExecuteHDMA would reload an entry from
// hdmaAddr=0, fundamentally broken. This test fails pre-fix and pins
// the post-fix contract that the snapshot round-trips end-to-end.
//
// Mirrors the systemState path: encodes DMAState through encoding/gob
// (matching state.go's gob.NewEncoder/Decoder usage) so the same
// silent-drop bug surfaces here.
func TestHDMAChannelStateRoundTrip(t *testing.T) {
	b := newTestBus()
	d := NewDMA(b, nil)
	c := &d.Channels[0]
	c.Control = 0x40 // indirect mode, mode 0
	c.Target = 0x18
	c.SrcBank = 0x7E
	c.SrcAddr = 0x3000
	c.IndirectBank = 0x7E
	d.HDMAEnable = 0x01

	// HDMA table at $7E:3000:
	//   line-byte 0x05 (5 lines, no repeat), indirect ptr 0x4000
	//   then a continuation entry the second ExecuteHDMA can read
	b.mem[0x7E3000] = 0x05
	b.mem[0x7E3001] = 0x00
	b.mem[0x7E3002] = 0x40
	// indirect data at $7E:4000
	b.mem[0x7E4000] = 0xAB

	d.ResetHDMA()
	// Execute one scanline to advance into a mid-table state where
	// hdmaLines > 0 (4 left after the first transfer), hdmaDoTransfer
	// is false (no repeat), hdmaAddr is past the entry header, and
	// hdmaIndirectAddr is set.
	d.ExecuteHDMA()

	wantLines := c.hdmaLines
	wantRepeat := c.hdmaRepeat
	wantDoTransfer := c.hdmaDoTransfer
	wantCompleted := c.hdmaCompleted
	wantHDMAAddr := c.hdmaAddr
	wantIndirect := c.hdmaIndirectAddr

	if wantLines == 0 {
		t.Fatalf("test setup: hdmaLines=0 after first ExecuteHDMA, expected mid-table state")
	}
	if wantHDMAAddr == 0 {
		t.Fatalf("test setup: hdmaAddr=0 after ResetHDMA + ExecuteHDMA, expected advanced pointer")
	}

	state := encodeDecodeDMAState(t, d.SaveState())
	d2 := NewDMA(b, nil)
	d2.LoadState(state)
	c2 := &d2.Channels[0]

	if got := c2.hdmaLines; got != wantLines {
		t.Fatalf("hdmaLines after round-trip = %d, want %d", got, wantLines)
	}
	if got := c2.hdmaRepeat; got != wantRepeat {
		t.Fatalf("hdmaRepeat after round-trip = %v, want %v", got, wantRepeat)
	}
	if got := c2.hdmaDoTransfer; got != wantDoTransfer {
		t.Fatalf("hdmaDoTransfer after round-trip = %v, want %v", got, wantDoTransfer)
	}
	if got := c2.hdmaCompleted; got != wantCompleted {
		t.Fatalf("hdmaCompleted after round-trip = %v, want %v", got, wantCompleted)
	}
	if got := c2.hdmaAddr; got != wantHDMAAddr {
		t.Fatalf("hdmaAddr after round-trip = %04X, want %04X", got, wantHDMAAddr)
	}
	if got := c2.hdmaIndirectAddr; got != wantIndirect {
		t.Fatalf("hdmaIndirectAddr after round-trip = %04X, want %04X", got, wantIndirect)
	}
}

// encodeDecodeDMAState mirrors the systemState path in state.go: it
// gob-encodes the DMAState and gob-decodes it into a fresh value, so
// the test exercises the same silent-drop behavior gob produces in the
// top-level Serialize/Unserialize.
func encodeDecodeDMAState(t *testing.T, state DMAState) DMAState {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	var out DMAState
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("gob decode: %v", err)
	}
	return out
}
