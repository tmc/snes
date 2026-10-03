package dmacorrelate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"-badflag"}},
		{"missing case", []string{"-trace", "trace.jsonl"}},
		{"missing trace", []string{"-case", "cases.jsonl"}},
		{"invalid format", []string{"-case", "cases.jsonl", "-trace", "trace.jsonl", "-format", "xml"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunExit(tt.args, &stdout, &stderr)
			if code != 2 {
				t.Errorf("got exit code %d, want 2; stderr:\n%s", code, stderr.String())
			}
		})
	}
}

func TestRunCLIBasic(t *testing.T) {
	tmpDir := t.TempDir()
	caseFile := filepath.Join(tmpDir, "cases.jsonl")
	traceFile := filepath.Join(tmpDir, "trace.jsonl")

	caseData := `{
		"case_id": "test_cli_case",
		"frame": 82,
		"entry_pc": 34300,
		"entry_seq": 100,
		"return_seq": 200,
		"instruction_count": 10,
		"initial_state": {"cycles": 1000},
		"observed_exit_state": {"cycles": 2000},
		"observed_writes": [
			{"address": 8260096, "value": 170},
			{"address": 8257536, "value": 42}
		]
	}`
	traceData := strings.Join([]string{
		`{"kind": "cpu_transition", "frame": 83, "cycle": 2500, "transition": {"kind": "nmi", "seq": 250}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 2600, "insn": {"seq": 260, "entry": {"pc": 32768, "a": 4, "p": 32, "cycles": 2600}, "fetches": [{"addr": 32768, "value": 141}, {"addr": 32769, "value": 1}, {"addr": 32770, "value": 67}]}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 2900, "insn": {"seq": 290, "entry": {"pc": 32777, "y": 1, "p": 16, "cycles": 2900}, "fetches": [{"addr": 32777, "value": 140}, {"addr": 32778, "value": 11}, {"addr": 32779, "value": 66}]}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 4500, "insn": {"seq": 300, "entry": {"pc": 32780, "cycles": 4500}, "fetches": [{"addr": 32780, "value": 234}]}}`,
	}, "\n")

	if err := os.WriteFile(caseFile, []byte(caseData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traceFile, []byte(traceData), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunExit([]string{
		"-case", caseFile,
		"-trace", traceFile,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run failed with code %d: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "# Offline PPU/DMA Consumption Correlation") {
		t.Errorf("expected output to contain title header, got: %s", outStr)
	}
	if !strings.Contains(outStr, "Entry PC `$0085FC` (Entry Seq `100`)") {
		t.Errorf("expected output to contain formatted entry PC and seq, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Other WRAM Writes") {
		t.Errorf("expected output to contain Other WRAM Writes section, got:\n%s", outStr)
	}
}

func TestRunCLIJSON(t *testing.T) {
	tmpDir := t.TempDir()
	caseFile := filepath.Join(tmpDir, "cases.jsonl")
	traceFile := filepath.Join(tmpDir, "trace.jsonl")

	caseData := `{
		"case_id": "test_json_case",
		"frame": 82,
		"entry_pc": 34300,
		"entry_seq": 100,
		"return_seq": 200,
		"instruction_count": 10,
		"initial_state": {"cycles": 1000},
		"observed_exit_state": {"cycles": 2000},
		"observed_writes": [
			{"address": 8260096, "value": 170}
		]
	}`
	traceData := strings.Join([]string{
		`{"kind": "cpu_transition", "frame": 83, "cycle": 2500, "transition": {"kind": "nmi", "seq": 250}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 2600, "insn": {"seq": 260, "entry": {"pc": 32768, "a": 4, "p": 32, "cycles": 2600}, "fetches": [{"addr": 32768, "value": 141}, {"addr": 32769, "value": 1}, {"addr": 32770, "value": 67}]}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 2900, "insn": {"seq": 290, "entry": {"pc": 32777, "y": 1, "p": 16, "cycles": 2900}, "fetches": [{"addr": 32777, "value": 140}, {"addr": 32778, "value": 11}, {"addr": 32779, "value": 66}]}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 4500, "insn": {"seq": 300, "entry": {"pc": 32780, "cycles": 4500}, "fetches": [{"addr": 32780, "value": 234}]}}`,
	}, "\n")

	if err := os.WriteFile(caseFile, []byte(caseData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traceFile, []byte(traceData), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunExit([]string{
		"-case", caseFile,
		"-trace", traceFile,
		"-format", "json",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run failed with code %d: %s", code, stderr.String())
	}

	var res CorrelationResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse json output: %v\noutput was:\n%s", err, stdout.String())
	}
	if res.CaseID != "test_json_case" {
		t.Errorf("got CaseID %q, want %q", res.CaseID, "test_json_case")
	}
	if res.EntryPC != 34300 {
		t.Errorf("got EntryPC %d, want 34300", res.EntryPC)
	}
	if len(res.HighTableWrites) != 1 {
		t.Errorf("got HighTableWrites len %d, want 1", len(res.HighTableWrites))
	}
}

func TestRunCLICaseSelection(t *testing.T) {
	tmpDir := t.TempDir()
	caseFile := filepath.Join(tmpDir, "cases.jsonl")
	traceFile := filepath.Join(tmpDir, "trace.jsonl")

	caseData := strings.Join([]string{
		`{"case_id": "case_alpha", "entry_seq": 100, "frame": 10, "initial_state": {"cycles": 100}, "observed_exit_state": {"cycles": 200}}`,
		`{"case_id": "case_beta", "entry_seq": 500, "frame": 20, "initial_state": {"cycles": 500}, "observed_exit_state": {"cycles": 600}}`,
	}, "\n")
	traceData := `{"kind": "cpu_transition", "frame": 21, "cycle": 700, "transition": {"kind": "nmi", "seq": 550}}`

	if err := os.WriteFile(caseFile, []byte(caseData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traceFile, []byte(traceData), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Without -id: should select first record ("case_alpha")
	{
		var stdout, stderr bytes.Buffer
		code := RunExit([]string{"-case", caseFile, "-trace", traceFile, "-format", "json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("default case run failed: %s", stderr.String())
		}
		var res CorrelationResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.CaseID != "case_alpha" {
			t.Errorf("got CaseID %q, want case_alpha", res.CaseID)
		}
	}

	// 2. With -id case_beta: should select "case_beta"
	{
		var stdout, stderr bytes.Buffer
		code := RunExit([]string{"-case", caseFile, "-trace", traceFile, "-id", "case_beta", "-format", "json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("id case_beta run failed: %s", stderr.String())
		}
		var res CorrelationResult
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if res.CaseID != "case_beta" {
			t.Errorf("got CaseID %q, want case_beta", res.CaseID)
		}
	}
}
