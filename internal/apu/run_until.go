package apu

import "math/bits"

// SyncMode describes why the APU is being advanced.
type SyncMode = uint8

const (
	SyncPostCPU SyncMode = iota
	SyncPortRead
	SyncPortWrite
	SyncSafety
	// SyncBeforeCPU completes zero-time events through a strictly earlier target.
	SyncBeforeCPU
)

// YieldReason describes why RunUntil stopped before its target cycle.
type YieldReason = uint8

const (
	YieldNone YieldReason = iota
	YieldAPUPortWrite
	YieldAPUPortRead
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
		if a.inputOp.waiting || a.dummyReadWaiting {
			if a.cycles > target || a.cycles == target && mode != SyncBeforeCPU {
				return RunResult{Yield: YieldAPUPortRead}
			}
			a.resumeInputRead()
		}
		if a.portAssignmentPending() {
			if (mode == SyncPostCPU || mode == SyncPortRead) && a.cycles >= target || mode == SyncBeforeCPU && a.cycles > target {
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
	hi, lo := bits.Mul64(masterCycles, apuFrequency)
	if hi >= masterFrequency {
		return ^uint64(0)
	}
	q, r := bits.Div64(hi, lo, masterFrequency)
	if q == ^uint64(0) {
		return q
	}
	if r != 0 {
		q++
	}
	return q
}

func (a *APU) pendingOutPortWriteWouldFlushBy(target uint64) bool {
	if a.pendingOutPortMask == 0 || a.pending == 0 {
		return false
	}
	remaining := 2*uint64(a.pending) - a.cycles%2
	if a.cycles > target || remaining > target-a.cycles {
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
