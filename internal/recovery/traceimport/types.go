package traceimport

import (
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
)

// SchemaVersion is the supported observation stream schema version.
const SchemaVersion = 2

// StreamRecord represents a single record in a trace stream.
type StreamRecord struct {
	ID         uint64            `json:"id"`
	Schema     int               `json:"schema"`
	Kind       string            `json:"kind"`
	Frame      uint64            `json:"frame,omitempty"`
	Run        *RunRecord        `json:"run,omitempty"`
	Insn       *InsnRecord       `json:"insn,omitempty"`
	Transition *TransitionRecord `json:"transition,omitempty"`
	Gap        *GapRecord        `json:"gap,omitempty"`
}

// RunRecord describes the execution environment and target ROM.
type RunRecord struct {
	ROM_SHA256         string            `json:"rom_sha256"`
	Mapper             string            `json:"mapper"`
	ROMProvenance      string            `json:"rom_provenance"`
	EngineRevision     string            `json:"engine_revision"`
	EngineDirty        bool              `json:"engine_dirty"`
	EngineDirtySHA256  string            `json:"engine_dirty_sha256,omitempty"`
	InitialStateSHA256 string            `json:"initial_state_sha256,omitempty"`
	ReplayInputSHA256  string            `json:"replay_input_sha256,omitempty"`
	Events             []string          `json:"events,omitempty"`
	Filters            map[string]any    `json:"filters,omitempty"`
	Limits             map[string]uint64 `json:"limits,omitempty"`
}

// CPUSnapshot records CPU register state.
type CPUSnapshot struct {
	A      uint16 `json:"a"`
	X      uint16 `json:"x"`
	Y      uint16 `json:"y"`
	S      uint16 `json:"s"`
	D      uint16 `json:"d"`
	DB     uint8  `json:"db"`
	PB     uint8  `json:"pb"`
	PC     uint16 `json:"pc"`
	P      uint8  `json:"p"`
	E      bool   `json:"e"`
	Cycles uint64 `json:"cycles"`
}

// BankAddr represents a 24-bit bank:address pair.
type BankAddr struct {
	Bank uint8  `json:"bank"`
	Addr uint16 `json:"addr"`
}

// Address returns the full 24-bit bus address.
func (b BankAddr) Address() uint32 {
	return (uint32(b.Bank) << 16) | uint32(b.Addr)
}

// Fetch represents an individual byte read during instruction fetch.
type Fetch struct {
	Addr      uint32  `json:"addr"`
	Value     uint8   `json:"value"`
	Role      string  `json:"role"`
	ROMOffset *uint32 `json:"rom_offset,omitempty"`
}

// InsnRecord records an executed instruction dispatch.
type InsnRecord struct {
	Seq               uint64      `json:"seq"`
	Entry             CPUSnapshot `json:"entry"`
	Exit              CPUSnapshot `json:"exit"`
	Fetches           []Fetch     `json:"fetches"`
	Length            int         `json:"length"`
	SequentialPC      BankAddr    `json:"sequential_pc"`
	SuccessorPC       BankAddr    `json:"successor_pc"`
	Status            string      `json:"status"`
	Fault             string      `json:"fault,omitempty"`
	SoftwareInterrupt string      `json:"software_interrupt,omitempty"`
	Issues            []string    `json:"issues,omitempty"`
}

// TransitionRecord records an interrupt or non-instruction transition.
type TransitionRecord struct {
	Seq        uint64      `json:"seq"`
	Kind       string      `json:"kind"`
	Before     CPUSnapshot `json:"before"`
	After      CPUSnapshot `json:"after"`
	VectorAddr uint32      `json:"vector_addr,omitempty"`
	HandlerPC  BankAddr    `json:"handler_pc,omitempty"`
	FromWait   bool        `json:"from_wait,omitempty"`
}

// GapRecord records excluded executions due to filtering.
type GapRecord struct {
	FirstSeq uint64 `json:"first_seq"`
	LastSeq  uint64 `json:"last_seq"`
	Reason   string `json:"reason"`
}

// Receipt describes the final completion status of an observation run.
type Receipt struct {
	Schema           int    `json:"schema"`
	Outcome          string `json:"outcome"`
	LastSeq          uint64 `json:"last_seq"`
	EventCount       uint64 `json:"event_count"`
	StreamSHA256     string `json:"stream_sha256"`
	TruncationReason string `json:"truncation_reason,omitempty"`
}

// ImportResult contains the parsed and verified recovery facts from a trace stream.
type ImportResult struct {
	RunMetadata  *RunRecord
	Receipt      *Receipt
	TotalRecords int
	StreamSHA256 string
	IsComplete   bool
	Instructions []recovery.Instruction
	Edges        []recovery.Edge
	Evidence     []recovery.Evidence
	Issues       []recovery.Issue
	Events       []coverage.Event
}

// MergeResult describes the outcome of merging an ImportResult into a Document.
type MergeResult struct {
	InstructionsAdded    int
	InstructionsExisting int
	EdgesAdded           int
	EvidenceAdded        int
	IssuesAdded          int
}
