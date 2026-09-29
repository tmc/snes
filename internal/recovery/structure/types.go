package structure

import "github.com/tmc/snes/internal/recovery"

// BasicBlock represents a straight-line sequence of instructions with single entry and exit.
type BasicBlock struct {
	ID           string                 `json:"id"`
	StartAddress uint32                 `json:"start_address"`
	EndAddress   uint32                 `json:"end_address"`
	StartOffset  uint32                 `json:"start_offset"`
	EndOffset    uint32                 `json:"end_offset"`
	Instructions []recovery.Instruction `json:"instructions"`
	Successors   []uint32               `json:"successors,omitempty"`
	Predecessors []uint32               `json:"predecessors,omitempty"`
}

// Routine represents an identified routine candidate.
type Routine struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	EntryAddress     uint32             `json:"entry_address"`
	EntryOffset      uint32             `json:"entry_offset"`
	Callers          []uint32           `json:"callers,omitempty"`
	Exits            []uint32           `json:"exits,omitempty"`
	BlockIDs         []string           `json:"block_ids"`
	InstructionCount int                `json:"instruction_count"`
	SharedTails      []string           `json:"shared_tails,omitempty"`
	Contexts         []recovery.Context `json:"contexts,omitempty"`
}

// MemoryReference records a memory access or hardware register reference made by an instruction.
type MemoryReference struct {
	InstructionID      string `json:"instruction_id"`
	InstructionAddress uint32 `json:"instruction_address"`
	Offset             uint32 `json:"offset"`
	EncodedAddress     uint32 `json:"encoded_address"`
	AddressSpace       string `json:"address_space"`
	HardwareName       string `json:"hardware_name,omitempty"`
	Direction          string `json:"direction"` // "read", "write", "read_write", "execute"
	AddressingMode     string `json:"addressing_mode"`
	Mnemonic           string `json:"mnemonic"`
}

// CFGNode represents a block or unresolved target in a control-flow graph.
type CFGNode struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	StartAddress uint32   `json:"start_address"`
	EndAddress   uint32   `json:"end_address,omitempty"`
	IsUnresolved bool     `json:"is_unresolved,omitempty"`
	IsExternal   bool     `json:"is_external,omitempty"`
	Instructions []string `json:"instructions,omitempty"`
}

// CFGEdge represents a directed transition in a control-flow graph.
type CFGEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`
	Provenance string `json:"provenance"` // "observed", "static"
}

// CFG represents a control-flow graph for a routine or set of blocks.
type CFG struct {
	RoutineID string    `json:"routine_id,omitempty"`
	Nodes     []CFGNode `json:"nodes"`
	Edges     []CFGEdge `json:"edges"`
}
