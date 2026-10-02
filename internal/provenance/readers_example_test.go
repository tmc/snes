package provenance_test

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/provenance"
)

func ExampleReadFrontier() {
	h := strings.Repeat("a", 64)
	w := provenance.Window{
		Schema: "snes-observation-window-v1", Complete: true, Coverage: provenance.WriterCoverage, To: 1,
		Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"},
		Frames:   []provenance.FrameIdentity{{PPUFrame: 1, VBlankCycle: 2, EndCycle: 3, StateSHA256: h, BusSHA256: h, PixelSHA256: h}},
		Events: []provenance.Event{
			{ID: 0, Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x1f05, Value: 7, PPUFrame: 1},
			{ID: 1, Kind: "bus", Actor: "cpu", Op: "read", Addr: 0x7e1f05, Value: 7, PPUFrame: 1, Cycle: 1},
		},
	}
	pin, _ := provenance.WindowSHA256(w)
	f, err := provenance.ReadFrontier(w, pin, 0)
	fmt.Printf("%06X %d %s %v\n", f.PhysicalAddress, len(f.Readers), f.Termination, err)
	// Output: 7E1F05 1 window_end <nil>
}
