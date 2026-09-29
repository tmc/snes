package analysis

import "github.com/tmc/snes/internal/recovery"

// Config specifies analysis constraints and limits.
type Config struct {
	MaxInstructions int
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
	Address uint32
	Context recovery.Context
}
