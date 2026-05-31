package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/dma"
)

type pto2ReferenceCadenceTraceSummary struct {
	rows            int
	instructionRows int
	refreshRows     int
	frame30Line     int
	frame30Cycles   uint64
	dmas            []pto2ReferenceTimingDMARow
}

type pto2UploaderCadenceMatch struct {
	vmaddr uint16
	goDMA  pto2GoBoundaryEvent
	refDMA pto2ReferenceTimingDMARow
}

const (
	pto2CPUCadenceTraceEnv     = "PTO2_CPU_CADENCE_TRACE"
	pto2CPUCadenceTracePathEnv = "PTO2_CPU_CADENCE_TRACE_PATH"
	pto2GoCPUTraceCompareEnv   = "PTO2_GO_CPU_CADENCE_TRACE"
	pto2RefCPUTraceCompareEnv  = "PTO2_REF_CPU_CADENCE_TRACE"
	pto2ROMEnv                 = "PTO2_ROM"
	pto2ROMName                = "P.T.O. II - Pacific Theater of Operations (USA).sfc"
)

type pto2GoCPUCadenceTraceSummary struct {
	rows              int
	instructionRows   int
	ioRows            int
	dmaRows           int
	frameRows         int
	firstFrame        int
	lastFrame         int
	uploaderDMAAt2098 bool
}

func TestPTO2CPUCadenceTraceScope(t *testing.T) {
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
	if len(matches) == 0 {
		t.Fatalf("no shared PTO2 uploader DMA vmaddr rows between %s and %s", goPath, refPath)
	}
	first := matches[0]
	if first.vmaddr != 0x2098 {
		t.Fatalf("first shared uploader vmaddr = %04X, want 2098", first.vmaddr)
	}
	firstDelta := int64(first.goDMA.cycles) - int64(first.refDMA.cycles)
	if firstDelta < 20000 {
		t.Fatalf("first shared uploader delta = %d cycles at vmaddr=%04X, want already-lost cadence before frame 30",
			firstDelta, first.vmaddr)
	}
	missing, ok := findPTO2UploaderCadenceMatch(matches, 0x2168)
	if !ok {
		t.Fatal("shared uploader cadence lacks vmaddr 2168")
	}
	if refSummary.frame30Line == 0 {
		t.Fatalf("%s has no reference frame-30 row", refPath)
	}
	if missing.refDMA.cycles >= refSummary.frame30Cycles {
		t.Fatalf("reference vmaddr 2168 cycle %d is not before frame-30 cycle %d",
			missing.refDMA.cycles, refSummary.frame30Cycles)
	}

	t.Logf("PTO2 cadence artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 reference rows=%d instruction_rows=%d refresh_rows=%d uploader_dmas=%d",
		refSummary.rows, refSummary.instructionRows, refSummary.refreshRows, len(refSummary.dmas))
	t.Logf("PTO2 first shared uploader DMA: vmaddr=%04X Go f=%d cycle=%d Ref f=%d cycle=%d delta=%d",
		first.vmaddr, first.goDMA.frame, first.goDMA.cycles, first.refDMA.frame, first.refDMA.cycles, firstDelta)
	t.Logf("PTO2 missing-window DMA: vmaddr=%04X Go f=%d cycle=%d Ref f=%d cycle=%d frame30=%d delta=%d",
		missing.vmaddr, missing.goDMA.frame, missing.goDMA.cycles,
		missing.refDMA.frame, missing.refDMA.cycles, refSummary.frame30Cycles,
		int64(missing.goDMA.cycles)-int64(missing.refDMA.cycles))
	if refSummary.instructionRows == 0 {
		t.Log("PTO2 reference artifact has no instruction rows; next gate needs a Go-vs-bsnes instruction-cycle artifact, not another boundary-only parser")
	}
}

func TestPTO2CPUCadenceTraceEmit(t *testing.T) {
	if os.Getenv(pto2CPUCadenceTraceEnv) == "" {
		t.Skipf("set %s=1 to emit a PTO2 Go CPU cadence trace", pto2CPUCadenceTraceEnv)
	}
	romPath := os.Getenv(pto2ROMEnv)
	if romPath == "" {
		var err error
		romPath, err = findPersistentSmallROM(pto2ROMName)
		if err != nil {
			t.Skipf("set %s=<path>: %v", pto2ROMEnv, err)
		}
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read rom: %v", err)
	}

	frameLo := envIntDefault(t, "PTO2_CPU_CADENCE_FRAME_LO", 27)
	frameHi := envIntDefault(t, "PTO2_CPU_CADENCE_FRAME_HI", 29)
	if frameLo < 0 || frameHi < frameLo {
		t.Fatalf("bad PTO2 CPU cadence frame range %d-%d", frameLo, frameHi)
	}

	outPath := os.Getenv(pto2CPUCadenceTracePathEnv)
	if outPath == "" {
		outPath = t.TempDir() + "/pto2-go-cpu-cadence.jsonl"
	}
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create %s: %v", outPath, err)
	}
	defer out.Close()
	w := bufio.NewWriterSize(out, 1<<20)
	enc := json.NewEncoder(w)

	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("load rom: %v", err)
	}
	sys.Power()

	currentFrame := 0
	frameStartCycles := uint64(0)
	write := func(v any) {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("encode trace row: %v", err)
		}
	}
	inFrame := func() bool {
		return currentFrame >= frameLo && currentFrame <= frameHi
	}

	prevBefore := sys.CPU.BeforeExecute
	sys.CPU.BeforeExecute = func() {
		if prevBefore != nil {
			prevBefore()
		}
		if !inFrame() {
			return
		}
		write(pto2CPUInstructionTraceRow(sys, rom, currentFrame, frameStartCycles))
	}

	prevReadHook := sys.Bus.ReadHook
	sys.Bus.ReadHook = func(addr uint32, value uint8) {
		if prevReadHook != nil {
			prevReadHook(addr, value)
		}
		if !inFrame() || !pto2CPUCadenceIOAddr(addr) {
			return
		}
		write(pto2CPUIOTraceRow(sys, "io-read", currentFrame, frameStartCycles, addr, value))
	}

	prevWriteHook := sys.Bus.WriteHook
	sys.Bus.WriteHook = func(addr uint32, value uint8) {
		if prevWriteHook != nil {
			prevWriteHook(addr, value)
		}
		if !inFrame() || !pto2CPUCadenceIOAddr(addr) {
			return
		}
		write(pto2CPUIOTraceRow(sys, "io-write", currentFrame, frameStartCycles, addr, value))
	}

	prevDMATrace := sys.DMA.Trace
	sys.DMA.Trace = func(tt dma.TransferTrace) {
		if inFrame() {
			write(pto2CPUDMATraceRow(sys, currentFrame, frameStartCycles, tt))
		}
		if prevDMATrace != nil {
			prevDMATrace(tt)
		}
	}

	for f := 0; f <= frameHi; f++ {
		currentFrame = f
		frameStartCycles = sys.CPU.Cycles
		if inFrame() {
			write(pto2CPUFrameTraceRow(sys, "start", currentFrame, frameStartCycles))
		}
		if err := sys.Run(); err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		if inFrame() {
			write(pto2CPUFrameTraceRow(sys, "end", currentFrame, frameStartCycles))
		}
	}

	sys.CPU.BeforeExecute = prevBefore
	sys.Bus.ReadHook = prevReadHook
	sys.Bus.WriteHook = prevWriteHook
	sys.DMA.Trace = prevDMATrace

	if err := w.Flush(); err != nil {
		t.Fatalf("flush trace: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close trace: %v", err)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read emitted trace: %v", err)
	}
	summary := readPTO2GoCPUCadenceTrace(t, raw)
	if summary.instructionRows == 0 {
		t.Fatalf("%s has no instruction rows", outPath)
	}
	if summary.frameRows < 2*(frameHi-frameLo+1) {
		t.Fatalf("%s frame rows = %d, want at least %d", outPath, summary.frameRows, 2*(frameHi-frameLo+1))
	}
	if summary.dmaRows == 0 {
		t.Fatalf("%s has no dma-start rows", outPath)
	}
	if frameLo <= 29 && frameHi >= 29 && !summary.uploaderDMAAt2098 {
		t.Logf("%s has no channel-7 uploader DMA at VMADDR $2098 in the emitted range", outPath)
	}

	t.Logf("PTO2 Go CPU cadence trace %s rows=%d sha256=%s", outPath, summary.rows, hashBytes(raw))
	t.Logf("PTO2 Go CPU cadence rows: instruction=%d io=%d dma=%d frame=%d frames=%d..%d uploader2098=%v",
		summary.instructionRows, summary.ioRows, summary.dmaRows, summary.frameRows,
		summary.firstFrame, summary.lastFrame, summary.uploaderDMAAt2098)
}

func TestPTO2CPUCadenceTraceCompare(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	if goTrace.instructionRows == 0 {
		t.Fatalf("%s has no instruction rows", goPath)
	}
	if refTrace.instructionRows == 0 {
		t.Fatalf("%s has no instruction rows", refPath)
	}
	if goTrace.uploader2098.line == 0 {
		t.Fatalf("%s has no channel-7 uploader DMA at VMADDR $2098", goPath)
	}
	if refTrace.uploader2098.line == 0 {
		t.Fatalf("%s has no channel-7 uploader DMA at VMADDR $2098", refPath)
	}

	result := comparePTO2CPUTraces(t, goTrace, refTrace)
	if result.kind == "" {
		t.Fatalf("no PTO2 CPU trace divergence before VMADDR $2098; go instructions=%d ref instructions=%d",
			len(goTrace.instructions), len(refTrace.instructions))
	}
	if result.goRow.cycles >= goTrace.uploader2098.cycles {
		t.Fatalf("Go divergence at cycle %d is not before Go $2098 uploader cycle %d",
			result.goRow.cycles, goTrace.uploader2098.cycles)
	}
	if result.refRow.cycles >= refTrace.uploader2098.cycles {
		t.Fatalf("reference divergence at cycle %d is not before reference $2098 uploader cycle %d",
			result.refRow.cycles, refTrace.uploader2098.cycles)
	}

	t.Logf("PTO2 CPU compare artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 CPU compare rows: go instruction=%d frames=%d..%d $2098 line=%d cycle=%d; ref instruction=%d frames=%d..%d $2098 line=%d cycle=%d",
		goTrace.instructionRows, goTrace.firstFrame, goTrace.lastFrame, goTrace.uploader2098.line, goTrace.uploader2098.cycles,
		refTrace.instructionRows, refTrace.firstFrame, refTrace.lastFrame, refTrace.uploader2098.line, refTrace.uploader2098.cycles)
	t.Logf("PTO2 CPU compare alignment: go line=%d f=%d %02X:%04X op=%02X cycle=%d; ref line=%d f=%d %02X:%04X op=%02X cycle=%d base_delta=%d",
		result.alignGo.line, result.alignGo.frame, result.alignGo.pb, result.alignGo.pc, result.alignGo.opcode, result.alignGo.cycles,
		result.alignRef.line, result.alignRef.frame, result.alignRef.pb, result.alignRef.pc, result.alignRef.opcode, result.alignRef.cycles,
		result.baseDelta)
	t.Logf("PTO2 CPU compare first divergence: kind=%s detail=%s go line=%d f=%d %02X:%04X op=%02X cycle=%d; ref line=%d f=%d %02X:%04X op=%02X cycle=%d",
		result.kind, result.detail,
		result.goRow.line, result.goRow.frame, result.goRow.pb, result.goRow.pc, result.goRow.opcode, result.goRow.cycles,
		result.refRow.line, result.refRow.frame, result.refRow.pb, result.refRow.pc, result.refRow.opcode, result.refRow.cycles)
	if attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta); ok {
		t.Logf("PTO2 CPU compare first divergence normalizes as refresh attribution: prev_delta=%d start_delta=%d next_delta=%d ref_refresh=%d..%d",
			attr.prevDelta, attr.startDelta, attr.nextDelta,
			attr.refresh[0].cycles, attr.refresh[len(attr.refresh)-1].cycles)
	}
}

