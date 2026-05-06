package parity

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/disasm"
)

type mathIOEvent struct {
	Frame        int    `json:"frame"`
	Cycles       uint64 `json:"cycles"`
	PB           uint8  `json:"pb"`
	PC           uint16 `json:"pc"`
	Kind         string `json:"kind"`
	Addr         uint32 `json:"addr"`
	Value        uint8  `json:"value"`
	A            uint16 `json:"a"`
	X            uint16 `json:"x"`
	Y            uint16 `json:"y"`
	P            uint8  `json:"p"`
	Multiplicand uint8  `json:"multiplicand"`
	Product      uint16 `json:"product"`
	Pending      uint16 `json:"pending"`
	ReadyCycle   uint64 `json:"ready_cycle"`
	Counter      uint8  `json:"counter"`
	Dividend     uint16 `json:"dividend"`
	Shift        uint16 `json:"shift"`
	Disasm       string `json:"disasm,omitempty"`
}

type mathIOTraceDevice struct {
	dev   bus.MemoryDevice
	sys   *snes.System
	frame *int
	trace *[]mathIOEvent
}

type cpuStatusTraceDevice struct {
	dev   bus.MemoryDevice
	sys   *snes.System
	frame *int
	trace *[]cpuStatusEvent
}

type cpuInstructionEvent struct {
	Frame    int    `json:"frame"`
	Cycles   uint64 `json:"cycles"`
	PB       uint8  `json:"pb"`
	PC       uint16 `json:"pc"`
	Opcode   uint8  `json:"opcode"`
	Operand0 uint8  `json:"operand0"`
	Operand1 uint8  `json:"operand1"`
	HCounter uint16 `json:"hcounter"`
	VCounter uint16 `json:"vcounter"`
	Field    uint8  `json:"field"`
	A        uint16 `json:"a"`
	X        uint16 `json:"x"`
	Y        uint16 `json:"y"`
	P        uint8  `json:"p"`
	DB       uint8  `json:"db"`
	D        uint16 `json:"d"`
	S        uint16 `json:"s"`
	Mar      uint32 `json:"mar"`
	Mdr      uint8  `json:"mdr"`
	Disasm   string `json:"disasm,omitempty"`
}

type cpuStatusEvent struct {
	Frame         int    `json:"frame"`
	Cycles        uint64 `json:"cycles"`
	PB            uint8  `json:"pb"`
	PC            uint16 `json:"pc"`
	Addr          uint32 `json:"addr"`
	Value         uint8  `json:"value"`
	HCounter      uint16 `json:"hcounter"`
	VCounter      uint16 `json:"vcounter"`
	PPUCycles     uint64 `json:"ppu_cycles,omitempty"`
	PPUFrameCount int    `json:"ppu_frame_count,omitempty"`
	PPUHCounter   uint16 `json:"ppu_hcounter,omitempty"`
	PPUVCounter   uint16 `json:"ppu_vcounter,omitempty"`
	PPUField      uint8  `json:"ppu_field,omitempty"`
	A             uint16 `json:"a"`
	X             uint16 `json:"x"`
	Y             uint16 `json:"y"`
	P             uint8  `json:"p"`
	DB            uint8  `json:"db"`
	D             uint16 `json:"d"`
	S             uint16 `json:"s"`
	Disasm        string `json:"disasm,omitempty"`
}

type cpuRefreshEvent struct {
	Frame               int    `json:"frame"`
	Cycles              uint64 `json:"cycles"`
	PB                  uint8  `json:"pb"`
	PC                  uint16 `json:"pc"`
	Phase               string `json:"phase"`
	Clocks              uint64 `json:"clocks"`
	HCounter            uint16 `json:"hcounter"`
	VCounter            uint16 `json:"vcounter"`
	DRAMRefreshPosition uint16 `json:"dramRefreshPosition"`
	DMACounter          uint8  `json:"dmaCounter"`
	Refresh             uint8  `json:"refresh"`
	Mar                 uint32 `json:"mar"`
	Mdr                 uint8  `json:"mdr"`
}

type cpuPhaseEvent struct {
	Event    string `json:"event"`
	Frame    int    `json:"frame"`
	Cycles   uint64 `json:"cycles"`
	PB       uint8  `json:"pb"`
	PC       uint16 `json:"pc"`
	HCounter uint16 `json:"hcounter"`
	VCounter uint16 `json:"vcounter"`
	Field    uint8  `json:"field"`
	Mar      uint32 `json:"mar"`
	Mdr      uint8  `json:"mdr"`
}

func (d *mathIOTraceDevice) Read(addr uint32) uint8 {
	value := d.dev.Read(addr)
	if isMathIOTraceAddr(addr) {
		*d.trace = append(*d.trace, d.event("read", addr, value))
	}
	return value
}

func (d *mathIOTraceDevice) Write(addr uint32, value uint8) {
	if isMathIOTraceAddr(addr) {
		*d.trace = append(*d.trace, d.event("write", addr, value))
	}
	d.dev.Write(addr, value)
}

func (d *mathIOTraceDevice) BlockRead(addr uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = d.Read(addr + uint32(i))
	}
	return buf
}

func (d *mathIOTraceDevice) event(kind string, addr uint32, value uint8) mathIOEvent {
	c := d.sys.CPU
	return mathIOEvent{
		Frame:        *d.frame,
		Cycles:       c.Cycles,
		PB:           c.PB,
		PC:           c.PC,
		Kind:         kind,
		Addr:         addr,
		Value:        value,
		A:            c.A,
		X:            c.X,
		Y:            c.Y,
		P:            c.P,
		Multiplicand: c.MultiplicandA,
		Product:      c.MultiplicationResult,
		Pending:      c.PendingProduct,
		ReadyCycle:   c.ProductReadyCycle,
		Counter:      c.MultiplyCounter,
		Dividend:     c.MultiplyDividend,
		Shift:        c.MultiplyShift,
		Disasm:       disasm.Disassemble65816(c, d.sys.Bus),
	}
}

func (d *cpuStatusTraceDevice) Read(addr uint32) uint8 {
	value := d.dev.Read(addr)
	switch addr & 0xffff {
	case 0x213f, 0x4212:
		*d.trace = append(*d.trace, d.event(addr, value))
	}
	return value
}

func (d *cpuStatusTraceDevice) Write(addr uint32, value uint8) {
	d.dev.Write(addr, value)
}

func (d *cpuStatusTraceDevice) BlockRead(addr uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = d.Read(addr + uint32(i))
	}
	return buf
}

