// Package callsummary analyzes 65816 callee subroutines to derive conservative
// register preservation contracts and caller-callee stack discipline summaries.
//
// Basic block lifting and interprocedural analysis require knowing which registers
// (A, X, Y, S, DP, DB, PB) and processor status flags (M, X, C, Z, N, I, D) are
// preserved across subroutine calls, which are clobbered, and which are guaranteed
// to hold specific constant values.
//
// The primary entry points are [AnalyzeInstructions], [AnalyzeBlock], and
// [AnalyzeRoutine].
package callsummary
