package apu

// SyncMode describes why the APU is being advanced.
type SyncMode = uint8

const (
	SyncPostCPU SyncMode = iota
	SyncPortRead
	SyncPortWrite
	SyncSafety
)

// YieldReason describes why RunUntil stopped before its target cycle.
type YieldReason = uint8

const (
	YieldNone YieldReason = iota
	YieldAPUPortWrite
)

// RunResult describes the result of a yield-capable APU run.
type RunResult = struct {
	Yield YieldReason
}

// RunUntil advances the APU until target time or an APU-local yield point.
func (a *APU) RunUntil(masterCycles, masterFrequency, apuFrequency uint64, mode SyncMode) RunResult {
	target := apuTargetCycles(masterCycles, masterFrequency, apuFrequency)
	return a.RunUntilTarget(target, mode)
}

// RunUntilTarget advances the APU until targetCycles or an APU-local yield point.
func (a *APU) RunUntilTarget(target uint64, mode SyncMode) RunResult {
	for {
		if a.portAssignmentPending() {
			if (mode == SyncPostCPU || mode == SyncPortRead) && a.cycles >= target {
				return RunResult{Yield: YieldAPUPortWrite}
			}
			a.resumePortAssignment()
		}
		if a.cycles >= target {
			break
		}
		if a.shouldYieldBeforeOutPortPublish(target, mode) {
			return RunResult{Yield: YieldAPUPortWrite}
		}
		a.runCycle()
		if a.shouldYieldBeforeOutPortPublish(target, mode) {
			return RunResult{Yield: YieldAPUPortWrite}
		}
	}
	return RunResult{}
}

func (a *APU) shouldYieldBeforeOutPortPublish(target uint64, mode SyncMode) bool {
	if mode != SyncPostCPU && mode != SyncPortRead {
		return false
	}
	return a.pendingOutPortWriteWouldFlushBy(target)
}

func apuTargetCycles(masterCycles, masterFrequency, apuFrequency uint64) uint64 {
	if masterFrequency == 0 || apuFrequency == 0 || masterFrequency == apuFrequency {
		return masterCycles
	}
	if masterCycles > (^uint64(0)-(masterFrequency-1))/apuFrequency {
		return ^uint64(0)
	}
	return (masterCycles*apuFrequency + masterFrequency - 1) / masterFrequency
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
