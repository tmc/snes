package analysis_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
)

func TestPlanFrontiers_CallReturn(t *testing.T) {
	res := &analysis.Result{
		ResetAddress: 0x008000,
		ResetOffset:  0x000000,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "205080",
				Opcode:   0x20, // JSR $8050
				Mnemonic: "JSR $8050",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
		},
		Issues: []recovery.Issue{
			{
				ID:       "iss-008000",
				Offset:   0x000000,
				Address:  0x008003,
				Reason:   "call fallthrough return context not assumed",
				Blocking: false,
			},
		},
	}

	plan, err := analysis.PlanFrontiers(res, 10)
	if err != nil {
		t.Fatalf("PlanFrontiers returned unexpected error: %v", err)
	}

	if plan.TotalFrontiers != 1 {
		t.Fatalf("want 1 total frontier, got %d", plan.TotalFrontiers)
	}
	if plan.HighPriorityCount != 1 {
		t.Fatalf("want 1 high priority frontier, got %d", plan.HighPriorityCount)
	}
	if len(plan.Requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(plan.Requests))
	}

	req := plan.Requests[0]
	if req.Frontier.Kind != analysis.FrontierCallReturn {
		t.Errorf("want kind %s, got %s", analysis.FrontierCallReturn, req.Frontier.Kind)
	}
	if req.Frontier.Address != 0x008003 {
		t.Errorf("want address 0x008003, got 0x%06X", req.Frontier.Address)
	}
	if req.Frontier.Priority != 90 {
		t.Errorf("want priority 90, got %d", req.Frontier.Priority)
	}
	if req.Frontier.SourceInstruction != "inst-008000" {
		t.Errorf("want source instruction inst-008000, got %q", req.Frontier.SourceInstruction)
	}
	if req.Strategy != analysis.StrategyCheckpointExploration {
		t.Errorf("want strategy %s, got %s", analysis.StrategyCheckpointExploration, req.Strategy)
	}
	if req.AcceptanceCondition == "" {
		t.Error("want non-empty acceptance condition")
	}
}

func TestPlanFrontiers_Dispatch(t *testing.T) {
	res := &analysis.Result{
		ResetAddress: 0x008000,
		ResetOffset:  0x000000,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-008010",
				Address:  0x008010,
				Offset:   0x000010,
				Bytes:    "7c0090",
				Opcode:   0x7C, // JMP ($9000,X)
				Mnemonic: "JMP ($9000,X)",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
		},
		Issues: []recovery.Issue{
			{
				ID:       "iss-008010",
				Offset:   0x000010,
				Address:  0x008010,
				Reason:   "indirect jump destination unresolved",
				Blocking: false,
			},
		},
	}

	plan, err := analysis.PlanFrontiers(res, 10)
	if err != nil {
		t.Fatalf("PlanFrontiers returned unexpected error: %v", err)
	}

	if plan.TotalFrontiers != 1 {
		t.Fatalf("want 1 total frontier, got %d", plan.TotalFrontiers)
	}
	if plan.HighPriorityCount != 1 {
		t.Fatalf("want 1 high priority frontier, got %d", plan.HighPriorityCount)
	}
	if len(plan.Requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(plan.Requests))
	}

	req := plan.Requests[0]
	if req.Frontier.Kind != analysis.FrontierDispatch {
		t.Errorf("want kind %s, got %s", analysis.FrontierDispatch, req.Frontier.Kind)
	}
	if req.Frontier.Priority != 100 {
		t.Errorf("want priority 100, got %d", req.Frontier.Priority)
	}
	if req.Frontier.ExpectedYield != 50 {
		t.Errorf("want expected yield 50, got %d", req.Frontier.ExpectedYield)
	}
	if req.Strategy != analysis.StrategyCheckpointExploration {
		t.Errorf("want strategy %s, got %s", analysis.StrategyCheckpointExploration, req.Strategy)
	}
}