func TestPTO2Frame0CycleDeltaClassification(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	result := comparePTO2CPUTraces(t, goTrace, refTrace)
	if attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta); ok {
		if attr.start.frame != 0 || attr.start.pb != 0xc0 || attr.start.pc != 0x8151 || attr.start.opcode != 0x91 {
			t.Fatalf("refresh attribution at go %s ref %s, want frame-0 C0:8151 op=91",
				pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
		}
		if len(attr.refresh) != 12 {
			t.Fatalf("reference refresh rows = %d, want 12 begin/active/inactive/end rows", len(attr.refresh))
		}
		normalized := comparePTO2CPUTracesNormalizeRefreshAttribution(t, goTrace, refTrace)
		if len(normalized.refreshAttributions) == 0 {
			t.Fatal("normalized compare did not record the frame-0 refresh attribution")
		}
		if normalized.refreshAttributions[0].start.line != attr.start.line {
			t.Fatalf("first normalized attribution line = %d, want %d",
				normalized.refreshAttributions[0].start.line, attr.start.line)
		}
		if normalized.result.kind != "" && normalized.result.goRow.line == result.goRow.line {
			t.Fatalf("normalized compare still stops at refresh attribution row %s",
				pto2CPUCompareRowLabel(result.goRow))
		}
		t.Logf("PTO2 frame-0 first split is refresh attribution: Go C0:8156->C0:8151 carries refresh, bsnes places refresh rows %d..%d before C0:8153",
			attr.refresh[0].cycles, attr.refresh[len(attr.refresh)-1].cycles)
		if normalized.result.kind == "" {
			t.Logf("PTO2 normalized compare: no remaining pre-$2098 divergence after %d refresh-attribution row(s)",
				len(normalized.refreshAttributions))
		} else {
			t.Logf("PTO2 normalized compare next divergence: kind=%s detail=%s go %s ref %s",
				normalized.result.kind, normalized.result.detail,
				pto2CPUCompareRowLabel(normalized.result.goRow),
				pto2CPUCompareRowLabel(normalized.result.refRow))
		}
		return
	}
	if result.kind != "cycle-delta" {
		t.Fatalf("first PTO2 split kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	if result.goRow.frame != 0 || result.refRow.frame != 0 ||
		result.goRow.pb != 0xc0 || result.refRow.pb != 0xc0 ||
		result.goRow.pc != 0x8153 || result.refRow.pc != 0x8153 ||
		result.goRow.opcode != 0xc8 || result.refRow.opcode != 0xc8 {
		t.Fatalf("first cycle-delta at go %s ref %s, want frame-0 C0:8153 op=C8",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if detail := pto2CPUStateDiff(result.goRow, result.refRow); detail != "" {
		t.Fatalf("first cycle-delta row also has CPU state mismatch: %s", detail)
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, result.goRow)
	if !ok {
		t.Fatalf("no Go instruction before %s", pto2CPUCompareRowLabel(result.goRow))
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, result.refRow)
	if !ok {
		t.Fatalf("no reference instruction before %s", pto2CPUCompareRowLabel(result.refRow))
	}
	if goPrev.pc != 0x8151 || refPrev.pc != 0x8151 || goPrev.opcode != 0x91 || refPrev.opcode != 0x91 {
		t.Fatalf("previous rows are go %s ref %s, want C0:8151 op=91",
			pto2CPUCompareRowLabel(goPrev), pto2CPUCompareRowLabel(refPrev))
	}
	if detail := pto2CPUStateDiff(goPrev, refPrev); detail != "" {
		t.Fatalf("previous STA rows have CPU state mismatch: %s", detail)
	}

	goDuration := result.goRow.cycles - goPrev.cycles
	refDuration := result.refRow.cycles - refPrev.cycles
	if goDuration != 44 || refDuration != 50 {
		t.Fatalf("C0:8151->C0:8153 duration go=%d ref=%d, want 44/50",
			goDuration, refDuration)
	}
	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	rowDelta := int64(result.goRow.cycles) - int64(result.refRow.cycles)
	if prevDelta != -6 || rowDelta != -12 {
		t.Fatalf("cycle deltas prev=%d row=%d, want -6/-12", prevDelta, rowDelta)
	}

	goLoop := pto2STAIndYLoopIntervals(goTrace, result.goRow.line)
	refLoop := pto2STAIndYLoopIntervals(refTrace, result.refRow.line)
	if len(goLoop) < 3 || len(refLoop) < 3 {
		t.Fatalf("need at least three C0:8151->C0:8153 intervals before the split, got go=%d ref=%d",
			len(goLoop), len(refLoop))
	}
	goLoop = goLoop[len(goLoop)-3:]
	refLoop = refLoop[len(refLoop)-3:]
	for i := range goLoop {
		goInt := goLoop[i]
		refInt := refLoop[i]
		if goInt.duration != 44 || refInt.duration != 50 {
			t.Fatalf("loop interval %d duration go=%d ref=%d, want 44/50",
				i, goInt.duration, refInt.duration)
		}
		t.Logf("PTO2 C0:8151->C0:8153 interval %d: go lines=%d->%d cycles=%d->%d duration=%d; ref lines=%d->%d cycles=%d->%d duration=%d; start_delta=%d end_delta=%d",
			i+1,
			goInt.from.line, goInt.to.line, goInt.from.cycles, goInt.to.cycles, goInt.duration,
			refInt.from.line, refInt.to.line, refInt.from.cycles, refInt.to.cycles, refInt.duration,
			int64(goInt.from.cycles)-int64(refInt.from.cycles),
			int64(goInt.to.cycles)-int64(refInt.to.cycles))
	}

	goWindowEvents := pto2NonInstructionRowsBetween(goTrace, goPrev.line, result.goRow.line)
	refWindowEvents := pto2NonInstructionRowsBetween(refTrace, refPrev.line, result.refRow.line)
	if len(goWindowEvents) != 0 || len(refWindowEvents) != 0 {
		t.Fatalf("unexpected non-instruction rows inside first-delta window: go=%s ref=%s",
			pto2CPUCompareRowsLabel(goWindowEvents), pto2CPUCompareRowsLabel(refWindowEvents))
	}
	refRefresh, ok := firstPTO2TraceKindAfter(refTrace, result.refRow.line, "refresh")
	if !ok {
		t.Fatal("reference trace has no refresh row after the frame-0 cycle-delta")
	}
	if refRefresh.cycles <= result.refRow.cycles {
		t.Fatalf("first reference refresh %s is not after split %s",
			pto2CPUCompareRowLabel(refRefresh), pto2CPUCompareRowLabel(result.refRow))
	}
	if _, ok := firstPTO2TraceKindAfter(goTrace, result.goRow.line, "refresh"); ok {
		t.Fatal("Go CPU cadence artifact unexpectedly contains explicit refresh rows")
	}

	t.Logf("PTO2 frame-0 cycle-delta artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-0 classification: cumulative STA ($08),Y timing delta; Go C0:8151->C0:8153 is 44 master cycles and bsnes is 50, so repeated store intervals move the base delta by -6 each loop until the third INY appears as go line=%d cycle=%d vs ref line=%d cycle=%d",
		result.goRow.line, result.goRow.cycles, result.refRow.line, result.refRow.cycles)
	t.Logf("PTO2 frame-0 immediate window: no traced IO/DMA/refresh/interrupt rows between go %s and %s or ref %s and %s; registers and flags match at both endpoints",
		pto2CPUCompareRowLabel(goPrev), pto2CPUCompareRowLabel(result.goRow),
		pto2CPUCompareRowLabel(refPrev), pto2CPUCompareRowLabel(result.refRow))
	t.Logf("PTO2 frame-0 next reference refresh after split: %s; Go artifact has no explicit refresh rows",
		pto2CPUCompareRowLabel(refRefresh))
}

func TestPTO2STA91RefreshBoundaryAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	result := comparePTO2CPUTraces(t, goTrace, refTrace)
	if result.kind != "cycle-delta" {
		t.Fatalf("first PTO2 split kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta)
	if !ok {
		t.Fatalf("first PTO2 split is not refresh-boundary attribution: go %s ref %s",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if attr.start.frame != 0 || attr.refStart.frame != 0 ||
		attr.start.pb != 0xc0 || attr.refStart.pb != 0xc0 ||
		attr.start.pc != 0x8151 || attr.refStart.pc != 0x8151 ||
		attr.start.opcode != 0x91 || attr.refStart.opcode != 0x91 {
		t.Fatalf("refresh-boundary start is go %s ref %s, want frame-0 C0:8151 op=91",
			pto2CPUCompareRowLabel(attr.start), pto2CPUCompareRowLabel(attr.refStart))
	}
	if attr.prev.pb != 0xc0 || attr.prev.pc != 0x8156 || attr.prev.opcode != 0xd0 ||
		attr.next.pb != 0xc0 || attr.next.pc != 0x8153 || attr.next.opcode != 0xc8 ||
		attr.refNext.pb != 0xc0 || attr.refNext.pc != 0x8153 || attr.refNext.opcode != 0xc8 {
		t.Fatalf("refresh-boundary window is go %s -> %s -> %s ref next %s, want C0:8156 -> C0:8151 -> C0:8153",
			pto2CPUCompareRowLabel(attr.prev),
			pto2CPUCompareRowLabel(attr.start),
			pto2CPUCompareRowLabel(attr.next),
			pto2CPUCompareRowLabel(attr.refNext))
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, attr.refStart)
	if !ok {
		t.Fatalf("no reference instruction before %s", pto2CPUCompareRowLabel(attr.refStart))
	}
	if !samePTO2CPUInstruction(attr.prev, refPrev) || pto2CPUStateDiff(attr.prev, refPrev) != "" {
		t.Fatalf("previous rows differ: go %s ref %s",
			pto2CPUCompareRowLabel(attr.prev), pto2CPUCompareRowLabel(refPrev))
	}

	goPrevToStart := attr.start.cycles - attr.prev.cycles
	refPrevToStart := attr.refStart.cycles - refPrev.cycles
	goStartToNext := attr.next.cycles - attr.start.cycles
	refStartToNext := attr.refNext.cycles - attr.refStart.cycles
	if goPrevToStart != refPrevToStart+40 || refStartToNext != goStartToNext+40 {
		t.Fatalf("refresh-boundary intervals go prev->start=%d ref prev->start=%d go start->next=%d ref start->next=%d, want the 40-cycle refresh on opposite sides",
			goPrevToStart, refPrevToStart, goStartToNext, refStartToNext)
	}
	if goStartToNext != 50 {
		t.Fatalf("Go C0:8151->C0:8153 duration = %d, want post-STA-idle 50 cycles", goStartToNext)
	}
	if attr.startDelta-result.baseDelta != 40 || attr.prevDelta != result.baseDelta || attr.nextDelta != result.baseDelta {
		t.Fatalf("deltas prev=%d start=%d next=%d base=%d, want only C0:8151 shifted by 40",
			attr.prevDelta, attr.startDelta, attr.nextDelta, result.baseDelta)
	}
	goWindowEvents := pto2NonInstructionRowsBetween(goTrace, attr.prev.line, attr.next.line)
	if len(goWindowEvents) != 0 {
		t.Fatalf("Go C0:8156->C0:8153 window has unexpected non-instruction rows: %s",
			pto2CPUCompareRowsLabel(goWindowEvents))
	}
	refWindowEvents := pto2NonInstructionRowsBetween(refTrace, refPrev.line, attr.refNext.line)
	if len(refWindowEvents) != len(attr.refresh) {
		t.Fatalf("reference C0:8156->C0:8153 window has non-refresh rows: %s",
			pto2CPUCompareRowsLabel(refWindowEvents))
	}
	if len(attr.refresh) != 12 {
		t.Fatalf("reference refresh rows = %d, want 12 begin/active/inactive/end rows", len(attr.refresh))
	}
	firstRefresh := attr.refresh[0]
	lastRefresh := attr.refresh[len(attr.refresh)-1]
	if firstRefresh.pc != 0x8152 || lastRefresh.pc != 0x8152 ||
		firstRefresh.cycles != attr.refStart.cycles+2 ||
		lastRefresh.cycles-firstRefresh.cycles != 40 {
		t.Fatalf("reference refresh span is %s .. %s, want C0:8152 from ref start+2 for 40 cycles",
			pto2CPUCompareRowLabel(firstRefresh), pto2CPUCompareRowLabel(lastRefresh))
	}

	t.Logf("PTO2 STA $91 refresh-boundary artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 STA $91 refresh-boundary attribution: raw compare stops at go %s ref %s base_delta=%d",
		pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow), result.baseDelta)
	t.Logf("PTO2 STA $91 intervals: Go C0:8156->C0:8151=%d, ref=%d; Go C0:8151->C0:8153=%d, ref=%d; reference refresh rows=%d span=%d..%d",
		goPrevToStart, refPrevToStart, goStartToNext, refStartToNext,
		len(attr.refresh), firstRefresh.cycles, lastRefresh.cycles)
}

func TestPTO2CPUCadenceTraceNormalizeRefreshAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	raw := comparePTO2CPUTraces(t, goTrace, refTrace)
	attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, raw.goRow, raw.refRow, raw.baseDelta)
	if !ok {
		t.Fatalf("raw first divergence is not a normalizable refresh attribution: kind=%s detail=%s go %s ref %s",
			raw.kind, raw.detail,
			pto2CPUCompareRowLabel(raw.goRow),
			pto2CPUCompareRowLabel(raw.refRow))
	}
	if attr.prev.pc != 0x8156 || attr.start.pc != 0x8151 || attr.next.pc != 0x8153 {
		t.Fatalf("refresh attribution window is go %s -> %s -> %s, want C0:8156 -> C0:8151 -> C0:8153",
			pto2CPUCompareRowLabel(attr.prev),
			pto2CPUCompareRowLabel(attr.start),
			pto2CPUCompareRowLabel(attr.next))
	}
	if attr.startDelta-attr.baseDelta != 40 {
		t.Fatalf("refresh attribution delta moved by %d cycles, want 40", attr.startDelta-attr.baseDelta)
	}

	normalized := comparePTO2CPUTracesNormalizeRefreshAttribution(t, goTrace, refTrace)
	if len(normalized.refreshAttributions) == 0 {
		t.Fatal("normalized compare recorded no refresh-attribution rows")
	}
	if normalized.refreshAttributions[0].start.line != raw.goRow.line {
		t.Fatalf("first normalized refresh-attribution line = %d, want raw divergence line %d",
			normalized.refreshAttributions[0].start.line, raw.goRow.line)
	}
	if normalized.result.kind != "" && normalized.result.goRow.line == raw.goRow.line {
		t.Fatalf("normalized compare did not advance past raw divergence %s",
			pto2CPUCompareRowLabel(raw.goRow))
	}

	t.Logf("PTO2 refresh normalization artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 refresh normalization: raw first divergence %s is normalized by reference refresh rows %d..%d",
		pto2CPUCompareRowLabel(raw.goRow),
		attr.refresh[0].cycles, attr.refresh[len(attr.refresh)-1].cycles)
	if normalized.result.kind == "" {
		t.Logf("PTO2 refresh normalization: no remaining pre-$2098 divergence after %d refresh-attribution row(s)",
			len(normalized.refreshAttributions))
	} else {
		t.Logf("PTO2 refresh normalization next divergence: kind=%s detail=%s go %s ref %s",
			normalized.result.kind, normalized.result.detail,
			pto2CPUCompareRowLabel(normalized.result.goRow),
			pto2CPUCompareRowLabel(normalized.result.refRow))
	}
}

func TestPTO2Frame1DMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	normalized := comparePTO2CPUTracesNormalizeRefreshAttribution(t, goTrace, refTrace)
	result := normalized.result
	if result.kind != "cycle-delta" {
		t.Fatalf("normalized compare kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	if result.goRow.frame != 1 || result.refRow.frame != 1 ||
		result.goRow.pb != 0xc0 || result.refRow.pb != 0xc0 ||
		result.goRow.pc != 0x8e66 || result.refRow.pc != 0x8e66 ||
		result.goRow.opcode != 0xea || result.refRow.opcode != 0xea {
		t.Fatalf("normalized cycle-delta at go %s ref %s, want frame-1 C0:8E66 op=EA",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if detail := pto2CPUStateDiff(result.goRow, result.refRow); detail != "" {
		t.Fatalf("C0:8E66 rows have CPU state mismatch: %s", detail)
	}

	attr, ok := classifyPTO2CPUDMAInstructionAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta)
	if !ok {
		t.Fatalf("C0:8E66 row is not a normalizable DMA/instruction attribution: go %s ref %s",
			pto2CPUCompareRowLabel(result.goRow),
			pto2CPUCompareRowLabel(result.refRow))
	}
	if attr.goWrite.cycles != 404566 || attr.refWrite.cycles != 404566 {
		t.Fatalf("$420B write cycle go=%d ref=%d, want 404566", attr.goWrite.cycles, attr.refWrite.cycles)
	}
	if int64(attr.goDMA.cycles)-int64(attr.refDMA.cycles) != 4 {
		t.Fatalf("DMA start delta = %d, want 4 cycles", int64(attr.goDMA.cycles)-int64(attr.refDMA.cycles))
	}

	t.Logf("PTO2 frame-1 C0:8E66 artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-1 C0:8E66 localization: normalized compare stops on trace attribution around STA $420B; $420B writes both at %d, Go DMA starts at %d and bsnes DMA starts at %d",
		attr.goWrite.cycles, attr.goDMA.cycles, attr.refDMA.cycles)
	t.Logf("PTO2 frame-1 C0:8E66 localization: Go logs C0:8E66 after DMA at %d, bsnes logs it before DMA at %d, and C0:8E67 is back within tolerance go=%d ref=%d delta=%d base_delta=%d",
		attr.start.cycles, attr.refStart.cycles,
		attr.next.cycles, attr.refNext.cycles, attr.nextDelta, attr.baseDelta)
	t.Logf("PTO2 frame-1 C0:8E66 events: Go write=%s; Go DMA=%s; reference write=%s; reference DMA=%s; reference refresh rows=%d span=%d..%d",
		pto2CPUCompareRowLabel(attr.goWrite),
		pto2CPUCompareRowLabel(attr.goDMA),
		pto2CPUCompareRowLabel(attr.refWrite),
		pto2CPUCompareRowLabel(attr.refDMA),
		len(attr.refRefresh), attr.refRefresh[0].cycles, attr.refRefresh[len(attr.refRefresh)-1].cycles)
}

func TestPTO2CPUCadenceTraceNormalizeDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	refreshOnly := comparePTO2CPUTracesNormalizeRefreshAttribution(t, goTrace, refTrace)
	attr, ok := classifyPTO2CPUDMAInstructionAttribution(goTrace, refTrace, refreshOnly.result.goRow, refreshOnly.result.refRow, refreshOnly.result.baseDelta)
	if !ok {
		t.Fatalf("refresh-normalized compare did not stop at DMA/instruction attribution: kind=%s detail=%s go %s ref %s",
			refreshOnly.result.kind, refreshOnly.result.detail,
			pto2CPUCompareRowLabel(refreshOnly.result.goRow),
			pto2CPUCompareRowLabel(refreshOnly.result.refRow))
	}

	normalized := comparePTO2CPUTracesNormalizeDMAInstructionAttribution(t, goTrace, refTrace)
	if len(normalized.refreshAttributions) == 0 {
		t.Fatal("normalized compare recorded no refresh-attribution rows")
	}
	if len(normalized.dmaInstructionAttributions) == 0 {
		t.Fatal("normalized compare recorded no DMA/instruction attribution rows")
	}
	if normalized.dmaInstructionAttributions[0].start.line != attr.start.line {
		t.Fatalf("first DMA/instruction attribution line = %d, want %d",
			normalized.dmaInstructionAttributions[0].start.line, attr.start.line)
	}
	if normalized.result.kind != "" && normalized.result.goRow.line == attr.start.line {
		t.Fatalf("normalized compare still stops at DMA/instruction attribution row %s",
			pto2CPUCompareRowLabel(attr.start))
	}

	t.Logf("PTO2 attribution normalization artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 attribution normalization: skipped %d refresh attribution row(s) and %d DMA/instruction attribution row(s)",
		len(normalized.refreshAttributions), len(normalized.dmaInstructionAttributions))
	t.Logf("PTO2 attribution normalization: C0:8E66 is trace placement around STA $420B; Go DMA starts at %d, bsnes DMA starts at %d, next C0:8E67 delta=%d base_delta=%d",
		attr.goDMA.cycles, attr.refDMA.cycles, attr.nextDelta, attr.baseDelta)
	if normalized.result.kind == "" {
		t.Log("PTO2 attribution normalization: no remaining pre-$2098 divergence after known attribution rows")
	} else {
		t.Logf("PTO2 attribution normalization next divergence: kind=%s detail=%s go %s ref %s",
			normalized.result.kind, normalized.result.detail,
			pto2CPUCompareRowLabel(normalized.result.goRow),
			pto2CPUCompareRowLabel(normalized.result.refRow))
	}
}

func TestPTO2CPUCadenceTraceNormalizeC0946ELongDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	beforeLongDMA := comparePTO2CPUTracesNormalizeDMAInstructionAttribution(t, goTrace, refTrace)
	attr, ok := classifyPTO2CPUC0946ELongDMAInstructionAttribution(goTrace, refTrace, beforeLongDMA.result.goRow, beforeLongDMA.result.refRow, beforeLongDMA.result.baseDelta)
	if !ok {
		t.Fatalf("DMA-normalized compare did not stop at C0:946E long-DMA attribution: kind=%s detail=%s go %s ref %s",
			beforeLongDMA.result.kind, beforeLongDMA.result.detail,
			pto2CPUCompareRowLabel(beforeLongDMA.result.goRow),
			pto2CPUCompareRowLabel(beforeLongDMA.result.refRow))
	}

	normalized := comparePTO2CPUTracesNormalizeC0946ELongDMAInstructionAttribution(t, goTrace, refTrace)
	if len(normalized.c0946ELongDMAInstructionAttributions) == 0 {
		t.Fatal("normalized compare recorded no C0:946E long-DMA attribution rows")
	}
	if normalized.c0946ELongDMAInstructionAttributions[0].start.line != attr.start.line {
		t.Fatalf("first C0:946E long-DMA attribution line = %d, want %d",
			normalized.c0946ELongDMAInstructionAttributions[0].start.line, attr.start.line)
	}
	if normalized.result.kind != "" && normalized.result.goRow.line == attr.start.line {
		t.Fatalf("normalized compare still stops at C0:946E attribution row %s",
			pto2CPUCompareRowLabel(attr.start))
	}

	next := normalized.result
	if next.kind != "cycle-delta" {
		t.Fatalf("post-C0:946E normalized compare kind = %q (%s), want cycle-delta",
			next.kind, next.detail)
	}
	if next.goRow.frame != 2 || next.refRow.frame != 2 ||
		next.goRow.pb != 0xc0 || next.refRow.pb != 0xc0 ||
		next.goRow.pc != 0x943f || next.refRow.pc != 0x943f ||
		next.goRow.opcode != 0xea || next.refRow.opcode != 0xea ||
		next.goRow.cycles != 954608 || next.refRow.cycles != 950364 {
		t.Fatalf("post-C0:946E normalized compare stopped at go %s ref %s, want frame-2 C0:943F op=EA cycles 954608/950364",
			pto2CPUCompareRowLabel(next.goRow),
			pto2CPUCompareRowLabel(next.refRow))
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, next.goRow)
	if !ok {
		t.Fatalf("no Go instruction before %s", pto2CPUCompareRowLabel(next.goRow))
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, next.refRow)
	if !ok {
		t.Fatalf("no reference instruction before %s", pto2CPUCompareRowLabel(next.refRow))
	}
	if goPrev.pb != 0xc0 || goPrev.pc != 0x943c || goPrev.opcode != 0x8d ||
		refPrev.pb != 0xc0 || refPrev.pc != 0x943c || refPrev.opcode != 0x8d {
		t.Fatalf("post-C0:946E previous rows are go %s ref %s, want C0:943C STA $420B",
			pto2CPUCompareRowLabel(goPrev),
			pto2CPUCompareRowLabel(refPrev))
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, next.goRow)
	if !ok {
		t.Fatalf("no Go instruction after %s", pto2CPUCompareRowLabel(next.goRow))
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, next.refRow)
	if !ok {
		t.Fatalf("no reference instruction after %s", pto2CPUCompareRowLabel(next.refRow))
	}
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if goNext.pb != 0xc0 || goNext.pc != 0x9440 || goNext.opcode != 0x60 ||
		refNext.pb != 0xc0 || refNext.pc != 0x9440 || refNext.opcode != 0x60 ||
		absInt64(nextDelta-next.baseDelta) > 8 {
		t.Fatalf("post-C0:946E next rows are go %s ref %s delta=%d base_delta=%d, want C0:9440 back within tolerance",
			pto2CPUCompareRowLabel(goNext),
			pto2CPUCompareRowLabel(refNext),
			nextDelta, next.baseDelta)
	}

	goBefore := pto2NonInstructionRowsBetween(goTrace, goPrev.line, next.goRow.line)
	refBefore := pto2NonInstructionRowsBetween(refTrace, refPrev.line, next.refRow.line)
	refAfter := pto2NonInstructionRowsBetween(refTrace, next.refRow.line, refNext.line)
	goWrite, ok := findPTO2IOWrite(goBefore, 0x420b)
	if !ok {
		t.Fatalf("Go C0:943C->C0:943F window has no $420B write")
	}
	refWrite, ok := findPTO2IOWrite(refBefore, 0x420b)
	if !ok {
		t.Fatalf("reference C0:943C->C0:943F window has no $420B write")
	}
	goDMA, ok := findPTO2DMAStart(goBefore, 7, 0x08, 0x22, 0x00, 0x9415, 0x80, 0x8000, 0x0200)
	if !ok {
		t.Fatalf("Go C0:943C->C0:943F window has no channel-7 CGRAM DMA start")
	}
	refDMA, ok := findPTO2DMAStart(refAfter, 7, 0x08, 0x22, 0x00, 0x9415, 0x80, 0x8000, 0x0200)
	if !ok {
		t.Fatalf("reference C0:943F->C0:9440 window has no channel-7 CGRAM DMA start")
	}
	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refDMA.line, refNext.line)
	if len(refRefresh) == 0 {
		t.Fatalf("reference C0:943F DMA window has no refresh rows")
	}

	t.Logf("PTO2 C0:946E normalization artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 C0:946E normalization: skipped %d refresh attribution row(s), %d DMA/instruction attribution row(s), and %d long-DMA attribution row(s)",
		len(normalized.refreshAttributions),
		len(normalized.dmaInstructionAttributions),
		len(normalized.c0946ELongDMAInstructionAttributions))
	t.Logf("PTO2 C0:946E normalization: Go DMA starts at %d, bsnes DMA starts at %d, traced Go transfer writes=%d, next C0:946F delta=%d base_delta=%d",
		attr.goDMA.cycles, attr.refDMA.cycles, attr.goTransferWrites, attr.nextDelta, attr.baseDelta)
	if normalized.result.kind == "" {
		t.Log("PTO2 C0:946E normalization: no remaining pre-$2098 divergence after known attribution rows")
	} else {
		t.Logf("PTO2 C0:946E normalization next divergence: kind=%s detail=%s go %s ref %s",
			normalized.result.kind, normalized.result.detail,
			pto2CPUCompareRowLabel(normalized.result.goRow),
			pto2CPUCompareRowLabel(normalized.result.refRow))
		t.Logf("PTO2 C0:943F next-blocker context: previous C0:943C STA $420B writes go=%d ref=%d; Go DMA=%s; reference DMA=%s; next C0:9440 delta=%d base_delta=%d; reference refresh rows=%d span=%d..%d",
			goWrite.cycles, refWrite.cycles,
			pto2CPUCompareRowLabel(goDMA),
			pto2CPUCompareRowLabel(refDMA),
			nextDelta, next.baseDelta,
			len(refRefresh), refRefresh[0].cycles, refRefresh[len(refRefresh)-1].cycles)
	}
}

func TestPTO2CPUCadenceTraceNormalizeC0943FCGRAMDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	beforeCGRAMDMA := comparePTO2CPUTracesNormalizeC0946ELongDMAInstructionAttribution(t, goTrace, refTrace)
	attr, ok := classifyPTO2CPUC0943FCGRAMDMAInstructionAttribution(goTrace, refTrace, beforeCGRAMDMA.result.goRow, beforeCGRAMDMA.result.refRow, beforeCGRAMDMA.result.baseDelta)
	if !ok {
		t.Fatalf("C0:946E-normalized compare did not stop at C0:943F CGRAM DMA attribution: kind=%s detail=%s go %s ref %s",
			beforeCGRAMDMA.result.kind, beforeCGRAMDMA.result.detail,
			pto2CPUCompareRowLabel(beforeCGRAMDMA.result.goRow),
			pto2CPUCompareRowLabel(beforeCGRAMDMA.result.refRow))
	}

	normalized := comparePTO2CPUTracesNormalizeC0943FCGRAMDMAInstructionAttribution(t, goTrace, refTrace)
	if len(normalized.c0943FCGRAMDMAInstructionAttributions) == 0 {
		t.Fatal("normalized compare recorded no C0:943F CGRAM DMA attribution rows")
	}
	if normalized.c0943FCGRAMDMAInstructionAttributions[0].start.line != attr.start.line {
		t.Fatalf("first C0:943F CGRAM DMA attribution line = %d, want %d",
			normalized.c0943FCGRAMDMAInstructionAttributions[0].start.line, attr.start.line)
	}
	if normalized.result.kind != "" && normalized.result.goRow.line == attr.start.line {
		t.Fatalf("normalized compare still stops at C0:943F attribution row %s",
			pto2CPUCompareRowLabel(attr.start))
	}

	t.Logf("PTO2 C0:943F normalization artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 C0:943F normalization: skipped %d refresh attribution row(s), %d DMA/instruction attribution row(s), %d long-DMA attribution row(s), and %d CGRAM DMA attribution row(s)",
		len(normalized.refreshAttributions),
		len(normalized.dmaInstructionAttributions),
		len(normalized.c0946ELongDMAInstructionAttributions),
		len(normalized.c0943FCGRAMDMAInstructionAttributions))
	t.Logf("PTO2 C0:943F normalization: Go DMA starts at %d, bsnes DMA starts at %d, Go DMA count=%d, traced Go $2122 writes=%d, next C0:9440 delta=%d base_delta=%d",
		attr.goDMA.cycles, attr.refDMA.cycles, attr.goDMA.count,
		attr.goTransferWrites, attr.nextDelta, attr.baseDelta)
	if normalized.result.kind == "" {
		t.Log("PTO2 C0:943F normalization: no remaining pre-$2098 divergence after known attribution rows")
	} else {
		t.Logf("PTO2 C0:943F normalization next divergence: kind=%s detail=%s go %s ref %s",
			normalized.result.kind, normalized.result.detail,
			pto2CPUCompareRowLabel(normalized.result.goRow),
			pto2CPUCompareRowLabel(normalized.result.refRow))
	}
}

func TestPTO2CPUCadenceTraceNormalizeC09498INIDISPDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	beforeINIDISPDMA := comparePTO2CPUTracesNormalizeC0943FCGRAMDMAInstructionAttribution(t, goTrace, refTrace)
	attr, ok := classifyPTO2CPUC09498INIDISPDMAInstructionAttribution(goTrace, refTrace, beforeINIDISPDMA.result.goRow, beforeINIDISPDMA.result.refRow, beforeINIDISPDMA.result.baseDelta)
	if !ok {
		t.Fatalf("C0:943F-normalized compare did not stop at C0:9498 INIDISP DMA attribution: kind=%s detail=%s go %s ref %s",
			beforeINIDISPDMA.result.kind, beforeINIDISPDMA.result.detail,
			pto2CPUCompareRowLabel(beforeINIDISPDMA.result.goRow),
			pto2CPUCompareRowLabel(beforeINIDISPDMA.result.refRow))
	}

	normalized := comparePTO2CPUTracesNormalizeAttributions(t, goTrace, refTrace)
	if len(normalized.c09498INIDISPDMAInstructionAttributions) == 0 {
		t.Fatal("normalized compare recorded no C0:9498 INIDISP DMA attribution rows")
	}
	if normalized.c09498INIDISPDMAInstructionAttributions[0].start.line != attr.start.line {
		t.Fatalf("first C0:9498 INIDISP DMA attribution line = %d, want %d",
			normalized.c09498INIDISPDMAInstructionAttributions[0].start.line, attr.start.line)
	}
	if normalized.result.kind != "" && normalized.result.goRow.line == attr.start.line {
		t.Fatalf("normalized compare still stops at C0:9498 attribution row %s",
			pto2CPUCompareRowLabel(attr.start))
	}

	t.Logf("PTO2 C0:9498 normalization artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 C0:9498 normalization: skipped %d refresh attribution row(s), %d DMA/instruction attribution row(s), %d long-DMA attribution row(s), %d CGRAM DMA attribution row(s), and %d INIDISP DMA attribution row(s)",
		len(normalized.refreshAttributions),
		len(normalized.dmaInstructionAttributions),
		len(normalized.c0946ELongDMAInstructionAttributions),
		len(normalized.c0943FCGRAMDMAInstructionAttributions),
		len(normalized.c09498INIDISPDMAInstructionAttributions))
	t.Logf("PTO2 C0:9498 normalization: Go DMA starts at %d, bsnes DMA starts at %d, Go DMA count=%d, traced Go $2100 writes=%d, next C0:9499 delta=%d base_delta=%d",
		attr.goDMA.cycles, attr.refDMA.cycles, attr.goDMA.count,
		attr.goTransferWrites, attr.nextDelta, attr.baseDelta)
	if normalized.result.kind == "" {
		t.Log("PTO2 C0:9498 normalization: no remaining pre-$2098 divergence after known attribution rows")
	} else {
		t.Logf("PTO2 C0:9498 normalization next divergence: kind=%s detail=%s go %s ref %s",
			normalized.result.kind, normalized.result.detail,
			pto2CPUCompareRowLabel(normalized.result.goRow),
			pto2CPUCompareRowLabel(normalized.result.refRow))
	}
}

func TestPTO2Frame1C0946ELongDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	normalized := comparePTO2CPUTracesNormalizeDMAInstructionAttribution(t, goTrace, refTrace)
	result := normalized.result
	if result.kind != "cycle-delta" {
		t.Fatalf("normalized compare kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	if result.goRow.frame != 1 || result.refRow.frame != 1 ||
		result.goRow.pb != 0xc0 || result.refRow.pb != 0xc0 ||
		result.goRow.pc != 0x946e || result.refRow.pc != 0x946e ||
		result.goRow.opcode != 0xea || result.refRow.opcode != 0xea {
		t.Fatalf("normalized cycle-delta at go %s ref %s, want frame-1 C0:946E op=EA",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if detail := pto2CPUStateDiff(result.goRow, result.refRow); detail != "" {
		t.Fatalf("C0:946E rows have CPU state mismatch: %s", detail)
	}

	attr, ok := classifyPTO2CPUC0946ELongDMAInstructionAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta)
	if !ok {
		t.Fatalf("C0:946E row is not a classifiable long-DMA/instruction attribution: go %s ref %s",
			pto2CPUCompareRowLabel(result.goRow),
			pto2CPUCompareRowLabel(result.refRow))
	}
	if attr.goWrite.cycles != 409806 || attr.refWrite.cycles != 409804 {
		t.Fatalf("$420B write cycle go=%d ref=%d, want 409806/409804",
			attr.goWrite.cycles, attr.refWrite.cycles)
	}
	if attr.goDMA.cycles != 409828 || attr.refDMA.cycles != 409824 {
		t.Fatalf("DMA start cycle go=%d ref=%d, want 409828/409824",
			attr.goDMA.cycles, attr.refDMA.cycles)
	}
	if attr.goTransferWrites != 65536 {
		t.Fatalf("Go DMA transfer writes = %d, want 65536", attr.goTransferWrites)
	}

	t.Logf("PTO2 frame-1 C0:946E artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-1 C0:946E localization: normalized compare stops on trace attribution around STA $420B; Go $420B write=%d, bsnes $420B write=%d",
		attr.goWrite.cycles, attr.refWrite.cycles)
	t.Logf("PTO2 frame-1 C0:946E localization: Go logs C0:946E after long DMA at %d, bsnes logs it before long DMA at %d, and C0:946F is back within tolerance go=%d ref=%d delta=%d base_delta=%d",
		attr.start.cycles, attr.refStart.cycles,
		attr.next.cycles, attr.refNext.cycles, attr.nextDelta, attr.baseDelta)
	t.Logf("PTO2 frame-1 C0:946E DMA: Go DMA=%s; reference DMA=%s; Go traced transfer writes=%d; reference refresh rows=%d span=%d..%d; Go frame rows after C0:946E=%d",
		pto2CPUCompareRowLabel(attr.goDMA),
		pto2CPUCompareRowLabel(attr.refDMA),
		attr.goTransferWrites,
		len(attr.refRefresh), attr.refRefresh[0].cycles, attr.refRefresh[len(attr.refRefresh)-1].cycles,
		len(attr.goFrameRows))
}

func TestPTO2Frame2C0943FCGRAMDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	normalized := comparePTO2CPUTracesNormalizeC0946ELongDMAInstructionAttribution(t, goTrace, refTrace)
	result := normalized.result
	if result.kind != "cycle-delta" {
		t.Fatalf("C0:946E-normalized compare kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	if result.goRow.frame != 2 || result.refRow.frame != 2 ||
		result.goRow.pb != 0xc0 || result.refRow.pb != 0xc0 ||
		result.goRow.pc != 0x943f || result.refRow.pc != 0x943f ||
		result.goRow.opcode != 0xea || result.refRow.opcode != 0xea {
		t.Fatalf("normalized cycle-delta at go %s ref %s, want frame-2 C0:943F op=EA",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if detail := pto2CPUStateDiff(result.goRow, result.refRow); detail != "" {
		t.Fatalf("C0:943F rows have CPU state mismatch: %s", detail)
	}

	attr, ok := classifyPTO2CPUC0943FCGRAMDMAInstructionAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta)
	if !ok {
		t.Fatalf("C0:943F row is not a classifiable CGRAM DMA/instruction attribution: go %s ref %s",
			pto2CPUCompareRowLabel(result.goRow),
			pto2CPUCompareRowLabel(result.refRow))
	}
	if attr.goWrite.cycles != 950362 || attr.refWrite.cycles != 950364 {
		t.Fatalf("$420B write cycle go=%d ref=%d, want 950362/950364",
			attr.goWrite.cycles, attr.refWrite.cycles)
	}
	if attr.goDMA.cycles != 950384 || attr.refDMA.cycles != 950384 {
		t.Fatalf("DMA start cycle go=%d ref=%d, want 950384/950384",
			attr.goDMA.cycles, attr.refDMA.cycles)
	}
	if attr.goDMA.count != 512 {
		t.Fatalf("Go CGRAM DMA count = %d, want 512", attr.goDMA.count)
	}
	if attr.goTransferWrites != 0 && attr.goTransferWrites != int(attr.goDMA.count) {
		t.Fatalf("Go traced $2122 writes = %d, want 0 for old artifacts or %d for extended traces",
			attr.goTransferWrites, attr.goDMA.count)
	}

	t.Logf("PTO2 frame-2 C0:943F artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-2 C0:943F localization: normalized compare stops on trace attribution around STA $420B; Go $420B write=%d, bsnes $420B write=%d",
		attr.goWrite.cycles, attr.refWrite.cycles)
	t.Logf("PTO2 frame-2 C0:943F localization: Go logs C0:943F after CGRAM DMA at %d, bsnes logs it before CGRAM DMA at %d, and C0:9440 is back within tolerance go=%d ref=%d delta=%d base_delta=%d",
		attr.start.cycles, attr.refStart.cycles,
		attr.next.cycles, attr.refNext.cycles, attr.nextDelta, attr.baseDelta)
	t.Logf("PTO2 frame-2 C0:943F DMA: Go DMA=%s; reference DMA=%s; Go DMA count=%d; traced Go $2122 writes=%d; reference refresh rows=%d span=%d..%d",
		pto2CPUCompareRowLabel(attr.goDMA),
		pto2CPUCompareRowLabel(attr.refDMA),
		attr.goDMA.count,
		attr.goTransferWrites,
		len(attr.refRefresh), attr.refRefresh[0].cycles, attr.refRefresh[len(attr.refRefresh)-1].cycles)
}

func TestPTO2Frame2C09498INIDISPDMAInstructionAttribution(t *testing.T) {
	goPath := os.Getenv(pto2GoCPUTraceCompareEnv)
	refPath := os.Getenv(pto2RefCPUTraceCompareEnv)
	if goPath == "" || refPath == "" {
		t.Skipf("set %s and %s", pto2GoCPUTraceCompareEnv, pto2RefCPUTraceCompareEnv)
	}

	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	refRaw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}

	goTrace := readPTO2CPUCompareTrace(t, goRaw, "go", -1)
	refTrace := readPTO2CPUCompareTrace(t, refRaw, "ref", goTrace.firstFrame)
	normalized := comparePTO2CPUTracesNormalizeC0943FCGRAMDMAInstructionAttribution(t, goTrace, refTrace)
	result := normalized.result
	if result.kind != "cycle-delta" {
		t.Fatalf("C0:943F-normalized compare kind = %q (%s), want cycle-delta", result.kind, result.detail)
	}
	if result.goRow.frame != 2 || result.refRow.frame != 2 ||
		result.goRow.pb != 0xc0 || result.refRow.pb != 0xc0 ||
		result.goRow.pc != 0x9498 || result.refRow.pc != 0x9498 ||
		result.goRow.opcode != 0xea || result.refRow.opcode != 0xea {
		t.Fatalf("normalized cycle-delta at go %s ref %s, want frame-2 C0:9498 op=EA",
			pto2CPUCompareRowLabel(result.goRow), pto2CPUCompareRowLabel(result.refRow))
	}
	if detail := pto2CPUStateDiff(result.goRow, result.refRow); detail != "" {
		t.Fatalf("C0:9498 rows have CPU state mismatch: %s", detail)
	}

	attr, ok := classifyPTO2CPUC09498INIDISPDMAInstructionAttribution(goTrace, refTrace, result.goRow, result.refRow, result.baseDelta)
	if !ok {
		t.Fatalf("C0:9498 row is not a classifiable INIDISP DMA/instruction attribution: go %s ref %s",
			pto2CPUCompareRowLabel(result.goRow),
			pto2CPUCompareRowLabel(result.refRow))
	}
	if attr.goWrite.cycles != 955006 || attr.refWrite.cycles != 955010 {
		t.Fatalf("$420B write cycle go=%d ref=%d, want 955006/955010",
			attr.goWrite.cycles, attr.refWrite.cycles)
	}
	if attr.goDMA.cycles != 955028 || attr.refDMA.cycles != 955032 {
		t.Fatalf("DMA start cycle go=%d ref=%d, want 955028/955032",
			attr.goDMA.cycles, attr.refDMA.cycles)
	}
	if attr.goDMA.count != 544 {
		t.Fatalf("Go INIDISP DMA count = %d, want 544", attr.goDMA.count)
	}
	if attr.goTransferWrites != 0 && attr.goTransferWrites != int(attr.goDMA.count) {
		t.Fatalf("Go traced $2100 writes = %d, want 0 for old artifacts or %d for extended traces",
			attr.goTransferWrites, attr.goDMA.count)
	}

	t.Logf("PTO2 frame-2 C0:9498 artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 frame-2 C0:9498 localization: normalized compare stops on trace attribution around STA $420B; Go $420B write=%d, bsnes $420B write=%d",
		attr.goWrite.cycles, attr.refWrite.cycles)
	t.Logf("PTO2 frame-2 C0:9498 localization: Go logs C0:9498 after INIDISP DMA at %d, bsnes logs it before INIDISP DMA at %d, and C0:9499 is back within tolerance go=%d ref=%d delta=%d base_delta=%d",
		attr.start.cycles, attr.refStart.cycles,
		attr.next.cycles, attr.refNext.cycles, attr.nextDelta, attr.baseDelta)
	t.Logf("PTO2 frame-2 C0:9498 DMA: Go DMA=%s; reference DMA=%s; Go DMA count=%d; traced Go $2100 writes=%d; reference refresh rows=%d span=%d..%d",
		pto2CPUCompareRowLabel(attr.goDMA),
		pto2CPUCompareRowLabel(attr.refDMA),
		attr.goDMA.count,
		attr.goTransferWrites,
		len(attr.refRefresh), attr.refRefresh[0].cycles, attr.refRefresh[len(attr.refRefresh)-1].cycles)
}

type pto2CPUInstructionRow struct {
	Kind      string `json:"kind"`
	Frame     int    `json:"frame"`
	Cycles    uint64 `json:"cycles"`
	CyInFrame uint64 `json:"cy_in_frame"`
	PB        uint8  `json:"pb"`
	PC        uint16 `json:"pc"`
	Opcode    uint8  `json:"opcode"`
	Operand0  uint8  `json:"operand0"`
	Operand1  uint8  `json:"operand1"`
	HCounter  uint16 `json:"hcounter"`
	VCounter  uint16 `json:"vcounter"`
	Field     uint8  `json:"field"`
	A         uint16 `json:"a"`
	X         uint16 `json:"x"`
	Y         uint16 `json:"y"`
	P         uint8  `json:"p"`
	DB        uint8  `json:"db"`
	D         uint16 `json:"d"`
	S         uint16 `json:"s"`
	MDR       uint8  `json:"mdr"`
}

type pto2CPUFrameRow struct {
	Kind      string `json:"kind"`
	Phase     string `json:"phase"`
	Frame     int    `json:"frame"`
	Cycles    uint64 `json:"cycles"`
	CyInFrame uint64 `json:"cy_in_frame"`
	HCounter  uint16 `json:"hcounter"`
	VCounter  uint16 `json:"vcounter"`
	Field     uint8  `json:"field"`
	PB        uint8  `json:"pb"`
	PC        uint16 `json:"pc"`
	VMAddr    uint16 `json:"vmaddr"`
	VMAIN     uint8  `json:"vmain"`
}

type pto2CPUEventRow struct {
	Kind      string `json:"kind"`
	Frame     int    `json:"frame"`
	Cycles    uint64 `json:"cycles"`
	CyInFrame uint64 `json:"cy_in_frame"`
	PB        uint8  `json:"pb"`
	PC        uint16 `json:"pc"`
	Opcode    uint8  `json:"opcode"`
	HCounter  uint16 `json:"hcounter"`
	VCounter  uint16 `json:"vcounter"`
	Field     uint8  `json:"field"`
	A         uint16 `json:"a"`
	X         uint16 `json:"x"`
	Y         uint16 `json:"y"`
	P         uint8  `json:"p"`
	DB        uint8  `json:"db"`
	D         uint16 `json:"d"`
	S         uint16 `json:"s"`
	MDR       uint8  `json:"mdr"`

	Addr  uint32 `json:"addr"`
	Value uint8  `json:"value"`

	Channel int    `json:"channel"`
	DMAP    uint8  `json:"dmap"`
	BBAD    uint8  `json:"bbad"`
	A1B     uint8  `json:"a1b"`
	A1T     uint16 `json:"a1t"`
	DAS     uint16 `json:"das"`
	Count   int    `json:"count"`
	VMAIN   uint8  `json:"vmain"`
	VMAddr  uint16 `json:"vmaddr"`
}

func pto2CPUInstructionTraceRow(sys *snes.System, rom []byte, frame int, frameStart uint64) pto2CPUInstructionRow {
	c := sys.CPU
	op0, _ := pto2ROMByte(sys, rom, uint32(c.LastOpcodePB)<<16|uint32(c.LastOpcodePC+1))
	op1, _ := pto2ROMByte(sys, rom, uint32(c.LastOpcodePB)<<16|uint32(c.LastOpcodePC+2))
	h, v, field := pto2CPUBeamAt(c.Cycles)
	return pto2CPUInstructionRow{
		Kind:      "instruction",
		Frame:     frame,
		Cycles:    c.Cycles,
		CyInFrame: c.Cycles - frameStart,
		PB:        c.LastOpcodePB,
		PC:        c.LastOpcodePC,
		Opcode:    c.LastOpcode,
		Operand0:  op0,
		Operand1:  op1,
		HCounter:  h,
		VCounter:  v,
		Field:     field,
		A:         c.A,
		X:         c.X,
		Y:         c.Y,
		P:         c.P,
		DB:        c.DB,
		D:         c.D,
		S:         c.S,
		MDR:       sys.Bus.MDR,
	}
}

func pto2CPUFrameTraceRow(sys *snes.System, phase string, frame int, frameStart uint64) pto2CPUFrameRow {
	cycles := sys.CPU.Cycles
	h, v, field := pto2CPUBeamAt(cycles)
	return pto2CPUFrameRow{
		Kind:      "frame",
		Phase:     phase,
		Frame:     frame,
		Cycles:    cycles,
		CyInFrame: cycles - frameStart,
		HCounter:  h,
		VCounter:  v,
		Field:     field,
		PB:        sys.CPU.PB,
		PC:        sys.CPU.PC,
		VMAddr:    sys.PPU.VRAMAddr,
		VMAIN:     sys.PPU.VRAMIncMode,
	}
}

func pto2CPUIOTraceRow(sys *snes.System, kind string, frame int, frameStart uint64, addr uint32, value uint8) pto2CPUEventRow {
	row := pto2CPUEventBase(sys, kind, frame, frameStart)
	row.Addr = addr & 0xffffff
	row.Value = value
	return row
}

func pto2CPUDMATraceRow(sys *snes.System, frame int, frameStart uint64, tt dma.TransferTrace) pto2CPUEventRow {
	row := pto2CPUEventBase(sys, "dma-start", frame, frameStart)
	row.Channel = tt.Channel
	row.DMAP = tt.Control
	row.BBAD = tt.Target
	row.A1B = tt.SrcBank
	row.A1T = tt.SrcAddr
	row.DAS = tt.Size
	row.Count = tt.Count
	row.VMAIN = sys.PPU.VRAMIncMode
	row.VMAddr = sys.PPU.VRAMAddr
	return row
}

func pto2CPUEventBase(sys *snes.System, kind string, frame int, frameStart uint64) pto2CPUEventRow {
	c := sys.CPU
	h, v, field := pto2CPUBeamAt(c.Cycles)
	return pto2CPUEventRow{
		Kind:      kind,
		Frame:     frame,
		Cycles:    c.Cycles,
		CyInFrame: c.Cycles - frameStart,
		PB:        c.LastOpcodePB,
		PC:        c.LastOpcodePC,
		Opcode:    c.LastOpcode,
		HCounter:  h,
		VCounter:  v,
		Field:     field,
		A:         c.A,
		X:         c.X,
		Y:         c.Y,
		P:         c.P,
		DB:        c.DB,
		D:         c.D,
		S:         c.S,
		MDR:       sys.Bus.MDR,
	}
}

func pto2ROMByte(sys *snes.System, rom []byte, addr uint32) (uint8, bool) {
	off, ok := sys.ROMAddress(addr)
	if !ok || int(off) >= len(rom) {
		return 0, false
	}
	return rom[off], true
}

func pto2CPUCadenceIOAddr(addr uint32) bool {
	off := addr & 0xffff
	switch {
	case off == 0x2100:
		return true
	case off >= 0x2115 && off <= 0x2119:
		return true
	case off >= 0x2121 && off <= 0x2122:
		return true
	case off >= 0x2140 && off <= 0x2143:
		return true
	case off == 0x4200 || off == 0x420b || off == 0x420c || off == 0x4210 || off == 0x4212:
		return true
	case off >= 0x4300 && off <= 0x437f:
		return true
	default:
		return false
	}
}

func pto2CPUBeamAt(cycles uint64) (uint16, uint16, uint8) {
	const (
		lineCycles         uint64 = 1364
		scanlinesPerFrame  uint64 = 262
		shortScanline      uint64 = 240
		shortScanlineDelta uint64 = 4
		evenFrameCycles           = scanlinesPerFrame * lineCycles
		oddFrameCycles            = evenFrameCycles - shortScanlineDelta
		fieldPairCycles           = evenFrameCycles + oddFrameCycles
	)
	rem := cycles % fieldPairCycles
	if rem < evenFrameCycles {
		return uint16(rem % lineCycles), uint16(rem / lineCycles), 0
	}
	rem -= evenFrameCycles
	shortStart := shortScanline * lineCycles
	switch {
	case rem < shortStart:
		return uint16(rem % lineCycles), uint16(rem / lineCycles), 1
	case rem < shortStart+lineCycles-shortScanlineDelta:
		return uint16(rem - shortStart), uint16(shortScanline), 1
	default:
		rem -= lineCycles - shortScanlineDelta
		return uint16(rem % lineCycles), uint16(shortScanline + 1 + rem/lineCycles), 1
	}
}

func readPTO2GoCPUCadenceTrace(t *testing.T, raw []byte) pto2GoCPUCadenceTraceSummary {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 Go CPU cadence trace")
	}
	var summary pto2GoCPUCadenceTraceSummary
	summary.firstFrame = -1
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		summary.rows++
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode PTO2 Go CPU cadence trace line %d: %v", summary.rows, err)
		}
		kind := strings.ToLower(jsonStringFieldDefault(fields, "kind"))
		frame := int(jsonNumberFieldDefault(fields, "frame"))
		if summary.firstFrame < 0 || frame < summary.firstFrame {
			summary.firstFrame = frame
		}
		if frame > summary.lastFrame {
			summary.lastFrame = frame
		}
		switch kind {
		case "instruction", "insn":
			summary.instructionRows++
		case "io-read", "io-write":
			summary.ioRows++
		case "dma-start":
			summary.dmaRows++
			channel := int(jsonNumberFieldDefault(fields, "channel"))
			vmaddr := uint16(jsonNumberFieldDefault(fields, "vmaddr"))
			if channel == 7 && vmaddr == 0x2098 {
				summary.uploaderDMAAt2098 = true
			}
		case "frame":
			summary.frameRows++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary.rows == 0 {
		t.Fatal("PTO2 Go CPU cadence trace has no rows")
	}
	return summary
}

func readPTO2GoUploaderDMAs(t *testing.T, raw []byte) []pto2GoBoundaryEvent {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 Go uploader trace")
	}
	var dmas []pto2GoBoundaryEvent
	for i, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("UPLOADER_BOUNDARY_EVENT\t")) {
			continue
		}
		ev := parsePTO2GoBoundaryEvent(t, i+1, string(line))
		if ev.kind == "dma" {
			dmas = append(dmas, ev)
		}
	}
	if len(dmas) == 0 {
		t.Fatal("PTO2 Go uploader trace has no DMA rows")
	}
	return dmas
}