func (d *cpuStatusTraceDevice) event(addr uint32, value uint8) cpuStatusEvent {
	c := d.sys.CPU
	h, v := cpumulBeamAt(c.Cycles)
	ppuState := d.sys.PPU.SaveState()
	return cpuStatusEvent{
		Frame:         *d.frame,
		Cycles:        c.Cycles,
		PB:            c.PB,
		PC:            c.PC,
		Addr:          addr & 0xffff,
		Value:         value,
		HCounter:      h,
		VCounter:      v,
		PPUCycles:     ppuState.Cycles,
		PPUFrameCount: ppuState.FrameCount,
		PPUHCounter:   uint16(ppuState.HCounter),
		PPUVCounter:   uint16(ppuState.VCounter),
		PPUField:      boolByte(ppuState.PPUField),
		A:             c.A,
		X:             c.X,
		Y:             c.Y,
		P:             c.P,
		DB:            c.DB,
		D:             c.D,
		S:             c.S,
		Disasm:        disasm.Disassemble65816(c, d.sys.Bus),
	}
}

func cpumulBeamAt(cycles uint64) (uint16, uint16) {
	const (
		lineCycles  = 1364
		frameCycles = 262 * lineCycles
	)
	frameCycle := cycles % frameCycles
	return uint16(frameCycle % lineCycles), uint16(frameCycle / lineCycles)
}

func TestCPUMulMathIOTrace(t *testing.T) {
	sys, trace := runCPUMulGoMathIOTrace(t)
	tc, ok := higanManifestCase(t, "CPUMul")
	if !ok {
		t.Fatalf("%s has no CPUMul row", higanTestROMManifestPath)
	}
	got := sys.Bus.Read(0x7e1ffc)
	if divergence, ok := higanKnownDivergence(tc, "WRAM", 0x1ffc); ok {
		if got != divergence.Go {
			t.Fatalf("CPUMul WRAM $%04X = %02X, want manifest Go value %02X", divergence.Addr, got, divergence.Go)
		}
	}

	counts := countMathIOEvents(trace)
	t.Logf("CPUMul Go math IO trace events=%d hash=%s writes4202=%d writes4203=%d reads4216=%d reads4217=%d final_wram_1ffc=%02x",
		len(trace), hashMathIOTrace(trace), counts["write4202"], counts["write4203"], counts["read4216"], counts["read4217"],
		got)
	logMathIOTraceSample(t, trace)
}

func runCPUMulGoMathIOTrace(t *testing.T) (*snes.System, []mathIOEvent) {
	t.Helper()
	tc, ok := higanManifestCase(t, "CPUMul")
	if !ok {
		t.Fatalf("%s has no CPUMul row", higanTestROMManifestPath)
	}
	checkFile(t, tc.Path)
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes(rom); got != tc.SHA256 {
		t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
	}

	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()

	var frame int
	var trace []mathIOEvent
	wrapMathIOTracePages(sys, &frame, &trace)

	for frame = 0; frame < tc.Frames; frame++ {
		if err := sys.Run(); err != nil {
			t.Fatal(err)
		}
	}

	counts := countMathIOEvents(trace)
	if counts["write4202"] == 0 {
		t.Fatal("CPUMul Go trace saw no $4202 multiplicand writes")
	}
	if counts["write4203"] == 0 {
		t.Fatal("CPUMul Go trace saw no $4203 multiplier writes")
	}
	if counts["read4216"] == 0 {
		t.Fatal("CPUMul Go trace saw no $4216 product-low reads")
	}
	if counts["read4216"]+counts["read4217"] == 0 {
		t.Fatal("CPUMul Go trace saw no $4216/$4217 product reads")
	}
	return sys, trace
}

