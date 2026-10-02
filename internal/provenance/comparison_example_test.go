package provenance_test

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/provenance"
)

func ExampleCompare() {
	h := strings.Repeat("a", 64)
	w := provenance.Window{Schema: "snes-observation-window-v1", Identity: provenance.Identity{ROMSHA256: h, StateSHA256: h, InputsSHA256: h, RunSHA256: h, Mode: "original_interpreter"}, From: 0, To: 1, Complete: true, Coverage: provenance.WriterCoverage, Frames: []provenance.FrameIdentity{{Frame: 0, PPUFrame: 1, StartCycle: 1, VBlankCycle: 2, EndCycle: 3, StateSHA256: h, BusSHA256: h, PixelSHA256: h}}}
	pin, _ := provenance.WindowSHA256(w)
	report, err := provenance.Compare(w, w, pin, pin, provenance.Selection{Frame: 0, RoutineStart: 0x8000, RoutineEnd: 0x8010, Sprite: 0})
	fmt.Println(err, report.CapturedProofEligible)
	// Output: <nil> false
}

func ExampleWindowSHA256() {
	pin, err := provenance.WindowSHA256(provenance.Window{})
	fmt.Println(len(pin), err)
	// Output: 64 <nil>
}
