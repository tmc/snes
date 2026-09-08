package scheduler

// SyncMode describes why a target thread is being advanced.
type SyncMode = uint8

const (
	SyncPostCPU SyncMode = iota
	SyncPortRead
	SyncPortWrite
	SyncSafety
	// SyncBeforeCPU completes zero-time events through a strictly earlier target.
	SyncBeforeCPU
)

// YieldReason describes why a yield-aware thread stopped before its target.
type YieldReason = uint8

const (
	YieldNone YieldReason = iota
	YieldAPUPortWrite
	YieldAPUPortRead
)

// SyncResult describes the result of a yield-aware synchronization.
type SyncResult = struct {
	Yield YieldReason
}

// YieldingThread can stop synchronization at an externally visible boundary.
type YieldingThread interface {
	RunUntilTarget(targetCycles uint64, mode SyncMode) SyncResult
}
