package planner

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis"
)

// FrontierKind classifies the nature of an unresolved code recovery gap.
type FrontierKind string

const (
	// FrontierIndirectTarget represents an unresolved indirect jump or call (e.g. JMP ($xxxx,X), JSR ($xxxx)).
	FrontierIndirectTarget FrontierKind = "FrontierIndirectTarget"

	// FrontierUnknownContext represents unknown M/X register width or DB context at an entry point.
	FrontierUnknownContext FrontierKind = "FrontierUnknownContext"

	// FrontierUnobservedBranch represents a conditional branch instruction where only one path has been observed.
	FrontierUnobservedBranch FrontierKind = "FrontierUnobservedBranch"

	// FrontierCallBoundary represents an unanalyzed callee halting caller continuation.
	FrontierCallBoundary FrontierKind = "FrontierCallBoundary"

	// FrontierUninitializedMemory represents a read from RAM without a prior observed write.
	FrontierUninitializedMemory FrontierKind = "FrontierUninitializedMemory"
)

// DefaultHighPriorityThreshold is the default cutoff score for high-priority frontiers.
const DefaultHighPriorityThreshold = 70.0

// Frontier represents an unresolved gap in recovered program code, context, or dataflow.
type Frontier struct {
	ID                string           `json:"id"`
	Address           uint32           `json:"address"`
	Offset            uint32           `json:"offset,omitempty"`
	Kind              FrontierKind     `json:"kind"`
	Reason            string           `json:"reason"`
	Context           recovery.Context `json:"context,omitempty"`
	SourceInstruction string           `json:"source_instruction,omitempty"`
	TargetAddress     uint32           `json:"target_address,omitempty"`
	BranchType        string           `json:"branch_type,omitempty"` // "taken" or "fallthrough"
	Frame             *uint64          `json:"frame,omitempty"`
	Blocking          bool             `json:"blocking,omitempty"`
}

// Experiment represents a prioritized, actionable experiment suggestion to resolve a recovery frontier.
// InformationGain and VerificationCost are heuristic rankings based on category weights,
// proximity, and local density rather than measured instruction yields or proven replay feasibility.
type Experiment struct {
	ID                string           `json:"id"`
	FrontierID        string           `json:"frontier_id"`
	Address           uint32           `json:"address"`
	Offset            uint32           `json:"offset,omitempty"`
	Classification    FrontierKind     `json:"classification"`
	RecommendedAction string           `json:"recommended_action"`
	PriorityScore     float64          `json:"priority_score"`
	InformationGain   float64          `json:"information_gain"`
	VerificationCost  float64          `json:"verification_cost"`
	Reason            string           `json:"reason,omitempty"`
	Context           recovery.Context `json:"context,omitempty"`
	SourceInstruction string           `json:"source_instruction,omitempty"`
}

// ExperimentPlan represents a prioritized schedule of experiments to expand recovery.
type ExperimentPlan struct {
	Experiments       []Experiment `json:"experiments"`
	TotalFrontiers    int          `json:"total_frontiers"`
	HighPriorityCount int          `json:"high_priority_count"`
}

// Options configures the frontier planning and ranking engine.
type Options struct {
	// MaxExperiments limits the number of experiments in the plan (0 for all).
	MaxExperiments int

	// MinScore excludes experiments with a priority score lower than this value.
	MinScore float64

	// HighPriorityCutoff specifies the threshold score for HighPriorityCount (default 70.0).
	HighPriorityCutoff float64
}

// Plan analyzes doc and produces an ordered ExperimentPlan.
func Plan(doc *recovery.Document, opts Options) (*ExperimentPlan, error) {
	if doc == nil {
		return nil, errors.New("planner: document is nil")
	}
	frontiers := Classify(doc)
	return Rank(frontiers, doc, opts), nil
}

// PlanResult analyzes an analysis Result and produces an ordered ExperimentPlan.
func PlanResult(res *analysis.Result, opts Options) (*ExperimentPlan, error) {
	if res == nil {
		return nil, errors.New("planner: result is nil")
	}
	doc := recovery.NewDocument(recovery.ROMIdentity{})
	doc.Instructions = res.Instructions
	doc.Edges = res.Edges
	doc.Issues = res.Issues
	return Plan(doc, opts)
}

