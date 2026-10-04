package callsummary

import (
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// RoutineAST represents the control-flow structure of a callee routine for preservation analysis.
type RoutineAST struct {
	EntryAddress uint32
	Blocks       []*structure.BasicBlock
	Instructions []recovery.Instruction
}
