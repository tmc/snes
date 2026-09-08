package dma

import "fmt"

func ExampleDMA_RunSlice() {
	d := NewDMA(nil, nil)
	var clock uint64
	d.SetClock(func() uint64 { return clock }, func(n uint64) { clock += n })
	d.Request(0) // No enabled channels acquire the bus.
	d.BeginEdge(8)
	fmt.Println(d.Busy(), d.RunSlice(8), clock)
	// Output: false 0 0
}
