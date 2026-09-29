package watches_test

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery/watches"
)

func Example() {
	wf := &watches.File{
		Format:        watches.FileFormatWatches,
		SchemaVersion: 1,
		Watches: []watches.WatchDefinition{
			{
				ID:          "coins",
				Name:        "Collected Coins",
				MemorySpace: "wram",
				Offset:      0x0420,
				Width:       1,
			},
		},
	}

	snap := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-1",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        0x1000,
		Sequence:      1,
		Frame:         60,
		Data:          make([]byte, 0x1000),
	}
	snap.Data[0x0420] = 99

	evals, err := watches.EvaluateAll(wf, snap)
	if err != nil {
		panic(err)
	}

	coinEval := evals["coins"]
	fmt.Printf("%s: %s (validity: %s)\n", coinEval.Name, coinEval.DecodedString, coinEval.Validity)
	// Output:
	// Collected Coins: 99 (validity: valid)
}
