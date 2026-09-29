package coverage

import "github.com/tmc/snes/internal/recovery"

// Quality indicates the completeness and confidence of an observation count.
type Quality string

const (
	QualityComplete    Quality = "complete"
	QualityFiltered    Quality = "filtered"
	QualityTruncated   Quality = "truncated"
	QualityUnavailable Quality = "unavailable"
	QualityUnknown     Quality = "unknown"
)

// Event records a single successfully retired CPU instruction dispatch.
// Events are inputs to a [Builder]; the index stores only aggregated [Site]s.
type Event struct {
	Seq           uint64
	Frame         uint64
	Address       uint32
	Offset        uint32
	HasROMOffset  bool
	InstructionID string
	Context       recovery.Context
}

// Site aggregates all executions of one instruction within one run.
//
// Frames and FrameHits are parallel slices: FrameHits[i] is the number of
// executions during frame Frames[i]. Frames is strictly increasing and only
// lists frames with at least one execution.
type Site struct {
	RunID         string           `json:"run_id"`
	InstructionID string           `json:"instruction_id"`
	Address       uint32           `json:"address"`
	Offset        uint32           `json:"offset,omitempty"`
	HasROMOffset  bool             `json:"has_rom_offset,omitempty"`
	Context       recovery.Context `json:"context"`
	Hits          uint64           `json:"hits"`
	FirstSeq      uint64           `json:"first_seq"`
	LastSeq       uint64           `json:"last_seq"`
	FirstFrame    uint64           `json:"first_frame"`
	LastFrame     uint64           `json:"last_frame"`
	Frames        []uint64         `json:"frames"`
	FrameHits     []uint64         `json:"frame_hits"`
}

// Gap records a range of excluded execution sequence numbers due to filtering.
type Gap struct {
	FirstSeq uint64 `json:"first_seq"`
	LastSeq  uint64 `json:"last_seq"`
	Reason   string `json:"reason"`
}

// RunInfo records metadata for an execution run contributing to coverage.
// EventCount is the total number of recorded executions; MinFrame and
// MaxFrame bound the frames in which they occurred.
type RunInfo struct {
	ID         string `json:"id"`
	ROM_SHA256 string `json:"rom_sha256"`
	EngineRev  string `json:"engine_revision,omitempty"`
	Outcome    string `json:"outcome"`
	EventCount uint64 `json:"event_count"`
	StreamSHA  string `json:"stream_sha256"`
	MinFrame   uint64 `json:"min_frame"`
	MaxFrame   uint64 `json:"max_frame"`
	IsComplete bool   `json:"is_complete"`
	Gaps       []Gap  `json:"gaps,omitempty"`
}

// Filter specifies constraints for coverage queries.
type Filter struct {
	RunIDs        []string
	FrameStart    *uint64 // inclusive A
	FrameEnd      *uint64 // exclusive B for [A, B)
	Address       *uint32 // CPU bus address
	Offset        *uint32 // physical ROM start offset
	InstructionID string
}

// CountSummary reports hit frequency and quality for an execution site,
// restricted to the query's frame interval.
//
// FirstFrame and LastFrame are the first and last frames in the interval with
// at least one execution; they are always exact.
//
// FirstSeq and LastSeq are exact sequence numbers of the first and last
// execution in the interval. Because only per-frame counts are stored, they
// are reported only when every contributing site's first (or last) execution
// overall falls inside the interval, and omitted otherwise. Sequence numbers
// are per run; a summary combining several runs reports the minimum and
// maximum over them.
type CountSummary struct {
	Hits       string  `json:"hits"` // decimal string preserving integer precision
	Quality    Quality `json:"quality"`
	ExactZero  bool    `json:"exact_zero,omitempty"`
	FirstSeq   uint64  `json:"first_seq,omitempty"`
	LastSeq    uint64  `json:"last_seq,omitempty"`
	FirstFrame *uint64 `json:"first_frame,omitempty"`
	LastFrame  *uint64 `json:"last_frame,omitempty"`
}

// BinCoverage represents aggregated hits across a contiguous ROM bin.
type BinCoverage struct {
	BinIndex     uint32  `json:"bin_index"`
	StartOffset  uint32  `json:"start_offset"`
	Size         uint32  `json:"size"`
	TotalHits    string  `json:"total_hits"`
	HottestStart uint32  `json:"hottest_start,omitempty"`
	MaxStartHits string  `json:"max_start_hits,omitempty"`
	Quality      Quality `json:"quality"`
}

// CoverageResult encapsulates the outcome of a coverage query.
type CoverageResult struct {
	ProjectROMHash string                  `json:"project_rom_hash"`
	SelectedRuns   []string                `json:"selected_runs"`
	FrameInterval  string                  `json:"frame_interval,omitempty"`
	TotalHits      string                  `json:"total_hits"`
	Quality        Quality                 `json:"quality"`
	ByOffset       map[uint32]CountSummary `json:"by_offset,omitempty"`
	ByAddress      map[uint32]CountSummary `json:"by_address,omitempty"`
	ByInstruction  map[string]CountSummary `json:"by_instruction,omitempty"`
	Bins           []BinCoverage           `json:"bins,omitempty"`
	Limitations    []string                `json:"limitations,omitempty"`
}
