package extractor

import (
	"encoding/json"
	"fmt"
)

const (
	// MaxCaptureEvents defines the hard limit on ingested capture stream events to bound heap usage.
	MaxCaptureEvents = 2_000_000

	// MaxHistoryEvents defines the hard limit on ingested history stream events to bound heap usage.
	MaxHistoryEvents = 2_000_000
)

// AddressRange is a half-open range of physical instruction bytes.
type AddressRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

// ConnectedContract defines the structural spans and control expectations for a connected routine closure.
type ConnectedContract struct {
	CallerPC               uint32              `json:"caller_pc"`                 // e.g. 0x0C:C40A (JSR $C435)
	CallerOpcode           byte                `json:"caller_opcode"`             // e.g. 0x20
	ContinuationPC         uint32              `json:"continuation_pc"`           // e.g. 0x0C:C40D
	Spans                  []AddressRange      `json:"spans"`                     // [0C:C435..0C:C44F), [00:8781..00:879C), [0C:C45B..0C:C47B)
	OuterJSRPC             uint32              `json:"outer_jsr_pc"`              // e.g. 0x0C:C43F
	OuterJSRResume         uint32              `json:"outer_jsr_resume"`          // e.g. 0x0C:C442
	DispatcherCallPC       uint32              `json:"dispatcher_call_pc"`        // e.g. 0x0C:C44B (JSL $008781)
	HelperEntryPC          uint32              `json:"helper_entry_pc"`           // e.g. 0x00:8781
	HelperExitPC           uint32              `json:"helper_exit_pc"`            // e.g. 0x00:8799 (JML [$0000])
	AllowedIndirectTargets map[uint32][]uint32 `json:"allowed_indirect_targets"` // 0x008799 -> [0x0CC45B]
	HandlerEntryPC         uint32              `json:"handler_entry_pc"`          // e.g. 0x0C:C45B
	HandlerReturnPC        uint32              `json:"handler_return_pc"`         // e.g. 0x0C:C47A (RTS)
	TerminalReturnPC       uint32              `json:"terminal_return_pc"`        // e.g. 0x0C:C447 (RTS)
	ExpectedEntryS         uint16              `json:"expected_entry_s"`          // e.g. 0x01F9
	ExpectedReturnS        uint16              `json:"expected_return_s"`         // e.g. 0x01FB
	StackReturnBytes       []byte              `json:"stack_return_bytes"`        // [0x0C, 0xC4]
	EarlyReturnBranchPC    uint32              `json:"early_return_branch_pc"`    // e.g. 0x0C:C43D
	Timer64PC              uint32              `json:"timer64_pc"`                // e.g. 0x0C:C465
	AllowedPathLengths     []int               `json:"allowed_path_lengths"`      // [5, 38, 39]

	// HandlerReturnPCs lists additional reviewed RTS sites.
	HandlerReturnPCs []uint32 `json:"handler_return_pcs,omitempty"`
}

// DispatchContract defines the evidence required to prove a jump-table / indirect dispatch entry.
type DispatchContract struct {
	CallerPC         uint32 `json:"caller_pc,omitempty"`          // e.g. 0x0C:C43F (JSR $C448)
	CallerOpcode     byte   `json:"caller_opcode,omitempty"`      // e.g. 0x20
	DispatcherPC     uint32 `json:"dispatcher_pc"`                // e.g. 0x0C:C448
	DispatcherCallPC uint32 `json:"dispatcher_call_pc,omitempty"` // e.g. 0x0C:C44B (JSL $008781)
	HelperEntryPC    uint32 `json:"helper_entry_pc,omitempty"`    // e.g. 0x00:8781
	HelperExitPC     uint32 `json:"helper_exit_pc,omitempty"`     // e.g. 0x00:8799
	HelperCount      int    `json:"helper_count,omitempty"`       // e.g. 15
	JumpTablePC      uint32 `json:"jump_table_pc"`                // e.g. 0x0C:C44F
	SelectorAddress  uint32 `json:"selector_address"`             // e.g. 0x7E:1E00
	SelectorIndex    uint8  `json:"selector_index"`               // e.g. 0
	ContinuationPC   uint32 `json:"continuation_pc"`              // e.g. 0x0C:C442
	ExpectedEntryS   uint16 `json:"expected_entry_s"`             // e.g. 0x01F7
	ExpectedReturnS  uint16 `json:"expected_return_s"`            // e.g. 0x01F9
	StackReturnBytes []byte `json:"stack_return_bytes"`           // e.g. [0x41, 0xC4]
	InstructionCount int    `json:"instruction_count"`            // e.g. 13
}

