package verify_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery/verify"
)

func ExampleVerify() {
	tmpDir, err := os.MkdirTemp("", "verify-example-*")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	orig := []byte{0x4C, 0x00, 0x80}
	rebuiltPath := filepath.Join(tmpDir, "rebuilt.sfc")
	if err := os.WriteFile(rebuiltPath, orig, 0644); err != nil {
		fmt.Println("Error:", err)
		return
	}

	receipt, err := verify.Verify(context.Background(), rebuiltPath, orig, verify.Config{})
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Outcome:", receipt.Outcome)
	fmt.Println("Mismatch count:", receipt.MismatchCount)

	// Output:
	// Outcome: matched
	// Mismatch count: 0
}
