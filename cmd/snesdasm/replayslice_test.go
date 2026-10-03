package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaySliceCommand(t *testing.T) {
	outDir := t.TempDir()

	args := []string{
		"replay-slice",
		"-events", "139205:139220",
		"-out", outDir,
	}

	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run replay-slice failed: %v (stderr: %s)", err, stderr.String())
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
	if summary["timeline_steps_verified"] != 4.0 && summary["timeline_steps_verified"] != 4 {
		t.Errorf("expected timeline_steps_verified=4, got %v", summary["timeline_steps_verified"])
	}

	preds, ok := summary["predictions"].(map[string]any)
	if !ok {
		t.Fatalf("missing predictions in summary: %+v", summary)
	}
	if preds["input_114"] != 119.0 && preds["input_114"] != 119 {
		t.Errorf("expected input_114=119, got %v", preds["input_114"])
	}
	if preds["input_116"] != 121.0 && preds["input_116"] != 121 {
		t.Errorf("expected input_116=121, got %v", preds["input_116"])
	}
	if preds["named_noop"] != 120.0 && preds["named_noop"] != 120 {
		t.Errorf("expected named_noop=120, got %v", preds["named_noop"])
	}
	if preds["reset_baseline"] != 120.0 && preds["reset_baseline"] != 120 {
		t.Errorf("expected reset_baseline=120, got %v", preds["reset_baseline"])
	}

	// Verify required artifacts exist on disk
	requiredFiles := []string{"case.json", "manifest.json", "generated.c", "timeline.json", "receipt.json"}
	for _, f := range requiredFiles {
		p := filepath.Join(outDir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing expected artifact %s: %v", f, err)
		}
	}

	// Verify timeline.json contents
	timelineBytes, err := os.ReadFile(filepath.Join(outDir, "timeline.json"))
	if err != nil {
		t.Fatalf("read timeline.json: %v", err)
	}
	var timeline map[string]any
	if err := json.Unmarshal(timelineBytes, &timeline); err != nil {
		t.Fatalf("parse timeline.json: %v", err)
	}
	if timeline["schema"] != "snes-instruction-timeline-v1" {
		t.Errorf("timeline schema = %v, want snes-instruction-timeline-v1", timeline["schema"])
	}
	if timeline["all_matched"] != true {
		t.Errorf("timeline all_matched = %v, want true", timeline["all_matched"])
	}
	if timeline["dual_backend_block_agreement"] != true {
		t.Errorf("timeline dual_backend_block_agreement = %v, want true", timeline["dual_backend_block_agreement"])
	}
	steps, ok := timeline["steps"].([]any)
	if !ok || len(steps) != 4 {
		t.Fatalf("expected 4 timeline steps, got %v", len(steps))
	}
	for i, s := range steps {
		sMap, ok := s.(map[string]any)
		if !ok || sMap["step_match"] != true {
			t.Errorf("timeline step[%d] failed match: %+v", i, s)
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
	if receipt["timeline_verified"] != true {
		t.Errorf("receipt.timeline_verified = %v, want true", receipt["timeline_verified"])
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

	refusalMeta, ok := receipt["refusal_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("missing refusal_metadata in receipt")
	}
	if refusalMeta["total_data_reads"] != 1.0 && refusalMeta["total_data_reads"] != 1 {
		t.Errorf("expected total_data_reads=1, got %v", refusalMeta["total_data_reads"])
	}
	if refusalMeta["total_data_writes"] != 1.0 && refusalMeta["total_data_writes"] != 1 {
		t.Errorf("expected total_data_writes=1, got %v", refusalMeta["total_data_writes"])
	}

	counterfactuals, ok := receipt["counterfactual_predictions"].([]any)
	if !ok || len(counterfactuals) != 5 {
		t.Fatalf("expected 5 counterfactual predictions (including baseline, no-op, reset), got %d", len(counterfactuals))
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

	// Verify manifest contents
	manifestBytes, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	if manifest["evidence_mode"] != "authentic_trace_stream" {
		t.Errorf("manifest evidence_mode = %v, want authentic_trace_stream", manifest["evidence_mode"])
	}
	artifacts, ok := manifest["artifacts"].([]any)
	if !ok || len(artifacts) != 4 {
		t.Errorf("expected 4 manifest artifacts, got %d", len(artifacts))
	}
}

func TestReplaySliceAlias(t *testing.T) {
	outDir := t.TempDir()

	args := []string{
		"genuine139220",
		"-out", outDir,
	}

	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run genuine139220 alias failed: %v (stderr: %s)", err, stderr.String())
	}

	var summary map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("failed to parse stdout json: %v", err)
	}

	if summary["status"] != "success" || summary["dual_backend_verified"] != true {
		t.Errorf("expected success with dual_backend_verified=true, got %+v", summary)
	}
}
