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

func TestCPUMulMathIOTrace(t *testing.T) {
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

	divergence := higanKnownDivergence(t, tc, "WRAM", 0x1ffc)
	if got := sys.Bus.Read(0x7e0000 | divergence.Addr); got != divergence.Go {
		t.Fatalf("CPUMul WRAM $%04X = %02X, want manifest Go value %02X", divergence.Addr, got, divergence.Go)
	}

	t.Logf("CPUMul Go math IO trace events=%d hash=%s writes4202=%d writes4203=%d reads4216=%d reads4217=%d final_wram_1ffc=%02x",
		len(trace), hashMathIOTrace(trace), counts["write4202"], counts["write4203"], counts["read4216"], counts["read4217"],
		sys.Bus.Read(0x7e1ffc))
	logMathIOTraceSample(t, trace)
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
	summary := summarizeMathIOJSONL(t, raw)
	if summary["write4202"] == 0 || summary["write4203"] == 0 || summary["read4216"]+summary["read4217"] == 0 {
		t.Fatalf("reference trace %s lacks required math IO coverage: %#v", path, summary)
	}
	t.Logf("CPUMul reference trace %s rows=%d sha256=%s writes4202=%d writes4203=%d reads4216=%d reads4217=%d",
		path, summary["rows"], hashBytes(raw), summary["write4202"], summary["write4203"], summary["read4216"], summary["read4217"])
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

func summarizeMathIOJSONL(t *testing.T, raw []byte) map[string]int {
	t.Helper()
	summary := map[string]int{}
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
		addr, ok := jsonNumberField(fields, "addr")
		if !ok {
			addr, ok = jsonNumberField(fields, "address")
		}
		if !ok {
			continue
		}
		kind, _ := fields["kind"].(string)
		if kind == "" {
			kind, _ = fields["op"].(string)
		}
		if kind == "" {
			kind, _ = fields["type"].(string)
		}
		kind = strings.ToLower(kind)
		read := kind == "read" || kind == "r"
		write := kind == "write" || kind == "w"
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
	return summary
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
