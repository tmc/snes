// Package parity contains reference-comparison and release-gate tests for the
// SNES emulator.
//
// The default test set must compile and run without commercial ROMs or local
// reference artifacts:
//
//	go test ./internal/parity
//
// Diagnostic probes that need local ROMs, patched cores, long traces, or
// investigator-only expectations are guarded by an environment variable, the
// parity_diagnostic build tag, or both:
//
//	go test -tags parity_diagnostic ./internal/parity -run TestName
package parity
