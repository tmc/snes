package parity

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

const pto2GoBoundaryTraceEnv = "PTO2_GO_BOUNDARY_TRACE"

type pto2GoFramePhaseSummary struct {
	frame30End       pto2GoBoundaryEvent
	lastFrame30DMA   pto2GoBoundaryEvent
	firstFrame31DMA  pto2GoBoundaryEvent
	frame30DMAAt2168 bool
}

type pto2GoBoundaryEvent struct {
	line      int
	kind      string
	frame     int
	cycles    uint64
	cyInFrame uint64
	pbpc      string
	vmaddr    uint16
}

func TestPTO2FramePhaseRootArtifacts(t *testing.T) {
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

	goSummary := readPTO2GoFramePhaseTrace(t, goRaw)
	refSummary := readPTO2ReferenceTimingTrace(t, refRaw)

	if goSummary.frame30End.line == 0 {
		t.Fatalf("%s has no Go frame-30 frame-end boundary event", goPath)
	}
	if goSummary.lastFrame30DMA.vmaddr != 0x2160 {
		t.Fatalf("%s last Go frame-30 uploader DMA vmaddr = %04X on line %d, want 2160",
			goPath, goSummary.lastFrame30DMA.vmaddr, goSummary.lastFrame30DMA.line)
	}
	if goSummary.frame30End.vmaddr != 0x2168 {
		t.Fatalf("%s Go frame-30 end vmaddr = %04X on line %d, want 2168",
			goPath, goSummary.frame30End.vmaddr, goSummary.frame30End.line)
	}
	if goSummary.frame30DMAAt2168 {
		t.Fatalf("%s has a Go frame-30 uploader DMA at or beyond vmaddr 2168", goPath)
	}
	if goSummary.firstFrame31DMA.vmaddr != 0x2168 {
		t.Fatalf("%s first Go frame-31 uploader DMA vmaddr = %04X on line %d, want 2168",
			goPath, goSummary.firstFrame31DMA.vmaddr, goSummary.firstFrame31DMA.line)
	}
	if refSummary.qualifyingLine == 0 || refSummary.qualifyingDMA.vmaddr != 0x2168 {
		t.Fatalf("%s reference frame-30 qualifying DMA vmaddr = %04X line=%d, want line with 2168",
			refPath, refSummary.qualifyingDMA.vmaddr, refSummary.qualifyingLine)
	}
	if refSummary.frame30Cycles == 0 {
		t.Fatalf("%s has no reference frame-30 cycle", refPath)
	}

	cycleDelta := int64(goSummary.frame30End.cycles) - int64(refSummary.frame30Cycles)
	t.Logf("PTO2 Go frame-30 end: line=%d cycle=%d cyInFrame=%d PB:PC=%s vmaddr=%04X",
		goSummary.frame30End.line, goSummary.frame30End.cycles, goSummary.frame30End.cyInFrame,
		goSummary.frame30End.pbpc, goSummary.frame30End.vmaddr)
	t.Logf("PTO2 Go frame-31 first missing DMA: line=%d cycle=%d cyInFrame=%d vmaddr=%04X",
		goSummary.firstFrame31DMA.line, goSummary.firstFrame31DMA.cycles,
		goSummary.firstFrame31DMA.cyInFrame, goSummary.firstFrame31DMA.vmaddr)
	t.Logf("PTO2 bsnes frame-30 first missing DMA: line=%d cycle=%d vmaddr=%04X; frame cycle=%d; Go-frame minus bsnes-frame cycles=%d",
		refSummary.qualifyingLine, refSummary.qualifyingDMA.cycles, refSummary.qualifyingDMA.vmaddr,
		refSummary.frame30Cycles, cycleDelta)
}

func readPTO2GoFramePhaseTrace(t *testing.T, raw []byte) pto2GoFramePhaseSummary {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 Go boundary trace")
	}
	var summary pto2GoFramePhaseSummary
	for i, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("UPLOADER_BOUNDARY_EVENT\t")) {
			continue
		}
		ev := parsePTO2GoBoundaryEvent(t, i+1, string(line))
		switch {
		case ev.kind == "frame-end" && ev.frame == 30:
			summary.frame30End = ev
		case ev.kind == "dma" && ev.frame == 30:
			if ev.vmaddr >= 0x2168 {
				summary.frame30DMAAt2168 = true
			}
			if summary.lastFrame30DMA.line == 0 || ev.cycles > summary.lastFrame30DMA.cycles {
				summary.lastFrame30DMA = ev
			}
		case ev.kind == "dma" && ev.frame == 31 && ev.vmaddr >= 0x2168:
			if summary.firstFrame31DMA.line == 0 || ev.cycles < summary.firstFrame31DMA.cycles {
				summary.firstFrame31DMA = ev
			}
		}
	}
	if summary.lastFrame30DMA.line == 0 {
		t.Fatal("PTO2 Go boundary trace has no frame-30 uploader DMA")
	}
	if summary.firstFrame31DMA.line == 0 {
		t.Fatal("PTO2 Go boundary trace has no frame-31 uploader DMA at or beyond 2168")
	}
	return summary
}

func parsePTO2GoBoundaryEvent(t *testing.T, lineNo int, line string) pto2GoBoundaryEvent {
	t.Helper()
	fields := strings.Fields(line)
	ev := pto2GoBoundaryEvent{line: lineNo}
	for _, field := range fields {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "kind":
			ev.kind = val
		case "f":
			ev.frame = atoiPTO2TraceField(t, lineNo, key, val)
		case "cy":
			ev.cycles = atouPTO2DecimalTraceField(t, lineNo, key, val)
		case "cyInFrame":
			ev.cyInFrame = atouPTO2DecimalTraceField(t, lineNo, key, val)
		case "pbpc":
			ev.pbpc = val
		case "vmaddr":
			ev.vmaddr = uint16(atouPTO2HexTraceField(t, lineNo, key, val))
		}
	}
	return ev
}

func atoiPTO2TraceField(t *testing.T, lineNo int, key, val string) int {
	t.Helper()
	n, err := strconv.Atoi(val)
	if err != nil {
		t.Fatalf("parse line %d field %s=%q: %v", lineNo, key, val, err)
	}
	return n
}

func atouPTO2DecimalTraceField(t *testing.T, lineNo int, key, val string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		t.Fatalf("parse line %d field %s=%q: %v", lineNo, key, val, err)
	}
	return n
}

func atouPTO2HexTraceField(t *testing.T, lineNo int, key, val string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(val, 16, 64)
	if err != nil {
		t.Fatalf("parse line %d field %s=%q: %v", lineNo, key, val, err)
	}
	return n
}