func TestPlanFrontiers_UncoveredBranchEdges(t *testing.T) {
	tests := []struct {
		name              string
		inst              recovery.Instruction
		edges             []recovery.Edge
		wantFrontiers     int
		wantBranchTarget  uint32
		wantFallthrough   uint32
		checkTargetAddr   bool
		checkFallthrough  bool
	}{
		{
			name: "missing branch-taken edge (only fallthrough edge present)",
			inst: recovery.Instruction{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "d006", // BNE +6 -> target 0x008008
				Opcode:   0xD0,
				Mnemonic: "BNE $8008",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
			edges: []recovery.Edge{
				{
					ID:          "edge-fallthrough",
					Kind:        "fallthrough",
					Source:      "inst-008000",
					Destination: 0x008002,
				},
			},
			wantFrontiers:    1,
			wantBranchTarget: 0x008008,
			checkTargetAddr:  true,
		},
		{
			name: "missing fallthrough edge (only branch-taken edge present)",
			inst: recovery.Instruction{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "f004", // BEQ +4 -> target 0x008006
				Opcode:   0xF0,
				Mnemonic: "BEQ $8006",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
			edges: []recovery.Edge{
				{
					ID:          "edge-branch",
					Kind:        "branch",
					Source:      "inst-008000",
					Destination: 0x008006,
				},
			},
			wantFrontiers:    1,
			wantFallthrough:  0x008002,
			checkFallthrough: true,
		},
		{
			name: "missing both edges",
			inst: recovery.Instruction{
				ID:       "inst-008020",
				Address:  0x008020,
				Offset:   0x000020,
				Bytes:    "90f8", // BCC -8 (0xF8 = -8) -> target 0x008020 + 2 - 8 = 0x00801A
				Opcode:   0x90,
				Mnemonic: "BCC $801A",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
			edges:            nil,
			wantFrontiers:    2,
			wantBranchTarget: 0x00801A,
			wantFallthrough:  0x008022,
			checkTargetAddr:  true,
			checkFallthrough: true,
		},
		{
			name: "fully covered branch has no missing edges",
			inst: recovery.Instruction{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "b004", // BCS +4
				Opcode:   0xB0,
				Mnemonic: "BCS $8006",
				Context:  recovery.Context{E: "set", M: "set", X: "set", C: "unknown"},
			},
			edges: []recovery.Edge{
				{
					ID:          "edge-branch",
					Kind:        "branch",
					Source:      "inst-008000",
					Destination: 0x008006,
				},
				{
					ID:          "edge-fallthrough",
					Kind:        "fallthrough",
					Source:      "inst-008000",
					Destination: 0x008002,
				},
			},
			wantFrontiers: 0,
		},
		{
			name: "non-branch instruction produces no branch frontiers",
			inst: recovery.Instruction{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "ad0020", // LDA $2000
				Opcode:   0xAD,
				Mnemonic: "LDA $2000",
			},
			edges:         nil,
			wantFrontiers: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &analysis.Result{
				Instructions: []recovery.Instruction{tc.inst},
				Edges:        tc.edges,
			}
			plan, err := analysis.PlanFrontiers(res, 0)
			if err != nil {
				t.Fatalf("PlanFrontiers failed: %v", err)
			}
			if len(plan.Requests) != tc.wantFrontiers {
				t.Fatalf("want %d requests, got %d", tc.wantFrontiers, len(plan.Requests))
			}

			if tc.checkTargetAddr {
				found := false
				for _, r := range plan.Requests {
					if r.Frontier.Address == tc.wantBranchTarget && r.Frontier.Kind == analysis.FrontierBranch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected branch target address 0x%06X in requests", tc.wantBranchTarget)
				}
			}

			if tc.checkFallthrough {
				found := false
				for _, r := range plan.Requests {
					if r.Frontier.Address == tc.wantFallthrough && r.Frontier.Kind == analysis.FrontierBranch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("missing expected fallthrough address 0x%06X in requests", tc.wantFallthrough)
				}
			}
		})
	}
}

func TestPlanFrontiers_PrioritySortingAndBudget(t *testing.T) {
	// Construct a Result with 5 diverse issues and gaps:
	// 1. Dispatch (Priority 100)
	// 2. Call-Return (Priority 90)
	// 3. Uncovered Branch (Priority 60)
	// 4. Blocking Decode Issue (Priority 45)
	// 5. Non-blocking Decode Issue (Priority 25)
	res := &analysis.Result{
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-branch",
				Address:  0x008020,
				Offset:   0x000020,
				Bytes:    "d004", // BNE +4
				Opcode:   0xD0,
				Mnemonic: "BNE $8026",
			},
		},
		Edges: []recovery.Edge{
			// Branch has fallthrough edge, missing taken edge.
			{
				ID:          "edge-ft",
				Kind:        "fallthrough",
				Source:      "inst-branch",
				Destination: 0x008022,
			},
		},
		Issues: []recovery.Issue{
			{
				ID:       "iss-dispatch",
				Offset:   0x000010,
				Address:  0x008010,
				Reason:   "indirect jump destination unresolved",
				Blocking: false,
			},
			{
				ID:       "iss-call",
				Offset:   0x000000,
				Address:  0x008003,
				Reason:   "call fallthrough return context not assumed",
				Blocking: false,
			},
			{
				ID:       "iss-blocking-decode",
				Offset:   0x000030,
				Address:  0x008030,
				Reason:   "unrecognized opcode 0xFF",
				Blocking: true,
			},
			{
				ID:       "iss-nonblocking-decode",
				Offset:   0x000040,
				Address:  0x008040,
				Reason:   "execution target outside mapped ROM space",
				Blocking: false,
			},
		},
	}

	t.Run("priority sorting order", func(t *testing.T) {
		plan, err := analysis.PlanFrontiers(res, 0)
		if err != nil {
			t.Fatalf("PlanFrontiers failed: %v", err)
		}

		if plan.TotalFrontiers != 5 {
			t.Fatalf("want 5 total frontiers, got %d", plan.TotalFrontiers)
		}
		if plan.HighPriorityCount != 2 { // Dispatch (100) + CallReturn (90)
			t.Fatalf("want 2 high priority frontiers, got %d", plan.HighPriorityCount)
		}
		if len(plan.Requests) != 5 {
			t.Fatalf("want 5 requests, got %d", len(plan.Requests))
		}

		wantKinds := []analysis.FrontierKind{
			analysis.FrontierDispatch,
			analysis.FrontierCallReturn,
			analysis.FrontierBranch,
			analysis.FrontierDecodeIssue,
			analysis.FrontierDecodeIssue,
		}

		for i, wantKind := range wantKinds {
			gotKind := plan.Requests[i].Frontier.Kind
			if gotKind != wantKind {
				t.Errorf("request %d: want kind %s, got %s", i, wantKind, gotKind)
			}
		}

		// Ensure priorities are strictly non-increasing.
		for i := 1; i < len(plan.Requests); i++ {
			prev := plan.Requests[i-1].Frontier.Priority
			curr := plan.Requests[i].Frontier.Priority
			if prev < curr {
				t.Errorf("priority not descending at index %d: prev %d < curr %d", i, prev, curr)
			}
		}
	})

	t.Run("budget bounding to 2 requests", func(t *testing.T) {
		plan, err := analysis.PlanFrontiers(res, 2)
		if err != nil {
			t.Fatalf("PlanFrontiers failed: %v", err)
		}

		if plan.TotalFrontiers != 5 {
			t.Errorf("want 5 total frontiers reported, got %d", plan.TotalFrontiers)
		}
		if plan.HighPriorityCount != 2 {
			t.Errorf("want 2 high priority count, got %d", plan.HighPriorityCount)
		}
		if len(plan.Requests) != 2 {
			t.Fatalf("want exactly 2 requests in bounded plan, got %d", len(plan.Requests))
		}

		// Top 2 should be Dispatch and CallReturn.
		if plan.Requests[0].Frontier.Kind != analysis.FrontierDispatch {
			t.Errorf("request 0: want %s, got %s", analysis.FrontierDispatch, plan.Requests[0].Frontier.Kind)
		}
		if plan.Requests[1].Frontier.Kind != analysis.FrontierCallReturn {
			t.Errorf("request 1: want %s, got %s", analysis.FrontierCallReturn, plan.Requests[1].Frontier.Kind)
		}

		// Estimated yield should match sum of the top 2.
		wantYield := plan.Requests[0].Frontier.ExpectedYield + plan.Requests[1].Frontier.ExpectedYield
		if plan.EstimatedYield != wantYield {
			t.Errorf("want estimated yield %d, got %d", wantYield, plan.EstimatedYield)
		}
	})

	t.Run("budget bounding to 1 request", func(t *testing.T) {
		plan, err := analysis.PlanFrontiers(res, 1)
		if err != nil {
			t.Fatalf("PlanFrontiers failed: %v", err)
		}
		if len(plan.Requests) != 1 {
			t.Fatalf("want 1 request, got %d", len(plan.Requests))
		}
		if plan.Requests[0].Frontier.Kind != analysis.FrontierDispatch {
			t.Errorf("want highest priority request %s, got %s", analysis.FrontierDispatch, plan.Requests[0].Frontier.Kind)
		}
	})

	t.Run("budget bounding with large maxRequests includes all", func(t *testing.T) {
		plan, err := analysis.PlanFrontiers(res, 100)
		if err != nil {
			t.Fatalf("PlanFrontiers failed: %v", err)
		}
		if len(plan.Requests) != 5 {
			t.Errorf("want 5 requests, got %d", len(plan.Requests))
		}
	})
}

