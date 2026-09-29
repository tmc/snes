package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery/verify"
)

func TestVerify(t *testing.T) {
	t.Run("ExactMatch", func(t *testing.T) {
		tempDir := t.TempDir()
		romPath := filepath.Join(tempDir, "rebuilt.sfc")

		data := []byte{0x01, 0x02, 0x03, 0x04}
		if err := os.WriteFile(romPath, data, 0644); err != nil {
			t.Fatal(err)
		}

		receipt, err := verify.Verify(context.Background(), romPath, data, verify.Config{})
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}
		if receipt.Outcome != verify.OutcomeMatched {
			t.Errorf("got outcome %v, want %v", receipt.Outcome, verify.OutcomeMatched)
		}
		if receipt.MismatchCount != 0 {
			t.Errorf("got %d mismatches, want 0", receipt.MismatchCount)
		}
	})

	t.Run("MismatchReport", func(t *testing.T) {
		tempDir := t.TempDir()
		romPath := filepath.Join(tempDir, "rebuilt.sfc")

		expected := []byte{0x01, 0x02, 0x03, 0x04}
		actual := []byte{0x01, 0x99, 0x03, 0x04}
		if err := os.WriteFile(romPath, actual, 0644); err != nil {
			t.Fatal(err)
		}

		receipt, err := verify.Verify(context.Background(), romPath, expected, verify.Config{})
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}
		if receipt.Outcome != verify.OutcomeMismatch {
			t.Errorf("got outcome %v, want %v", receipt.Outcome, verify.OutcomeMismatch)
		}
		if receipt.MismatchCount != 1 {
			t.Errorf("got %d mismatches, want 1", receipt.MismatchCount)
		}
		if len(receipt.Mismatches) != 1 {
			t.Fatalf("expected 1 reported mismatch, got %d", len(receipt.Mismatches))
		}
		m := receipt.Mismatches[0]
		if m.Offset != 1 || m.Expected != 0x02 || m.Actual != 0x99 {
			t.Errorf("mismatch details incorrect: %+v", m)
		}
		if m.SNESAddress != 0x008001 {
			t.Errorf("snes address 0x%06x, want 0x008001", m.SNESAddress)
		}
	})

	t.Run("RebuiltFileMissing", func(t *testing.T) {
		receipt, err := verify.Verify(context.Background(), "/nonexistent/path/rebuilt.sfc", []byte{1, 2}, verify.Config{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Outcome != verify.OutcomeBuildFailed {
			t.Errorf("got outcome %v, want %v", receipt.Outcome, verify.OutcomeBuildFailed)
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := verify.Verify(ctx, "any", []byte{1}, verify.Config{})
		if err == nil {
			t.Error("expected error on cancelled context, got nil")
		}
	})

	t.Run("ToolchainTimeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		cfg := verify.Config{
			ToolchainCmd: []string{"sleep", "1"},
		}
		receipt, err := verify.Verify(ctx, "any", []byte{1}, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Outcome != verify.OutcomeTimedOut && receipt.Outcome != verify.OutcomeBuildFailed {
			t.Errorf("got outcome %v, want %v or %v", receipt.Outcome, verify.OutcomeTimedOut, verify.OutcomeBuildFailed)
		}
	})
}