// Candidate defines the specification of a mined routine to extract.
// It supports both top-level and nested candidate fields from miner proposals.
type Candidate struct {
	ID                            string              `json:"id"`
	Kind                          string              `json:"kind"`
	Status                        string              `json:"status,omitempty"`
	Start                         uint32              `json:"start,omitempty"`
	End                           uint32              `json:"end,omitempty"`
	Entry                         uint32              `json:"entry"`
	Returns                       []uint32            `json:"returns"`
	RoutineReturns                []uint32            `json:"routine_returns"`
	InstructionCount              int                 `json:"instruction_count"`
	ByteSpan                      int                 `json:"byte_span"`
	EntryContext                  *EntryContext       `json:"entry_context,omitempty"`
	EntryContexts                 []EntryContext      `json:"entry_contexts"`
	Dispatch                      *DispatchContract   `json:"dispatch,omitempty"`
	Connected                     *ConnectedContract  `json:"connected,omitempty"`
	ObservedEntryHits             string              `json:"observed_entry_hits,omitempty"`
	ReportedCompleteExecutions    int                 `json:"reported_complete_executions,omitempty"`
	ReportedInterruptedExecutions int                 `json:"reported_interrupted_executions,omitempty"`
	Proposal                      *Proposal           `json:"proposal,omitempty"`
}

// Proposal represents a miner candidate proposal sub-object.
type Proposal struct {
	Entry            uint32              `json:"entry,omitempty"`
	Start            uint32              `json:"start,omitempty"`
	End              uint32              `json:"end,omitempty"`
	InstructionIDs   []string            `json:"instruction_ids,omitempty"`
	InstructionCount int                 `json:"instruction_count,omitempty"`
	ByteSpan         int                 `json:"byte_span,omitempty"`
	EntryContexts    []EntryContext      `json:"entry_contexts,omitempty"`
	Returns          []uint32            `json:"returns,omitempty"`
	RoutineReturns   []uint32            `json:"routine_returns,omitempty"`
	Dispatch         *DispatchContract   `json:"dispatch,omitempty"`
	Connected        *ConnectedContract  `json:"connected,omitempty"`
}

// EntryContext defines expected CPU status flags at routine entry.
type EntryContext struct {
	E string `json:"e,omitempty"`
	M string `json:"m,omitempty"`
	X string `json:"x,omitempty"`
	C string `json:"c,omitempty"`
}

