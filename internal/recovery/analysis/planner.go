package analysis

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// FrontierKind classifies the nature of an unresolved recovery gap.
type FrontierKind string

const (
	FrontierCallReturn     FrontierKind = "FrontierCallReturn"
	FrontierDispatch       FrontierKind = "FrontierDispatch"
	FrontierBranch         FrontierKind = "FrontierBranch"
	FrontierDecodeIssue    FrontierKind = "FrontierDecodeIssue"
	FrontierMissingWitness FrontierKind = "FrontierMissingWitness"
)

// StrategyKind identifies the recommended recovery strategy for a gap.
type StrategyKind string

const (
	StrategyStaticAnalysis        StrategyKind = "StaticAnalysis"
	StrategyCheckpointExploration StrategyKind = "CheckpointExploration"
	StrategyTraceRecapture        StrategyKind = "TraceRecapture"
)

// HighPriorityThreshold is the priority score cutoff at or above which a frontier is
// considered high priority.
const HighPriorityThreshold = 70

// Frontier represents an unresolved gap in recovered program code or context.
type Frontier struct {
	Address           uint32           `json:"address"`
	Offset            uint32           `json:"offset"`
	Kind              FrontierKind     `json:"kind"`
	Reason            string           `json:"reason"`
	Context           recovery.Context `json:"context"`
	SourceInstruction string           `json:"source_instruction,omitempty"`
	Priority          int              `json:"priority"`
	ExpectedYield     int              `json:"expected_yield"`
	Cost              int              `json:"cost"`
}

// InvestigationRequest represents an actionable experiment request to resolve a frontier.
type InvestigationRequest struct {
	ID                  string           `json:"id"`
	Frontier            Frontier         `json:"frontier"`
	Strategy            StrategyKind     `json:"strategy"`
	EventClasses        []string         `json:"event_classes,omitempty"`
	BoundedSteps        int              `json:"bounded_steps"`
	AcceptanceCondition string           `json:"acceptance_condition"`
}

// Plan represents an ordered investigation plan addressing recovery frontiers.
type Plan struct {
	Requests          []InvestigationRequest `json:"requests"`
	TotalFrontiers    int                    `json:"total_frontiers"`
	HighPriorityCount int                    `json:"high_priority_count"`
	EstimatedYield    int                    `json:"estimated_yield"`
}

// PlanFrontiers analyzes res and generates an ordered investigation plan.
// It discovers call-return, dispatch, decode, and uncovered branch frontiers,
// prioritizing frontiers by expected yield and branching potential.
// If maxRequests > 0, the plan includes at most maxRequests investigation requests.
// If maxRequests <= 0, all discovered frontiers are included in the plan.
func PlanFrontiers(res *Result, maxRequests int) (*Plan, error) {
	if res == nil {
		return nil, errors.New("planner: result is nil")
	}

	frontiers := extractFrontiers(res)

	// Sort frontiers deterministically: highest priority first, then yield, cost, address.
	sort.Slice(frontiers, func(i, j int) bool {
		if frontiers[i].Priority != frontiers[j].Priority {
			return frontiers[i].Priority > frontiers[j].Priority
		}
		if frontiers[i].ExpectedYield != frontiers[j].ExpectedYield {
			return frontiers[i].ExpectedYield > frontiers[j].ExpectedYield
		}
		if frontiers[i].Cost != frontiers[j].Cost {
			return frontiers[i].Cost < frontiers[j].Cost
		}
		if frontiers[i].Address != frontiers[j].Address {
			return frontiers[i].Address < frontiers[j].Address
		}
		if frontiers[i].Offset != frontiers[j].Offset {
			return frontiers[i].Offset < frontiers[j].Offset
		}
		return frontiers[i].Reason < frontiers[j].Reason
	})

	highPriorityCount := 0
	for _, f := range frontiers {
		if f.Priority >= HighPriorityThreshold {
			highPriorityCount++
		}
	}

	selected := frontiers
	if maxRequests > 0 && len(selected) > maxRequests {
		selected = selected[:maxRequests]
	}

	requests := make([]InvestigationRequest, len(selected))
	estimatedYield := 0
	for i, f := range selected {
		requests[i] = makeInvestigationRequest(fmt.Sprintf("req-%04d", i+1), f)
		estimatedYield += f.ExpectedYield
	}

	return &Plan{
		Requests:          requests,
		TotalFrontiers:    len(frontiers),
		HighPriorityCount: highPriorityCount,
		EstimatedYield:    estimatedYield,
	}, nil
}

