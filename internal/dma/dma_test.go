package dma

import "testing"

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