// Classify extracts and categorizes unresolved code frontiers from doc.
func Classify(doc *recovery.Document) []Frontier {
	if doc == nil {
		return nil
	}

	var frontiers []Frontier
	seen := make(map[string]int)

	add := func(f Frontier) {
		key := fmt.Sprintf("%s:%06X:%06X:%s", f.Kind, f.Address, f.Offset, f.BranchType)
		if idx, ok := seen[key]; ok {
			if frontiers[idx].Frame == nil && f.Frame != nil {
				frontiers[idx].Frame = f.Frame
			}
			if frontiers[idx].TargetAddress == 0 && f.TargetAddress != 0 {
				frontiers[idx].TargetAddress = f.TargetAddress
			}
			if !frontiers[idx].Blocking && f.Blocking {
				frontiers[idx].Blocking = f.Blocking
			}
			if frontiers[idx].SourceInstruction == "" && f.SourceInstruction != "" {
				frontiers[idx].SourceInstruction = f.SourceInstruction
			}
			return
		}
		seen[key] = len(frontiers)
		frontiers = append(frontiers, f)
	}

	// 1. Build lookup tables for instructions, edges, and evidence.
	instByID := make(map[string]recovery.Instruction, len(doc.Instructions))
	instByAddr := make(map[uint32]recovery.Instruction, len(doc.Instructions))
	instByOffset := make(map[uint32]recovery.Instruction, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		instByID[inst.ID] = inst
		instByAddr[inst.Address] = inst
		instByOffset[inst.Offset] = inst
	}

	edgesBySrc := make(map[string][]recovery.Edge, len(doc.Edges))
	incomingEdges := make(map[uint32][]recovery.Edge)
	for _, e := range doc.Edges {
		edgesBySrc[e.Source] = append(edgesBySrc[e.Source], e)
		if e.Destination != 0 {
			incomingEdges[e.Destination] = append(incomingEdges[e.Destination], e)
		}
	}

	evidenceFrames := make(map[string]uint64)
	docEvidenceByID := make(map[string]recovery.Evidence, len(doc.Evidence))
	for _, ev := range doc.Evidence {
		docEvidenceByID[ev.ID] = ev
		if f, ok := extractFrameNumber(ev.Details); ok {
			evidenceFrames[ev.ID] = f
		} else if f, ok := extractFrameNumber(ev.ID); ok {
			evidenceFrames[ev.ID] = f
		}
	}

	getInstFrame := func(inst recovery.Instruction) *uint64 {
		for _, eid := range inst.Evidence {
			if f, ok := evidenceFrames[eid]; ok {
				return &f
			}
			if f, ok := extractFrameNumber(eid); ok {
				return &f
			}
		}
		return nil
	}

	// 2. Classify frontiers from recovery.Issues.
	for _, issue := range doc.Issues {
		f := classifyIssue(issue, instByOffset, instByAddr, getInstFrame)
		add(f)
	}

	// 3. Classify frontiers from Instructions & Edges.
	for _, inst := range doc.Instructions {
		frame := getInstFrame(inst)

		// 3a. FrontierIndirectTarget: unresolved indirect jump or call.
		if isIndirectBranchOrCall(inst.Opcode, inst.Mnemonic) {
			edges := edgesBySrc[inst.ID]
			resolved := false
			for _, e := range edges {
				if e.Destination != 0 {
					resolved = true
					break
				}
			}
			if !resolved {
				tableAddr := extractIndirectTableAddress(inst)
				add(Frontier{
					Address:           inst.Address,
					Offset:            inst.Offset,
					Kind:              FrontierIndirectTarget,
					Reason:            fmt.Sprintf("indirect target unresolved at $%06X", inst.Address),
					Context:           inst.Context,
					SourceInstruction: inst.ID,
					TargetAddress:     tableAddr,
					Frame:             frame,
				})
			}
		}

		// 3b. FrontierUnobservedBranch: conditional branch with only one observed path.
		if isConditionalBranch(inst.Opcode) {
			instEdges := edgesBySrc[inst.ID]
			hasBranch := false
			hasFallthrough := false
			for _, e := range instEdges {
				if !isDynamicObservedEdge(e, docEvidenceByID) {
					continue
				}
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
			pb := inst.Address & 0xFF0000
			pc := uint16(inst.Address & 0xFFFF)
			if len(instBytes) >= 2 {
				rel := int8(instBytes[1])
				branchTarget = pb | uint32(uint16(int32(pc)+2+int32(rel)))
				fallthroughTarget = pb | uint32(pc+2)
			} else {
				branchTarget = pb | uint32(pc+2)
				fallthroughTarget = pb | uint32(pc+2)
			}

			// If only one path observed or both missing.
			if !hasBranch {
				targetOffset, ok := loROMAddressToOffset(branchTarget)
				if !ok {
					targetOffset = inst.Offset
				}
				add(Frontier{
					Address:           branchTarget,
					Offset:            targetOffset,
					Kind:              FrontierUnobservedBranch,
					Reason:            fmt.Sprintf("conditional branch taken edge to $%06X unobserved", branchTarget),
					Context:           inst.Context,
					SourceInstruction: inst.ID,
					TargetAddress:     branchTarget,
					BranchType:        "taken",
					Frame:             frame,
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
					Kind:              FrontierUnobservedBranch,
					Reason:            fmt.Sprintf("conditional branch fallthrough edge to $%06X unobserved", fallthroughTarget),
					Context:           inst.Context,
					SourceInstruction: inst.ID,
					TargetAddress:     fallthroughTarget,
					BranchType:        "fallthrough",
					Frame:             frame,
				})
			}
		}

		// 3c. FrontierCallBoundary: unanalyzed callee halting caller continuation.
		if isCall(inst.Opcode, inst.Mnemonic) {
			calleeAddr := extractCallTarget(inst)
			_, calleeDecoded := instByAddr[calleeAddr]

			// Check if caller continuation exists.
			instEdges := edgesBySrc[inst.ID]
			hasContinuation := false
			for _, e := range instEdges {
				if e.Kind == "fallthrough" || e.Kind == "return" {
					hasContinuation = true
					break
				}
			}

			if !calleeDecoded || !hasContinuation {
				add(Frontier{
					Address:           inst.Address,
					Offset:            inst.Offset,
					Kind:              FrontierCallBoundary,
					Reason:            fmt.Sprintf("call at $%06X: unanalyzed callee $%06X halting caller continuation", inst.Address, calleeAddr),
					Context:           inst.Context,
					SourceInstruction: inst.ID,
					TargetAddress:     calleeAddr,
					Frame:             frame,
				})
			}
		}

		// 3d. FrontierUnknownContext: unknown M/X register width or DB context at entry points.
		if isEntryPoint(inst, incomingEdges) && isUnknownContext(inst.Context) {
			add(Frontier{
				Address:           inst.Address,
				Offset:            inst.Offset,
				Kind:              FrontierUnknownContext,
				Reason:            fmt.Sprintf("entry point $%06X has unknown M/X register width context", inst.Address),
				Context:           inst.Context,
				SourceInstruction: inst.ID,
				Frame:             frame,
			})
		}
	}

	// 4. Assign deterministic IDs to frontiers.
	for i := range frontiers {
		frontiers[i].ID = fmt.Sprintf("front-%04d", i+1)
	}

	return frontiers
}

// Rank scores and orders frontiers into a prioritized ExperimentPlan.
func Rank(frontiers []Frontier, doc *recovery.Document, opts Options) *ExperimentPlan {
	if len(frontiers) == 0 {
		return &ExperimentPlan{
			Experiments:       []Experiment{},
			TotalFrontiers:    0,
			HighPriorityCount: 0,
		}
	}

	cutoff := opts.HighPriorityCutoff
	if cutoff <= 0 {
		cutoff = DefaultHighPriorityThreshold
	}

	// Index instructions and objects for proximity and density calculations.
	knownAddrs := make([]uint32, 0, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		knownAddrs = append(knownAddrs, inst.Address)
	}
	sort.Slice(knownAddrs, func(i, j int) bool { return knownAddrs[i] < knownAddrs[j] })

	experiments := make([]Experiment, 0, len(frontiers))
	for _, f := range frontiers {
		infoGain, cost, prio := scoreFrontier(f, doc, knownAddrs)
		if opts.MinScore > 0 && prio < opts.MinScore {
			continue
		}

		action := buildAction(f)

		exp := Experiment{
			ID:                f.ID,
			FrontierID:        f.ID,
			Address:           f.Address,
			Offset:            f.Offset,
			Classification:    f.Kind,
			RecommendedAction: action,
			PriorityScore:     prio,
			InformationGain:   infoGain,
			VerificationCost:  cost,
			Reason:            f.Reason,
			Context:           f.Context,
			SourceInstruction: f.SourceInstruction,
		}
		experiments = append(experiments, exp)
	}

	// Sort experiments deterministically:
	// PriorityScore descending, InformationGain descending, VerificationCost ascending, Address ascending.
	sort.Slice(experiments, func(i, j int) bool {
		if experiments[i].PriorityScore != experiments[j].PriorityScore {
			return experiments[i].PriorityScore > experiments[j].PriorityScore
		}
		if experiments[i].InformationGain != experiments[j].InformationGain {
			return experiments[i].InformationGain > experiments[j].InformationGain
		}
		if experiments[i].VerificationCost != experiments[j].VerificationCost {
			return experiments[i].VerificationCost < experiments[j].VerificationCost
		}
		if experiments[i].Address != experiments[j].Address {
			return experiments[i].Address < experiments[j].Address
		}
		return experiments[i].FrontierID < experiments[j].FrontierID
	})

	highPriorityCount := 0
	for _, exp := range experiments {
		if exp.PriorityScore >= cutoff {
			highPriorityCount++
		}
	}

	totalFrontiers := len(frontiers)

	// Apply MaxExperiments bound.
	if opts.MaxExperiments > 0 && len(experiments) > opts.MaxExperiments {
		experiments = experiments[:opts.MaxExperiments]
	}

	// Assign clean experiment IDs.
	for i := range experiments {
		experiments[i].ID = fmt.Sprintf("exp-%04d", i+1)
	}

	return &ExperimentPlan{
		Experiments:       experiments,
		TotalFrontiers:    totalFrontiers,
		HighPriorityCount: highPriorityCount,
	}
}

// scoreFrontier calculates heuristic rankings for a frontier.
// InformationGain and VerificationCost are heuristic category/density rankings,
// not measured instruction yields or independently proven replay feasibility. Frame
// references are suggestive exploration starting points extracted from evidence annotations;
// they reduce heuristic verification cost but require admitted checkpoint/state verification.
func scoreFrontier(f Frontier, doc *recovery.Document, knownAddrs []uint32) (float64, float64, float64) {
	// Base Information Gain by category (heuristic priority ranking).
	var baseGain float64
	switch f.Kind {
	case FrontierIndirectTarget:
		baseGain = 75.0
	case FrontierUnknownContext:
		baseGain = 65.0
	case FrontierCallBoundary:
		baseGain = 60.0
	case FrontierUnobservedBranch:
		baseGain = 45.0
	case FrontierUninitializedMemory:
		baseGain = 40.0
	default:
		baseGain = 35.0
	}

	// Base Verification Cost by category.
	var baseCost float64
	switch f.Kind {
	case FrontierUnobservedBranch:
		baseCost = 20.0
	case FrontierUnknownContext:
		baseCost = 25.0
	case FrontierCallBoundary:
		baseCost = 35.0
	case FrontierIndirectTarget:
		baseCost = 40.0
	case FrontierUninitializedMemory:
		baseCost = 50.0
	default:
		baseCost = 30.0
	}

	// 1. Proximity bonus to known code.
	minDist := minDistanceToAddresses(f.Address, knownAddrs)
	if minDist == 0 {
		baseGain += 15.0
	} else if minDist <= 32 {
		baseGain += 12.0
	} else if minDist <= 256 {
		baseGain += 8.0
	} else if minDist <= 1024 {
		baseGain += 4.0
	} else if minDist > 4096 {
		baseCost += 20.0 // Isolated/far code has higher setup cost.
	}

	// 2. Code density around frontier (within 512 bytes).
	nearbyCount := countAddressesInRange(f.Address, 512, knownAddrs)
	if nearbyCount > 0 {
		baseGain += math.Min(float64(nearbyCount)*0.5, 10.0)
	}

	// 3. Routine size bonus if in known object.
	for _, obj := range doc.Objects {
		if obj.Kind == "routine" && f.Offset >= obj.Offset && f.Offset < obj.Offset+obj.Length {
			baseGain += math.Min(float64(obj.Length)/20.0, 15.0)
			break
		}
	}

	// 4. Blocking issue bonus.
	if f.Blocking {
		baseGain += 20.0
	}

	// 5. Verification cost reduction if trace checkpoint / frame is available.
	if f.Frame != nil {
		baseCost = math.Max(baseCost-15.0, 10.0)
	}

	// Calculate PriorityScore: (InformationGain / VerificationCost) * 50.0
	priorityScore := (baseGain / baseCost) * 50.0
	priorityScore = math.Round(priorityScore*10.0) / 10.0

	return baseGain, baseCost, priorityScore
}

func buildAction(f Frontier) string {
	frameNum := f.Frame

	switch f.Kind {
	case FrontierIndirectTarget:
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d to observe indirect target at $%06X", *frameNum, f.Address)
		}
		if f.TargetAddress != 0 {
			return fmt.Sprintf("analyze dispatch table at $%04X", f.TargetAddress)
		}
		return fmt.Sprintf("analyze dispatch table at $%06X", f.Address)

	case FrontierUnknownContext:
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d with inferred M/X context", *frameNum)
		}
		return fmt.Sprintf("resolve register context (M/X/DB) at $%06X via caller trace or prelude analysis", f.Address)

	case FrontierUnobservedBranch:
		target := f.TargetAddress
		if target == 0 {
			target = f.Address
		}
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d to synthesize cold branch input to $%06X", *frameNum, target)
		}
		if f.BranchType != "" {
			return fmt.Sprintf("synthesize cold branch input to force %s edge to $%06X", f.BranchType, target)
		}
		return fmt.Sprintf("synthesize cold branch input to force edge to $%06X", target)

	case FrontierCallBoundary:
		target := f.TargetAddress
		if target == 0 {
			target = f.Address
		}
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d to trace callee execution at $%06X", *frameNum, target)
		}
		return fmt.Sprintf("analyze callee subroutine at $%06X to resume caller continuation", target)

	case FrontierUninitializedMemory:
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d to capture prior RAM writes to $%06X", *frameNum, f.Address)
		}
		return fmt.Sprintf("locate initializing routine or reset write for RAM at $%06X", f.Address)

	default:
		if frameNum != nil {
			return fmt.Sprintf("run checkpoint exploration from frame #%d", *frameNum)
		}
		return fmt.Sprintf("investigate frontier at $%06X: %s", f.Address, f.Reason)
	}
}

