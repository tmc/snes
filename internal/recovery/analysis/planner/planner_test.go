package planner_test

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
	"github.com/tmc/snes/internal/recovery/analysis/planner"
)

func TestClassify_Categories(t *testing.T) {
	tests := []struct {
		name         string
		doc          *recovery.Document
		wantKind     planner.FrontierKind
		wantAddr     uint32
		wantActionSub string
	}{
		{
			name: "FrontierIndirectTarget_JMP_Indexed_Indirect",
			doc: &recovery.Document{
				Instructions: []recovery.Instruction{
					{
						ID:       "inst-8010",
						Address:  0x008010,
						Offset:   0x000010,
						Bytes:    "7C0090",
						Opcode:   0x7C, // JMP ($9000,X)
						Mnemonic: "JMP ($9000,X)",
						Context:  recovery.Context{E: "set", M: "set", X: "set"},
					},
				},
			},
			wantKind:      planner.FrontierIndirectTarget,
			wantAddr:      0x008010,
			wantActionSub: "analyze dispatch table",
		},
		{
			name: "FrontierIndirectTarget_Issue",
			doc: &recovery.Document{
				Issues: []recovery.Issue{
					{
						ID:      "iss-8020",
						Address: 0x008020,
						Offset:  0x000020,
						Reason:  "indirect jump destination unresolved",
					},
				},
			},
			wantKind:      planner.FrontierIndirectTarget,
			wantAddr:      0x008020,
			wantActionSub: "analyze dispatch table",
		},
		{
			name: "FrontierUnknownContext_Instruction",
			doc: &recovery.Document{
				Instructions: []recovery.Instruction{
					{
						ID:       "inst-8000",
						Address:  0x008000,
						Offset:   0x000000,
						Bytes:    "A900",
						Opcode:   0xA9,
						Mnemonic: "LDA #$00",
						Context:  recovery.Context{E: "set", M: "unknown", X: "unknown"},
					},
				},
			},
			wantKind:      planner.FrontierUnknownContext,
			wantAddr:      0x008000,
			wantActionSub: "resolve register context",
		},
		{
			name: "FrontierUnknownContext_Issue",
			doc: &recovery.Document{
				Issues: []recovery.Issue{
					{
						ID:      "iss-8000",
						Address: 0x008000,
						Offset:  0x000000,
						Reason:  "unknown M/X register width context at entry point",
					},
				},
			},
			wantKind:      planner.FrontierUnknownContext,
			wantAddr:      0x008000,
			wantActionSub: "resolve register context",
		},
		{
			name: "FrontierUnobservedBranch_MissingTaken",
			doc: &recovery.Document{
				Instructions: []recovery.Instruction{
					{
						ID:       "inst-8030",
						Address:  0x008030,
						Offset:   0x000030,
						Bytes:    "D006", // BNE +6 -> 0x8038
						Opcode:   0xD0,
						Mnemonic: "BNE $8038",
						Context:  recovery.Context{E: "set", M: "set", X: "set"},
					},
				},
				Edges: []recovery.Edge{
					{
						ID:          "edge-1",
						Kind:        "fallthrough",
						Source:      "inst-8030",
						Destination: 0x008032,
					},
				},
			},
			wantKind:      planner.FrontierUnobservedBranch,
			wantAddr:      0x008038,
			wantActionSub: "synthesize cold branch input",
		},
		{
			name: "FrontierUnobservedBranch_MissingFallthrough",
			doc: &recovery.Document{
				Instructions: []recovery.Instruction{
					{
						ID:       "inst-8040",
						Address:  0x008040,
						Offset:   0x000040,
						Bytes:    "F004", // BEQ +4 -> 0x8046
						Opcode:   0xF0,
						Mnemonic: "BEQ $8046",
						Context:  recovery.Context{E: "set", M: "set", X: "set"},
					},
				},
				Edges: []recovery.Edge{
					{
						ID:          "edge-2",
						Kind:        "branch",
						Source:      "inst-8040",
						Destination: 0x008046,
					},
				},
			},
			wantKind:      planner.FrontierUnobservedBranch,
			wantAddr:      0x008042,
			wantActionSub: "synthesize cold branch input",
		},
		{
			name: "FrontierCallBoundary_UnanalyzedCallee",
			doc: &recovery.Document{
				Instructions: []recovery.Instruction{
					{
						ID:       "inst-8050",
						Address:  0x008050,
						Offset:   0x000050,
						Bytes:    "200090", // JSR $9000
						Opcode:   0x20,
						Mnemonic: "JSR $9000",
						Context:  recovery.Context{E: "set", M: "set", X: "set"},
					},
				},
			},
			wantKind:      planner.FrontierCallBoundary,
			wantAddr:      0x008050,
			wantActionSub: "analyze callee subroutine",
		},
		{
			name: "FrontierCallBoundary_Issue",
			doc: &recovery.Document{
				Issues: []recovery.Issue{
					{
						ID:      "iss-8060",
						Address: 0x008060,
						Offset:  0x000060,
						Reason:  "callee halts without return halting caller continuation",
					},
				},
			},
			wantKind:      planner.FrontierCallBoundary,
			wantAddr:      0x008060,
			wantActionSub: "analyze callee subroutine",
		},
		{
			name: "FrontierUninitializedMemory_Issue",
			doc: &recovery.Document{
				Issues: []recovery.Issue{
					{
						ID:      "iss-ram",
						Address: 0x7E0100,
						Offset:  0x000100,
						Reason:  "read from RAM without prior observed write at $7E0100",
					},
				},
			},
			wantKind:      planner.FrontierUninitializedMemory,
			wantAddr:      0x7E0100,
			wantActionSub: "locate initializing routine",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frontiers := planner.Classify(tc.doc)
			if len(frontiers) == 0 {
				t.Fatalf("expected at least 1 frontier, got 0")
			}
			found := false
			for _, f := range frontiers {
				if f.Kind == tc.wantKind && f.Address == tc.wantAddr {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("did not find expected frontier kind %s at address $%06X; got %+v", tc.wantKind, tc.wantAddr, frontiers)
			}

			plan, err := planner.Plan(tc.doc, planner.Options{})
			if err != nil {
				t.Fatalf("Plan failed: %v", err)
			}
			if len(plan.Experiments) == 0 {
				t.Fatalf("expected experiments in plan, got 0")
			}
			exp := plan.Experiments[0]
			if !strings.Contains(exp.RecommendedAction, tc.wantActionSub) {
				t.Errorf("action %q does not contain expected substring %q", exp.RecommendedAction, tc.wantActionSub)
			}
		})
	}
}

