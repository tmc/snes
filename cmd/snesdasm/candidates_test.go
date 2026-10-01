package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/candidates"
)

func TestCandidatesCommand(t *testing.T) {
	dir := t.TempDir()
	// Use the recovery path to produce a valid document rather than weakening decode.
	rom := makeSyntheticLoROM(64)
	rp := filepath.Join(dir, "rom.sfc")
	if err := os.WriteFile(rp, rom, 0600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-rom", rp, "-out", project}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(project, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := run([]string{"candidates", "-project", project, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report candidates.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != "snes-candidate-proposals-v1" {
		t.Fatal("missing proposal schema")
	}
	after, err := os.ReadFile(filepath.Join(project, "recovery.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("candidate command mutated recovery")
	}
	for _, args := range [][]string{{"-project", project, "-format", "wrong"}, {"-project", project, "-limit", "-1"}, {"-project", project, "-max-depth", "0"}, {}} {
		if err := runCandidates(args, &stdout, &stderr); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
	stdout.Reset()
	if err := runCandidates([]string{"-project", project}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "unqualified") {
		t.Fatal("human output omits qualification")
	}
}