func TestPlanFrontiers_RealisticPlan(t *testing.T) {
	// Construct a synthetic LoROM image where:
	// - Reset vector at $7FFC points to $8000.
	// - At $8000:
	//     $8000: CLC (18)
	//     $8001: JSR $8050 (20 50 80) -> callee has divergent return context or indirect jump
	//     $8004: RTS (60)
	// - At $8050 (callee):
	//     $8050: JMP ($9000,X) (7C 00 90) -> unresolved dispatch table
	rom := make([]byte, 64*1024)

	// Set reset vector
	binary.LittleEndian.PutUint16(rom[0x7FFC:], 0x8000)

	// Code at $00:8000 (offset 0)
	rom[0] = 0x18 // CLC
	rom[1] = 0x20 // JSR $8050
	rom[2] = 0x50
	rom[3] = 0x80
	rom[4] = 0x60 // RTS

	// Code at $00:8050 (offset 0x50)
	rom[0x50] = 0x7C // JMP ($9000,X) - indirect jump without bounds
	rom[0x51] = 0x00
	rom[0x52] = 0x90

	doc := &recovery.Document{
		ROM: recovery.ROMIdentity{
			NormalizedSHA256: "test-sha256",
		},
	}

	res, err := analysis.AnalyzeLoROM(rom, doc, analysis.Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	plan, err := analysis.PlanFrontiers(res, 5)
	if err != nil {
		t.Fatalf("PlanFrontiers failed: %v", err)
	}

	if plan.TotalFrontiers == 0 {
		t.Fatal("expected at least one frontier from unresolved dispatch or call return")
	}

	for _, req := range plan.Requests {
		if req.ID == "" {
			t.Error("request ID should not be empty")
		}
		if req.AcceptanceCondition == "" {
			t.Errorf("request %s acceptance condition should not be empty", req.ID)
		}
		if req.BoundedSteps <= 0 {
			t.Errorf("request %s bounded steps should be positive, got %d", req.ID, req.BoundedSteps)
		}
		if len(req.EventClasses) == 0 {
			t.Errorf("request %s should have event classes", req.ID)
		}
	}
}

func TestPlanFrontiers_NilResult(t *testing.T) {
	_, err := analysis.PlanFrontiers(nil, 5)
	if err == nil {
		t.Fatal("expected error for nil result, got nil")
	}
}

func ExamplePlanFrontiers() {
	res := &analysis.Result{
		ResetAddress: 0x008000,
		ResetOffset:  0x000000,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst-008000",
				Address:  0x008000,
				Offset:   0x000000,
				Bytes:    "205080",
				Opcode:   0x20, // JSR $8050
				Mnemonic: "JSR $8050",
			},
		},
		Issues: []recovery.Issue{
			{
				ID:       "iss-008000",
				Offset:   0x000000,
				Address:  0x008003,
				Reason:   "call fallthrough return context not assumed",
				Blocking: false,
			},
		},
	}

	plan, err := analysis.PlanFrontiers(res, 1)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Printf("Total Frontiers: %d\n", plan.TotalFrontiers)
	fmt.Printf("High Priority: %d\n", plan.HighPriorityCount)
	fmt.Printf("Request Strategy: %s\n", plan.Requests[0].Strategy)
	// Output:
	// Total Frontiers: 1
	// High Priority: 1
	// Request Strategy: CheckpointExploration
}
