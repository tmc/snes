package dma

import (
	"fmt"
	"reflect"
	"testing"
)

type hdmaBus struct {
	mem     map[uint32]uint8
	events  []string
	onWrite func(uint32, uint8)
}

func (b *hdmaBus) Read(addr uint32) uint8 {
	b.events = append(b.events, fmt.Sprintf("R%06X", addr))
	return b.mem[addr]
}
func (b *hdmaBus) Write(addr uint32, value uint8) {
	b.events = append(b.events, fmt.Sprintf("W%06X=%02X", addr, value))
	b.mem[addr] = value
	if b.onWrite != nil {
		b.onWrite(addr, value)
	}
}

func newHDMAFixture() (*DMA, *hdmaBus) {
	b := &hdmaBus{mem: map[uint32]uint8{}}
	d := NewDMA(b, nil)
	c := &d.Channels[0]
	c.Control = 0
	c.Target = 0x18
	c.SrcBank = 0x40
	c.SrcAddr = 0x3000
	d.HDMAEnable = 1
	return d, b
}

func TestHDMATransfersBeforeReloads(t *testing.T) {
	d, b := newHDMAFixture()
	d.Channels[1] = d.Channels[0]
	d.Channels[1].Index = 1
	d.Channels[1].SrcAddr = 0x3100
	d.Channels[1].Target = 0x19
	d.HDMAEnable = 3
	b.mem[0x403000], b.mem[0x403001] = 1, 0xaa
	b.mem[0x403100], b.mem[0x403101] = 1, 0xbb
	b.onWrite = func(addr uint32, value uint8) {
		if addr == 0x2119 {
			b.mem[0x403002] = 2
		}
	}
	runHDMA(t, d, true)
	b.events = nil
	runHDMA(t, d, false)
	want := []string{"R403001", "W002118=AA", "R403101", "W002119=BB", "R403002", "R403102"}
	if !reflect.DeepEqual(b.events, want) {
		t.Fatalf("bus order=%v, want %v", b.events, want)
	}
	if !d.Channels[0].Active || d.Channels[0].LineCount != 2 {
		t.Fatal("channel0 reloaded before channel1's bus effect")
	}
	if d.Channels[0].TableAddr != 0x3003 || d.Channels[1].TableAddr != 0x3103 {
		t.Fatal("table readback did not advance")
	}
}

func TestHDMAIndirectTerminatorReads(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(fmt.Sprint(later), func(t *testing.T) {
			d, b := newHDMAFixture()
			d.Channels[0].Control = 0x40
			b.mem[0x403001], b.mem[0x403002] = 0x34, 0x12
			want := []string{"R403000", "R403001"}
			wantAddr := uint16(0x3002)
			wantSize := uint16(0x3400)
			if later {
				d.HDMAEnable = 3
				d.Channels[1] = d.Channels[0]
				d.Channels[1].Index = 1
				d.Channels[1].Control = 0
				d.Channels[1].SrcAddr = 0x3100
				want = append(want, "R403002", "R403100")
				wantAddr = 0x3003
				wantSize = 0x1234
			}
			runHDMA(t, d, true)
			if !reflect.DeepEqual(b.events, want) {
				t.Fatalf("terminator reads=%v, want %v", b.events, want)
			}
			if d.Channels[0].Active || d.Channels[0].TableAddr != wantAddr || d.Channels[0].Size != wantSize {
				t.Fatalf("terminated registers=%+v", d.Channels[0])
			}
		})
	}
}

func TestHDMAReadsOnSkippedLines(t *testing.T) {
	d, b := newHDMAFixture()
	b.mem[0x403000], b.mem[0x403001] = 3, 0x55
	runHDMA(t, d, true)
	runHDMA(t, d, false)
	b.events = nil
	runHDMA(t, d, false)
	if want := []string{"R403002"}; !reflect.DeepEqual(b.events, want) {
		t.Fatalf("skip bus events=%v, want %v", b.events, want)
	}
	if d.Channels[0].TableAddr != 0x3002 || d.Channels[0].LineCount != 1 {
		t.Fatal("skip changed table pointer or missed count decrement")
	}
}

func TestHDMA128LineCounter(t *testing.T) {
	d, b := newHDMAFixture()
	b.mem[0x403000], b.mem[0x403001] = 0x80, 0x55
	writes := 0
	b.onWrite = func(uint32, uint8) { writes++ }
	runHDMA(t, d, true)
	runHDMA(t, d, false)
	if d.Channels[0].LineCount != 0x7f || d.Channels[0].hdmaDoTransfer {
		t.Fatal("0x80 did not decrement to non-repeating0x7f")
	}
	for i := 1; i < 128; i++ {
		runHDMA(t, d, false)
	}
	if writes != 1 || d.Channels[0].Active {
		t.Fatalf("128-line entry writes=%d active=%v", writes, d.Channels[0].Active)
	}
}