// Normalize ensures canonical field values whether defined at top-level or under proposal.
func (c *Candidate) Normalize() {
	if c.Entry == 0 && c.Start != 0 {
		c.Entry = c.Start
	}
	if c.ByteSpan == 0 && c.End > c.Start && c.Start > 0 {
		c.ByteSpan = int(c.End - c.Start)
	}
	if len(c.EntryContexts) == 0 && c.EntryContext != nil {
		c.EntryContexts = []EntryContext{*c.EntryContext}
	}
	if c.Proposal != nil {
		if c.Entry == 0 {
			if c.Proposal.Entry != 0 {
				c.Entry = c.Proposal.Entry
			} else if c.Proposal.Start != 0 {
				c.Entry = c.Proposal.Start
			}
		}
		if c.InstructionCount == 0 {
			if c.Proposal.InstructionCount != 0 {
				c.InstructionCount = c.Proposal.InstructionCount
			} else if len(c.Proposal.InstructionIDs) > 0 {
				c.InstructionCount = len(c.Proposal.InstructionIDs)
			}
		}
		if c.ByteSpan == 0 {
			if c.Proposal.ByteSpan != 0 {
				c.ByteSpan = c.Proposal.ByteSpan
			} else if c.Proposal.End > c.Proposal.Start && c.Proposal.Start > 0 {
				c.ByteSpan = int(c.Proposal.End - c.Proposal.Start)
			}
		}
		if len(c.EntryContexts) == 0 && len(c.Proposal.EntryContexts) > 0 {
			c.EntryContexts = c.Proposal.EntryContexts
		}
		if len(c.Returns) == 0 && len(c.Proposal.Returns) > 0 {
			c.Returns = c.Proposal.Returns
		}
		if len(c.RoutineReturns) == 0 && len(c.Proposal.RoutineReturns) > 0 {
			c.RoutineReturns = c.Proposal.RoutineReturns
		}
		if c.Dispatch == nil && c.Proposal.Dispatch != nil {
			c.Dispatch = c.Proposal.Dispatch
		}
		if c.Connected == nil && c.Proposal.Connected != nil {
			c.Connected = c.Proposal.Connected
		}
	}
	if c.Dispatch != nil {
		if c.InstructionCount == 0 && c.Dispatch.InstructionCount != 0 {
			c.InstructionCount = c.Dispatch.InstructionCount
		}
		if c.Kind == "" {
			c.Kind = "dispatch_handler"
		}
	}
	if c.Connected != nil {
		if c.Kind == "" {
			c.Kind = "connected_routine"
		}
		if c.InstructionCount == 0 && len(c.Connected.AllowedPathLengths) > 1 {
			c.InstructionCount = c.Connected.AllowedPathLengths[1]
		}
	}
}

// LoadCandidate decodes a candidate from JSON, handling either a single candidate object
// or a proposals container (e.g. {"candidates": [...]}).
func LoadCandidate(data []byte, targetID string) (Candidate, error) {
	// 1. Try decoding as a proposals container first.
	var container struct {
		Candidates []Candidate `json:"candidates"`
	}
	if err := json.Unmarshal(data, &container); err == nil && len(container.Candidates) > 0 {
		if targetID != "" {
			for _, cand := range container.Candidates {
				if cand.ID == targetID {
					cand.Normalize()
					return cand, nil
				}
			}
			return Candidate{}, fmt.Errorf("candidate %q not found in candidate list", targetID)
		}
		cand := container.Candidates[0]
		cand.Normalize()
		return cand, nil
	}

	// 2. Decode as a single candidate.
	var cand Candidate
	if err := json.Unmarshal(data, &cand); err != nil {
		return Candidate{}, fmt.Errorf("unmarshal candidate: %w", err)
	}
	cand.Normalize()
	return cand, nil
}

