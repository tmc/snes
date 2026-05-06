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
	A        uint16 `json:"a"`
	X        uint16 `json:"x"`
	Y        uint16 `json:"y"`
	P        uint8  `json:"p"`
	DB       uint8  `json:"db"`
	D        uint16 `json:"d"`
	S        uint16 `json:"s"`
	Disasm   string `json:"disasm,omitempty"`
}

type cpuStatusEvent struct {
	Frame    int    `json:"frame"`
	Cycles   uint64 `json:"cycles"`
	PB       uint8  `json:"pb"`
	PC       uint16 `json:"pc"`
	Addr     uint32 `json:"addr"`
	Value    uint8  `json:"value"`
	HCounter uint16 `json:"hcounter"`
	VCounter uint16 `json:"vcounter"`
	A        uint16 `json:"a"`
	X        uint16 `json:"x"`
	Y        uint16 `json:"y"`
	P        uint8  `json:"p"`
	DB       uint8  `json:"db"`
	D        uint16 `json:"d"`
	S        uint16 `json:"s"`
	Disasm   string `json:"disasm,omitempty"`
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
	return cpuStatusEvent{
		Frame:  *d.frame,
		Cycles: c.Cycles,
		PB:     c.PB,
		PC:     c.PC,
		Addr:   addr & 0xffff,
		Value:  value,
		A:      c.A,
		X:      c.X,
		Y:      c.Y,
		P:      c.P,
		DB:     c.DB,
		D:      c.D,
		S:      c.S,
		Disasm: disasm.Disassemble65816(c, d.sys.Bus),
	}
}

func TestCPUMulMathIOTrace(t *testing.T) {
	sys, trace := runCPUMulGoMathIOTrace(t)
	tc, ok := higanManifestCase(t, "CPUMul")
	if !ok {
		t.Fatalf("%s has no CPUMul row", higanTestROMManifestPath)
	}
	divergence := higanKnownDivergence(t, tc, "WRAM", 0x1ffc)
	if got := sys.Bus.Read(0x7e0000 | divergence.Addr); got != divergence.Go {
		t.Fatalf("CPUMul WRAM $%04X = %02X, want manifest Go value %02X", divergence.Addr, got, divergence.Go)
	}

	counts := countMathIOEvents(trace)
	t.Logf("CPUMul Go math IO trace events=%d hash=%s writes4202=%d writes4203=%d reads4216=%d reads4217=%d final_wram_1ffc=%02x",
		len(trace), hashMathIOTrace(trace), counts["write4202"], counts["write4203"], counts["read4216"], counts["read4217"],
		sys.Bus.Read(0x7e1ffc))
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
	refInstructions, refStatus, refMath, summary := readReferenceCPUMulJSONL(t, raw)
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
	compareCPUMulInstructionDrift(t, goInstructions, refInstructions, goMath[0], refMath[0])
	if len(goMath) >= 3 && len(refMath) >= 3 {
		t.Logf("CPUMul event 2 timing: Go frame=%d cycle=%d PB:PC=%02X:%04X; Ref frame=%d cycle=%d PB:PC=%02X:%04X",
			goMath[2].Frame, goMath[2].Cycles, goMath[2].PB, goMath[2].PC,
			refMath[2].Frame, refMath[2].Cycles, refMath[2].PB, refMath[2].PC)
		logCPUMulInstructionHistogram(t, goInstructions, goMath[1].Cycles, goMath[2].Cycles)
		logCPUMulStatusWindow(t, goStatus, refStatus, goMath[2], refMath[2])
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
		instructions = append(instructions, cpuInstructionEvent{
			Cycles:   c.Cycles,
			PB:       c.PB,
			PC:       c.PC,
			Opcode:   sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC)),
			Operand0: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+1)),
			Operand1: sys.Bus.Read(uint32(c.PB)<<16 | uint32(c.PC+2)),
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

func higanKnownDivergence(t *testing.T, tc higanTestROMCase, region string, addr uint32) higanTestROMKnownDivergence {
	t.Helper()
	for _, divergence := range tc.KnownDivergences {
		if divergence.Region == region && divergence.Addr == addr {
			return divergence
		}
	}
	t.Fatalf("%s has no known divergence for %s $%04X", tc.Name, region, addr)
	return higanTestROMKnownDivergence{}
}

func readReferenceMathIOJSONL(t *testing.T, raw []byte) ([]mathIOEvent, map[string]int) {
	t.Helper()
	_, _, trace, summary := readReferenceCPUMulJSONL(t, raw)
	return trace, summary
}

func readReferenceCPUMulJSONL(t *testing.T, raw []byte) ([]cpuInstructionEvent, []cpuStatusEvent, []mathIOEvent, map[string]int) {
	t.Helper()
	summary := map[string]int{}
	var instructions []cpuInstructionEvent
	var status []cpuStatusEvent
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
				A:        uint16(jsonNumberFieldDefault(fields, "a")),
				X:        uint16(jsonNumberFieldDefault(fields, "x")),
				Y:        uint16(jsonNumberFieldDefault(fields, "y")),
				P:        uint8(jsonNumberFieldDefault(fields, "p")),
				DB:       uint8(jsonNumberFieldDefault(fields, "db")),
				D:        uint16(jsonNumberFieldDefault(fields, "d")),
				S:        uint16(jsonNumberFieldDefault(fields, "s")),
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
			status = append(status, cpuStatusEvent{
				Frame:    int(jsonNumberFieldDefault(fields, "frame")),
				Cycles:   jsonNumberFieldDefault(fields, "cycles"),
				PB:       uint8(jsonNumberFieldDefault(fields, "pb")),
				PC:       uint16(jsonNumberFieldDefault(fields, "pc")),
				Addr:     uint32(addr),
				Value:    uint8(jsonNumberFieldDefaultAny(fields, "value", "data")),
				HCounter: uint16(jsonNumberFieldDefault(fields, "hcounter")),
				VCounter: uint16(jsonNumberFieldDefault(fields, "vcounter")),
				A:        uint16(jsonNumberFieldDefault(fields, "a")),
				X:        uint16(jsonNumberFieldDefault(fields, "x")),
				Y:        uint16(jsonNumberFieldDefault(fields, "y")),
				P:        uint8(jsonNumberFieldDefault(fields, "p")),
				DB:       uint8(jsonNumberFieldDefault(fields, "db")),
				D:        uint16(jsonNumberFieldDefault(fields, "d")),
				S:        uint16(jsonNumberFieldDefault(fields, "s")),
			})
			switch uint32(addr) & 0xffff {
			case 0x213f:
				summary["status213f"]++
			case 0x4212:
				summary["status4212"]++
			}
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
	return instructions, status, trace, summary
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
