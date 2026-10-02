package decomp_test

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery/decomp"
)

func ExampleGenerateSemanticRegionC() {
	_, err := decomp.GenerateSemanticRegionC(nil)
	fmt.Println(err)
	// Output: generate region C: nil RegionIR
}

func ExampleValidateSemanticSource() {
	err := decomp.ValidateSemanticSource(nil, decomp.SemanticSource{})
	fmt.Println(err)
	// Output: generate region C: nil RegionIR
}
