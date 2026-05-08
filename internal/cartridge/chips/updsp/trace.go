package updsp

import (
	"sync"
	"sync/atomic"
)

// TraceSample records one CPU-side IO access against the DR/SR window.
type TraceSample struct {
	Addr    uint32
	Value   uint8
	IsWrite bool
	IsDR    bool
}

var (
	traceEnabled atomic.Bool
	traceReads   atomic.Uint64
	traceWrites  atomic.Uint64

	traceMu      sync.Mutex
	traceSamples []TraceSample
	traceLimit   = 64
)

// EnableTrace turns on uPDSP IO tracing. While enabled, every Mapper.Read and
// Mapper.Write that hits the DR/SR window increments a counter and (up to a
// fixed limit) records the access for later inspection. Default off; trace
// data is process-global so tests must call ResetTrace before use.
func EnableTrace() { traceEnabled.Store(true) }

// DisableTrace turns tracing back off. Counters and samples are preserved.
func DisableTrace() { traceEnabled.Store(false) }

// ResetTrace clears counters and samples without changing the enabled flag.
func ResetTrace() {
	traceReads.Store(0)
	traceWrites.Store(0)
	traceMu.Lock()
	traceSamples = traceSamples[:0]
	traceMu.Unlock()
}

// TraceSnapshot is a copy of the current trace counters and recorded samples.
type TraceSnapshot struct {
	Reads   uint64
	Writes  uint64
	Samples []TraceSample
}

// Snapshot returns a copy of the current trace state.
func Snapshot() TraceSnapshot {
	s := TraceSnapshot{
		Reads:  traceReads.Load(),
		Writes: traceWrites.Load(),
	}
	traceMu.Lock()
	s.Samples = append([]TraceSample(nil), traceSamples...)
	traceMu.Unlock()
	return s
}

func recordTrace(addr uint32, val uint8, isWrite, isDR bool) {
	if isWrite {
		traceWrites.Add(1)
	} else {
		traceReads.Add(1)
	}
	traceMu.Lock()
	if len(traceSamples) < traceLimit {
		traceSamples = append(traceSamples, TraceSample{
			Addr:    addr,
			Value:   val,
			IsWrite: isWrite,
			IsDR:    isDR,
		})
	}
	traceMu.Unlock()
}
