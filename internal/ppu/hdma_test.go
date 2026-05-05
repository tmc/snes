package ppu

import "testing"

type hdmaSpy struct {
	p     *PPU
	calls []hdmaCall
}

type hdmaCall struct {
	h int
	v int
}

func (s *hdmaSpy) ExecuteHDMA() {
	s.calls = append(s.calls, hdmaCall{h: s.p.hCounter, v: s.p.vCounter})
}

func (s *hdmaSpy) ResetHDMA() {}

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