func TestPlan_TraceCheckpointAction(t *testing.T) {
	doc := &recovery.Document{
		Evidence: []recovery.Evidence{
			{
				ID:      "trace-ev-1",
				Kind:    "trace",
				Details: "frame #142 execution checkpoint",
			},
		},
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-8020",
				Address:  0x008020,
				Offset:   0x000020,
				Bytes:    "D004", // BNE +4 -> 0x8026
				Opcode:   0xD0,
				Mnemonic: "BNE $8026",
				Context:  recovery.Context{E: "set", M: "set", X: "set"},
				Evidence: []string{"trace-ev-1"},
			},
		},
		Edges: []recovery.Edge{
			{
				ID:          "edge-fallthrough",
				Kind:        "fallthrough",
				Source:      "inst-8020",
				Destination: 0x008022,
			},
		},
	}

	plan, err := planner.Plan(doc, planner.Options{})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Experiments) != 1 {
		t.Fatalf("expected 1 experiment, got %d", len(plan.Experiments))
	}
	exp := plan.Experiments[0]
	if !strings.Contains(exp.RecommendedAction, "frame #142") {
		t.Errorf("expected action to reference frame #142, got %q", exp.RecommendedAction)
	}
	// Checkpoint presence should reduce verification cost.
	if exp.VerificationCost > 20.0 {
		t.Errorf("expected reduced verification cost <= 20.0 due to checkpoint, got %.1f", exp.VerificationCost)
	}
}

func TestPlan_ScoringAndRanking(t *testing.T) {
	doc := &recovery.Document{
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-known",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "EA",
				Opcode:   0xEA,
				Mnemonic: "NOP",
				Context:  recovery.Context{E: "set", M: "set", X: "set"},
			},
		},
		Issues: []recovery.Issue{
			{
				ID:       "iss-dispatch",
				Address:  0x008010,
				Offset:   0x000010,
				Reason:   "indirect jump destination unresolved",
				Blocking: true, // High information gain
			},
			{
				ID:       "iss-ram",
				Address:  0x7E2000,
				Offset:   0x002000,
				Reason:   "read from RAM without prior observed write",
				Blocking: false,
			},
		},
	}

	plan, err := planner.Plan(doc, planner.Options{})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Experiments) != 2 {
		t.Fatalf("expected 2 experiments, got %d", len(plan.Experiments))
	}

	// Dispatch table with blocking issue and proximity to known code should rank higher
	// than isolated uninitialized RAM read.
	if plan.Experiments[0].Classification != planner.FrontierIndirectTarget {
		t.Errorf("expected top experiment to be FrontierIndirectTarget, got %s", plan.Experiments[0].Classification)
	}
	if plan.Experiments[0].PriorityScore <= plan.Experiments[1].PriorityScore {
		t.Errorf("expected first experiment score (%.1f) > second experiment score (%.1f)",
			plan.Experiments[0].PriorityScore, plan.Experiments[1].PriorityScore)
	}
}

