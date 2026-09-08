package trace

import (
	"math"
	"testing"
)

func TestCPUSpaceBankBoundaries(t *testing.T) {
	for _, tt := range []struct {
		addr   uint32
		space  string
		offset uint32
	}{
		{0x002140, "apu", 0x2140}, {0xbf2140, "apu", 0x2140}, {0x402140, "cpu", 0x402140},
		{0xc02100, "cpu", 0xc02100}, {0x3f2100, "ppu", 0x2100}, {0x804300, "dma", 0x4300},
		{0x404300, "cpu", 0x404300}, {0x7e2140, "wram", 0x2140}, {0x7f2140, "wram", 0x12140},
	} {
		if space, offset := CPUSpace(tt.addr); space != tt.space || offset != tt.offset {
			t.Errorf("%06x = %s:%x, want %s:%x", tt.addr, space, offset, tt.space, tt.offset)
		}
	}
}

func TestTraceWindowBounds(t *testing.T) {
	q := Query{Events: []Event{{ID: 1}, {ID: 2}, {ID: 3}}}
	for _, tt := range []struct {
		name                string
		id                  uint64
		before, after, want int
	}{
		{"negative before", 2, -1, 0, 0}, {"negative after", 2, 0, -1, 0}, {"missing", 4, 1, 1, 0},
		{"center", 2, 0, 0, 1}, {"clamp", 2, math.MaxInt, math.MaxInt, 3}, {"start", 1, 2, 1, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := q.TraceWindow(tt.id, tt.before, tt.after)
			if len(got) != tt.want {
				t.Fatalf("len = %d, want %d", len(got), tt.want)
			}
			if tt.want == 1 && got[0].ID != 2 {
				t.Fatal(got)
			}
		})
	}
}
