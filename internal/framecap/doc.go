// Package framecap stores completed SNES frames alongside a runtime
// trace.
//
// A capture directory holds:
//
//	frames.jsonl          versioned manifest: a header, then one record per frame
//	frames.receipt.json   written last; a manifest without it is incomplete
//	blobs/<id>.gz         gzip-compressed native pixels, one per distinct frame
//	png/<number>.png      optional RGB888 exports of selected frames
//
// Every completed frame gets a manifest record, whether or not its
// pixels are stored. Stored pixels are exact: little-endian BGR555 at
// the frame's native dimensions. A frame's content ID is the SHA-256
// of its format, dimensions and pixels, so identical frames share one
// blob. PNG exports are for viewing only and are never the provenance
// source. Readers use Open and Capture.Pixels, which hide the storage
// encoding named in the header.
//
// # Timing
//
// A frame is captured when the beam first enters vertical blank after
// its last visible line is rendered. Cycles are master clocks on the
// CPU cycle timeline of the trace. Frame N covers the half-open
// interval [Start, End), where End is the Start of frame N+1; its
// pixels were complete at VBlank, so [Start, VBlank) is its display
// period. The last frame of a run has no End.
//
// Seq bounds are the first trace sequence numbers whose entry cycle is
// at or after each boundary, so frame N's observations are those with
// SeqStart <= seq < SeqEnd. They are computed from cycles, not from the
// host order of events, because the PPU is synchronized lazily. A bound
// is absent if it fell outside the recent history the Writer retains.
//
// # Rendering
//
// Frames are 256 pixels wide, or 512 if any line was rendered in BG
// mode 5 or 6; then 256-pixel lines are doubled and Record.HiresLines
// lists the hires lines. Height is 224, or 240 with overscan. The
// renderer draws one field per frame in interlace mode; Record.Field
// and Record.Interlace identify it, and fields are not woven.
// Pseudo-hires is not rendered and is flagged by Record.PseudoHires.
// A frame resumed from a state load has a nonzero FirstLine.
//
// Use Create and Writer to record frames and Open to read them.
package framecap
