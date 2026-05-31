package scheduler

// SyncMode describes why a target thread is being advanced.
type SyncMode = uint8

const (
	SyncPostCPU SyncMode = iota
	SyncPortRead
	SyncPortWrite
	SyncSafety
)

// YieldReason describes why a yield-aware thread stopped before its target.
type YieldReason = uint8

const (
	YieldNone YieldReason = iota
	YieldAPUPortWrite
)

// SyncResult describes the result of a yield-aware synchronization.
type SyncResult = struct {
	Yield YieldReason
}

// YieldingThread can stop synchronization at an externally visible boundary.
type YieldingThread interface {
	RunUntil(masterCycles, masterFrequency, targetFrequency uint64, mode SyncMode) SyncResult
}
