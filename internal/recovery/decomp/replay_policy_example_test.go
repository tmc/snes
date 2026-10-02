package decomp_test

import (
	"context"
	"fmt"

	"github.com/tmc/snes/internal/recovery/decomp"
)

func ExampleEvidenceVerifier_ExecuteThreeWayReplay() {
	v := decomp.NewEvidenceVerifier("captures")
	receipt := v.ExecuteThreeWayReplay(context.Background(), nil, decomp.ReplayCase{}, nil, decomp.VerifyConfig{})
	fmt.Println(receipt.Discrepancy)
	// Output: nil block IR
}

func ExampleEvidenceVerifier_ValidateReplayReceiptFreshness() {
	v := decomp.NewEvidenceVerifier("captures")
	receipt := decomp.ReplayReceipt{}
	v.ValidateReplayReceiptFreshness(&receipt, nil, nil, "", "", "")
	fmt.Println(receipt.Metadata.IsStale)
	// Output: true
}