func extractFrontiers(res *Result) []Frontier {
	var frontiers []Frontier
	seen := make(map[string]bool)

	add := func(f Frontier) {
		key := fmt.Sprintf("%s:%06x:%06x:%s", f.Kind, f.Address, f.Offset, f.Reason)
		if !seen[key] {
			seen[key] = true
			frontiers = append(frontiers, f)
		}
	}

	// 1. Build lookup tables for instructions and edges.
	instByOffset := make(map[uint32]recovery.Instruction, len(res.Instructions))
	instByAddress := make(map[uint32]recovery.Instruction, len(res.Instructions))
	for _, inst := range res.Instructions {
		instByOffset[inst.Offset] = inst
		instByAddress[inst.Address] = inst
	}

	edgesBySource := make(map[string][]recovery.Edge)
	for _, e := range res.Edges {
		edgesBySource[e.Source] = append(edgesBySource[e.Source], e)
	}

	// 2. Identify frontiers from Result.Issues.
	for _, issue := range res.Issues {
		f := frontierFromIssue(issue, instByOffset, instByAddress)
		add(f)
	}

	// 3. Identify uncovered conditional branch frontiers from Instructions and Edges.
	for _, inst := range res.Instructions {
		if !isConditionalBranch(inst.Opcode) {
			continue
		}

		instEdges := edgesBySource[inst.ID]
		hasBranch := false
		hasFallthrough := false
		for _, e := range instEdges {
			switch e.Kind {
			case "branch":
				hasBranch = true
			case "fallthrough":
				hasFallthrough = true
			}
		}

		var instBytes []byte
		if len(inst.Bytes) >= 4 {
			instBytes, _ = hex.DecodeString(inst.Bytes)
		}

		var branchTarget uint32
		var fallthroughTarget uint32
		if len(instBytes) >= 2 {
			rel := int8(instBytes[1])
			branchTarget = uint32(int32(inst.Address) + 2 + int32(rel))
			fallthroughTarget = inst.Address + uint32(len(instBytes))
		} else {
			branchTarget = inst.Address + 2
			fallthroughTarget = inst.Address + 2
		}

		if !hasBranch {
			targetOffset, ok := loROMAddressToOffset(branchTarget)
			if !ok {
				targetOffset = inst.Offset
			}
			add(Frontier{
				Address:           branchTarget,
				Offset:            targetOffset,
				Kind:              FrontierBranch,
				Reason:            fmt.Sprintf("conditional branch taken edge to $%06X unresolved", branchTarget),
				Context:           inst.Context,
				SourceInstruction: inst.ID,
				Priority:          60,
				ExpectedYield:     15,
				Cost:              150,
			})
		}

		if !hasFallthrough {
			fallthroughOffset, ok := loROMAddressToOffset(fallthroughTarget)
			if !ok {
				fallthroughOffset = inst.Offset + 2
			}
			add(Frontier{
				Address:           fallthroughTarget,
				Offset:            fallthroughOffset,
				Kind:              FrontierBranch,
				Reason:            fmt.Sprintf("conditional branch fallthrough edge to $%06X unresolved", fallthroughTarget),
				Context:           inst.Context,
				SourceInstruction: inst.ID,
				Priority:          60,
				ExpectedYield:     15,
				Cost:              150,
			})
		}
	}

	return frontiers
}