// CPUState records the 65816 CPU registers at an entry or exit boundary.
type CPUState struct {
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

// MemoryCell represents a single memory address and value.
type MemoryCell struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

// MemorySource records the verification provenance of an initial memory cell.
type MemorySource struct {
	Address uint32 `json:"address"`
	Source  string `json:"source"`
}

// RoutineCaseV1 represents an emitted snes-routine-case-v1 test vector.
type RoutineCaseV1 struct {
	SchemaVersion           string       `json:"schema_version"`
	CaseID                  string       `json:"case_id"`
	RoutineID               string       `json:"routine_id"`
	EntryPC                 uint32       `json:"entry_pc"`
	ReturnInsnPC            uint32       `json:"return_insn_pc"`
	ROMSHA256               string       `json:"rom_sha256"`
	RunID                   string       `json:"run_id"`
	StreamSHA256            string       `json:"stream_sha256"`
	EngineRevision          string       `json:"engine_revision"`
	Frame                   int          `json:"frame"`
	CallSeq                 uint64       `json:"call_seq"`
	CallPC                  uint32       `json:"call_pc"`
	EntrySeq                uint64       `json:"entry_seq"`
	ExitSeq                 uint64       `json:"exit_seq"`
	ReturnSeq               uint64       `json:"return_seq"`
	ObservedNextPC          uint32       `json:"observed_next_pc"`
	InstructionCount        int          `json:"instruction_count"`
	InitialState            CPUState     `json:"initial_state"`
	ObservedExitState       CPUState     `json:"observed_exit_state"`
	InitialMemory           []MemoryCell `json:"initial_memory"`
	ObservedWrites          []MemoryCell `json:"observed_writes"`
	ObservedEffectsCaptured bool         `json:"observed_effects_captured"`
	Evidence                CaseEvidence `json:"evidence"`
}

// CaseEvidence records the input files and receipts used to derive a case.
type CaseEvidence struct {
	Label               string         `json:"label"`
	Corpus              string         `json:"corpus"`
	Fixture             FixtureRef     `json:"fixture"`
	Capture             StreamRef      `json:"capture"`
	History             StreamRef      `json:"history"`
	Inputs              *FileRef       `json:"inputs,omitempty"`
	StartBoundary       string         `json:"start_boundary,omitempty"`
	Checkpoint          *CheckpointRef `json:"checkpoint,omitempty"`
	InitialMemorySource []MemorySource `json:"initial_memory_source"`
}

// FileRef points to a referenced file and its hash.
type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// StreamRef points to a stream file, its hash, receipt, and summary.
type StreamRef struct {
	Path    string   `json:"path"`
	SHA256  string   `json:"sha256"`
	Receipt *FileRef `json:"receipt,omitempty"`
	Summary *FileRef `json:"summary,omitempty"`
}

// FixtureRef describes the retained fixture trace stream.
type FixtureRef struct {
	Path               string   `json:"path"`
	SHA256             string   `json:"sha256"`
	DecompressedSHA256 string   `json:"decompressed_sha256"`
	EngineRevision     string   `json:"engine_revision"`
	Receipt            *FileRef `json:"receipt,omitempty"`
	Summary            *FileRef `json:"summary,omitempty"`
}

// CheckpointRef points to a savestate checkpoint.
type CheckpointRef struct {
	Path                  string   `json:"path"`
	SHA256                string   `json:"sha256"`
	AbsoluteFrame         int      `json:"absolute_frame,omitempty"`
	PowerOnHistorySummary *FileRef `json:"power_on_history_summary,omitempty"`
}

// ProposedTrustRoot describes the proposed trust root manifest.
type ProposedTrustRoot struct {
	Label      string         `json:"label"`
	Corpus     string         `json:"corpus"`
	ROMSHA256  string         `json:"rom_sha256"`
	Fixture    FixtureRef     `json:"fixture"`
	Capture    StreamRef      `json:"capture"`
	History    StreamRef      `json:"history"`
	Checkpoint *CheckpointRef `json:"checkpoint,omitempty"`
	Inputs     *FileRef       `json:"inputs,omitempty"`
	Status     string         `json:"status"`
}

// ExtractionReceipt records derivation metadata for reproducibility and complete-vs-hit accounting.
type ExtractionReceipt struct {
	RoutineID          string         `json:"routine_id"`
	ROMSHA256          string         `json:"rom_sha256"`
	FixtureSHA256      string         `json:"fixture_sha256"`
	FixtureDecSHA256   string         `json:"fixture_decompressed_sha256"`
	CaptureSHA256      string         `json:"capture_sha256"`
	HistorySHA256      string         `json:"history_sha256"`
	TotalEntryHits     int            `json:"total_entry_hits"`
	CompleteExecutions int            `json:"complete_executions"`
	RejectedExecutions int            `json:"rejected_executions"`
	RefusalReasons     map[string]int `json:"refusal_reasons,omitempty"`
	NegativeControls   int            `json:"negative_controls"`
	SwapControls       int            `json:"swap_controls"`
}