func readPTO2ReferenceCadenceTrace(t *testing.T, raw []byte) pto2ReferenceCadenceTraceSummary {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 reference cadence trace")
	}
	var summary pto2ReferenceCadenceTraceSummary
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		summary.rows++
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode PTO2 reference cadence line %d: %v", summary.rows, err)
		}
		event := strings.ToLower(jsonStringFieldDefault(fields, "event"))
		kind := strings.ToLower(jsonStringFieldDefault(fields, "kind"))
		frame := int(jsonNumberFieldDefault(fields, "frame"))
		if kind == "instruction" || event == "instruction" || kind == "insn" || event == "insn" {
			summary.instructionRows++
			continue
		}
		if kind == "refresh" || event == "cpu-refresh" {
			summary.refreshRows++
			continue
		}
		if frame == 30 && (event == "frame" || kind == "frame") {
			summary.frame30Line = summary.rows
			summary.frame30Cycles = jsonNumberFieldDefaultAny(fields, "cycles", "cycle")
			continue
		}
		if event != "dma-start" && kind != "dma-start" {
			continue
		}
		dma := pto2ReferenceTimingDMARow{
			line:    summary.rows,
			frame:   frame,
			cycles:  jsonNumberFieldDefaultAny(fields, "cycles", "cycle"),
			pb:      uint8(jsonNumberFieldDefault(fields, "pb")),
			pc:      uint16(jsonNumberFieldDefault(fields, "pc")),
			channel: int(jsonNumberFieldDefault(fields, "channel")),
			vmain:   uint8(jsonNumberFieldDefault(fields, "vmain")),
			vmaddr:  uint16(jsonNumberFieldDefault(fields, "vmaddr")),
			das:     uint16(jsonNumberFieldDefault(fields, "das")),
		}
		if dma.pb == 0xc0 && dma.pc == 0x8cb9 && dma.channel == 7 && dma.vmain == 0x80 && dma.das == 0x0010 {
			summary.dmas = append(summary.dmas, dma)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary.rows == 0 {
		t.Fatal("PTO2 reference cadence trace has no rows")
	}
	if len(summary.dmas) == 0 {
		t.Fatal("PTO2 reference cadence trace has no uploader DMA rows")
	}
	return summary
}

