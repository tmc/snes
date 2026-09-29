package framecap

import "github.com/tmc/snes/internal/trace"

// SchemaVersion is the manifest schema.
const SchemaVersion = 1

// File names within a capture directory.
const (
	ManifestName = "frames.jsonl"
	ReceiptName  = "frames.receipt.json"
	blobDir      = "blobs"
	pngDir       = "png"
)

// A Header is the first manifest line (kind "frame_run").
//
// Run is the run header of the trace captured in the same run, so the
// two artifacts share one run identity.
type Header struct {
	Schema    int            `json:"schema"`
	Kind      string         `json:"kind"`
	Run       *trace.RunInfo `json:"run"`
	Trace     string         `json:"trace,omitempty"`
	Storage   string         `json:"storage"`
	Selection Selection      `json:"selection"`
	Limits    Limits         `json:"limits"`
}

// Storage encodings.
const StorageGzipNative = "gzip-bgr555le"

// Selection says which frames have stored pixels: frame numbers at or
// after From that are multiples of Every.
type Selection struct {
	From  int `json:"from"`
	Every int `json:"every"`
}

// Limits bound stored content. Zero means unlimited.
type Limits struct {
	Frames int   `json:"frames,omitempty"`
	Bytes  int64 `json:"bytes,omitempty"`
}

// A Record describes one completed frame (kind "frame").
//
// Content fields are set only when Stored is true. End and SeqEnd are
// absent for the last frame of a run, whose end was not reached.
type Record struct {
	Kind   string `json:"kind"`
	Index  int    `json:"index"`
	Number int    `json:"number"`
	// TraceFrame is the trace's frame counter ("frame" in trace
	// records) while the frame completed.
	TraceFrame *int `json:"trace_frame,omitempty"`

	Start  uint64  `json:"start"`
	VBlank uint64  `json:"vblank"`
	End    *uint64 `json:"end,omitempty"`

	SeqStart  *uint64 `json:"seq_start,omitempty"`
	SeqVBlank *uint64 `json:"seq_vblank,omitempty"`
	SeqEnd    *uint64 `json:"seq_end,omitempty"`

	Stored bool   `json:"stored"`
	Skip   string `json:"skip,omitempty"` // why pixels were not stored

	Field       int    `json:"field"`
	Interlace   bool   `json:"interlace"`
	Overscan    bool   `json:"overscan"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Format      string `json:"format,omitempty"`
	HiresLines  []Span `json:"hires_lines,omitempty"`
	PseudoHires bool   `json:"pseudo_hires,omitempty"`
	FirstLine   int    `json:"first_line,omitempty"`

	ContentID string `json:"content_id,omitempty"`
	Blob      string `json:"blob,omitempty"`
	// DupOf is the Index of the first record with the same content.
	DupOf *int `json:"dup_of,omitempty"`

	PNG       string `json:"png,omitempty"`
	PNGSHA256 string `json:"png_sha256,omitempty"`
}

// A Span is the inclusive line range [First, Last].
type Span struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

// Skip reasons.
const (
	SkipSelection = "selection"
	SkipLimit     = "limit"
	SkipError     = "error" // an earlier write failed
)

// A Receipt is written after the manifest is closed.
type Receipt struct {
	Schema           int           `json:"schema"`
	Outcome          trace.Outcome `json:"outcome"`
	Frames           int           `json:"frames"`
	Stored           int           `json:"stored"`
	Unique           int           `json:"unique"`
	Bytes            int64         `json:"bytes"`
	ManifestSHA256   string        `json:"manifest_sha256"`
	TruncationReason string        `json:"truncation_reason,omitempty"`
	Error            string        `json:"error,omitempty"`
}

func spans(lines []bool) []Span {
	var s []Span
	for y, on := range lines {
		if !on {
			continue
		}
		if n := len(s); n > 0 && s[n-1].Last == y-1 {
			s[n-1].Last = y
			continue
		}
		s = append(s, Span{y, y})
	}
	return s
}
