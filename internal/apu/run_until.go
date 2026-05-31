package apu

// SyncMode describes why the APU is being advanced.
type SyncMode uint8

const (
	SyncPostCPU SyncMode = iota
	SyncPortRead
	SyncPortWrite
	SyncSafety
)

// YieldReason describes why RunUntil stopped before its target cycle.
type YieldReason uint8

const (
	YieldNone YieldReason = iota
	YieldAPUPortWrite
)

// RunResult describes the result of a yield-capable APU run.
type RunResult struct {
	Yield YieldReason
}

// RunUntil advances the APU until target time or an APU-local yield point.
func (a *APU) RunUntil(masterCycles, masterFrequency, apuFrequency uint64, mode SyncMode) RunResult {
	target := apuTargetCycles(masterCycles, masterFrequency, apuFrequency)
	for a.cycles < target {
		if mode == SyncPostCPU && a.pendingOutPortWriteWouldFlushBy(target) {
			return RunResult{Yield: YieldAPUPortWrite}
		}
		a.Run()
		if mode == SyncPostCPU && a.pendingOutPortWriteWouldFlushBy(target) {
			return RunResult{Yield: YieldAPUPortWrite}
		}
	}
	return RunResult{}
}

func apuTargetCycles(masterCycles, masterFrequency, apuFrequency uint64) uint64 {
	if masterFrequency == 0 || apuFrequency == 0 || masterFrequency == apuFrequency {
		return masterCycles
	}
	if masterCycles > ^uint64(0)/apuFrequency {
		return ^uint64(0)
	}
	return masterCycles * apuFrequency / masterFrequency
}

func (a *APU) pendingOutPortWriteWouldFlushBy(target uint64) bool {
	if a.pendingOutPortMask == 0 || a.pending == 0 {
		return false
	}
	if a.cycles+uint64(a.pending) > target {
		return false
	}
	return a.hasUnpublishedOutPortWrite()
}

func (a *APU) hasUnpublishedOutPortWrite() bool {
	for i := 0; i < 4; i++ {
		if a.pendingOutPortMask&(1<<uint(i)) != 0 && a.OutPorts[i] != a.pendingOutPorts[i] {
			return true
		}
	}
	return false
}
