package parity

import (
	"os"
	"testing"
)

type pto2STA91CadenceReduction struct {
	intervals               int
	matchedStoreIntervals   int
	matchedControlIntervals int
	refreshAttributions     int
	rawDurationDelta        int64
	normalizedDurationDelta int64
	unclassified            []pto2STA91CadenceInterval
}

type pto2STA91CadenceInterval struct {
	goStart     pto2CPUCompareRow
	refStart    pto2CPUCompareRow
	goNext      pto2CPUCompareRow
	refNext     pto2CPUCompareRow
	goDuration  uint64
	refDuration uint64
}

func TestPTO2STA91CadenceFramePhaseReducer(t *testing.T) {
	goCPUPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refCPUPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	goBoundaryPath := os.Getenv(pto2GoBoundaryTraceEnv)
	refBoundaryPath := os.Getenv(pto2ReferenceCPUMulTraceEnv)
	if goCPUPath == "" || refCPUPath == "" || goBoundaryPath == "" || refBoundaryPath == "" {
		t.Skipf("set %s, %s, %s, and %s",
			pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv,
			pto2GoBoundaryTraceEnv, pto2ReferenceCPUMulTraceEnv)
	}

	goRaw, err := os.ReadFile(goCPUPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refCPUPath)
	if err != nil {
		t.Fatal(err)
	}
	goBoundaryRaw, err := os.ReadFile(goBoundaryPath)
	if err != nil {
		t.Fatal(err)
	}
	refBoundaryRaw, err := os.ReadFile(refBoundaryPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	reduction := reducePTO2STA91Cadence(t, goTrace, refTrace)
	if reduction.intervals == 0 {
		t.Fatal("no C0:8151 -> C0:8153 STA ($08),Y intervals before uploader VMADDR $2098")
	}
	if reduction.matchedStoreIntervals == 0 {
		t.Fatal("no matching 50-cycle C0:8151 -> C0:8153 store intervals before uploader VMADDR $2098")
	}
	if len(reduction.unclassified) != 0 {
		first := reduction.unclassified[0]
		t.Fatalf("unclassified C0:8151 cadence interval: go %s -> %s duration=%d; ref %s -> %s duration=%d",
			pto2CPUCompareRowLabel(first.goStart),
			pto2CPUCompareRowLabel(first.goNext),
			first.goDuration,
			pto2CPUCompareRowLabel(first.refStart),
			pto2CPUCompareRowLabel(first.refNext),
			first.refDuration)
	}
	if reduction.normalizedDurationDelta != 0 {
		t.Fatalf("normalized C0:8151 cadence contribution = %d cycles, want 0",
			reduction.normalizedDurationDelta)
	}

	goDmas := readPTO2GoUploaderDMAs(t, goBoundaryRaw)
	refSummary := readPTO2ReferenceCadenceTrace(t, refBoundaryRaw)
	matches := matchPTO2UploaderCadence(goDmas, refSummary.dmas)
	first, ok := findPTO2UploaderCadenceMatch(matches, 0x2098)
	if !ok {
		t.Fatal("shared uploader cadence lacks first VMADDR $2098")
	}
	missing, ok := findPTO2UploaderCadenceMatch(matches, 0x2168)
	if !ok {
		t.Fatal("shared uploader cadence lacks frame-split VMADDR $2168")
	}
	phaseDelta := int64(missing.goDMA.cycles) - int64(missing.refDMA.cycles)
	if phaseDelta <= 0 {
		t.Fatalf("frame-split VMADDR $2168 delta = %d, want Go later than reference", phaseDelta)
	}

	t.Logf("PTO2 C0:8151 reducer artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goCPUPath, hashBytes(goRaw), refCPUPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-phase artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goBoundaryPath, hashBytes(goBoundaryRaw), refBoundaryPath, hashBytes(refBoundaryRaw))
	t.Logf("PTO2 C0:8151 reducer: intervals=%d matched_store=%d refresh_attribution=%d raw_delta=%d normalized_delta=%d",
		reduction.intervals, reduction.matchedStoreIntervals,
		reduction.refreshAttributions, reduction.rawDurationDelta,
		reduction.normalizedDurationDelta)
	t.Logf("PTO2 C0:8151 reducer: matched_control_with_refresh=%d",
		reduction.matchedControlIntervals)
	t.Logf("PTO2 uploader cadence: first shared VMADDR $2098 delta=%d; frame-split VMADDR $2168 delta=%d",
		int64(first.goDMA.cycles)-int64(first.refDMA.cycles), phaseDelta)
	t.Logf("PTO2 conclusion: C0:8151 store/control cadence normalizes to zero cumulative cycles before $2098, so it does not explain the $2168 frame-phase split")
}

func reducePTO2STA91Cadence(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2STA91CadenceReduction {
	t.Helper()
	goStart, refStart, ok := alignPTO2CPUTraceInstructions(goTrace, refTrace)
	if !ok {
		t.Fatalf("no shared PTO2 instruction signature near Go frame %d before VMADDR $2098", goTrace.firstFrame)
	}
	baseDelta := int64(goTrace.instructions[goStart].cycles) - int64(refTrace.instructions[refStart].cycles)

	var out pto2STA91CadenceReduction
	for gi, ri := goStart, refStart; gi+1 < len(goTrace.instructions) && ri+1 < len(refTrace.instructions); gi, ri = gi+1, ri+1 {
		goRow := goTrace.instructions[gi]
		refRow := refTrace.instructions[ri]
		if !samePTO2CPUInstruction(goRow, refRow) {
			break
		}
		if goRow.pb != 0xc0 || goRow.pc != 0x8151 || goRow.opcode != 0x91 {
			continue
		}

		goNext := goTrace.instructions[gi+1]
		refNext := refTrace.instructions[ri+1]
		if goNext.pb != 0xc0 || goNext.pc != 0x8153 || goNext.opcode != 0xc8 ||
			refNext.pb != 0xc0 || refNext.pc != 0x8153 || refNext.opcode != 0xc8 {
			continue
		}

		interval := pto2STA91CadenceInterval{
			goStart:     goRow,
			refStart:    refRow,
			goNext:      goNext,
			refNext:     refNext,
			goDuration:  goNext.cycles - goRow.cycles,
			refDuration: refNext.cycles - refRow.cycles,
		}
		out.intervals++
		out.rawDurationDelta += int64(interval.goDuration) - int64(interval.refDuration)

		if interval.goDuration == interval.refDuration {
			if interval.goDuration == 50 {
				out.matchedStoreIntervals++
			} else {
				out.matchedControlIntervals++
			}
			continue
		}
		attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, goRow, refRow, baseDelta)
		if ok {
			goPrev, _ := previousPTO2CPUInstruction(goTrace, goRow)
			refPrev, _ := previousPTO2CPUInstruction(refTrace, refRow)
			out.refreshAttributions++
			out.normalizedDurationDelta += int64(attr.next.cycles-goPrev.cycles) -
				int64(attr.refNext.cycles-refPrev.cycles)
			continue
		}
		out.unclassified = append(out.unclassified, interval)
		out.normalizedDurationDelta += int64(interval.goDuration) - int64(interval.refDuration)
	}
	return out
}
