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

func TestHDMAUsesSrcBankTableAddressAndReadsTableHighByte(t *testing.T) {
	b := newTestBus()
	d := NewDMA(b, nil)

	d.Write(0x4308, 0x34) // A2AxL
	d.Write(0x4309, 0x12) // A2AxH
	if got := d.Channels[0].TableAddr; got != 0x1234 {
		t.Fatalf("table addr = %04X, want 1234", got)
	}

	c := &d.Channels[0]
	c.Control = 0x00 // mode 0, direct
	c.Target = 0x18
	c.SrcBank = 0x40
	c.TableAddr = 0x3000
	d.HDMAEnable = 0x01

	// [line count=1][data=0x5A]
	b.mem[0x403000] = 0x01
	b.mem[0x403001] = 0x5A

	d.ResetHDMA()
	d.ExecuteHDMA()

	if got := len(b.writes); got == 0 {
		t.Fatal("no HDMA writes, want one")
	}
	if got := b.writes[0].addr; got != 0x2118 {
		t.Fatalf("hdma dest addr = %04X, want 2118", got)
	}
	if got := b.writes[0].val; got != 0x5A {
		t.Fatalf("hdma value = %02X, want 5A", got)
	}
}