func TestPlan_OptionsBounding(t *testing.T) {
	doc := &recovery.Document{
		Issues: []recovery.Issue{
			{ID: "i1", Address: 0x008010, Reason: "indirect jump destination unresolved"},
			{ID: "i2", Address: 0x008020, Reason: "unknown M/X register width context"},
			{ID: "i3", Address: 0x008030, Reason: "callee halts without return"},
		},
	}

	// 1. MaxExperiments = 1
	p1, err := planner.Plan(doc, planner.Options{MaxExperiments: 1})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(p1.Experiments) != 1 {
		t.Errorf("expected 1 experiment, got %d", len(p1.Experiments))
	}
	if p1.TotalFrontiers != 3 {
		t.Errorf("expected TotalFrontiers = 3, got %d", p1.TotalFrontiers)
	}

	// 2. MaxExperiments = 0 (unlimited)
	pAll, err := planner.Plan(doc, planner.Options{MaxExperiments: 0})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(pAll.Experiments) != 3 {
		t.Errorf("expected 3 experiments, got %d", len(pAll.Experiments))
	}

	// 3. MinScore threshold
	pMin, err := planner.Plan(doc, planner.Options{MinScore: 999.0})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(pMin.Experiments) != 0 {
		t.Errorf("expected 0 experiments with min score 999.0, got %d", len(pMin.Experiments))
	}
}

func TestPlan_NilAndEmpty(t *testing.T) {
	_, err := planner.Plan(nil, planner.Options{})
	if err == nil {
		t.Error("expected error for nil document, got nil")
	}

	_, err = planner.PlanResult(nil, planner.Options{})
	if err == nil {
		t.Error("expected error for nil result, got nil")
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{})
	p, err := planner.Plan(doc, planner.Options{})
	if err != nil {
		t.Fatalf("Plan failed for empty document: %v", err)
	}
	if len(p.Experiments) != 0 || p.TotalFrontiers != 0 {
		t.Errorf("expected 0 experiments for empty document, got %+v", p)
	}
}

func TestPlanResult(t *testing.T) {
	res := &analysis.Result{
		ResetAddress: 0x008000,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-8000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "7C0090",
				Opcode:   0x7C,
				Mnemonic: "JMP ($9000,X)",
				Context:  recovery.Context{E: "set", M: "set", X: "set"},
			},
		},
	}

	plan, err := planner.PlanResult(res, planner.Options{MaxExperiments: 5})
	if err != nil {
		t.Fatalf("PlanResult failed: %v", err)
	}
	if len(plan.Experiments) != 1 {
		t.Fatalf("expected 1 experiment, got %d", len(plan.Experiments))
	}
	if plan.Experiments[0].Classification != planner.FrontierIndirectTarget {
		t.Errorf("expected FrontierIndirectTarget, got %s", plan.Experiments[0].Classification)
	}
}

func TestPlan_RecoveryDocumentJSON(t *testing.T) {
	docJSON := `{
		"format": "snes-recovery",
		"schema": 1,
		"rom": {
			"original_sha256": "abcdef",
			"original_size": 32768,
			"normalized_sha256": "abcdef",
			"normalized_size": 32768,
			"normalization": "none",
			"mapper": "lorom"
		},
		"producer": {
			"tool": "snesdasm",
			"version": "1.0.0"
		},
		"evidence": [
			{"id": "ev-1", "kind": "trace", "details": "frame #250 checkpoint"}
		],
		"instructions": [
			{
				"id": "inst-1",
				"architecture": "65c816",
				"address": 32768,
				"offset": 0,
				"bytes": "7C0090",
				"opcode": 124,
				"mnemonic": "JMP ($9000,X)",
				"mode": "absolute_indexed_indirect",
				"context": {"e": "set", "m": "set", "x": "set", "c": "unknown"},
				"evidence": ["ev-1"]
			}
		],
		"edges": [],
		"objects": [],
		"issues": [
			{
				"id": "iss-1",
				"offset": 0,
				"address": 32768,
				"reason": "indirect jump destination unresolved",
				"blocking": true
			}
		]
	}`

	doc, err := recovery.Decode(strings.NewReader(docJSON))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	plan, err := planner.Plan(doc, planner.Options{MaxExperiments: 10})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if plan.TotalFrontiers != 1 {
		t.Fatalf("expected 1 total frontier, got %d", plan.TotalFrontiers)
	}
	if len(plan.Experiments) != 1 {
		t.Fatalf("expected 1 experiment, got %d", len(plan.Experiments))
	}
	exp := plan.Experiments[0]
	if exp.Classification != planner.FrontierIndirectTarget {
		t.Errorf("expected FrontierIndirectTarget, got %s", exp.Classification)
	}
	if !strings.Contains(exp.RecommendedAction, "frame #250") {
		t.Errorf("expected action to contain frame #250, got %q", exp.RecommendedAction)
	}
}

