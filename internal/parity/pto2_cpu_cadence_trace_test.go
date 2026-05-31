package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
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

	frameLo := envIntDefault(t, "PTO2_CPU_CADENCE_FRAME_LO", 28)
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
	case off >= 0x2115 && off <= 0x2119:
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
