package cworkbench_test

import (
	"fmt"
	"github.com/tmc/snes/internal/editor/cworkbench"
)

func ExampleOpen() {
	_, err := cworkbench.Open(cworkbench.Config{})
	fmt.Println(err)
	// Output: notes path must be absolute
}
func ExampleNote() {
	n := cworkbench.Note{Address: 0x0cc46c, Name: "rotation_delta", Type: "uint8_t", Hypothesis: "proposed meaning"}
	fmt.Printf("$%06X: %s (%s)\n", n.Address, n.Name, n.Type)
	// Output: $0CC46C: rotation_delta (uint8_t)
}