func matchPTO2UploaderCadence(goDmas []pto2GoBoundaryEvent, refDmas []pto2ReferenceTimingDMARow) []pto2UploaderCadenceMatch {
	refByVMAddr := make(map[uint16]pto2ReferenceTimingDMARow)
	for _, dma := range refDmas {
		if _, ok := refByVMAddr[dma.vmaddr]; !ok {
			refByVMAddr[dma.vmaddr] = dma
		}
	}
	var matches []pto2UploaderCadenceMatch
	for _, dma := range goDmas {
		ref, ok := refByVMAddr[dma.vmaddr]
		if !ok {
			continue
		}
		matches = append(matches, pto2UploaderCadenceMatch{
			vmaddr: dma.vmaddr,
			goDMA:  dma,
			refDMA: ref,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].vmaddr < matches[j].vmaddr
	})
	return matches
}

func findPTO2UploaderCadenceMatch(matches []pto2UploaderCadenceMatch, vmaddr uint16) (pto2UploaderCadenceMatch, bool) {
	for _, match := range matches {
		if match.vmaddr == vmaddr {
			return match, true
		}
	}
	return pto2UploaderCadenceMatch{}, false
}

type pto2CPUCompareTrace struct {
	source          string
	rows            int
	instructionRows int
	firstFrame      int
	lastFrame       int
	instructions    []pto2CPUCompareRow
	allRows         []pto2CPUCompareRow
	uploader2098    pto2CPUCompareRow
}