func TestCPUMulReferenceTraceArtifact(t *testing.T) {
	path := os.Getenv("HIGAN_CPUMUL_REF_TRACE")
	if path == "" {
		path = os.Getenv("CPUMUL_REF_TRACE")
	}
	if path == "" {
		t.Skip("set HIGAN_CPUMUL_REF_TRACE or CPUMUL_REF_TRACE to a bsnes/ares JSONL math-IO trace artifact")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	refTrace, summary := readReferenceMathIOJSONL(t, raw)
	if summary["write4202"] == 0 || summary["write4203"] == 0 || summary["read4216"]+summary["read4217"] == 0 {
		t.Fatalf("reference trace %s lacks required math IO coverage: %#v", path, summary)
	}
	t.Logf("CPUMul reference trace %s rows=%d sha256=%s writes4202=%d writes4203=%d reads4216=%d reads4217=%d",
		path, summary["rows"], hashBytes(raw), summary["write4202"], summary["write4203"], summary["read4216"], summary["read4217"])
	_, goTrace := runCPUMulGoMathIOTrace(t)
	compareMathIOTrace(t, goTrace, refTrace)
}

func TestCPUMulCycleDriftLocalization(t *testing.T) {
	path := os.Getenv("HIGAN_CPUMUL_REF_TRACE")
	if path == "" {
		path = os.Getenv("CPUMUL_REF_TRACE")
	}
	if path == "" {
		t.Skip("set HIGAN_CPUMUL_REF_TRACE or CPUMUL_REF_TRACE to a bsnes/ares JSONL instruction trace artifact")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	refInstructions, refStatus, refRefresh, refPhase, refMath, summary := readReferenceCPUMulJSONL(t, raw)
	if len(refInstructions) == 0 {
		t.Fatalf("reference trace %s has no instruction rows before first math IO", path)
	}
	if len(refMath) == 0 {
		t.Fatalf("reference trace %s has no math IO rows", path)
	}
	goInstructions, goStatus, goMath := runCPUMulGoTraceToMathCount(t, minInt(3, len(refMath)))
	if len(goMath) == 0 {
		t.Fatal("Go trace did not reach first math IO")
	}
	if len(goInstructions) == 0 {
		t.Fatal("Go trace produced no instruction rows before first math IO")
	}
	t.Logf("CPUMul reference trace rows=%d sha256=%s instruction_rows=%d math_rows=%d",
		summary["rows"], hashBytes(raw), len(refInstructions), len(refMath))
	logCPUMulInitialPhase(t, goInstructions, refInstructions, refPhase)
	compareCPUMulInstructionDrift(t, goInstructions, refInstructions, goMath[0], refMath[0])
	if len(goMath) >= 3 && len(refMath) >= 3 {
		t.Logf("CPUMul event 2 timing: Go frame=%d cycle=%d PB:PC=%02X:%04X; Ref frame=%d cycle=%d PB:PC=%02X:%04X",
			goMath[2].Frame, goMath[2].Cycles, goMath[2].PB, goMath[2].PC,
			refMath[2].Frame, refMath[2].Cycles, refMath[2].PB, refMath[2].PC)
		logCPUMulInstructionHistogram(t, goInstructions, goMath[1].Cycles, goMath[2].Cycles)
		goDrift, refDrift, ok := logCPUMulPostMathInstructionDrift(t, goInstructions, refInstructions, goMath[1], refMath[1])
		logCPUMulStatusWindow(t, goStatus, refStatus, goMath[2], refMath[2])
		if ok {
			logCPUMulStatusReadsBeforeSplit(t, "Go", goStatus, goInstructions, goDrift.Cycles)
			logCPUMulStatusReadsBeforeSplit(t, "Ref", refStatus, refInstructions, refDrift.Cycles)
			logCPUMulSTAT78SplitEvidence(t, goStatus, refStatus, goDrift.Cycles, refDrift.Cycles)
			logCPUMulReferenceRefreshWindow(t, refRefresh, refDrift)
		}
	}
}

func wrapMathIOTracePages(sys *snes.System, frame *int, trace *[]mathIOEvent) {
	for bank := uint32(0); bank < 0x40; bank++ {
		wrapMathIOTracePage(sys, bank, frame, trace)
		wrapMathIOTracePage(sys, bank|0x80, frame, trace)
	}
}

func wrapMathIOTracePage(sys *snes.System, bank uint32, frame *int, trace *[]mathIOEvent) {
	page := uint32(0x42)
	dev := sys.Bus.GetPage(bank, page)
	sys.Bus.Map(bank<<16|page<<8, bank<<16|page<<8|0xff, &mathIOTraceDevice{
		dev:   dev,
		sys:   sys,
		frame: frame,
		trace: trace,
	})
}

func wrapCPUStatusTracePages(sys *snes.System, frame *int, trace *[]cpuStatusEvent) {
	for bank := uint32(0); bank < 0x40; bank++ {
		wrapCPUStatusTracePage(sys, bank, 0x21, frame, trace)
		wrapCPUStatusTracePage(sys, bank|0x80, 0x21, frame, trace)
		wrapCPUStatusTracePage(sys, bank, 0x42, frame, trace)
		wrapCPUStatusTracePage(sys, bank|0x80, 0x42, frame, trace)
	}
}

func wrapCPUStatusTracePage(sys *snes.System, bank, page uint32, frame *int, trace *[]cpuStatusEvent) {
	dev := sys.Bus.GetPage(bank, page)
	sys.Bus.Map(bank<<16|page<<8, bank<<16|page<<8|0xff, &cpuStatusTraceDevice{
		dev:   dev,
		sys:   sys,
		frame: frame,
		trace: trace,
	})
}

func isMathIOTraceAddr(addr uint32) bool {
	switch addr & 0xffff {
	case 0x4202, 0x4203, 0x4216, 0x4217:
		return true
	default:
		return false
	}
}

func countMathIOEvents(trace []mathIOEvent) map[string]int {
	counts := map[string]int{}
	for _, ev := range trace {
		switch ev.Addr & 0xffff {
		case 0x4202:
			if ev.Kind == "write" {
				counts["write4202"]++
			}
		case 0x4203:
			if ev.Kind == "write" {
				counts["write4203"]++
			}
		case 0x4216:
			if ev.Kind == "read" {
				counts["read4216"]++
			}
		case 0x4217:
			if ev.Kind == "read" {
				counts["read4217"]++
			}
		}
	}
	return counts
}

func runCPUMulGoInstructionTraceToFirstMath(t *testing.T) ([]cpuInstructionEvent, []mathIOEvent) {
	instructions, _, math := runCPUMulGoTraceToMathCount(t, 1)
	return instructions, math
}

func runCPUMulGoInstructionTraceToMathCount(t *testing.T, mathEvents int) ([]cpuInstructionEvent, []mathIOEvent) {
	instructions, _, math := runCPUMulGoTraceToMathCount(t, mathEvents)
	return instructions, math
}

func runCPUMulGoTraceToMathCount(t *testing.T, mathEvents int) ([]cpuInstructionEvent, []cpuStatusEvent, []mathIOEvent) {
	t.Helper()
	tc, ok := higanManifestCase(t, "CPUMul")
	if !ok {
		t.Fatalf("%s has no CPUMul row", higanTestROMManifestPath)
	}
	checkFile(t, tc.Path)
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes(rom); got != tc.SHA256 {
		t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
	}

	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()

	var frame int
	var mathTrace []mathIOEvent
	var statusTrace []cpuStatusEvent
	wrapCPUStatusTracePages(sys, &frame, &statusTrace)
	wrapMathIOTracePages(sys, &frame, &mathTrace)

	var instructions []cpuInstructionEvent
	for len(mathTrace) < mathEvents {
		c := sys.CPU
		ppuState := sys.PPU.SaveState()
		instructions = append(instructions, cpuInstructionEvent{
			Cycles:   c.Cycles,
			PB:       c.PB,
			PC:       c.PC,
			Opcode:   sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC)),
			Operand0: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+1)),
			Operand1: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+2)),
			HCounter: uint16(ppuState.HCounter),
			VCounter: uint16(ppuState.VCounter),
			Field:    boolByte(ppuState.PPUField),
			A:        c.A,
			X:        c.X,
			Y:        c.Y,
			P:        c.P,
			DB:       c.DB,
			D:        c.D,
			S:        c.S,
			Disasm:   disasm.Disassemble65816(c, sys.Bus),
		})
		c.Run()
		if len(instructions) > 1000000 {
			t.Fatalf("Go trace did not reach %d math IO events within 1000000 CPU instructions; got %d", mathEvents, len(mathTrace))
		}
	}
	return instructions, statusTrace, mathTrace
}

