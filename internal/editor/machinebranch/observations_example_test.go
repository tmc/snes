package machinebranch_test

import (
	"fmt"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

func ExampleCheckObservations() {
	// Missing observation windows refuse rather than providing an explanation.
	err := machinebranch.CheckObservations(nil, nil)
	fmt.Println(err != nil)
	// Output: true
}
