package statewrites_test

import (
	"fmt"
	"github.com/tmc/snes/internal/editor/statewrites"
	"github.com/tmc/snes/internal/provenance"
	"strings"
)

func ExampleBuild() {
	h := strings.Repeat("a", 64)
	w := provenance.Window{Schema: "snes-observation-window-v1", Complete: true, Coverage: provenance.WriterCoverage, To: 1,
		Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"},
		Frames:   []provenance.FrameIdentity{{Frame: 0, PPUFrame: 1, StartCycle: 0, VBlankCycle: 2, EndCycle: 3, StateSHA256: h, BusSHA256: h, PixelSHA256: h}},
		Events:   []provenance.Event{{ID: 0, Frame: 0, PPUFrame: 1, Cycle: 1, Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x81, Value: 3, PC: 0x008000}}}
	pin, _ := provenance.WindowSHA256(w)
	timeline, err := statewrites.Build(w, pin)
	if err != nil {
		fmt.Println(err)
		return
	}
	writes, _ := timeline.Select(0x7e0081, 0, 1)
	fmt.Printf("%06X=%d initial unknown=%v\n", writes[0].Address, writes[0].After, writes[0].Before == nil)
	// Output: 7E0081=3 initial unknown=true
}

func ExampleTimeline_Select() {
	timeline := statewrites.Timeline{From: 0, To: 1, Writes: []statewrites.Write{{Frame: 0, Address: 0x7e0081, After: 3}}}
	writes, err := timeline.Select(0x800081, 0, 1)
	fmt.Println(len(writes), err)
	// Output: 1 <nil>
}
