package provenance_test

import (
	"fmt"
	"github.com/tmc/snes/internal/provenance"
)

func ExampleAnalyze() {
	events := []provenance.Event{
		{ID: 0, Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x800a00, Value: 7, Frame: 82, PC: 0x8600},
		{ID: 1, Kind: "dma", Channel: 0, Count: 1, Target: 4, Addr: 0xa00},
		{ID: 2, Kind: "bus", Actor: "dma_or_hdma", Op: "read", Addr: 0xa00, Value: 7},
		{ID: 3, Kind: "bus", Actor: "dma_or_hdma", Op: "write", Addr: 0x2104, Value: 7},
		{ID: 4, Kind: "ppu", Op: "write", Space: "oam", Addr: 512, Value: 7},
	}
	result, err := provenance.Analyze(events, 82, 0x85fc, 0x8781)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Links[0].Status)
	// Output: observed_last_writer
}
