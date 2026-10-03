package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenuine139220Command(t *testing.T) {
	outDir := t.TempDir()

	args := []string{
		"genuine139220",
		"-out", outDir,
	}

	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run genuine139220 failed: %v (stderr: %s)", err, stderr.String())
	}

	var summary map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("failed to parse stdout json: %v (stdout: %s)", err, stdout.String())
	}

	if summary["status"] != "success" {
		t.Errorf("expected status=success, got %v", summary["status"])
	}
	if summary["dual_backend_verified"] != true {
		t.Errorf("expected dual_backend_verified=true, got %v", summary["dual_backend_verified"])
	}

	// Verify required artifacts exist on disk
	requiredFiles := []string{"case.json", "manifest.json", "generated.c", "receipt.json"}
	for _, f := range requiredFiles {
		p := filepath.Join(outDir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing expected artifact %s: %v", f, err)
		}
	}

	// Verify receipt contents
	receiptBytes, err := os.ReadFile(filepath.Join(outDir, "receipt.json"))
	if err != nil {
		t.Fatalf("read receipt.json: %v", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatalf("parse receipt.json: %v", err)
	}

	if receipt["verified"] != true {
		t.Errorf("receipt.verified = %v, want true", receipt["verified"])
	}
	if receipt["observed_effects_capture"] != false {
		t.Errorf("receipt.observed_effects_capture = %v, want false", receipt["observed_effects_capture"])
	}
	if receipt["captured_proof_eligible"] != false {
		t.Errorf("receipt.captured_proof_eligible = %v, want false", receipt["captured_proof_eligible"])
	}
	if receipt["admission_digest"] != "" {
		t.Errorf("receipt.admission_digest = %v, want empty", receipt["admission_digest"])
	}

	compareWrites, ok := receipt["compare_writes"].(map[string]any)
	if !ok {
		t.Fatalf("missing compare_writes in receipt")
	}
	for backend, v := range compareWrites {
		bMap, ok := v.(map[string]any)
		if !ok || bMap["match"] != true {
			t.Errorf("compare_writes[%s].match != true: %+v", backend, v)
		}
	}
}
