package dma

import (
	"fmt"
	"reflect"
	"testing"
)

type timedBus struct {
	now    uint64
	mem    map[uint32]uint8
	events []string
}

func (b *timedBus) Read(a uint32) uint8 {
	b.events = append(b.events, fmt.Sprintf("%d:R%06x", b.now, a))
	return b.mem[a]
}
func (b *timedBus) Write(a uint32, v uint8) {
	b.events = append(b.events, fmt.Sprintf("%d:W%06x=%02x", b.now, a, v))
	b.mem[a] = v
}
func timedFixture() (*DMA, *timedBus) {
	b := &timedBus{mem: map[uint32]uint8{0x403000: 1, 0x403001: 0xaa}}
	d := NewDMA(b, nil)
	d.SetClock(func() uint64 { return b.now }, func(n uint64) { b.now += n })
	d.Channels[0] = Channel{Index: 0, SrcBank: 0x40, SrcAddr: 0x3000, Target: 0x18}
	d.HDMAEnable = 1
	return d, b
}
func timedDrain(t *testing.T, d *DMA) {
	t.Helper()
	for n := 0; d.Busy(); n++ {
		if n > 1000000 {
			t.Fatal("DMA did not finish")
		}
		if err := ValidateExecution(d.SaveState().Execution); err != nil {
			t.Fatalf("saved phase rejected: %+v: %v", d.SaveState().Execution, err)
		}
		d.RunSlice(1)
	}
}
func timedEdge(t *testing.T, d *DMA, b *timedBus) {
	t.Helper()
	d.BeginEdge(8)
	if d.Busy() {
		t.Fatal("first edge stole bus")
	}
	b.now += 8
	d.BeginEdge(8)
	if !d.Busy() {
		t.Fatal("second edge did not acquire bus")
	}
	timedDrain(t, d)
}

func TestTimedHDMAHalfReadRestore(t *testing.T) {
	for split := uint64(0); split <= 32; split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			d, b := timedFixture()
			d.RequestHDMA(0, true)
			d.BeginEdge(8)
			b.now = 8
			d.BeginEdge(8)
			d.RunSlice(split)
			state := d.SaveState()
			clock := b.now
			events := append([]string(nil), b.events...)
			timedDrain(t, d)
			wantState, wantClock, wantEvents := d.SaveState(), b.now, append([]string(nil), b.events...)
			e, c := timedFixture()
			if err := e.LoadState(state); err != nil {
				t.Fatal(err)
			}
			c.now = clock
			c.events = events
			timedDrain(t, e)
			if c.now != wantClock || !reflect.DeepEqual(c.events, wantEvents) || !reflect.DeepEqual(e.SaveState(), wantState) {
				t.Fatalf("split %d restore mismatch: clocks %d/%d events %v/%v", split, c.now, wantClock, c.events, wantEvents)
			}
			if wantClock != 40 || !reflect.DeepEqual(wantEvents, []string{"28:R403000"}) {
				t.Fatalf("setup clocks/events=%d %v", wantClock, wantEvents)
			}
		})
	}
}

func TestTimedHDMATransferAndReload(t *testing.T) {
	d, b := timedFixture()
	d.RequestHDMA(0, true)
	timedEdge(t, d, b)
	b.events = nil
	d.RequestHDMA(b.now, false)
	timedEdge(t, d, b)
	want := []string{"68:R403001", "72:W002118=aa", "76:R403002"}
	if !reflect.DeepEqual(b.events, want) || b.now != 88 {
		t.Fatalf("run=%v at %d, want %v at 88", b.events, b.now, want)
	}
}

func TestTimedHDMAAllChannels(t *testing.T) {
	d, b := timedFixture()
	d.HDMAEnable = 0xff
	for i := range d.Channels {
		d.Channels[i] = d.Channels[0]
		d.Channels[i].Index = i
		d.Channels[i].SrcAddr += uint16(16 * i)
		d.Channels[i].Target += uint8(i)
		b.mem[0x403000+uint32(16*i)] = 1
		b.mem[0x403001+uint32(16*i)] = uint8(i + 1)
	}
	d.RequestHDMA(0, true)
	timedEdge(t, d, b)
	b.events = nil
	d.RequestHDMA(b.now, false)
	timedEdge(t, d, b)
	if len(b.events) != 24 {
		t.Fatalf("events=%v", b.events)
	}
	for i := 0; i < 8; i++ {
		if b.mem[0x2118+uint32(i)] != uint8(i+1) {
			t.Fatalf("channel %d missing", i)
		}
	}
	// First 16 operations are transfers; only then may descriptors reload.
	for i := 0; i < 8; i++ {
		want := fmt.Sprintf("R%06x", 0x403002+16*i)
		got := b.events[16+i]
		if got[len(got)-len(want):] != want {
			t.Fatalf("descriptor order=%v", b.events)
		}
	}
}