func TestHDMALiveRegisterPointers(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		d, b := newHDMAFixture()
		b.mem[0x403000] = 0x82
		b.mem[0x404010] = 0x66
		runHDMA(t, d, true)
		d.Write(0x4308, 0x10)
		d.Write(0x4309, 0x40)
		d.Write(0x430a, 0x81)
		b.events = nil
		runHDMA(t, d, false)
		want := []string{"R404010", "W002118=66", "R404011"}
		if !reflect.DeepEqual(b.events, want) {
			t.Fatalf("live table events=%v", b.events)
		}
		if d.Read(0x4308) != 0x12 || d.Read(0x4309) != 0x40 || d.Channels[0].Active {
			t.Fatal("table/count readback differs from live state")
		}
	})
	t.Run("indirect", func(t *testing.T) {
		d, b := newHDMAFixture()
		d.Channels[0].Control = 0x40
		d.Channels[0].IndirectBank = 0x7e
		b.mem[0x403000] = 2
		b.mem[0x7e2000] = 0x77
		runHDMA(t, d, true)
		d.Write(0x4305, 0)
		d.Write(0x4306, 0x20)
		b.events = nil
		runHDMA(t, d, false)
		if b.mem[0x2118] != 0x77 || d.Read(0x4305) != 1 || d.Read(0x4306) != 0x20 {
			t.Fatal("indirect pointer write/readback did not track transfer")
		}
	})
}

func TestHDMADirectionAndBAddressWrap(t *testing.T) {
	t.Run("reverse", func(t *testing.T) {
		d, b := newHDMAFixture()
		d.Channels[0].Control = 0x80
		b.mem[0x403000] = 1
		b.mem[0x2118] = 0x67
		runHDMA(t, d, true)
		b.events = nil
		runHDMA(t, d, false)
		want := []string{"R002118", "W403001=67", "R403002"}
		if !reflect.DeepEqual(b.events, want) {
			t.Fatalf("reverse events=%v", b.events)
		}
	})
	t.Run("wrap", func(t *testing.T) {
		d, b := newHDMAFixture()
		d.Channels[0].Control = 1
		d.Channels[0].Target = 0xff
		b.mem[0x403000], b.mem[0x403001], b.mem[0x403002] = 1, 0x12, 0x34
		runHDMA(t, d, true)
		runHDMA(t, d, false)
		if b.mem[0x21ff] != 0x12 || b.mem[0x2100] != 0x34 {
			t.Fatal("B-bus offset did not wrap at8 bits")
		}
		if _, ok := b.mem[0x2200]; ok {
			t.Fatal("HDMA escaped B-bus window")
		}
	})
	t.Run("WRAM pair", func(t *testing.T) {
		d, b := newHDMAFixture()
		d.Channels[0].SrcBank = 0x7e
		d.Channels[0].Target = 0x80
		b.mem[0x7e3000], b.mem[0x7e3001] = 1, 0x77
		runHDMA(t, d, true)
		b.events = nil
		runHDMA(t, d, false)
		want := []string{"R7E3001", "R7E3002"}
		if !reflect.DeepEqual(b.events, want) {
			t.Fatalf("invalid WRAM pair events=%v", b.events)
		}
	})
}

func TestHDMAOrderStateResume(t *testing.T) {
	d, b := newHDMAFixture()
	b.mem[0x403000] = 0x83
	b.mem[0x403001] = 1
	b.mem[0x403002] = 2
	b.mem[0x403003] = 3
	runHDMA(t, d, true)
	runHDMA(t, d, false)
	state := d.SaveState()
	restored, other := newHDMAFixture()
	for addr, value := range b.mem {
		other.mem[addr] = value
	}
	restored.LoadState(state)
	b.events = nil
	other.events = nil
	runHDMA(t, d, false)
	runHDMA(t, restored, false)
	runHDMA(t, d, false)
	runHDMA(t, restored, false)
	if !reflect.DeepEqual(b.events, other.events) || !reflect.DeepEqual(d.SaveState(), restored.SaveState()) {
		t.Fatal("restored HDMA bus order or continuation differs")
	}
	if b.mem[0x2118] != 3 || d.Channels[0].Active {
		t.Fatal("continuation did not transfer and terminate")
	}
}

func TestHDMATransferModeAddressMatrix(t *testing.T) {
	for mode, want := range [][]uint32{
		{0x21ff}, {0x21ff, 0x2100}, {0x21ff, 0x21ff}, {0x21ff, 0x21ff, 0x2100, 0x2100},
		{0x21ff, 0x2100, 0x2101, 0x2102}, {0x21ff, 0x2100, 0x21ff, 0x2100}, {0x21ff, 0x21ff}, {0x21ff, 0x21ff, 0x2100, 0x2100},
	} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			d, b := newHDMAFixture()
			d.Channels[0].Control = uint8(mode)
			d.Channels[0].Target = 0xff
			b.mem[0x403000] = 1
			for i := range want {
				b.mem[0x403001+uint32(i)] = uint8(i + 1)
			}
			var addresses []uint32
			b.onWrite = func(addr uint32, value uint8) {
				addresses = append(addresses, addr)
				if value != uint8(len(addresses)) {
					t.Errorf("byte %d value=%d", len(addresses), value)
				}
			}
			runHDMA(t, d, true)
			runHDMA(t, d, false)
			if !reflect.DeepEqual(addresses, want) {
				t.Fatalf("mode%d addresses=%v, want %v", mode, addresses, want)
			}
		})
	}
}
