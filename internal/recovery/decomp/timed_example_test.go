package decomp_test

import (
	"fmt"
	"github.com/tmc/snes/internal/recovery/decomp"
)

func ExampleGenerateTimedRegionC() {
	_, err := decomp.GenerateTimedRegionC(nil, nil, decomp.TimedPlan{}, nil)
	fmt.Println(err)
	// Output: timed C: unsupported entry context
}
