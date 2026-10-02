package decomp_test

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery/decomp"
)

func ExampleNewEvidenceVerifierWithPolicy() {
	// Supply reviewed policy and owned ROM bytes explicitly; there is no lookup.
	_, err := decomp.NewEvidenceVerifierWithPolicy("captures", decomp.AdmissionPolicy{}, nil)
	fmt.Println(err)
	// Output: admission policy: ROM must contain 1..4194304 bytes
}

func ExampleEvidenceVerifier_PolicySHA256() {
	v := decomp.NewEvidenceVerifier("captures")
	fmt.Println(len(v.PolicySHA256()))
	// Output: 64
}
