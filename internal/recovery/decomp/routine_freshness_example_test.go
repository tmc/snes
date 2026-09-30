package decomp

import "fmt"

func ExampleCompiledRoutineRunner_BindRegion() {
	var runner *CompiledRoutineRunner
	err := runner.BindRegion(nil, "current-project")
	fmt.Println(err)
	// Output: missing runner or region
}

func ExampleValidateRoutineReplayReceiptFreshness() {
	receipt := ReplayReceipt{Eligible: true, CapturedProofEligible: true}
	ValidateRoutineReplayReceiptFreshness(&receipt, nil, nil, "", "", "", nil)
	fmt.Println(receipt.Eligible, receipt.CapturedProofEligible, receipt.Metadata.IsStale)
	// Output: false false true
}
