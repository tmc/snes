package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis/planner"
)

func TestPlanCommand(t *testing.T) {
	dir := t.TempDir()
	rom := makeSyntheticLoROM(64)
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80
	rom[0x0000] = 0xD0 // BNE +4 -> 0x8006
	rom[0x0001] = 0x04
	rom[0x0002] = 0x7C // JMP ($9000,X)
	rom[0x0003] = 0x00
	rom[0x0004] = 0x90
	romPath := filepath.Join(dir, "rom.sfc")
	if err := os.WriteFile(romPath, rom, 0600); err != nil {
		t.Fatal(err)
	}

	project := filepath.Join(dir, "project")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-rom", romPath, "-out", project}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	// 1. Test `snesdasm plan -project ... -format json`
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"plan", "-project", project, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm plan failed: %v", err)
	}

	var plan planner.ExperimentPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("failed to unmarshal JSON plan: %v\noutput: %s", err, stdout.String())
	}
	if plan.TotalFrontiers == 0 {
		t.Errorf("expected at least 1 frontier in plan, got 0")
	}

	// 2. Test `snesdasm plan -project ... -format text`
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"plan", "-project", project, "-format", "text"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm plan failed: %v", err)
	}
	textOut := stdout.String()
	if !strings.Contains(textOut, "Recovery experiment plan") {
		t.Errorf("expected plan header in text output, got: %s", textOut)
	}
	if !strings.Contains(textOut, "action:") {
		t.Errorf("expected action: in text output, got: %s", textOut)
	}

	// 3. Test `snesdasm plan -project ... -limit 1`
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"plan", "-project", project, "-limit", "1", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm plan with -limit 1 failed: %v", err)
	}
	var boundedPlan planner.ExperimentPlan
	if err := json.Unmarshal(stdout.Bytes(), &boundedPlan); err != nil {
		t.Fatalf("failed to unmarshal JSON plan: %v", err)
	}
	if len(boundedPlan.Experiments) > 1 {
		t.Errorf("expected at most 1 experiment with -limit 1, got %d", len(boundedPlan.Experiments))
	}

	// 4. Test `snesdasm queue -plan -project ...`
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"queue", "-plan", "-project", project, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm queue -plan failed: %v", err)
	}
	var queuePlan planner.ExperimentPlan
	if err := json.Unmarshal(stdout.Bytes(), &queuePlan); err != nil {
		t.Fatalf("failed to unmarshal JSON plan from queue -plan: %v", err)
	}
	if queuePlan.TotalFrontiers != plan.TotalFrontiers {
		t.Errorf("queue -plan total frontiers %d != plan total frontiers %d", queuePlan.TotalFrontiers, plan.TotalFrontiers)
	}

	// 5. Test error cases
	errorCases := [][]string{
		{},                                  // missing -project
		{"-format", "xml"},                  // missing -project
		{"-project", project, "-format", "yaml"}, // invalid format
		{"-project", project, "-limit", "-5"},    // invalid limit
	}
	for _, args := range errorCases {
		stdout.Reset()
		stderr.Reset()
		if err := runPlan(args, &stdout, &stderr); err == nil {
			t.Errorf("expected error for args %v, got nil", args)
		}
	}
}

func TestPlanCommand_SyntheticFrontiers(t *testing.T) {
	dir := t.TempDir()
	doc := recovery.NewDocument(recovery.ROMIdentity{})
	doc.Instructions = []recovery.Instruction{
		{
			ID:       "inst-8000",
			Address:  0x008000,
			Offset:   0x000000,
			Bytes:    "7C0090",
			Opcode:   0x7C,
			Mnemonic: "JMP ($9000,X)",
			Context:  recovery.Context{E: "set", M: "set", X: "set"},
		},
	}
	doc.Issues = []recovery.Issue{
		{
			ID:       "iss-1",
			Address:  0x7E0040,
			Offset:   0x000040,
			Reason:   "read from RAM without prior observed write",
			Blocking: false,
		},
	}

	f, err := os.Create(filepath.Join(dir, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.Encode(f, doc); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	var stdout, stderr bytes.Buffer
	if err := runPlan([]string{"-project", dir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runPlan failed: %v", err)
	}

	var plan planner.ExperimentPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("failed to unmarshal JSON plan: %v", err)
	}
	if plan.TotalFrontiers != 2 {
		t.Errorf("expected 2 total frontiers, got %d", plan.TotalFrontiers)
	}
}
