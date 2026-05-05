package ppu

import "testing"

type hdmaSpy struct {
	p      *PPU
	calls  []hdmaCall
	events []hdmaEvent
}

type hdmaCall struct {
	h int
	v int
}

type hdmaEvent struct {
	kind  string
	h     int
	v     int
	frame int
}

func (s *hdmaSpy) ExecuteHDMA() {
	s.calls = append(s.calls, hdmaCall{h: s.p.hCounter, v: s.p.vCounter})
	s.events = append(s.events, hdmaEvent{
		kind:  "execute",
		h:     s.p.hCounter,
		v:     s.p.vCounter,
		frame: s.p.FrameCount,
	})
}

func (s *hdmaSpy) ResetHDMA() {
	s.events = append(s.events, hdmaEvent{
		kind:  "reset",
		h:     s.p.hCounter,
		v:     s.p.vCounter,
		frame: s.p.FrameCount,
	})
}

func TestHDMAStopsAtVisibleLineBoundary(t *testing.T) {
	tests := []struct {
		name   string
		setini uint8
		v      int
		want   int
	}{
		{"standard last active line", 0x00, 224, 1},
		{"standard first vblank line", 0x00, 225, 0},
		{"overscan extended active line", 0x04, 240, 1},
		{"overscan first vblank line", 0x04, 241, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPPU()
			p.SETINI = tt.setini
			p.vCounter = tt.v
			p.hCounter = 273
			spy := &hdmaSpy{p: p}
			p.DMA = spy

			p.Run()
			if got := len(spy.calls); got != tt.want {
				t.Fatalf("HDMA calls = %d, want %d", got, tt.want)
			}
			if tt.want == 1 {
				call := spy.calls[0]
				if call.h != 274 || call.v != tt.v {
					t.Fatalf("HDMA call at H=%d V=%d, want H=274 V=%d", call.h, call.v, tt.v)
				}
			}
		})
	}
}

func TestHDMAResetRunsAtScanlineZeroStart(t *testing.T) {
	p := NewPPU()
	p.vCounter = 261
	p.hCounter = 340
	p.FrameCount = 41
	p.NMIFlag = true
	p.RangeOver = true
	p.TimeOver = true

	spy := &hdmaSpy{p: p}
	p.DMA = spy

	p.Run()

	if p.hCounter != 0 || p.vCounter != 0 {
		t.Fatalf("counters after frame wrap = H=%d V=%d, want H=0 V=0", p.hCounter, p.vCounter)
	}
	if p.FrameCount != 42 {
		t.Fatalf("frame count = %d, want 42", p.FrameCount)
	}
	if p.NMIFlag || p.RangeOver || p.TimeOver {
		t.Fatalf("frame flags after wrap = NMI:%v range:%v time:%v, want all clear", p.NMIFlag, p.RangeOver, p.TimeOver)
	}
	if len(spy.events) != 1 {
		t.Fatalf("HDMA events = %d, want 1 reset event: %#v", len(spy.events), spy.events)
	}
	ev := spy.events[0]
	if ev.kind != "reset" || ev.h != 0 || ev.v != 0 || ev.frame != 42 {
		t.Fatalf("HDMA reset event = %#v, want reset at H=0 V=0 frame=42", ev)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("ExecuteHDMA calls at frame wrap = %d, want 0", len(spy.calls))
	}
}
