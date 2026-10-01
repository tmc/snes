// Package workflow records bounded recovery work in a durable task journal.
// Run selects one pinned candidate, writes a capture request, accepts an explicit
// pinned evidence delivery, extracts cases, and waits for an operator-reviewed
// policy before calling the existing recovery queue. It never invokes a capture
// producer or discovers authority from a proposed trust root.
//
// Journal generations and artifact hashes support deterministic resume. A waiting
// state is actionable missing evidence; qualification concerns only persisted
// selected CPU/WRAM samples. The Unix OS lock releases when a process exits.
package workflow