func hashMathIOTrace(trace []mathIOEvent) string {
	h := sha256.New()
	for _, ev := range trace {
		fmt.Fprintf(h, "%d/%d/%02x/%04x/%s/%06x/%02x/%04x/%04x/%04x/%02x/%02x/%04x/%04x/%d/%d/%04x/%04x\n",
			ev.Frame, ev.Cycles, ev.PB, ev.PC, ev.Kind, ev.Addr, ev.Value,
			ev.A, ev.X, ev.Y, ev.P, ev.Multiplicand, ev.Product, ev.Pending,
			ev.ReadyCycle, ev.Counter, ev.Dividend, ev.Shift)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func logMathIOTraceSample(t *testing.T, trace []mathIOEvent) {
	t.Helper()
	limit := len(trace)
	if limit > 16 {
		limit = 16
	}
	for i := 0; i < limit; i++ {
		ev := trace[i]
		t.Logf("  mathio[%02d] frame=%d cyc=%d PB:PC=%02X:%04X %s $%04X=%02X A=%04X X=%04X Y=%04X P=%02X mul=%02X prod=%04X pending=%04X ready=%d ctr=%d div=%04X shift=%04X ; %s",
			i, ev.Frame, ev.Cycles, ev.PB, ev.PC, ev.Kind, ev.Addr&0xffff, ev.Value,
			ev.A, ev.X, ev.Y, ev.P, ev.Multiplicand, ev.Product, ev.Pending,
			ev.ReadyCycle, ev.Counter, ev.Dividend, ev.Shift, ev.Disasm)
	}
	if len(trace) > limit {
		t.Logf("  ... %d more math IO events", len(trace)-limit)
	}
}

func higanKnownDivergence(tc higanTestROMCase, region string, addr uint32) (higanTestROMKnownDivergence, bool) {
	for _, divergence := range tc.KnownDivergences {
		if divergence.Region == region && divergence.Addr == addr {
			return divergence, true
		}
	}
	return higanTestROMKnownDivergence{}, false
}

func readReferenceMathIOJSONL(t *testing.T, raw []byte) ([]mathIOEvent, map[string]int) {
	t.Helper()
	_, _, _, _, trace, summary := readReferenceCPUMulJSONL(t, raw)
	return trace, summary
}

func readReferenceCPUMulJSONL(t *testing.T, raw []byte) ([]cpuInstructionEvent, []cpuStatusEvent, []cpuRefreshEvent, []cpuPhaseEvent, []mathIOEvent, map[string]int) {
	t.Helper()
	summary := map[string]int{}
	var instructions []cpuInstructionEvent
	var status []cpuStatusEvent
	var refresh []cpuRefreshEvent
	var phase []cpuPhaseEvent
	var trace []mathIOEvent
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		summary["rows"]++
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode reference trace line %d: %v", summary["rows"], err)
		}
		kind, _ := fields["kind"].(string)
		if kind == "" {
			kind, _ = fields["op"].(string)
		}
		if kind == "" {
			kind, _ = fields["type"].(string)
		}
		kind = strings.ToLower(kind)
		if kind == "instruction" {
			instructions = append(instructions, cpuInstructionEvent{
				Frame:    int(jsonNumberFieldDefault(fields, "frame")),
				Cycles:   jsonNumberFieldDefault(fields, "cycles"),
				PB:       uint8(jsonNumberFieldDefault(fields, "pb")),
				PC:       uint16(jsonNumberFieldDefault(fields, "pc")),
				Opcode:   uint8(jsonNumberFieldDefault(fields, "opcode")),
				Operand0: uint8(jsonNumberFieldDefault(fields, "operand0")),
				Operand1: uint8(jsonNumberFieldDefault(fields, "operand1")),
				HCounter: uint16(jsonNumberFieldDefault(fields, "hcounter")),
				VCounter: uint16(jsonNumberFieldDefault(fields, "vcounter")),
				Field:    uint8(jsonNumberFieldDefault(fields, "field")),
				A:        uint16(jsonNumberFieldDefault(fields, "a")),
				X:        uint16(jsonNumberFieldDefault(fields, "x")),
				Y:        uint16(jsonNumberFieldDefault(fields, "y")),
				P:        uint8(jsonNumberFieldDefault(fields, "p")),
				DB:       uint8(jsonNumberFieldDefault(fields, "db")),
				D:        uint16(jsonNumberFieldDefault(fields, "d")),
				S:        uint16(jsonNumberFieldDefault(fields, "s")),
				Mar:      uint32(jsonNumberFieldDefault(fields, "mar")),
				Mdr:      uint8(jsonNumberFieldDefault(fields, "mdr")),
			})
			continue
		}
		if kind == "status-read" {
			addr, ok := jsonNumberField(fields, "addr")
			if !ok {
				addr, ok = jsonNumberField(fields, "address")
			}
			if !ok {
				continue
			}
			value := uint8(jsonNumberFieldDefaultAny(fields, "value", "data"))
			ppuField := uint8(jsonNumberFieldDefault(fields, "ppu_field"))
			if ppuField == 0 && value&0x80 != 0 {
				ppuField = 1
			}
			status = append(status, cpuStatusEvent{
				Frame:         int(jsonNumberFieldDefault(fields, "frame")),
				Cycles:        jsonNumberFieldDefault(fields, "cycles"),
				PB:            uint8(jsonNumberFieldDefault(fields, "pb")),
				PC:            uint16(jsonNumberFieldDefault(fields, "pc")),
				Addr:          uint32(addr),
				Value:         value,
				HCounter:      uint16(jsonNumberFieldDefault(fields, "hcounter")),
				VCounter:      uint16(jsonNumberFieldDefault(fields, "vcounter")),
				PPUCycles:     jsonNumberFieldDefaultAny(fields, "ppu_cycles", "ppuCycles"),
				PPUFrameCount: int(jsonNumberFieldDefaultAny(fields, "ppu_frame_count", "ppuFrameCount")),
				PPUHCounter:   uint16(jsonNumberFieldDefaultAny(fields, "ppu_hcounter", "hcounter")),
				PPUVCounter:   uint16(jsonNumberFieldDefaultAny(fields, "ppu_vcounter", "vcounter")),
				PPUField:      ppuField,
				A:             uint16(jsonNumberFieldDefault(fields, "a")),
				X:             uint16(jsonNumberFieldDefault(fields, "x")),
				Y:             uint16(jsonNumberFieldDefault(fields, "y")),
				P:             uint8(jsonNumberFieldDefault(fields, "p")),
				DB:            uint8(jsonNumberFieldDefault(fields, "db")),
				D:             uint16(jsonNumberFieldDefault(fields, "d")),
				S:             uint16(jsonNumberFieldDefault(fields, "s")),
			})
			switch uint32(addr) & 0xffff {
			case 0x213f:
				summary["status213f"]++
			case 0x4212:
				summary["status4212"]++
			}
			continue
		}
		if kind == "refresh" {
			refresh = append(refresh, cpuRefreshEvent{
				Frame:               int(jsonNumberFieldDefault(fields, "frame")),
				Cycles:              jsonNumberFieldDefaultAny(fields, "cycles", "cycle"),
				PB:                  uint8(jsonNumberFieldDefault(fields, "pb")),
				PC:                  uint16(jsonNumberFieldDefault(fields, "pc")),
				Phase:               jsonStringFieldDefault(fields, "phase"),
				Clocks:              jsonNumberFieldDefault(fields, "clocks"),
				HCounter:            uint16(jsonNumberFieldDefault(fields, "hcounter")),
				VCounter:            uint16(jsonNumberFieldDefault(fields, "vcounter")),
				DRAMRefreshPosition: uint16(jsonNumberFieldDefault(fields, "dramRefreshPosition")),
				DMACounter:          uint8(jsonNumberFieldDefault(fields, "dmaCounter")),
				Refresh:             uint8(jsonNumberFieldDefault(fields, "refresh")),
				Mar:                 uint32(jsonNumberFieldDefault(fields, "mar")),
				Mdr:                 uint8(jsonNumberFieldDefault(fields, "mdr")),
			})
			summary["refresh"]++
			continue
		}
		if kind == "phase" {
			phase = append(phase, cpuPhaseEvent{
				Event:    jsonStringFieldDefault(fields, "event"),
				Frame:    int(jsonNumberFieldDefault(fields, "frame")),
				Cycles:   jsonNumberFieldDefaultAny(fields, "cycles", "cycle"),
				PB:       uint8(jsonNumberFieldDefault(fields, "pb")),
				PC:       uint16(jsonNumberFieldDefault(fields, "pc")),
				HCounter: uint16(jsonNumberFieldDefault(fields, "hcounter")),
				VCounter: uint16(jsonNumberFieldDefault(fields, "vcounter")),
				Field:    uint8(jsonNumberFieldDefault(fields, "field")),
				Mar:      uint32(jsonNumberFieldDefault(fields, "mar")),
				Mdr:      uint8(jsonNumberFieldDefault(fields, "mdr")),
			})
			summary["phase"]++
			continue
		}
		addr, ok := jsonNumberField(fields, "addr")
		if !ok {
			addr, ok = jsonNumberField(fields, "address")
		}
		if !ok {
			continue
		}
		read := kind == "read" || kind == "r"
		write := kind == "write" || kind == "w"
		if !read && !write {
			continue
		}
		addr32 := uint32(addr)
		if !isMathIOTraceAddr(addr32) {
			continue
		}
		ev := mathIOEvent{
			Frame:        int(jsonNumberFieldDefault(fields, "frame")),
			Cycles:       jsonNumberFieldDefault(fields, "cycles"),
			PB:           uint8(jsonNumberFieldDefault(fields, "pb")),
			PC:           uint16(jsonNumberFieldDefault(fields, "pc")),
			Kind:         mapMathIOKind(read, write),
			Addr:         addr32,
			Value:        uint8(jsonNumberFieldDefaultAny(fields, "value", "data")),
			Multiplicand: uint8(jsonNumberFieldDefault(fields, "multiplicand")),
			Product:      uint16(jsonNumberFieldDefault(fields, "product")),
			Pending:      uint16(jsonNumberFieldDefault(fields, "pending")),
			ReadyCycle:   jsonNumberFieldDefaultAny(fields, "ready_cycle", "readyCycle"),
			Counter:      uint8(jsonNumberFieldDefault(fields, "counter")),
			Dividend:     uint16(jsonNumberFieldDefault(fields, "dividend")),
			Shift:        uint16(jsonNumberFieldDefault(fields, "shift")),
		}
		trace = append(trace, ev)
		switch uint32(addr) & 0xffff {
		case 0x4202:
			if write {
				summary["write4202"]++
			}
		case 0x4203:
			if write {
				summary["write4203"]++
			}
		case 0x4216:
			if read {
				summary["read4216"]++
			}
		case 0x4217:
			if read {
				summary["read4217"]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return instructions, status, refresh, phase, trace, summary
}

func compareMathIOTrace(t *testing.T, goTrace, refTrace []mathIOEvent) {
	t.Helper()
	if len(goTrace) == 0 || len(refTrace) == 0 {
		t.Fatalf("cannot compare empty math IO traces: Go=%d Ref=%d", len(goTrace), len(refTrace))
	}
	if len(goTrace) != len(refTrace) {
		t.Logf("CPUMul math IO filtered event count differs: Go=%d Ref=%d; aligning first %d events",
			len(goTrace), len(refTrace), minInt(len(goTrace), len(refTrace)))
	}
	n := minInt(len(goTrace), len(refTrace))
	for i := 0; i < n; i++ {
		g, r := goTrace[i], refTrace[i]
		if g.Kind != r.Kind || (g.Addr&0xffff) != (r.Addr&0xffff) || g.Value != r.Value {
			t.Fatalf("CPUMul math IO sequence mismatch at event %d: Go %s $%04X=%02X frame=%d cyc=%d PB:PC=%02X:%04X; Ref %s $%04X=%02X frame=%d cyc=%d PB:PC=%02X:%04X",
				i, g.Kind, g.Addr&0xffff, g.Value, g.Frame, g.Cycles, g.PB, g.PC,
				r.Kind, r.Addr&0xffff, r.Value, r.Frame, r.Cycles, r.PB, r.PC)
		}
		if g.Frame != r.Frame || g.Cycles != r.Cycles || g.PB != r.PB || g.PC != r.PC {
			t.Logf("CPUMul first math IO timing/context mismatch at event %d: %s $%04X=%02X; Go frame=%d cyc=%d PB:PC=%02X:%04X product=%04X pending=%04X counter=%d dividend=%04X shift=%04X; Ref frame=%d cyc=%d PB:PC=%02X:%04X product=%04X pending=%04X counter=%d dividend=%04X shift=%04X",
				i, g.Kind, g.Addr&0xffff, g.Value,
				g.Frame, g.Cycles, g.PB, g.PC, g.Product, g.Pending, g.Counter, g.Dividend, g.Shift,
				r.Frame, r.Cycles, r.PB, r.PC, r.Product, r.Pending, r.Counter, r.Dividend, r.Shift)
			t.Logf("CPUMul math IO op/address/value sequence matches through %d filtered events", n)
			return
		}
	}
	if len(goTrace) != len(refTrace) {
		t.Fatalf("CPUMul math IO sequence length mismatch after %d matching events: Go=%d Ref=%d", n, len(goTrace), len(refTrace))
	}
	t.Logf("CPUMul math IO op/address/value/timing context matches across %d filtered events", n)
}

func compareCPUMulInstructionDrift(t *testing.T, goTrace, refTrace []cpuInstructionEvent, goFirstMath, refFirstMath mathIOEvent) {
	t.Helper()
	n := minInt(len(goTrace), len(refTrace))
	if n == 0 {
		t.Fatalf("cannot compare empty instruction traces: Go=%d Ref=%d", len(goTrace), len(refTrace))
	}
	var prevDelta int64
	haveDelta := false
	for i := 0; i < n; i++ {
		g, r := goTrace[i], refTrace[i]
		if g.PB != r.PB || g.PC != r.PC || g.Opcode != r.Opcode {
			t.Fatalf("CPUMul instruction sequence mismatch at row %d: Go %02X:%04X opcode=%02X cycle=%d; Ref %02X:%04X opcode=%02X cycle=%d",
				i, g.PB, g.PC, g.Opcode, g.Cycles, r.PB, r.PC, r.Opcode, r.Cycles)
		}
		delta := int64(r.Cycles) - int64(g.Cycles)
		if !haveDelta {
			prevDelta = delta
			haveDelta = true
			continue
		}
		if delta != prevDelta {
			t.Logf("CPUMul first instruction cycle-delta change at row %d: PB:PC=%02X:%04X opcode=%02X operands=%02X %02X; previous_delta=%d current_delta=%d Go cycle=%d Ref cycle=%d Go A/X/Y/P=%04X/%04X/%04X/%02X Ref A/X/Y/P=%04X/%04X/%04X/%02X ; %s",
				i, g.PB, g.PC, g.Opcode, g.Operand0, g.Operand1, prevDelta, delta,
				g.Cycles, r.Cycles, g.A, g.X, g.Y, g.P, r.A, r.X, r.Y, r.P, g.Disasm)
			t.Logf("CPUMul first math write: Go frame=%d cycle=%d PB:PC=%02X:%04X %s $%04X=%02X; Ref frame=%d cycle=%d PB:PC=%02X:%04X %s $%04X=%02X",
				goFirstMath.Frame, goFirstMath.Cycles, goFirstMath.PB, goFirstMath.PC, goFirstMath.Kind, goFirstMath.Addr&0xffff, goFirstMath.Value,
				refFirstMath.Frame, refFirstMath.Cycles, refFirstMath.PB, refFirstMath.PC, refFirstMath.Kind, refFirstMath.Addr&0xffff, refFirstMath.Value)
			return
		}
	}
	if len(goTrace) != len(refTrace) {
		t.Fatalf("CPUMul instruction traces match for %d rows but lengths differ: Go=%d Ref=%d", n, len(goTrace), len(refTrace))
	}
	mathDelta := int64(refFirstMath.Cycles) - int64(goFirstMath.Cycles)
	t.Logf("CPUMul instruction cycle delta remains %d through %d rows; first math delta=%d Go cycle=%d Ref cycle=%d",
		prevDelta, n, mathDelta, goFirstMath.Cycles, refFirstMath.Cycles)
}

func logCPUMulInitialPhase(t *testing.T, goInstructions, refInstructions []cpuInstructionEvent, refPhase []cpuPhaseEvent) {
	t.Helper()
	limit := len(refPhase)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		ev := refPhase[i]
		t.Logf("CPUMul ref phase[%02d] event=%s frame=%d cycle=%d PB:PC=%02X:%04X h=%d v=%d field=%d mar=%06X mdr=%02X",
			i, ev.Event, ev.Frame, ev.Cycles, ev.PB, ev.PC, ev.HCounter, ev.VCounter, ev.Field, ev.Mar, ev.Mdr)
	}
	if len(refInstructions) > 0 {
		ev := refInstructions[0]
		t.Logf("CPUMul ref first instruction cycle=%d PB:PC=%02X:%04X opcode=%02X h=%d v=%d field=%d mar=%06X mdr=%02X",
			ev.Cycles, ev.PB, ev.PC, ev.Opcode, ev.HCounter, ev.VCounter, ev.Field, ev.Mar, ev.Mdr)
	}
	if len(goInstructions) > 0 {
		ev := goInstructions[0]
		t.Logf("CPUMul Go first instruction cycle=%d PB:PC=%02X:%04X opcode=%02X h=%d v=%d field=%d",
			ev.Cycles, ev.PB, ev.PC, ev.Opcode, ev.HCounter, ev.VCounter, ev.Field)
	}
}

func logCPUMulInstructionHistogram(t *testing.T, trace []cpuInstructionEvent, start, end uint64) {
	t.Helper()
	type key struct {
		pb uint8
		pc uint16
		op uint8
	}
	counts := map[key]int{}
	first := map[key]cpuInstructionEvent{}
	total := 0
	for _, ev := range trace {
		if ev.Cycles <= start || ev.Cycles > end {
			continue
		}
		k := key{pb: ev.PB, pc: ev.PC, op: ev.Opcode}
		counts[k]++
		if _, ok := first[k]; !ok {
			first[k] = ev
		}
		total++
	}
	t.Logf("instruction window (%d,%d] total=%d unique=%d", start, end, total, len(counts))
	for rank := 0; rank < 12 && len(counts) > 0; rank++ {
		var best key
		bestCount := -1
		for k, n := range counts {
			if n > bestCount {
				best, bestCount = k, n
			}
		}
		ev := first[best]
		t.Logf("hot[%02d] count=%d PB:PC=%02X:%04X opcode=%02X ; %s",
			rank, bestCount, best.pb, best.pc, best.op, ev.Disasm)
		delete(counts, best)
	}
}

func logCPUMulPostMathInstructionDrift(t *testing.T, goTrace, refTrace []cpuInstructionEvent, goStart, refStart mathIOEvent) (cpuInstructionEvent, cpuInstructionEvent, bool) {
	t.Helper()
	gi := firstInstructionAfter(goTrace, goStart.Cycles)
	ri := firstInstructionAfter(refTrace, refStart.Cycles)
	var prevDelta int64
	haveDelta := false
	for row := 0; gi < len(goTrace) && ri < len(refTrace); row, gi, ri = row+1, gi+1, ri+1 {
		g, r := goTrace[gi], refTrace[ri]
		if g.PB != r.PB || g.PC != r.PC || g.Opcode != r.Opcode {
			t.Logf("CPUMul post-math instruction sequence mismatch at row %d: Go %02X:%04X opcode=%02X cycle=%d; Ref %02X:%04X opcode=%02X cycle=%d",
				row, g.PB, g.PC, g.Opcode, g.Cycles, r.PB, r.PC, r.Opcode, r.Cycles)
			if gi > 0 && ri > 0 {
				pg, pr := goTrace[gi-1], refTrace[ri-1]
				t.Logf("CPUMul post-math previous row before sequence mismatch: Go %02X:%04X opcode=%02X cycle=%d A/P=%04X/%02X; Ref %02X:%04X opcode=%02X cycle=%d A/P=%04X/%02X",
					pg.PB, pg.PC, pg.Opcode, pg.Cycles, pg.A, pg.P,
					pr.PB, pr.PC, pr.Opcode, pr.Cycles, pr.A, pr.P)
			}
			return g, r, true
		}
		delta := int64(r.Cycles) - int64(g.Cycles)
		if !haveDelta {
			prevDelta = delta
			haveDelta = true
			continue
		}
		if delta != prevDelta {
			if gi > 0 && ri > 0 {
				pg, pr := goTrace[gi-1], refTrace[ri-1]
				t.Logf("CPUMul post-math previous row: Go %02X:%04X opcode=%02X cycle=%d; Ref %02X:%04X opcode=%02X cycle=%d",
					pg.PB, pg.PC, pg.Opcode, pg.Cycles, pr.PB, pr.PC, pr.Opcode, pr.Cycles)
			}
			t.Logf("CPUMul post-math cycle-delta change at row %d: PB:PC=%02X:%04X opcode=%02X operands=%02X %02X previous_delta=%d current_delta=%d Go cycle=%d Ref cycle=%d Go A/X/Y/P=%04X/%04X/%04X/%02X Ref A/X/Y/P=%04X/%04X/%04X/%02X ; %s",
				row, g.PB, g.PC, g.Opcode, g.Operand0, g.Operand1, prevDelta, delta,
				g.Cycles, r.Cycles, g.A, g.X, g.Y, g.P, r.A, r.X, r.Y, r.P, g.Disasm)
			logCPUMulRefreshPhase(t, g, r)
			return g, r, true
		}
	}
	if haveDelta {
		t.Logf("CPUMul post-math instruction cycle delta remains %d through compared rows", prevDelta)
	}
	return cpuInstructionEvent{}, cpuInstructionEvent{}, false
}

func firstInstructionAfter(trace []cpuInstructionEvent, cycle uint64) int {
	for i, ev := range trace {
		if ev.Cycles > cycle {
			return i
		}
	}
	return len(trace)
}

func logCPUMulRefreshPhase(t *testing.T, goEvent, refEvent cpuInstructionEvent) {
	t.Helper()
	goLine, goH, goPos := cpumulRefreshPhase(goEvent.Cycles)
	refLine, refH, refPos := cpumulRefreshPhase(refEvent.Cycles)
	t.Logf("CPUMul refresh phase at drift: Go line=%d h=%d dyn_pos=%d; Ref line=%d h=%d dyn_pos=%d",
		goLine, goH, goPos, refLine, refH, refPos)
}

func cpumulRefreshPhase(cycles uint64) (line uint64, h uint64, pos uint64) {
	const scanlineCycles = 1364
	line = cycles / scanlineCycles
	h = cycles % scanlineCycles
	lineStart := line * scanlineCycles
	pos = 530 + 8 - lineStart%8
	return line, h, pos
}

func logCPUMulStatusWindow(t *testing.T, goStatus, refStatus []cpuStatusEvent, goEvent, refEvent mathIOEvent) {
	t.Helper()
	goCounts := countCPUMulStatusBefore(goStatus, goEvent.Cycles)
	refCounts := countCPUMulStatusBefore(refStatus, refEvent.Cycles)
	t.Logf("CPUMul status reads before event 2: Go 213f=%d 4212=%d total=%d; Ref 213f=%d 4212=%d total=%d",
		goCounts[0x213f], goCounts[0x4212], goCounts[0],
		refCounts[0x213f], refCounts[0x4212], refCounts[0])
	logCPUMulStatusTransitions(t, "Go", goStatus, goEvent.Cycles)
	logCPUMulStatusTransitions(t, "Ref", refStatus, refEvent.Cycles)
}

func countCPUMulStatusBefore(trace []cpuStatusEvent, end uint64) map[uint32]int {
	counts := map[uint32]int{}
	for _, ev := range trace {
		if ev.Cycles >= end {
			break
		}
		switch ev.Addr & 0xffff {
		case 0x213f, 0x4212:
			counts[ev.Addr&0xffff]++
			counts[0]++
		}
	}
	return counts
}

func logCPUMulStatusTransitions(t *testing.T, label string, trace []cpuStatusEvent, end uint64) {
	t.Helper()
	type key struct {
		addr  uint32
		value uint8
	}
	var prev key
	havePrev := false
	logged := 0
	total := 0
	for _, ev := range trace {
		if ev.Cycles >= end {
			break
		}
		k := key{addr: ev.Addr & 0xffff, value: ev.Value}
		if havePrev && k == prev {
			continue
		}
		total++
		if logged < 12 {
			t.Logf("CPUMul %s status transition[%02d] cycle=%d PB:PC=%02X:%04X addr=%04X value=%02X h=%d v=%d P=%02X",
				label, logged, ev.Cycles, ev.PB, ev.PC, ev.Addr&0xffff, ev.Value, ev.HCounter, ev.VCounter, ev.P)
			logged++
		}
		prev = k
		havePrev = true
	}
	t.Logf("CPUMul %s status transitions before event 2=%d", label, total)
}

func logCPUMulStatusReadsBeforeSplit(t *testing.T, label string, status []cpuStatusEvent, instructions []cpuInstructionEvent, splitCycle uint64) {
	t.Helper()
	var reads []cpuStatusEvent
	for _, ev := range status {
		if ev.Cycles > splitCycle {
			break
		}
		if ev.Addr&0xffff == 0x213f {
			reads = append(reads, ev)
		}
	}
	if len(reads) == 0 {
		t.Logf("CPUMul %s has no $213F status reads before split cycle %d", label, splitCycle)
		return
	}
	start := len(reads) - 20
	if start < 0 {
		start = 0
	}
	for i := start; i < len(reads); i++ {
		ev := reads[i]
		next, ok := firstInstructionAtOrAfter(instructions, ev.Cycles)
		if !ok {
			t.Logf("CPUMul %s $213F[%02d] cycle=%d PB:PC=%02X:%04X value=%02X preP=%02X preN=%d h=%d v=%d next=<none>",
				label, i-start, ev.Cycles, ev.PB, ev.PC, ev.Value, ev.P, boolByte(ev.P&0x80 != 0), ev.HCounter, ev.VCounter)
			continue
		}
		t.Logf("CPUMul %s $213F[%02d] cycle=%d PB:PC=%02X:%04X value=%02X preP=%02X preN=%d h=%d v=%d next=%02X:%04X nextCycle=%d nextA=%04X nextP=%02X nextN=%d",
			label, i-start, ev.Cycles, ev.PB, ev.PC, ev.Value, ev.P, boolByte(ev.P&0x80 != 0),
			ev.HCounter, ev.VCounter, next.PB, next.PC, next.Cycles, next.A, next.P, boolByte(next.P&0x80 != 0))
	}
}

func logCPUMulSTAT78SplitEvidence(t *testing.T, goStatus, refStatus []cpuStatusEvent, goSplitCycle, refSplitCycle uint64) {
	t.Helper()
	ref, ok := firstCPUMulSTAT78FieldDrop(refStatus, refSplitCycle)
	if !ok {
		t.Logf("CPUMul STAT78 split evidence unavailable: reference has no $213F field drop before cycle %d", refSplitCycle)
		return
	}
	goEv, exact := findCPUMulStatusRead(goStatus, ref.Cycles, ref.PB, ref.PC, 0x213f)
	if !exact {
		var nearest bool
		goEv, nearest = lastCPUMulStatusReadBefore(goStatus, 0x213f, goSplitCycle)
		if !nearest {
			t.Logf("CPUMul STAT78 split evidence: ref cycle=%d PB:PC=%02X:%04X value=%02X ppu h=%d v=%d field=%d; Go has no comparable $213F read before cycle %d",
				ref.Cycles, ref.PB, ref.PC, ref.Value, ref.PPUHCounter, ref.PPUVCounter, ref.PPUField, goSplitCycle)
			return
		}
	}
	match := "matched"
	if !exact {
		match = "nearest"
	}
	t.Logf("CPUMul STAT78 split evidence (%s Go read): Go cycle=%d PB:PC=%02X:%04X value=%02X cpu_beam h=%d v=%d ppu_cycle=%d ppu_frame=%d ppu_h=%d ppu_v=%d ppu_field=%d; Ref cycle=%d PB:PC=%02X:%04X value=%02X ppu_h=%d ppu_v=%d ppu_field=%d",
		match,
		goEv.Cycles, goEv.PB, goEv.PC, goEv.Value, goEv.HCounter, goEv.VCounter,
		goEv.PPUCycles, goEv.PPUFrameCount, goEv.PPUHCounter, goEv.PPUVCounter, goEv.PPUField,
		ref.Cycles, ref.PB, ref.PC, ref.Value, ref.PPUHCounter, ref.PPUVCounter, ref.PPUField)
}

func firstCPUMulSTAT78FieldDrop(status []cpuStatusEvent, end uint64) (cpuStatusEvent, bool) {
	var prev cpuStatusEvent
	havePrev := false
	for _, ev := range status {
		if ev.Cycles > end {
			break
		}
		if ev.Addr&0xffff != 0x213f {
			continue
		}
		if havePrev && prev.Value&0x80 != 0 && ev.Value&0x80 == 0 {
			return ev, true
		}
		prev = ev
		havePrev = true
	}
	return cpuStatusEvent{}, false
}

func findCPUMulStatusRead(status []cpuStatusEvent, cycle uint64, pb uint8, pc uint16, addr uint32) (cpuStatusEvent, bool) {
	for _, ev := range status {
		if ev.Cycles == cycle && ev.PB == pb && ev.PC == pc && ev.Addr&0xffff == addr&0xffff {
			return ev, true
		}
	}
	return cpuStatusEvent{}, false
}

func lastCPUMulStatusReadBefore(status []cpuStatusEvent, addr uint32, end uint64) (cpuStatusEvent, bool) {
	var last cpuStatusEvent
	ok := false
	for _, ev := range status {
		if ev.Cycles > end {
			break
		}
		if ev.Addr&0xffff == addr&0xffff {
			last = ev
			ok = true
		}
	}
	return last, ok
}

func firstInstructionAtOrAfter(trace []cpuInstructionEvent, cycle uint64) (cpuInstructionEvent, bool) {
	for _, ev := range trace {
		if ev.Cycles >= cycle {
			return ev, true
		}
	}
	return cpuInstructionEvent{}, false
}

func logCPUMulReferenceRefreshWindow(t *testing.T, trace []cpuRefreshEvent, drift cpuInstructionEvent) {
	t.Helper()
	if len(trace) == 0 {
		t.Log("CPUMul reference trace has no refresh substep rows")
		return
	}
	begin := -1
	for i, ev := range trace {
		if ev.Cycles > drift.Cycles {
			break
		}
		if ev.Phase == "begin" && ev.PB == drift.PB && ev.PC == drift.PC {
			begin = i
		}
	}
	if begin < 0 {
		t.Logf("CPUMul reference trace has %d refresh rows, but none before drift PB:PC %02X:%04X cycle %d",
			len(trace), drift.PB, drift.PC, drift.Cycles)
		return
	}
	for i := begin; i < len(trace) && i < begin+12; i++ {
		ev := trace[i]
		if ev.PB != drift.PB || ev.PC != drift.PC {
			break
		}
		t.Logf("CPUMul ref refresh[%02d] phase=%s cycle=%d clocks=%d h=%d v=%d pos=%d dma=%d refresh=%d mar=%06X mdr=%02X",
			i-begin, ev.Phase, ev.Cycles, ev.Clocks, ev.HCounter, ev.VCounter,
			ev.DRAMRefreshPosition, ev.DMACounter, ev.Refresh, ev.Mar, ev.Mdr)
		if ev.Phase == "end" {
			return
		}
	}
}

func mapMathIOKind(read, write bool) string {
	switch {
	case read:
		return "read"
	case write:
		return "write"
	default:
		return ""
	}
}

func jsonNumberFieldDefault(fields map[string]any, name string) uint64 {
	n, _ := jsonNumberField(fields, name)
	return n
}

func jsonNumberFieldDefaultAny(fields map[string]any, names ...string) uint64 {
	for _, name := range names {
		if n, ok := jsonNumberField(fields, name); ok {
			return n
		}
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func jsonNumberField(fields map[string]any, name string) (uint64, bool) {
	value, ok := fields[name]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return uint64(v), true
	case string:
		if n, err := strconv.ParseUint(v, 0, 64); err == nil {
			return n, true
		}
		if n, err := strconv.ParseUint(v, 16, 64); err == nil {
			return n, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func jsonStringFieldDefault(fields map[string]any, name string) string {
	value, _ := fields[name].(string)
	return value
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}
