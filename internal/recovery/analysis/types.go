package analysis

import "github.com/tmc/snes/internal/recovery"

// DispatchWitness represents an observed indirect jump destination.
type DispatchWitness struct {
	SourceAddress uint32
	TargetAddress uint32
	TargetContext recovery.Context
	Evidence      []string
}

// Config specifies analysis constraints and limits.
type Config struct {
	MaxInstructions   int
	DispatchWitnesses []DispatchWitness
}

// Result contains the recovered instructions, control-flow edges, and issues.
type Result struct {
	ResetAddress uint32
	ResetOffset  uint32
	Instructions []recovery.Instruction
	Edges        []recovery.Edge
	Issues       []recovery.Issue
}

// WorkItem represents a pending address and processor status context to analyze.
type WorkItem struct {
	Address   uint32
	Context   recovery.Context
	Preceding []recovery.Instruction
}
