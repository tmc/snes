package workflow_test

import (
	"context"
	"fmt"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func Example() {
	in := workflow.Input{Path: "config.json", SHA256: "operator-pinned-file-digest"}
	fmt.Println(in.Path) // Output: config.json
}
func ExampleRun() {
	_, err := workflow.Run(context.Background(), workflow.Options{})
	fmt.Println(err) // Output: missing task directory or transition budget outside bounds
}
func ExampleConfig() {
	c := workflow.Config{MaxFrames: 920, MaxCases: 1, MaxSteps: 100, QueueLimit: 100}
	fmt.Println(c.MaxFrames, c.MaxCases) // Output: 920 1
}
func ExampleStream() {
	s := workflow.Stream{Trace: workflow.Input{Path: "/capture.jsonl"}}
	fmt.Println(s.Trace.Path) // Output: /capture.jsonl
}
func ExampleEvidence() {
	e := workflow.Evidence{}
	fmt.Println(e.Capture.Trace.Path == "") // Output: true
}
func ExampleOptions() {
	o := workflow.Options{Dir: "/task", MaxTransitions: 1}
	fmt.Println(o.MaxTransitions) // Output: 1
}
func ExampleState() {
	s := workflow.State{Phase: "await_policy"}
	fmt.Println(s.Phase) // Output: await_policy
}
func ExampleInput() {
	in := workflow.Input{Path: "/policy.json"}
	fmt.Println(in.Path) // Output: /policy.json
}