type pto2CPUCompareRow struct {
	line                int
	kind                string
	frame               int
	cycles              uint64
	pb                  uint8
	pc                  uint16
	opcode              uint8
	operand0            uint8
	operand1            uint8
	cyInFrame           uint64
	hcounter            uint16
	vcounter            uint16
	field               uint8
	a                   uint16
	x                   uint16
	y                   uint16
	p                   uint8
	db                  uint8
	d                   uint16
	s                   uint16
	mdr                 uint8
	mar                 uint32
	refresh             uint8
	wai                 uint8
	stp                 uint8
	phase               string
	clocks              uint64
	dramRefreshPosition uint64
	dmaCounter          uint64
	addr                uint32
	value               uint8
	channel             int
	dmap                uint8
	bbad                uint8
	a1b                 uint8
	a1t                 uint16
	vmain               uint8
	vmaddr              uint16
	das                 uint16
	count               uint32
}

type pto2CPUCompareJSONRow struct {
	Kind                string `json:"kind"`
	Event               string `json:"event"`
	Frame               int    `json:"frame"`
	Cycles              uint64 `json:"cycles"`
	Cycle               uint64 `json:"cycle"`
	PB                  uint64 `json:"pb"`
	PC                  uint64 `json:"pc"`
	Opcode              uint64 `json:"opcode"`
	Operand0            uint64 `json:"operand0"`
	Operand1            uint64 `json:"operand1"`
	CyInFrame           uint64 `json:"cy_in_frame"`
	HCounter            uint64 `json:"hcounter"`
	VCounter            uint64 `json:"vcounter"`
	Field               uint64 `json:"field"`
	A                   uint64 `json:"a"`
	X                   uint64 `json:"x"`
	Y                   uint64 `json:"y"`
	P                   uint64 `json:"p"`
	DB                  uint64 `json:"db"`
	D                   uint64 `json:"d"`
	S                   uint64 `json:"s"`
	MDR                 uint64 `json:"mdr"`
	MAR                 uint64 `json:"mar"`
	Refresh             uint64 `json:"refresh"`
	WAI                 uint64 `json:"wai"`
	STP                 uint64 `json:"stp"`
	Phase               string `json:"phase"`
	Clocks              uint64 `json:"clocks"`
	DRAMRefreshPosition uint64 `json:"dramRefreshPosition"`
	DMACounter          uint64 `json:"dmaCounter"`
	Addr                uint64 `json:"addr"`
	Value               uint64 `json:"value"`
	Channel             int    `json:"channel"`
	DMAP                uint64 `json:"dmap"`
	BBAD                uint64 `json:"bbad"`
	A1B                 uint64 `json:"a1b"`
	A1T                 uint64 `json:"a1t"`
	VMAIN               uint64 `json:"vmain"`
	VMAddr              uint64 `json:"vmaddr"`
	DAS                 uint64 `json:"das"`
	Count               uint64 `json:"count"`
}

