package parity

import (
	"os"
	"testing"
)

type pto2Post2098UploaderStep struct {
	from    pto2UploaderCadenceMatch
	to      pto2UploaderCadenceMatch
	goStep  uint64
	refStep uint64
	extra   int64
	toDelta int64
}

func TestPTO2Post2098UploaderCadenceReducer(t *testing.T) {
	goPath := os.Getenv(pto2GoBoundaryTraceEnv)
	refPath := os.Getenv(pto2ReferenceCPUMulTraceEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoBoundaryTraceEnv, pto2ReferenceCPUMulTraceEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goDmas := readPTO2GoUploaderDMAs(t, goRaw)
	refSummary := readPTO2ReferenceCadenceTrace(t, refRaw)
	matches := matchPTO2UploaderCadence(goDmas, refSummary.dmas)
	window := pto2UploaderCadenceWindow(t, matches, 0x2098, 0x2168)
	steps := pto2Post2098UploaderSteps(t, window)

	first := window[0]
	split := window[len(window)-1]
	firstDelta := int64(first.goDMA.cycles) - int64(first.refDMA.cycles)
	splitDelta := int64(split.goDMA.cycles) - int64(split.refDMA.cycles)
	if firstDelta != 62754 {
		t.Fatalf("first shared VMADDR $2098 delta = %d, want 62754", firstDelta)
	}
	if splitDelta != 112646 {
		t.Fatalf("frame-split VMADDR $2168 delta = %d, want 112646", splitDelta)
	}

	var totalExtra int64
	minExtra := steps[0].extra
	maxExtra := steps[0].extra
	preFrameMinExtra := steps[0].extra
	preFrameMaxExtra := steps[0].extra
	for _, step := range steps {
		if step.extra <= 0 {
			t.Fatalf("post-$2098 uploader step %04X->%04X extra = %d, want positive Go delay",
				step.from.vmaddr, step.to.vmaddr, step.extra)
		}
		totalExtra += step.extra
		if step.extra < minExtra {
			minExtra = step.extra
		}
		if step.extra > maxExtra {
			maxExtra = step.extra
		}
		if step.to.vmaddr <= 0x2160 {
			if step.extra < preFrameMinExtra {
				preFrameMinExtra = step.extra
			}
			if step.extra > preFrameMaxExtra {
				preFrameMaxExtra = step.extra
			}
		}
	}
	if totalExtra != splitDelta-firstDelta {
		t.Fatalf("post-$2098 accumulated step extra = %d, want split-first delta %d",
			totalExtra, splitDelta-firstDelta)
	}

	frameCrossing := steps[len(steps)-1]
	if frameCrossing.from.vmaddr != 0x2160 || frameCrossing.to.vmaddr != 0x2168 {
		t.Fatalf("last post-$2098 step is %04X->%04X, want 2160->2168",
			frameCrossing.from.vmaddr, frameCrossing.to.vmaddr)
	}
	if frameCrossing.extra > preFrameMaxExtra+128 || frameCrossing.extra < preFrameMinExtra-128 {
		t.Fatalf("frame-crossing step extra = %d outside pre-frame range %d..%d",
			frameCrossing.extra, preFrameMinExtra, preFrameMaxExtra)
	}
	if maxExtra-minExtra > 128 {
		t.Fatalf("post-$2098 per-uploader extra range = %d..%d, want a stable repeated cadence loss",
			minExtra, maxExtra)
	}

	t.Logf("PTO2 post-$2098 reducer artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 post-$2098 uploader cadence: matches=%d first_delta=%d split_delta=%d accumulated_extra=%d",
		len(window), firstDelta, splitDelta, totalExtra)
	t.Logf("PTO2 post-$2098 per-uploader extra range: min=%d max=%d pre_frame_min=%d pre_frame_max=%d",
		minExtra, maxExtra, preFrameMinExtra, preFrameMaxExtra)
	t.Logf("PTO2 first post-$2098 step: %04X->%04X Go=%d Ref=%d extra=%d to_delta=%d",
		steps[0].from.vmaddr, steps[0].to.vmaddr,
		steps[0].goStep, steps[0].refStep, steps[0].extra, steps[0].toDelta)
	t.Logf("PTO2 frame-crossing post-$2098 step: %04X->%04X Go=%d Ref=%d extra=%d to_delta=%d",
		frameCrossing.from.vmaddr, frameCrossing.to.vmaddr,
		frameCrossing.goStep, frameCrossing.refStep, frameCrossing.extra, frameCrossing.toDelta)
	t.Logf("PTO2 post-$2098 conclusion: the $2098->$2168 loss begins at the first following uploader burst and repeats every burst; the $2160->$2168 frame crossing is not a singular extra jump")
}

func pto2UploaderCadenceWindow(t *testing.T, matches []pto2UploaderCadenceMatch, lo, hi uint16) []pto2UploaderCadenceMatch {
	t.Helper()
	var window []pto2UploaderCadenceMatch
	for _, match := range matches {
		if match.vmaddr < lo || match.vmaddr > hi {
			continue
		}
		window = append(window, match)
	}
	want := int((hi-lo)/8) + 1
	if len(window) != want {
		t.Fatalf("uploader cadence window %04X..%04X has %d shared DMA rows, want %d",
			lo, hi, len(window), want)
	}
	for i, match := range window {
		wantVMAddr := lo + uint16(i*8)
		if match.vmaddr != wantVMAddr {
			t.Fatalf("uploader cadence row %d vmaddr = %04X, want %04X",
				i, match.vmaddr, wantVMAddr)
		}
	}
	return window
}

func pto2Post2098UploaderSteps(t *testing.T, window []pto2UploaderCadenceMatch) []pto2Post2098UploaderStep {
	t.Helper()
	if len(window) < 2 {
		t.Fatal("need at least two uploader cadence rows")
	}
	steps := make([]pto2Post2098UploaderStep, 0, len(window)-1)
	for i := 1; i < len(window); i++ {
		from := window[i-1]
		to := window[i]
		goStep := to.goDMA.cycles - from.goDMA.cycles
		refStep := to.refDMA.cycles - from.refDMA.cycles
		steps = append(steps, pto2Post2098UploaderStep{
			from:    from,
			to:      to,
			goStep:  goStep,
			refStep: refStep,
			extra:   int64(goStep) - int64(refStep),
			toDelta: int64(to.goDMA.cycles) - int64(to.refDMA.cycles),
		})
	}
	return steps
}
