package scheduler

import (
	"reflect"
	"testing"
)

type dmaCPU struct {
	fakeThread
	s *Scheduler
}

func (c *dmaCPU) Run()                { c.cycles += 6; c.s.AddDMACycles(8); c.cycles += 6 }
func (c *dmaCPU) AdvanceDMA(n uint64) { c.cycles += n }

func TestDMAWaitDeliveredOnceBeforeRetirement(t *testing.T) {
	s := NewScheduler()
	c := &dmaCPU{s: s}
	c.frequency = 1
	s.RegisterCPU(c, 1)
	var got [][2]uint64
	s.SetAfterCPU(func(n uint64) { got = append(got, [2]uint64{c.cycles, n}) })
	s.runCPU()
	want := [][2]uint64{{14, 8}, {20, 12}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cartridge clock deliveries=%v want %v", got, want)
	}
}