func frontierFromIssue(
	issue recovery.Issue,
	instByOffset map[uint32]recovery.Instruction,
	instByAddress map[uint32]recovery.Instruction,
) Frontier {
	var sourceID string
	var ctx recovery.Context
	if inst, ok := instByOffset[issue.Offset]; ok {
		sourceID = inst.ID
		ctx = inst.Context
	} else if inst, ok := instByAddress[issue.Address]; ok {
		sourceID = inst.ID
		ctx = inst.Context
	}

	reasonLower := strings.ToLower(issue.Reason)

	switch {
	case strings.Contains(reasonLower, "call fallthrough return context") ||
		(strings.Contains(reasonLower, "call") && strings.Contains(reasonLower, "return")):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierCallReturn,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Priority:          90,
			ExpectedYield:     30,
			Cost:              100,
		}

	case strings.Contains(reasonLower, "indirect jump") || strings.Contains(reasonLower, "dispatch"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierDispatch,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Priority:          100,
			ExpectedYield:     50,
			Cost:              200,
		}

	case strings.Contains(reasonLower, "unrecognized opcode") ||
		strings.Contains(reasonLower, "extends past end of rom") ||
		strings.Contains(reasonLower, "sizing") ||
		strings.Contains(reasonLower, "outside mapped rom") ||
		issue.Blocking:
		prio := 25
		yield := 5
		if issue.Blocking {
			prio = 45
			yield = 10
		}
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierDecodeIssue,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Priority:          prio,
			ExpectedYield:     yield,
			Cost:              50,
		}

	case strings.Contains(reasonLower, "witness") || strings.Contains(reasonLower, "dependency"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierMissingWitness,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Priority:          35,
			ExpectedYield:     8,
			Cost:              250,
		}

	default:
		prio := 30
		yield := 8
		kind := FrontierMissingWitness
		if issue.Blocking {
			prio = 40
			yield = 10
			kind = FrontierDecodeIssue
		}
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              kind,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Priority:          prio,
			ExpectedYield:     yield,
			Cost:              100,
		}
	}
}

func makeInvestigationRequest(id string, f Frontier) InvestigationRequest {
	var (
		strategy   StrategyKind
		events     []string
		steps      int
		acceptance string
	)

	switch f.Kind {
	case FrontierCallReturn:
		strategy = StrategyCheckpointExploration
		events = []string{"cpu.call", "cpu.return", "cpu.status"}
		steps = 5000
		acceptance = fmt.Sprintf("witness call return to $%06X with determinate status flags (E, M, X)", f.Address)

	case FrontierDispatch:
		strategy = StrategyCheckpointExploration
		events = []string{"cpu.jump_indirect", "mem.read", "cpu.registers"}
		steps = 10000
		acceptance = fmt.Sprintf("witness indirect jump targets executed from $%06X", f.Address)

	case FrontierBranch:
		strategy = StrategyCheckpointExploration
		events = []string{"cpu.branch", "cpu.status"}
		steps = 3000
		if f.SourceInstruction != "" {
			acceptance = fmt.Sprintf("exercise uncovered edge to $%06X from branch %s", f.Address, f.SourceInstruction)
		} else {
			acceptance = fmt.Sprintf("exercise uncovered edge to $%06X", f.Address)
		}

	case FrontierDecodeIssue:
		if strings.Contains(strings.ToLower(f.Reason), "outside mapped rom") {
			strategy = StrategyStaticAnalysis
			events = []string{"cpu.fetch"}
			steps = 500
			acceptance = fmt.Sprintf("verify bus address mapping for $%06X", f.Address)
		} else {
			strategy = StrategyTraceRecapture
			events = []string{"cpu.fetch", "cpu.decode"}
			steps = 1000
			acceptance = fmt.Sprintf("resolve valid instruction decode or confirm unmapped space at $%06X", f.Address)
		}

	case FrontierMissingWitness:
		strategy = StrategyTraceRecapture
		events = []string{"mem.read", "mem.write", "cpu.state"}
		steps = 5000
		acceptance = fmt.Sprintf("capture memory witness for $%06X", f.Address)

	default:
		strategy = StrategyCheckpointExploration
		events = []string{"cpu.state"}
		steps = 2000
		acceptance = fmt.Sprintf("resolve gap at $%06X: %s", f.Address, f.Reason)
	}

	return InvestigationRequest{
		ID:                  id,
		Frontier:            f,
		Strategy:            strategy,
		EventClasses:        events,
		BoundedSteps:        steps,
		AcceptanceCondition: acceptance,
	}
}

func isConditionalBranch(opcode byte) bool {
	switch opcode {
	case 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0:
		return true
	default:
		return false
	}
}

func loROMAddressToOffset(addr uint32) (uint32, bool) {
	bank := (addr >> 16) & 0xFF
	bankOffset := addr & 0xFFFF
	if bankOffset < 0x8000 || bank == 0x7E || bank == 0x7F {
		return 0, false
	}
	var physBank uint32
	if bank >= 0x80 {
		physBank = bank - 0x80
	} else {
		physBank = bank
	}
	return (physBank * 32768) + (bankOffset - 0x8000), true
}
