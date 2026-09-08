package cpu

import (
	"github.com/tmc/snes/internal/bus"
	"reflect"
	"testing"
)

func TestBusEdgeCoversReadsWritesAndIdle(t *testing.T) {
	for _, tt := range []struct {
		name  string
		code  []byte
		edges []uint64
	}{
		{"read", []byte{0xad, 0x34, 0x12}, []uint64{8, 8, 8, 8}},
		{"write", []byte{0x8d, 0x34, 0x12}, []uint64{8, 8, 8, 8}},
		{"idle", []byte{0xea}, []uint64{8, 6}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := bus.NewBus()
			m := &MockMemory{}
			b.Map(0, 0xffff, m)
			copy(m.Data[0x8000:], tt.code)
			c := NewCPU(b)
			c.PC = 0x8000
			var edges []uint64
			c.BusEdge = func(n uint64) {
				edges = append(edges, n)
				if len(edges) == 2 {
					c.AdvanceDMA(10)
				}
			}
			c.Step()
			if !reflect.DeepEqual(edges, tt.edges) {
				t.Fatalf("edges=%v want %v", edges, tt.edges)
			}
			want := uint64(10)
			for _, n := range tt.edges {
				want += n
			}
			if c.Cycles != want {
				t.Fatalf("cycles=%d want %d", c.Cycles, want)
			}
		})
	}
}
