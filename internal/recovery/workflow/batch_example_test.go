package workflow

import (
	"context"
	"fmt"
)

func ExampleBatchTask() {
	t := BatchTask{Config: Input{Path: "/owned/candidate-config.json", SHA256: "exact digest"}}
	fmt.Println(t.Config.Path)
	// Output: /owned/candidate-config.json
}

func ExampleBatchRow() {
	r := BatchRow{CandidateID: "leaf-0ed60b", Status: "unexecuted", Stage: "await_capture"}
	fmt.Println(r.CandidateID, r.Status, r.Stage)
	// Output: leaf-0ed60b unexecuted await_capture
}

func ExampleBatchReport() {
	r := BatchReport{Accepted: 1, Refused: 9}
	fmt.Println(r.Accepted, r.Refused, r.Unexecuted)
	// Output: 1 9 0
}

func ExampleRunBatch() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunBatch(ctx, "/owned/batch", Input{})
	fmt.Println(err)
	// Output: context canceled
}