func classifyIssue(
	issue recovery.Issue,
	instByOffset map[uint32]recovery.Instruction,
	instByAddr map[uint32]recovery.Instruction,
	getInstFrame func(recovery.Instruction) *uint64,
) Frontier {
	var (
		sourceID string
		ctx      recovery.Context
		frame    *uint64
	)

	if inst, ok := instByOffset[issue.Offset]; ok {
		sourceID = inst.ID
		ctx = inst.Context
		frame = getInstFrame(inst)
	} else if inst, ok := instByAddr[issue.Address]; ok {
		sourceID = inst.ID
		ctx = inst.Context
		frame = getInstFrame(inst)
	}

	if frame == nil {
		if f, ok := extractFrameNumber(issue.Reason); ok {
			frame = &f
		}
	}

	reasonLower := strings.ToLower(issue.Reason)

	switch {
	// Uninitialized RAM read
	case strings.Contains(reasonLower, "uninitialized") ||
		strings.Contains(reasonLower, "read from ram without prior") ||
		strings.Contains(reasonLower, "read before write"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierUninitializedMemory,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	// Indirect Jump / Dispatch Table
	case strings.Contains(reasonLower, "indirect jump") ||
		strings.Contains(reasonLower, "indirect call") ||
		strings.Contains(reasonLower, "dispatch"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierIndirectTarget,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	// Call fallthrough / return context not assumed - MUST be checked before generic "context not assumed"
	case strings.Contains(reasonLower, "call fallthrough"):
		frontierAddr := issue.Address
		frontierOffset := issue.Offset
		var targetAddr uint32
		if inst, ok := instByOffset[issue.Offset]; ok {
			frontierAddr = inst.Address
			frontierOffset = inst.Offset
			sourceID = inst.ID
			if isCall(inst.Opcode, inst.Mnemonic) {
				targetAddr = extractCallTarget(inst)
			}
		} else if strings.HasPrefix(issue.ID, "iss-") {
			if parsed, err := strconv.ParseUint(issue.ID[4:], 16, 32); err == nil {
				if inst, ok := instByAddr[uint32(parsed)]; ok {
					frontierAddr = inst.Address
					frontierOffset = inst.Offset
					sourceID = inst.ID
					if isCall(inst.Opcode, inst.Mnemonic) {
						targetAddr = extractCallTarget(inst)
					}
				}
			}
		} else if inst, ok := instByAddr[issue.Address]; ok {
			frontierAddr = inst.Address
			frontierOffset = inst.Offset
			sourceID = inst.ID
			if isCall(inst.Opcode, inst.Mnemonic) {
				targetAddr = extractCallTarget(inst)
			}
		}
		return Frontier{
			Address:           frontierAddr,
			Offset:            frontierOffset,
			Kind:              FrontierCallBoundary,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			TargetAddress:     targetAddr,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	// Unknown context (register width M/X, DB)
	case strings.Contains(reasonLower, "context not assumed") ||
		strings.Contains(reasonLower, "unknown context") ||
		strings.Contains(reasonLower, "register width") ||
		strings.Contains(reasonLower, "unknown m/x") ||
		strings.Contains(reasonLower, "width unknown") ||
		strings.Contains(reasonLower, "db context"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierUnknownContext,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	// Callee / Call Boundary
	case strings.Contains(reasonLower, "callee") ||
		strings.Contains(reasonLower, "call boundary") ||
		strings.Contains(reasonLower, "halts without return") ||
		strings.Contains(reasonLower, "caller continuation"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierCallBoundary,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	// Unobserved conditional branch
	case strings.Contains(reasonLower, "conditional branch") ||
		strings.Contains(reasonLower, "unobserved branch") ||
		strings.Contains(reasonLower, "uncovered edge"):
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierUnobservedBranch,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}

	default:
		// Default to FrontierUnknownContext if blocking or generic context issue,
		// otherwise FrontierUnknownContext.
		return Frontier{
			Address:           issue.Address,
			Offset:            issue.Offset,
			Kind:              FrontierUnknownContext,
			Reason:            issue.Reason,
			Context:           ctx,
			SourceInstruction: sourceID,
			Frame:             frame,
			Blocking:          issue.Blocking,
		}
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

func isIndirectBranchOrCall(opcode byte, mnemonic string) bool {
	switch opcode {
	case 0x6C, 0x7C, 0xDC, 0xFC:
		return true
	}
	mn := strings.ToUpper(mnemonic)
	return strings.HasPrefix(mn, "JMP (") ||
		strings.HasPrefix(mn, "JMP [") ||
		strings.HasPrefix(mn, "JML [") ||
		strings.HasPrefix(mn, "JSR (")
}

func isCall(opcode byte, mnemonic string) bool {
	if opcode == 0x20 || opcode == 0x22 {
		return true
	}
	mn := strings.ToUpper(mnemonic)
	return strings.HasPrefix(mn, "JSR ") || strings.HasPrefix(mn, "JSL ")
}

func isEntryPoint(inst recovery.Instruction, incomingEdges map[uint32][]recovery.Edge) bool {
	edges := incomingEdges[inst.Address]
	return len(edges) == 0
}

func isUnknownContext(ctx recovery.Context) bool {
	return ctx.M == "unknown" || ctx.M == "" ||
		ctx.X == "unknown" || ctx.X == "" ||
		ctx.E == "unknown"
}

func extractIndirectTableAddress(inst recovery.Instruction) uint32 {
	if len(inst.Bytes) >= 6 {
		b, err := hex.DecodeString(inst.Bytes)
		if err == nil && len(b) >= 3 {
			return uint32(b[1]) | (uint32(b[2]) << 8)
		}
	}
	return 0
}

func extractCallTarget(inst recovery.Instruction) uint32 {
	if len(inst.Bytes) >= 6 {
		b, err := hex.DecodeString(inst.Bytes)
		if err == nil {
			if inst.Opcode == 0x22 && len(b) >= 4 { // JSL long
				return uint32(b[1]) | (uint32(b[2]) << 8) | (uint32(b[3]) << 16)
			}
			if len(b) >= 3 { // JSR absolute
				bank := inst.Address & 0xFF0000
				return bank | uint32(b[1]) | (uint32(b[2]) << 8)
			}
		}
	}
	return 0
}

var frameRegex = regexp.MustCompile(`(?i)(?:frame\s*(?:#|:|=)?\s*|#)(\d+)`)

func extractFrameNumber(s string) (uint64, bool) {
	matches := frameRegex.FindStringSubmatch(s)
	if len(matches) >= 2 {
		n, err := strconv.ParseUint(matches[1], 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
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

func minDistanceToAddresses(target uint32, sortedAddrs []uint32) uint32 {
	if len(sortedAddrs) == 0 {
		return 0
	}
	idx := sort.Search(len(sortedAddrs), func(i int) bool {
		return sortedAddrs[i] >= target
	})
	minDist := uint32(math.MaxUint32)
	if idx < len(sortedAddrs) {
		dist := sortedAddrs[idx] - target
		if dist < minDist {
			minDist = dist
		}
	}
	if idx > 0 {
		dist := target - sortedAddrs[idx-1]
		if dist < minDist {
			minDist = dist
		}
	}
	return minDist
}

func countAddressesInRange(center uint32, radius uint32, sortedAddrs []uint32) int {
	if len(sortedAddrs) == 0 {
		return 0
	}
	var minVal uint32
	if center > radius {
		minVal = center - radius
	}
	maxVal := center + radius

	start := sort.Search(len(sortedAddrs), func(i int) bool {
		return sortedAddrs[i] >= minVal
	})
	end := sort.Search(len(sortedAddrs), func(i int) bool {
		return sortedAddrs[i] > maxVal
	})
	return end - start
}

// isDynamicObservedEdge reports whether an edge represents an authentic dynamically observed transition,
// separating derived static reachability from dynamic execution evidence.
func isDynamicObservedEdge(e recovery.Edge, docEvidenceByID map[string]recovery.Evidence) bool {
	if len(e.Evidence) == 0 {
		if ev, ok := docEvidenceByID[e.ID]; ok && isDynamicEvidence(ev) {
			return true
		}
		return false
	}

	for _, evID := range e.Evidence {
		switch evID {
		case "derived", "static":
			continue
		case "observed", "trace", "execution", "dynamic":
			return true
		}
		if ev, ok := docEvidenceByID[evID]; ok {
			if isDynamicEvidence(ev) {
				return true
			}
			continue
		}
		if strings.HasPrefix(evID, "trace-") || strings.HasPrefix(evID, "ev-edge-") || strings.HasPrefix(evID, "obs") {
			return true
		}
	}
	return false
}

func isDynamicEvidence(ev recovery.Evidence) bool {
	kind := strings.ToLower(ev.Kind)
	if kind == "derived" || kind == "static" {
		return false
	}
	if kind == "observed" || kind == "trace" || kind == "execution" || kind == "dynamic" {
		return true
	}
	details := strings.ToLower(ev.Details)
	if strings.Contains(details, "trace") || strings.Contains(details, "frame") || strings.Contains(details, "checkpoint") || strings.Contains(details, "execution") {
		return true
	}
	return false
}