type pto2CPUCompareResult struct {
	kind      string
	detail    string
	baseDelta int64
	alignGo   pto2CPUCompareRow
	alignRef  pto2CPUCompareRow
	goRow     pto2CPUCompareRow
	refRow    pto2CPUCompareRow
}

type pto2CPURefreshAttribution struct {
	prev       pto2CPUCompareRow
	start      pto2CPUCompareRow
	refStart   pto2CPUCompareRow
	next       pto2CPUCompareRow
	refNext    pto2CPUCompareRow
	refresh    []pto2CPUCompareRow
	baseDelta  int64
	prevDelta  int64
	startDelta int64
	nextDelta  int64
}

type pto2CPUDMAInstructionAttribution struct {
	prev       pto2CPUCompareRow
	refPrev    pto2CPUCompareRow
	start      pto2CPUCompareRow
	refStart   pto2CPUCompareRow
	next       pto2CPUCompareRow
	refNext    pto2CPUCompareRow
	goWrite    pto2CPUCompareRow
	refWrite   pto2CPUCompareRow
	goDMA      pto2CPUCompareRow
	refDMA     pto2CPUCompareRow
	refRefresh []pto2CPUCompareRow
	baseDelta  int64
	prevDelta  int64
	startDelta int64
	nextDelta  int64
}

type pto2CPUC0946ELongDMAInstructionAttribution struct {
	prev             pto2CPUCompareRow
	refPrev          pto2CPUCompareRow
	start            pto2CPUCompareRow
	refStart         pto2CPUCompareRow
	next             pto2CPUCompareRow
	refNext          pto2CPUCompareRow
	goWrite          pto2CPUCompareRow
	refWrite         pto2CPUCompareRow
	goDMA            pto2CPUCompareRow
	refDMA           pto2CPUCompareRow
	goFrameRows      []pto2CPUCompareRow
	refRefresh       []pto2CPUCompareRow
	goTransferWrites int
	baseDelta        int64
	prevDelta        int64
	startDelta       int64
	nextDelta        int64
}

type pto2CPUC0943FCGRAMDMAInstructionAttribution struct {
	prev             pto2CPUCompareRow
	refPrev          pto2CPUCompareRow
	start            pto2CPUCompareRow
	refStart         pto2CPUCompareRow
	next             pto2CPUCompareRow
	refNext          pto2CPUCompareRow
	goWrite          pto2CPUCompareRow
	refWrite         pto2CPUCompareRow
	goDMA            pto2CPUCompareRow
	refDMA           pto2CPUCompareRow
	refRefresh       []pto2CPUCompareRow
	goTransferWrites int
	baseDelta        int64
	prevDelta        int64
	startDelta       int64
	nextDelta        int64
}

type pto2CPUC09498INIDISPDMAInstructionAttribution struct {
	prev             pto2CPUCompareRow
	refPrev          pto2CPUCompareRow
	start            pto2CPUCompareRow
	refStart         pto2CPUCompareRow
	next             pto2CPUCompareRow
	refNext          pto2CPUCompareRow
	goWrite          pto2CPUCompareRow
	refWrite         pto2CPUCompareRow
	goDMA            pto2CPUCompareRow
	refDMA           pto2CPUCompareRow
	refRefresh       []pto2CPUCompareRow
	goTransferWrites int
	baseDelta        int64
	prevDelta        int64
	startDelta       int64
	nextDelta        int64
}

type pto2CPUNormalizedCompareResult struct {
	result                                  pto2CPUCompareResult
	refreshAttributions                     []pto2CPURefreshAttribution
	dmaInstructionAttributions              []pto2CPUDMAInstructionAttribution
	c0946ELongDMAInstructionAttributions    []pto2CPUC0946ELongDMAInstructionAttribution
	c0943FCGRAMDMAInstructionAttributions   []pto2CPUC0943FCGRAMDMAInstructionAttribution
	c09498INIDISPDMAInstructionAttributions []pto2CPUC09498INIDISPDMAInstructionAttribution
}

