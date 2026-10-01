// Package extractor provides generic captured-case extraction for SNES routines.
//
// It joins CPU execution records, data-bus memory accesses, and prior write history
// into deterministic snes-routine-case-v1 replay specifications with dual fixture
// digest verification, complete-vs-hit accounting, stack semantics verification,
// and negative rejection controls.
//
// Streaming hashing, gzip decompression, and line decoding operate with bounded O(1)
// memory buffers. In-memory event structures scale linearly with event count, bounded
// by MaxCaptureEvents and MaxHistoryEvents (failing closed if exceeded).
package extractor