func TestTimedHDMAInterruptsGeneralDMA(t *testing.T) {
	for _, tc := range []struct{ cycle, trigger, want uint64 }{{8, 36, 80}, {6, 28, 64}} {
		t.Run(fmt.Sprint(tc.cycle), func(t *testing.T) {
			d, b := timedFixture()
			d.Channels[0].Size = 3
			d.ResetHDMA()
			b.events = nil
			d.SetClock(func() uint64 { return b.now }, func(n uint64) {
				b.now += n
				if b.now == tc.trigger {
					d.RequestHDMA(b.now, false)
				}
			})
			d.Request(1)
			d.BeginEdge(tc.cycle)
			b.now += tc.cycle
			d.BeginEdge(tc.cycle)
			timedDrain(t, d)
			writes := 0
			for _, ev := range b.events {
				for _, c := range ev {
					if c == 'W' {
						writes++
					}
				}
			}
			// HDMA cancels its GDMA channel between bytes. Both the inner HDMA
			// return and the outer GDMA return resynchronize to the interrupted CPU
			// cycle, without adding those CPU waits to counter.dma.
			if b.now != tc.want || writes != 2 || d.Enable != 0 || d.Channels[0].SrcAddr != 0x3001 {
				t.Fatalf("clock=%d want=%d writes=%d events=%v channel=%+v", b.now, tc.want, writes, b.events, d.Channels[0])
			}
		})
	}
}

func TestTimedHDMATransferHalfRestore(t *testing.T) {
	for split := uint64(0); split <= 40; split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			d, b := timedFixture()
			d.RequestHDMA(0, true)
			timedEdge(t, d, b)
			d.RequestHDMA(b.now, false)
			d.BeginEdge(8)
			b.now += 8
			d.BeginEdge(8)
			d.RunSlice(split)
			state, clock := d.SaveState(), b.now
			mem := map[uint32]uint8{}
			for k, v := range b.mem {
				mem[k] = v
			}
			events := append([]string(nil), b.events...)
			timedDrain(t, d)
			e, c := timedFixture()
			if err := e.LoadState(state); err != nil {
				t.Fatal(err)
			}
			c.now, c.mem, c.events = clock, mem, events
			timedDrain(t, e)
			if !reflect.DeepEqual(c, b) || !reflect.DeepEqual(e.SaveState(), d.SaveState()) {
				t.Fatalf("split %d: resumed=%+v uninterrupted=%+v", split, c, b)
			}
		})
	}
}

func TestTimedHDMAFutureRequest(t *testing.T) {
	d, b := timedFixture()
	d.RequestHDMA(12, true)
	d.BeginEdge(8)
	if d.execution.Armed {
		t.Fatal("future event armed early")
	}
	b.now = 12
	d.BeginEdge(8)
	if !d.execution.Armed {
		t.Fatal("reached event not armed")
	}
}

func TestTimedHDMACompletedChannelDoesNotTakeBus(t *testing.T) {
	d, b := timedFixture()
	b.mem[0x403000] = 0
	d.RequestHDMA(0, true)
	timedEdge(t, d, b)
	before := b.now
	d.RequestHDMA(before, false)
	d.BeginEdge(8)
	if d.execution.Armed || d.Busy() || b.now != before {
		t.Fatal("completed channels armed a scanline transfer")
	}
}