func readPTO2CPUCompareTrace(t *testing.T, raw []byte, source string, minFrame int) pto2CPUCompareTrace {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("empty PTO2 %s CPU trace", source)
	}
	trace := pto2CPUCompareTrace{
		source:     source,
		firstFrame: -1,
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if minFrame >= 0 {
			frame, ok := pto2JSONLineFrame(line)
			if !ok || frame < minFrame {
				continue
			}
		}
		trace.rows = lineNo
		var fields pto2CPUCompareJSONRow
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode PTO2 %s CPU trace line %d: %v", source, lineNo, err)
		}
		cycles := fields.Cycles
		if cycles == 0 {
			cycles = fields.Cycle
		}
		row := pto2CPUCompareRow{
			line:                trace.rows,
			kind:                pto2CPUCompareKind(fields.Kind, fields.Event),
			frame:               fields.Frame,
			cycles:              cycles,
			pb:                  uint8(fields.PB),
			pc:                  uint16(fields.PC),
			opcode:              uint8(fields.Opcode),
			operand0:            uint8(fields.Operand0),
			operand1:            uint8(fields.Operand1),
			cyInFrame:           fields.CyInFrame,
			hcounter:            uint16(fields.HCounter),
			vcounter:            uint16(fields.VCounter),
			field:               uint8(fields.Field),
			a:                   uint16(fields.A),
			x:                   uint16(fields.X),
			y:                   uint16(fields.Y),
			p:                   uint8(fields.P),
			db:                  uint8(fields.DB),
			d:                   uint16(fields.D),
			s:                   uint16(fields.S),
			mdr:                 uint8(fields.MDR),
			mar:                 uint32(fields.MAR),
			refresh:             uint8(fields.Refresh),
			wai:                 uint8(fields.WAI),
			stp:                 uint8(fields.STP),
			phase:               fields.Phase,
			clocks:              fields.Clocks,
			dramRefreshPosition: fields.DRAMRefreshPosition,
			dmaCounter:          fields.DMACounter,
			addr:                uint32(fields.Addr),
			value:               uint8(fields.Value),
			channel:             fields.Channel,
			dmap:                uint8(fields.DMAP),
			bbad:                uint8(fields.BBAD),
			a1b:                 uint8(fields.A1B),
			a1t:                 uint16(fields.A1T),
			vmain:               uint8(fields.VMAIN),
			vmaddr:              uint16(fields.VMAddr),
			das:                 uint16(fields.DAS),
			count:               uint32(fields.Count),
		}
		trace.allRows = append(trace.allRows, row)
		if trace.firstFrame < 0 || row.frame < trace.firstFrame {
			trace.firstFrame = row.frame
		}
		if row.frame > trace.lastFrame {
			trace.lastFrame = row.frame
		}
		switch row.kind {
		case "instruction":
			trace.instructionRows++
			if trace.uploader2098.line == 0 {
				trace.instructions = append(trace.instructions, row)
			}
		case "dma-start":
			if trace.uploader2098.line == 0 && row.channel == 7 && row.vmain == 0x80 && row.das == 0x0010 && row.vmaddr == 0x2098 {
				trace.uploader2098 = row
				return trace
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lineNo == 0 {
		t.Fatalf("PTO2 %s CPU trace has no rows", source)
	}
	return trace
}

func pto2JSONLineFrame(line []byte) (int, bool) {
	const key = `"frame":`
	idx := bytes.Index(line, []byte(key))
	if idx < 0 {
		return 0, false
	}
	idx += len(key)
	n := 0
	start := idx
	for idx < len(line) && line[idx] >= '0' && line[idx] <= '9' {
		n = n*10 + int(line[idx]-'0')
		idx++
	}
	return n, idx > start
}

func pto2CPUCompareKind(kind, event string) string {
	kind = strings.ToLower(kind)
	event = strings.ToLower(event)
	switch {
	case kind == "instruction" || kind == "insn" || event == "instruction" || event == "cpu-instruction" || event == "insn":
		return "instruction"
	case kind == "dma-start" || event == "dma-start":
		return "dma-start"
	default:
		return kind
	}
}

func comparePTO2CPUTraces(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUCompareResult {
	t.Helper()
	goStart, refStart, ok := alignPTO2CPUTraceInstructions(goTrace, refTrace)
	if !ok {
		t.Fatalf("no shared PTO2 instruction signature near Go frame %d before VMADDR $2098", goTrace.firstFrame)
	}
	result := pto2CPUCompareResult{
		baseDelta: int64(goTrace.instructions[goStart].cycles) - int64(refTrace.instructions[refStart].cycles),
		alignGo:   goTrace.instructions[goStart],
		alignRef:  refTrace.instructions[refStart],
	}
	for gi, ri := goStart, refStart; gi < len(goTrace.instructions) && ri < len(refTrace.instructions); gi, ri = gi+1, ri+1 {
		goRow := goTrace.instructions[gi]
		refRow := refTrace.instructions[ri]
		if !samePTO2CPUInstruction(goRow, refRow) {
			result.kind = "instruction-sequence"
			result.detail = "pb:pc/opcode/operand sequence differs"
			result.goRow = goRow
			result.refRow = refRow
			return result
		}
		if detail := pto2CPUStateDiff(goRow, refRow); detail != "" {
			result.kind = "cpu-state"
			result.detail = detail
			result.goRow = goRow
			result.refRow = refRow
			return result
		}
		delta := int64(goRow.cycles) - int64(refRow.cycles)
		if absInt64(delta-result.baseDelta) > 8 {
			result.kind = "cycle-delta"
			result.detail = "cycle delta moved by more than one 8-cycle DMA quantum"
			result.goRow = goRow
			result.refRow = refRow
			return result
		}
	}
	return result
}

func comparePTO2CPUTracesNormalizeRefreshAttribution(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUNormalizedCompareResult {
	t.Helper()
	return comparePTO2CPUTracesNormalizeAttributionsMode(t, goTrace, refTrace, false, false, false, false)
}

func comparePTO2CPUTracesNormalizeDMAInstructionAttribution(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUNormalizedCompareResult {
	t.Helper()
	return comparePTO2CPUTracesNormalizeAttributionsMode(t, goTrace, refTrace, true, false, false, false)
}

func comparePTO2CPUTracesNormalizeC0946ELongDMAInstructionAttribution(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUNormalizedCompareResult {
	t.Helper()
	return comparePTO2CPUTracesNormalizeAttributionsMode(t, goTrace, refTrace, true, true, false, false)
}

func comparePTO2CPUTracesNormalizeC0943FCGRAMDMAInstructionAttribution(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUNormalizedCompareResult {
	t.Helper()
	return comparePTO2CPUTracesNormalizeAttributionsMode(t, goTrace, refTrace, true, true, true, false)
}

func comparePTO2CPUTracesNormalizeAttributions(t *testing.T, goTrace, refTrace pto2CPUCompareTrace) pto2CPUNormalizedCompareResult {
	t.Helper()
	return comparePTO2CPUTracesNormalizeAttributionsMode(t, goTrace, refTrace, true, true, true, true)
}

func comparePTO2CPUTracesNormalizeAttributionsMode(t *testing.T, goTrace, refTrace pto2CPUCompareTrace, normalizeDMAInstruction, normalizeC0946ELongDMAInstruction, normalizeC0943FCGRAMDMAInstruction, normalizeC09498INIDISPDMAInstruction bool) pto2CPUNormalizedCompareResult {
	t.Helper()
	goStart, refStart, ok := alignPTO2CPUTraceInstructions(goTrace, refTrace)
	if !ok {
		t.Fatalf("no shared PTO2 instruction signature near Go frame %d before VMADDR $2098", goTrace.firstFrame)
	}
	result := pto2CPUCompareResult{
		baseDelta: int64(goTrace.instructions[goStart].cycles) - int64(refTrace.instructions[refStart].cycles),
		alignGo:   goTrace.instructions[goStart],
		alignRef:  refTrace.instructions[refStart],
	}
	out := pto2CPUNormalizedCompareResult{result: result}
	for gi, ri := goStart, refStart; gi < len(goTrace.instructions) && ri < len(refTrace.instructions); gi, ri = gi+1, ri+1 {
		goRow := goTrace.instructions[gi]
		refRow := refTrace.instructions[ri]
		if !samePTO2CPUInstruction(goRow, refRow) {
			result.kind = "instruction-sequence"
			result.detail = "pb:pc/opcode/operand sequence differs"
			result.goRow = goRow
			result.refRow = refRow
			out.result = result
			return out
		}
		if detail := pto2CPUStateDiff(goRow, refRow); detail != "" {
			result.kind = "cpu-state"
			result.detail = detail
			result.goRow = goRow
			result.refRow = refRow
			out.result = result
			return out
		}
		delta := int64(goRow.cycles) - int64(refRow.cycles)
		if absInt64(delta-result.baseDelta) <= 8 {
			continue
		}
		if attr, ok := classifyPTO2CPURefreshAttribution(goTrace, refTrace, goRow, refRow, result.baseDelta); ok {
			out.refreshAttributions = append(out.refreshAttributions, attr)
			continue
		}
		if normalizeDMAInstruction {
			if attr, ok := classifyPTO2CPUDMAInstructionAttribution(goTrace, refTrace, goRow, refRow, result.baseDelta); ok {
				out.dmaInstructionAttributions = append(out.dmaInstructionAttributions, attr)
				continue
			}
		}
		if normalizeC0946ELongDMAInstruction {
			if attr, ok := classifyPTO2CPUC0946ELongDMAInstructionAttribution(goTrace, refTrace, goRow, refRow, result.baseDelta); ok {
				out.c0946ELongDMAInstructionAttributions = append(out.c0946ELongDMAInstructionAttributions, attr)
				continue
			}
		}
		if normalizeC0943FCGRAMDMAInstruction {
			if attr, ok := classifyPTO2CPUC0943FCGRAMDMAInstructionAttribution(goTrace, refTrace, goRow, refRow, result.baseDelta); ok {
				out.c0943FCGRAMDMAInstructionAttributions = append(out.c0943FCGRAMDMAInstructionAttributions, attr)
				continue
			}
		}
		if normalizeC09498INIDISPDMAInstruction {
			if attr, ok := classifyPTO2CPUC09498INIDISPDMAInstructionAttribution(goTrace, refTrace, goRow, refRow, result.baseDelta); ok {
				out.c09498INIDISPDMAInstructionAttributions = append(out.c09498INIDISPDMAInstructionAttributions, attr)
				continue
			}
		}
		result.kind = "cycle-delta"
		result.detail = "cycle delta moved by more than one 8-cycle DMA quantum"
		result.goRow = goRow
		result.refRow = refRow
		out.result = result
		return out
	}
	out.result = result
	return out
}

func classifyPTO2CPURefreshAttribution(goTrace, refTrace pto2CPUCompareTrace, goRow, refRow pto2CPUCompareRow, baseDelta int64) (pto2CPURefreshAttribution, bool) {
	if goRow.kind != "instruction" || refRow.kind != "instruction" {
		return pto2CPURefreshAttribution{}, false
	}
	if !samePTO2CPUInstruction(goRow, refRow) || pto2CPUStateDiff(goRow, refRow) != "" {
		return pto2CPURefreshAttribution{}, false
	}
	startDelta := int64(goRow.cycles) - int64(refRow.cycles)
	if startDelta-baseDelta != 40 {
		return pto2CPURefreshAttribution{}, false
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPURefreshAttribution{}, false
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPURefreshAttribution{}, false
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPURefreshAttribution{}, false
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPURefreshAttribution{}, false
	}

	if !samePTO2CPUInstruction(goPrev, refPrev) || !samePTO2CPUInstruction(goNext, refNext) {
		return pto2CPURefreshAttribution{}, false
	}
	if pto2CPUStateDiff(goPrev, refPrev) != "" || pto2CPUStateDiff(goNext, refNext) != "" {
		return pto2CPURefreshAttribution{}, false
	}

	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if prevDelta != baseDelta || nextDelta != baseDelta {
		return pto2CPURefreshAttribution{}, false
	}

	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refRow.line, refNext.line)
	if len(refRefresh) == 0 {
		return pto2CPURefreshAttribution{}, false
	}
	if len(pto2RefreshRowsBetween(goTrace.allRows, goRow.line, goNext.line)) != 0 {
		return pto2CPURefreshAttribution{}, false
	}
	firstRefresh := refRefresh[0]
	lastRefresh := refRefresh[len(refRefresh)-1]
	if lastRefresh.cycles < firstRefresh.cycles || lastRefresh.cycles-firstRefresh.cycles != 40 {
		return pto2CPURefreshAttribution{}, false
	}

	goPrevToStart := goRow.cycles - goPrev.cycles
	refPrevToStart := refRow.cycles - refPrev.cycles
	goStartToNext := goNext.cycles - goRow.cycles
	refStartToNext := refNext.cycles - refRow.cycles
	if goPrevToStart != refPrevToStart+40 || refStartToNext != goStartToNext+40 {
		return pto2CPURefreshAttribution{}, false
	}

	return pto2CPURefreshAttribution{
		prev:       goPrev,
		start:      goRow,
		refStart:   refRow,
		next:       goNext,
		refNext:    refNext,
		refresh:    refRefresh,
		baseDelta:  baseDelta,
		prevDelta:  prevDelta,
		startDelta: startDelta,
		nextDelta:  nextDelta,
	}, true
}

func classifyPTO2CPUDMAInstructionAttribution(goTrace, refTrace pto2CPUCompareTrace, goRow, refRow pto2CPUCompareRow, baseDelta int64) (pto2CPUDMAInstructionAttribution, bool) {
	if goRow.kind != "instruction" || refRow.kind != "instruction" {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if !samePTO2CPUInstruction(goRow, refRow) || pto2CPUStateDiff(goRow, refRow) != "" {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if goRow.pb != 0xc0 || refRow.pb != 0xc0 ||
		goRow.pc != 0x8e66 || refRow.pc != 0x8e66 ||
		goRow.opcode != 0xea || refRow.opcode != 0xea {
		return pto2CPUDMAInstructionAttribution{}, false
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}

	if !samePTO2CPUInstruction(goPrev, refPrev) || !samePTO2CPUInstruction(goNext, refNext) {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if pto2CPUStateDiff(goPrev, refPrev) != "" || pto2CPUStateDiff(goNext, refNext) != "" {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if goPrev.pb != 0xc0 || goPrev.pc != 0x8e63 || goPrev.opcode != 0x8d ||
		goPrev.operand0 != 0x0b || goPrev.operand1 != 0x42 {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if goNext.pb != 0xc0 || goNext.pc != 0x8e67 || goNext.opcode != 0x64 {
		return pto2CPUDMAInstructionAttribution{}, false
	}

	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	startDelta := int64(goRow.cycles) - int64(refRow.cycles)
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if absInt64(startDelta-baseDelta) <= 8 {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if absInt64(prevDelta-baseDelta) > 8 || absInt64(nextDelta-baseDelta) > 8 {
		return pto2CPUDMAInstructionAttribution{}, false
	}

	goBefore := pto2NonInstructionRowsBetween(goTrace, goPrev.line, goRow.line)
	refBefore := pto2NonInstructionRowsBetween(refTrace, refPrev.line, refRow.line)
	refAfter := pto2NonInstructionRowsBetween(refTrace, refRow.line, refNext.line)
	goWrite, ok := findPTO2IOWrite(goBefore, 0x420b)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	refWrite, ok := findPTO2IOWrite(refBefore, 0x420b)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if goWrite.value != 0x80 || refWrite.value != 0x80 || goWrite.cycles != refWrite.cycles {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	goDMA, ok := findPTO2DMAStart(goBefore, 7, 0x00, 0x04, 0x00, 0x0a00, 0x80, 0x0000, 0x0220)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	refDMA, ok := findPTO2DMAStart(refAfter, 7, 0x00, 0x04, 0x00, 0x0a00, 0x80, 0x0000, 0x0220)
	if !ok {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goDMA.cycles)-int64(refDMA.cycles)-baseDelta) > 8 {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	if len(pto2NonInstructionRowsBetween(goTrace, goRow.line, goNext.line)) != 0 {
		return pto2CPUDMAInstructionAttribution{}, false
	}
	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refRow.line, refNext.line)
	if len(refRefresh) == 0 {
		return pto2CPUDMAInstructionAttribution{}, false
	}

	return pto2CPUDMAInstructionAttribution{
		prev:       goPrev,
		refPrev:    refPrev,
		start:      goRow,
		refStart:   refRow,
		next:       goNext,
		refNext:    refNext,
		goWrite:    goWrite,
		refWrite:   refWrite,
		goDMA:      goDMA,
		refDMA:     refDMA,
		refRefresh: refRefresh,
		baseDelta:  baseDelta,
		prevDelta:  prevDelta,
		startDelta: startDelta,
		nextDelta:  nextDelta,
	}, true
}

func classifyPTO2CPUC0946ELongDMAInstructionAttribution(goTrace, refTrace pto2CPUCompareTrace, goRow, refRow pto2CPUCompareRow, baseDelta int64) (pto2CPUC0946ELongDMAInstructionAttribution, bool) {
	if goRow.kind != "instruction" || refRow.kind != "instruction" {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if !samePTO2CPUInstruction(goRow, refRow) || pto2CPUStateDiff(goRow, refRow) != "" {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if goRow.frame != 1 || refRow.frame != 1 ||
		goRow.pb != 0xc0 || refRow.pb != 0xc0 ||
		goRow.pc != 0x946e || refRow.pc != 0x946e ||
		goRow.opcode != 0xea || refRow.opcode != 0xea {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	if !samePTO2CPUInstruction(goPrev, refPrev) || !samePTO2CPUInstruction(goNext, refNext) {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if pto2CPUStateDiff(goPrev, refPrev) != "" || pto2CPUStateDiff(goNext, refNext) != "" {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if goPrev.pb != 0xc0 || goPrev.pc != 0x946b || goPrev.opcode != 0x8d ||
		goPrev.operand0 != 0x0b || goPrev.operand1 != 0x42 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if goNext.pb != 0xc0 || goNext.pc != 0x946f || goNext.opcode != 0x60 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	startDelta := int64(goRow.cycles) - int64(refRow.cycles)
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if absInt64(startDelta-baseDelta) <= 8 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if absInt64(prevDelta-baseDelta) > 8 || absInt64(nextDelta-baseDelta) > 8 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	goBefore := pto2NonInstructionRowsBetween(goTrace, goPrev.line, goRow.line)
	refBefore := pto2NonInstructionRowsBetween(refTrace, refPrev.line, refRow.line)
	refAfter := pto2NonInstructionRowsBetween(refTrace, refRow.line, refNext.line)
	goWrite, ok := findPTO2IOWrite(goBefore, 0x420b)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	refWrite, ok := findPTO2IOWrite(refBefore, 0x420b)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if goWrite.value != 0x80 || refWrite.value != 0x80 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goWrite.cycles)-int64(refWrite.cycles)-baseDelta) > 8 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	goDMA, ok := findPTO2DMAStart(goBefore, 7, 0x09, 0x18, 0x00, 0x9415, 0x80, 0x0000, 0x0000)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	refDMA, ok := findPTO2DMAStart(refAfter, 7, 0x09, 0x18, 0x00, 0x9415, 0x80, 0x0000, 0x0000)
	if !ok {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goDMA.cycles)-int64(refDMA.cycles)-baseDelta) > 8 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	if goDMA.count != 65536 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	goTransferWrites := countPTO2IOWritesBetween(goTrace, goDMA.line, goRow.line, 0x2118, 0x2119)
	if goTransferWrites != int(goDMA.count) {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	goFrameRows := pto2NonInstructionRowsBetween(goTrace, goRow.line, goNext.line)
	if !pto2FrameBoundaryRows(goFrameRows) {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}
	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refDMA.line, refNext.line)
	if len(refRefresh) == 0 {
		return pto2CPUC0946ELongDMAInstructionAttribution{}, false
	}

	return pto2CPUC0946ELongDMAInstructionAttribution{
		prev:             goPrev,
		refPrev:          refPrev,
		start:            goRow,
		refStart:         refRow,
		next:             goNext,
		refNext:          refNext,
		goWrite:          goWrite,
		refWrite:         refWrite,
		goDMA:            goDMA,
		refDMA:           refDMA,
		goFrameRows:      goFrameRows,
		refRefresh:       refRefresh,
		goTransferWrites: goTransferWrites,
		baseDelta:        baseDelta,
		prevDelta:        prevDelta,
		startDelta:       startDelta,
		nextDelta:        nextDelta,
	}, true
}

func classifyPTO2CPUC0943FCGRAMDMAInstructionAttribution(goTrace, refTrace pto2CPUCompareTrace, goRow, refRow pto2CPUCompareRow, baseDelta int64) (pto2CPUC0943FCGRAMDMAInstructionAttribution, bool) {
	if goRow.kind != "instruction" || refRow.kind != "instruction" {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if !samePTO2CPUInstruction(goRow, refRow) || pto2CPUStateDiff(goRow, refRow) != "" {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if goRow.frame != 2 || refRow.frame != 2 ||
		goRow.pb != 0xc0 || refRow.pb != 0xc0 ||
		goRow.pc != 0x943f || refRow.pc != 0x943f ||
		goRow.opcode != 0xea || refRow.opcode != 0xea {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	if !samePTO2CPUInstruction(goPrev, refPrev) || !samePTO2CPUInstruction(goNext, refNext) {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if pto2CPUStateDiff(goPrev, refPrev) != "" || pto2CPUStateDiff(goNext, refNext) != "" {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if goPrev.pb != 0xc0 || goPrev.pc != 0x943c || goPrev.opcode != 0x8d ||
		goPrev.operand0 != 0x0b || goPrev.operand1 != 0x42 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if goNext.pb != 0xc0 || goNext.pc != 0x9440 || goNext.opcode != 0x60 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	startDelta := int64(goRow.cycles) - int64(refRow.cycles)
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if absInt64(startDelta-baseDelta) <= 8 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if absInt64(prevDelta-baseDelta) > 8 || absInt64(nextDelta-baseDelta) > 8 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	goBefore := pto2NonInstructionRowsBetween(goTrace, goPrev.line, goRow.line)
	refBefore := pto2NonInstructionRowsBetween(refTrace, refPrev.line, refRow.line)
	refAfter := pto2NonInstructionRowsBetween(refTrace, refRow.line, refNext.line)
	goWrite, ok := findPTO2IOWrite(goBefore, 0x420b)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	refWrite, ok := findPTO2IOWrite(refBefore, 0x420b)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if goWrite.value != 0x80 || refWrite.value != 0x80 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goWrite.cycles)-int64(refWrite.cycles)-baseDelta) > 8 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	goDMA, ok := findPTO2DMAStart(goBefore, 7, 0x08, 0x22, 0x00, 0x9415, 0x80, 0x8000, 0x0200)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	refDMA, ok := findPTO2DMAStart(refAfter, 7, 0x08, 0x22, 0x00, 0x9415, 0x80, 0x8000, 0x0200)
	if !ok {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goDMA.cycles)-int64(refDMA.cycles)-baseDelta) > 8 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if goDMA.count != 512 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	goTransferWrites := countPTO2IOWritesBetween(goTrace, goDMA.line, goRow.line, 0x2122)
	if goTransferWrites != 0 && goTransferWrites != int(goDMA.count) {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	if len(pto2NonInstructionRowsBetween(goTrace, goRow.line, goNext.line)) != 0 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}
	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refDMA.line, refNext.line)
	if len(refRefresh) == 0 {
		return pto2CPUC0943FCGRAMDMAInstructionAttribution{}, false
	}

	return pto2CPUC0943FCGRAMDMAInstructionAttribution{
		prev:             goPrev,
		refPrev:          refPrev,
		start:            goRow,
		refStart:         refRow,
		next:             goNext,
		refNext:          refNext,
		goWrite:          goWrite,
		refWrite:         refWrite,
		goDMA:            goDMA,
		refDMA:           refDMA,
		refRefresh:       refRefresh,
		goTransferWrites: goTransferWrites,
		baseDelta:        baseDelta,
		prevDelta:        prevDelta,
		startDelta:       startDelta,
		nextDelta:        nextDelta,
	}, true
}

func classifyPTO2CPUC09498INIDISPDMAInstructionAttribution(goTrace, refTrace pto2CPUCompareTrace, goRow, refRow pto2CPUCompareRow, baseDelta int64) (pto2CPUC09498INIDISPDMAInstructionAttribution, bool) {
	if goRow.kind != "instruction" || refRow.kind != "instruction" {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if !samePTO2CPUInstruction(goRow, refRow) || pto2CPUStateDiff(goRow, refRow) != "" {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if goRow.frame != 2 || refRow.frame != 2 ||
		goRow.pb != 0xc0 || refRow.pb != 0xc0 ||
		goRow.pc != 0x9498 || refRow.pc != 0x9498 ||
		goRow.opcode != 0xea || refRow.opcode != 0xea {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	goPrev, ok := previousPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	refPrev, ok := previousPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	goNext, ok := nextPTO2CPUInstruction(goTrace, goRow)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	refNext, ok := nextPTO2CPUInstruction(refTrace, refRow)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	if !samePTO2CPUInstruction(goPrev, refPrev) || !samePTO2CPUInstruction(goNext, refNext) {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if pto2CPUStateDiff(goPrev, refPrev) != "" || pto2CPUStateDiff(goNext, refNext) != "" {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if goPrev.pb != 0xc0 || goPrev.pc != 0x9495 || goPrev.opcode != 0x8d ||
		goPrev.operand0 != 0x0b || goPrev.operand1 != 0x42 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if goNext.pb != 0xc0 || goNext.pc != 0x9499 || goNext.opcode != 0x60 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	prevDelta := int64(goPrev.cycles) - int64(refPrev.cycles)
	startDelta := int64(goRow.cycles) - int64(refRow.cycles)
	nextDelta := int64(goNext.cycles) - int64(refNext.cycles)
	if absInt64(startDelta-baseDelta) <= 8 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if absInt64(prevDelta-baseDelta) > 8 || absInt64(nextDelta-baseDelta) > 8 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	goBefore := pto2NonInstructionRowsBetween(goTrace, goPrev.line, goRow.line)
	refBefore := pto2NonInstructionRowsBetween(refTrace, refPrev.line, refRow.line)
	refAfter := pto2NonInstructionRowsBetween(refTrace, refRow.line, refNext.line)
	goWrite, ok := findPTO2IOWrite(goBefore, 0x420b)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	refWrite, ok := findPTO2IOWrite(refBefore, 0x420b)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if goWrite.value != 0x80 || refWrite.value != 0x80 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goWrite.cycles)-int64(refWrite.cycles)) > 8 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	goDMA, ok := findPTO2DMAStart(goBefore, 7, 0x08, 0x00, 0x00, 0x9415, 0x80, 0x8000, 0x0220)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	refDMA, ok := findPTO2DMAStart(refAfter, 7, 0x08, 0x00, 0x00, 0x9415, 0x80, 0x8000, 0x0220)
	if !ok {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if absInt64(int64(goDMA.cycles)-int64(refDMA.cycles)) > 8 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if goDMA.count != 544 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	goTransferWrites := countPTO2IOWritesBetween(goTrace, goDMA.line, goRow.line, 0x2100)
	if goTransferWrites != 0 && goTransferWrites != int(goDMA.count) {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	if len(pto2NonInstructionRowsBetween(goTrace, goRow.line, goNext.line)) != 0 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}
	refRefresh := pto2RefreshRowsBetween(refTrace.allRows, refDMA.line, refNext.line)
	if len(refRefresh) == 0 {
		return pto2CPUC09498INIDISPDMAInstructionAttribution{}, false
	}

	return pto2CPUC09498INIDISPDMAInstructionAttribution{
		prev:             goPrev,
		refPrev:          refPrev,
		start:            goRow,
		refStart:         refRow,
		next:             goNext,
		refNext:          refNext,
		goWrite:          goWrite,
		refWrite:         refWrite,
		goDMA:            goDMA,
		refDMA:           refDMA,
		refRefresh:       refRefresh,
		goTransferWrites: goTransferWrites,
		baseDelta:        baseDelta,
		prevDelta:        prevDelta,
		startDelta:       startDelta,
		nextDelta:        nextDelta,
	}, true
}

func alignPTO2CPUTraceInstructions(goTrace, refTrace pto2CPUCompareTrace) (int, int, bool) {
	bestGo := -1
	bestRef := -1
	var bestDelta uint64
	for gi, goRow := range goTrace.instructions {
		if gi >= 256 {
			break
		}
		for ri, refRow := range refTrace.instructions {
			if refRow.frame < goTrace.firstFrame {
				continue
			}
			if !samePTO2CPUInstruction(goRow, refRow) {
				continue
			}
			delta := absUint64Diff(goRow.cycles, refRow.cycles)
			if bestGo < 0 || delta < bestDelta {
				bestGo = gi
				bestRef = ri
				bestDelta = delta
			}
		}
	}
	return bestGo, bestRef, bestGo >= 0
}

func samePTO2CPUInstruction(a, b pto2CPUCompareRow) bool {
	return a.pb == b.pb &&
		a.pc == b.pc &&
		a.opcode == b.opcode &&
		a.operand0 == b.operand0 &&
		a.operand1 == b.operand1
}

func pto2CPUStateDiff(a, b pto2CPUCompareRow) string {
	switch {
	case a.a != b.a:
		return fmt.Sprintf("A differs go=%04X ref=%04X", a.a, b.a)
	case a.x != b.x:
		return fmt.Sprintf("X differs go=%04X ref=%04X", a.x, b.x)
	case a.y != b.y:
		return fmt.Sprintf("Y differs go=%04X ref=%04X", a.y, b.y)
	case a.p != b.p:
		return fmt.Sprintf("P differs go=%02X ref=%02X", a.p, b.p)
	case a.db != b.db:
		return fmt.Sprintf("DB differs go=%02X ref=%02X", a.db, b.db)
	case a.d != b.d:
		return fmt.Sprintf("D differs go=%04X ref=%04X", a.d, b.d)
	case a.s != b.s:
		return fmt.Sprintf("S differs go=%04X ref=%04X", a.s, b.s)
	default:
		return ""
	}
}

type pto2CPUCycleInterval struct {
	from     pto2CPUCompareRow
	to       pto2CPUCompareRow
	duration uint64
}

func previousPTO2CPUInstruction(trace pto2CPUCompareTrace, row pto2CPUCompareRow) (pto2CPUCompareRow, bool) {
	for i, inst := range trace.instructions {
		if inst.line != row.line {
			continue
		}
		if i == 0 {
			return pto2CPUCompareRow{}, false
		}
		return trace.instructions[i-1], true
	}
	return pto2CPUCompareRow{}, false
}

func nextPTO2CPUInstruction(trace pto2CPUCompareTrace, row pto2CPUCompareRow) (pto2CPUCompareRow, bool) {
	for i, inst := range trace.instructions {
		if inst.line != row.line {
			continue
		}
		if i+1 >= len(trace.instructions) {
			return pto2CPUCompareRow{}, false
		}
		return trace.instructions[i+1], true
	}
	return pto2CPUCompareRow{}, false
}

func pto2STAIndYLoopIntervals(trace pto2CPUCompareTrace, throughLine int) []pto2CPUCycleInterval {
	var intervals []pto2CPUCycleInterval
	for i := 1; i < len(trace.instructions); i++ {
		from := trace.instructions[i-1]
		to := trace.instructions[i]
		if to.line > throughLine {
			break
		}
		if from.pb == 0xc0 && from.pc == 0x8151 && from.opcode == 0x91 &&
			to.pb == 0xc0 && to.pc == 0x8153 && to.opcode == 0xc8 {
			intervals = append(intervals, pto2CPUCycleInterval{
				from:     from,
				to:       to,
				duration: to.cycles - from.cycles,
			})
		}
	}
	return intervals
}

func pto2NonInstructionRowsBetween(trace pto2CPUCompareTrace, lineLo, lineHi int) []pto2CPUCompareRow {
	var rows []pto2CPUCompareRow
	for _, row := range trace.allRows {
		if row.line <= lineLo || row.line >= lineHi {
			continue
		}
		if row.kind == "instruction" {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func firstPTO2TraceKindAfter(trace pto2CPUCompareTrace, line int, kind string) (pto2CPUCompareRow, bool) {
	for _, row := range trace.allRows {
		if row.line <= line || row.kind != kind {
			continue
		}
		return row, true
	}
	return pto2CPUCompareRow{}, false
}

func findPTO2IOWrite(rows []pto2CPUCompareRow, addr uint32) (pto2CPUCompareRow, bool) {
	for _, row := range rows {
		if row.kind == "io-write" && row.addr == addr {
			return row, true
		}
	}
	return pto2CPUCompareRow{}, false
}

func findPTO2DMAStart(rows []pto2CPUCompareRow, channel int, dmap, bbad, a1b uint8, a1t uint16, vmain uint8, vmaddr uint16, das uint16) (pto2CPUCompareRow, bool) {
	for _, row := range rows {
		if row.kind == "dma-start" &&
			row.channel == channel &&
			row.dmap == dmap &&
			row.bbad == bbad &&
			row.a1b == a1b &&
			row.a1t == a1t &&
			row.vmain == vmain &&
			row.vmaddr == vmaddr &&
			row.das == das {
			return row, true
		}
	}
	return pto2CPUCompareRow{}, false
}

func countPTO2IOWritesBetween(trace pto2CPUCompareTrace, lineLo, lineHi int, addrs ...uint32) int {
	n := 0
	for _, row := range trace.allRows {
		if row.line <= lineLo || row.line >= lineHi || row.kind != "io-write" {
			continue
		}
		for _, addr := range addrs {
			if row.addr == addr {
				n++
				break
			}
		}
	}
	return n
}

func pto2FrameBoundaryRows(rows []pto2CPUCompareRow) bool {
	return len(rows) == 2 &&
		rows[0].kind == "frame" &&
		rows[1].kind == "frame" &&
		rows[0].phase == "end" &&
		rows[1].phase == "start" &&
		rows[0].cycles == rows[1].cycles
}

func pto2CPUCompareRowLabel(row pto2CPUCompareRow) string {
	label := fmt.Sprintf("line=%d kind=%s f=%d %02X:%04X op=%02X cycle=%d h=%d v=%d A/X/Y/P=%04X/%04X/%04X/%02X",
		row.line, row.kind, row.frame, row.pb, row.pc, row.opcode, row.cycles,
		row.hcounter, row.vcounter, row.a, row.x, row.y, row.p)
	if row.kind == "refresh" {
		label += fmt.Sprintf(" phase=%s clocks=%d refresh=%d dram=%d dmaCounter=%d",
			row.phase, row.clocks, row.refresh, row.dramRefreshPosition, row.dmaCounter)
	}
	if row.kind == "dma-start" {
		label += fmt.Sprintf(" ch=%d dmap=%02X bbad=%02X a1=%02X:%04X vmain=%02X vmaddr=%04X das=%04X count=%d",
			row.channel, row.dmap, row.bbad, row.a1b, row.a1t, row.vmain, row.vmaddr, row.das, row.count)
	}
	if row.kind == "io-read" || row.kind == "io-write" {
		label += fmt.Sprintf(" addr=%06X value=%02X", row.addr, row.value)
	}
	return label
}

func pto2CPUCompareRowsLabel(rows []pto2CPUCompareRow) string {
	if len(rows) == 0 {
		return "none"
	}
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		labels = append(labels, pto2CPUCompareRowLabel(row))
	}
	return strings.Join(labels, "; ")
}

func absUint64Diff(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
