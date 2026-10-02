package exploration_test

import (
	"context"
	"fmt"
	"github.com/tmc/snes/internal/exploration"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func Example() {
	fmt.Println("pinned coverage -> capture gaps -> bounded branches") // Output: pinned coverage -> capture gaps -> bounded branches
}
func ExampleConfig() {
	c := exploration.Config{BaselineFrames: 240}
	fmt.Println(c.BaselineFrames) // Output: 240
}
func ExampleReport() {
	r := exploration.Report{Stage: "discovery"}
	fmt.Println(r.Stage) // Output: discovery
}
func ExampleRun() {
	_, err := exploration.Run(context.Background(), "relative", workflow.Input{}, false)
	fmt.Println(err) // Output: output must be absolute
}