func TestTimedHDMAModesMatchBusSemantics(t *testing.T) {
	for mode := uint8(0); mode < 8; mode++ {
		for _, indirect := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("mode%d/indirect%v/reverse%v", mode, indirect, reverse), func(t *testing.T) {
					d, b := timedFixture()
					e, c := timedFixture()
					for _, f := range []struct {
						d *DMA
						b *timedBus
					}{{d, b}, {e, c}} {
						ch := &f.d.Channels[0]
						ch.Control = mode
						ch.IndirectBank = 0x41
						if indirect {
							ch.Control |= 0x40
						}
						if reverse {
							ch.Control |= 0x80
						}
						f.b.mem[0x403000] = 0x82
						addr := uint32(0x403001)
						if indirect {
							f.b.mem[0x403001] = 0x40
							f.b.mem[0x403002] = 0x20
							addr = 0x412040
						}
						for i := 0; i < hdmaTransferLength(mode)*2; i++ {
							f.b.mem[addr+uint32(i)] = uint8(i + 1)
						}
						for i := uint32(0); i < 4; i++ {
							f.b.mem[0x2118+i] = uint8(0xa0 + i)
						}
					}
					d.RequestHDMA(0, true)
					timedEdge(t, d, b)
					e.ResetHDMA()
					for line := 0; line < 2; line++ {
						b.events, c.events = nil, nil
						d.RequestHDMA(b.now, false)
						timedEdge(t, d, b)
						e.ExecuteHDMA()
						normalize := func(events []string) []string {
							out := make([]string, len(events))
							for i, v := range events {
								for j, ch := range v {
									if ch == ':' {
										out[i] = v[j+1:]
										break
									}
								}
							}
							return out
						}
						ds, es := d.SaveState(), e.SaveState()
						if !reflect.DeepEqual(normalize(b.events), normalize(c.events)) || !reflect.DeepEqual(b.mem, c.mem) || ds.Channels[0] != es.Channels[0] || ds.HDMA[0] != es.HDMA[0] {
							t.Fatalf("line%d timed=%v immediate=%v", line, b.events, c.events)
						}
					}
				})
			}
		}
	}
}

func TestTimedGeneralDMAHalfRestore(t *testing.T) {
	for split := uint64(0); split < 64; split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			d, b := timedFixture()
			d.HDMAEnable = 0
			d.Channels[0].Size = 2
			d.Request(1)
			d.BeginEdge(8)
			b.now = 8
			d.BeginEdge(8)
			d.RunSlice(split)
			state, clock := d.SaveState(), b.now
			mem := map[uint32]uint8{}
			for k, v := range b.mem {
				mem[k] = v
			}
			events := append([]string(nil), b.events...)
			timedDrain(t, d)
			e, c := timedFixture()
			if err := e.LoadState(state); err != nil {
				t.Fatal(err)
			}
			c.now, c.mem, c.events = clock, mem, events
			timedDrain(t, e)
			if !reflect.DeepEqual(c, b) || !reflect.DeepEqual(e.SaveState(), d.SaveState()) {
				t.Fatalf("split %d mismatch", split)
			}
		})
	}
}

func TestTimedHDMASetupCancelsPendingDMA(t *testing.T) {
	d, b := timedFixture()
	d.Channels[0].Size = 2
	d.Request(1)
	d.RequestHDMA(0, true)
	timedEdge(t, d, b)
	// No DMA divider alignment precedes HDMA when GDMA is already enabled.
	// Setup consumes16, then the canceled DMA yields after a full8 CPU sync.
	if b.now != 32 || d.Enable != 0 || d.execution.Pending || !reflect.DeepEqual(b.events, []string{"20:R403000"}) {
		t.Fatalf("clock=%d events=%v state=%+v", b.now, b.events, d.execution)
	}
}

func TestTimedExecutionRejectsUnsafeContinuation(t *testing.T) {
	d, _ := timedFixture()
	before := d.SaveState()
	for _, bad := range []ExecutionState{
		{Phase: phaseHDMAByte, Channel: 8, ClockCount: 8},
		{Phase: phaseHDMAChannel, Channel: 8, HDMAReturn: phaseGPByte, DMAChannel: 8, General: true, ClockCount: 8},
		{Phase: phaseGPByte, DMAChannel: 8, ClockCount: 8},
		{Phase: phaseWrite, Resume: phaseWrite, ClockCount: 8},
		{Phase: phaseReadDone, Next: phaseReadDone, ClockCount: 8},
		{Phase: phaseHDMAReturn, HDMAReturn: phaseHDMAReturn, ClockCount: 8},
		{Phase: phaseFinish, ClockCount: 0},
		{Phase: phaseIdle, Wait: 1},
	} {
		state := before
		state.Execution = bad
		if err := d.LoadState(state); err == nil {
			t.Fatalf("accepted unsafe continuation %+v", bad)
		}
		if d.SaveState() != before {
			t.Fatal("invalid load mutated controller")
		}
	}
	d.Request(1)
	before = d.SaveState()
	for _, n := range []uint64{0, 1, 2, 7, 13} {
		d.BeginEdge(n)
		if d.SaveState() != before {
			t.Fatalf("invalid edge %d mutated ownership", n)
		}
	}
}
