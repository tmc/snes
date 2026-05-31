package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const pto2ReferenceCPUMulTraceEnv = "PTO2_REF_CPUMUL_TRACE"

type pto2ReferenceTimingDMARow struct {
	line    int
	frame   int
	cycles  uint64
	pb      uint8
	pc      uint16
	channel int
	vmain   uint8
	vmaddr  uint16
	das     uint16
}

type pto2ReferenceTimingTraceSummary struct {
	rows           int
	dmaStarts      int
	frame30Line    int
	frame30Cycles  uint64
	qualifyingLine int
	qualifyingDMA  pto2ReferenceTimingDMARow
}

type pto2ReferenceCallerBoundarySummary struct {
	rows            int
	instructionRows int
	dmaStarts       int
	completeChains  int
	frame30         pto2ReferenceCallerBoundaryRow
	firstAtOrAfter  pto2ReferenceCallerBoundary
	qualifying      pto2ReferenceCallerBoundary
}

type pto2ReferenceCallerBoundary struct {
	call8bf0  pto2ReferenceCallerBoundaryRow
	call8650  pto2ReferenceCallerBoundaryRow
	entry8c8b pto2ReferenceCallerBoundaryRow
	dma       pto2ReferenceCallerBoundaryRow
	dec8cb9   pto2ReferenceCallerBoundaryRow
	ret8cd5   pto2ReferenceCallerBoundaryRow
	ret8653   pto2ReferenceCallerBoundaryRow
	ret8bf3   pto2ReferenceCallerBoundaryRow
}

type pto2ReferenceCallerBoundaryRow struct {
	line     int
	kind     string
	event    string
	frame    int
	cycles   uint64
	pb       uint8
	pc       uint16
	opcode   uint8
	channel  int
	dmap     uint8
	bbad     uint8
	a1b      uint8
	a1t      uint16
	das      uint16
	vmain    uint8
	vmaddr   uint16
	hcounter uint16
	vcounter uint16
	field    uint8
}

func TestPTO2ReferenceTimingTrace(t *testing.T) {
	path := os.Getenv(pto2ReferenceCPUMulTraceEnv)
	if path == "" {
		t.Skipf("set %s to a bsnes CPUMulTrace JSONL artifact", pto2ReferenceCPUMulTraceEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	summary := readPTO2ReferenceTimingTrace(t, raw)
	if summary.frame30Line == 0 {
		t.Fatalf("%s has no frame-30 frame row", path)
	}
	if summary.qualifyingLine == 0 {
		t.Fatalf("%s has no frame-30 channel-7 dma-start from C0:8CB9 with vmain=$80 das=$0010 vmaddr >= $2168 before frame row line %d; frame-30 dma-start rows before frame=%d",
			path, summary.frame30Line, summary.dmaStarts)
	}
	dma := summary.qualifyingDMA
	t.Logf("PTO2 reference trace %s rows=%d sha256=%s", path, summary.rows, hashBytes(raw))
	t.Logf("PTO2 reference frame-30 boundary line=%d qualifying dma line=%d", summary.frame30Line, summary.qualifyingLine)
	t.Logf("PTO2 reference dma-start: frame=%d cycle=%d PB:PC=%02X:%04X channel=%d vmain=%02X das=%04X vmaddr=%04X",
		dma.frame, dma.cycles, dma.pb, dma.pc, dma.channel, dma.vmain, dma.das, dma.vmaddr)
}

func TestPTO2ReferenceCallerBoundaryTrace(t *testing.T) {
	path := os.Getenv(pto2RefCPUTraceCompareEnv)
	if path == "" {
		t.Skipf("set %s to a bsnes CPUMulTrace JSONL artifact with instruction rows", pto2RefCPUTraceCompareEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	summary := readPTO2ReferenceCallerBoundaryTrace(t, raw)
	if summary.instructionRows == 0 {
		t.Fatalf("%s has no instruction rows; regenerate with BSNES_CPUMUL_TRACE_INSTRUCTIONS=1", path)
	}
	if summary.frame30.line == 0 {
		t.Fatalf("%s has no frame-30 frame row", path)
	}
	if summary.qualifying.dma.line == 0 {
		if summary.firstAtOrAfter.dma.line != 0 {
			t.Fatalf("%s has no complete frame-30 caller chain for vmaddr $2168 before frame line %d; first complete chain at/after $2168 is vmaddr=%04X line=%d",
				path, summary.frame30.line, summary.firstAtOrAfter.dma.vmaddr, summary.firstAtOrAfter.dma.line)
		}
		t.Fatalf("%s has no complete frame-30 C0:8BF0 -> C0:8650 -> C0:8C8B caller chain reaching vmaddr $2168 before frame line %d; dma-start rows=%d complete_chains=%d",
			path, summary.frame30.line, summary.dmaStarts, summary.completeChains)
	}
	q := summary.qualifying
	if q.dma.cycles >= summary.frame30.cycles || q.dma.line >= summary.frame30.line {
		t.Fatalf("qualifying reference DMA is not before frame-30 boundary: dma line=%d cycle=%d frame line=%d cycle=%d",
			q.dma.line, q.dma.cycles, summary.frame30.line, summary.frame30.cycles)
	}
	if !pto2ReferenceCallerBoundaryOrdered(q) {
		t.Fatalf("qualifying reference caller chain is out of order: %s", pto2ReferenceCallerBoundaryLabel(q))
	}

	t.Logf("PTO2 reference caller boundary artifact %s rows=%d sha256=%s",
		path, summary.rows, hashBytes(raw))
	t.Logf("PTO2 reference caller boundary rows: instructions=%d dma_starts=%d complete_chains=%d frame30 line=%d cycle=%d",
		summary.instructionRows, summary.dmaStarts, summary.completeChains,
		summary.frame30.line, summary.frame30.cycles)
	t.Logf("PTO2 reference caller boundary chain: %s", pto2ReferenceCallerBoundaryLabel(q))
}

func readPTO2ReferenceTimingTrace(t *testing.T, raw []byte) pto2ReferenceTimingTraceSummary {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 reference trace")
	}
	var summary pto2ReferenceTimingTraceSummary
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
			t.Fatalf("decode PTO2 reference trace line %d: %v", summary.rows, err)
		}
		frame := int(jsonNumberFieldDefault(fields, "frame"))
		event := strings.ToLower(jsonStringFieldDefault(fields, "event"))
		kind := strings.ToLower(jsonStringFieldDefault(fields, "kind"))
		if frame == 30 && (event == "frame" || kind == "frame") {
			summary.frame30Line = summary.rows
			summary.frame30Cycles = jsonNumberFieldDefaultAny(fields, "cycles", "cycle")
			break
		}
		if frame != 30 || (event != "dma-start" && kind != "dma-start") {
			continue
		}
		summary.dmaStarts++
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
		if dma.pb != 0xc0 || dma.pc != 0x8cb9 || dma.channel != 7 || dma.vmain != 0x80 || dma.das != 0x0010 || dma.vmaddr < 0x2168 {
			continue
		}
		if summary.qualifyingLine == 0 || summary.qualifyingDMA.vmaddr != 0x2168 || dma.vmaddr == 0x2168 {
			summary.qualifyingLine = dma.line
			summary.qualifyingDMA = dma
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary.rows == 0 {
		t.Fatal("PTO2 reference trace has no rows")
	}
	return summary
}

func readPTO2ReferenceCallerBoundaryTrace(t *testing.T, raw []byte) pto2ReferenceCallerBoundarySummary {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty PTO2 reference caller-boundary trace")
	}
	var summary pto2ReferenceCallerBoundarySummary
	var chain pto2ReferenceCallerBoundary
	finishChain := func() {
		if chain.dma.line == 0 {
			return
		}
		if !pto2ReferenceCallerBoundaryOrdered(chain) {
			return
		}
		summary.completeChains++
		if chain.dma.vmaddr >= 0x2168 &&
			(summary.firstAtOrAfter.dma.line == 0 || chain.dma.vmaddr < summary.firstAtOrAfter.dma.vmaddr) {
			summary.firstAtOrAfter = chain
		}
		if chain.dma.vmaddr == 0x2168 && summary.qualifying.dma.line == 0 {
			summary.qualifying = chain
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		summary.rows++
		row := parsePTO2ReferenceCallerBoundaryRow(t, summary.rows, line)
		if row.kind == "instruction" {
			summary.instructionRows++
		}
		if row.frame != 30 {
			continue
		}
		if row.event == "frame" || row.kind == "frame" {
			finishChain()
			summary.frame30 = row
			break
		}
		switch {
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8bf0:
			finishChain()
			chain = pto2ReferenceCallerBoundary{call8bf0: row}
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8650 && chain.call8bf0.line != 0:
			chain.call8650 = row
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8c8b && chain.call8650.line != 0:
			chain.entry8c8b = row
		case row.kind == "dma-start" && pto2ReferenceUploaderDMA(row):
			summary.dmaStarts++
			if chain.entry8c8b.line != 0 {
				chain.dma = row
			}
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8cb9 && chain.dma.line != 0:
			chain.dec8cb9 = row
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8cd5 && chain.dma.line != 0:
			chain.ret8cd5 = row
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8653 && chain.ret8cd5.line != 0:
			chain.ret8653 = row
		case row.kind == "instruction" && row.pb == 0xc0 && row.pc == 0x8bf3 && chain.ret8653.line != 0:
			chain.ret8bf3 = row
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary.rows == 0 {
		t.Fatal("PTO2 reference caller-boundary trace has no rows")
	}
	return summary
}

func parsePTO2ReferenceCallerBoundaryRow(t *testing.T, lineNo int, line []byte) pto2ReferenceCallerBoundaryRow {
	t.Helper()
	var fields pto2CPUCompareJSONRow
	if err := json.Unmarshal(line, &fields); err != nil {
		t.Fatalf("decode PTO2 reference caller-boundary trace line %d: %v", lineNo, err)
	}
	kind := pto2CPUCompareKind(fields.Kind, fields.Event)
	if strings.EqualFold(fields.Event, "frame") {
		kind = "frame"
	}
	cycles := fields.Cycles
	if cycles == 0 {
		cycles = fields.Cycle
	}
	return pto2ReferenceCallerBoundaryRow{
		line:     lineNo,
		kind:     kind,
		event:    strings.ToLower(fields.Event),
		frame:    fields.Frame,
		cycles:   cycles,
		pb:       uint8(fields.PB),
		pc:       uint16(fields.PC),
		opcode:   uint8(fields.Opcode),
		channel:  fields.Channel,
		dmap:     uint8(fields.DMAP),
		bbad:     uint8(fields.BBAD),
		a1b:      uint8(fields.A1B),
		a1t:      uint16(fields.A1T),
		das:      uint16(fields.DAS),
		vmain:    uint8(fields.VMAIN),
		vmaddr:   uint16(fields.VMAddr),
		hcounter: uint16(fields.HCounter),
		vcounter: uint16(fields.VCounter),
		field:    uint8(fields.Field),
	}
}

func pto2ReferenceUploaderDMA(row pto2ReferenceCallerBoundaryRow) bool {
	return row.pb == 0xc0 &&
		row.pc == 0x8cb9 &&
		row.channel == 7 &&
		row.dmap == 0x01 &&
		row.bbad == 0x18 &&
		row.a1b == 0x00 &&
		row.a1t == 0x09df &&
		row.das == 0x0010 &&
		row.vmain == 0x80
}

func pto2ReferenceCallerBoundaryOrdered(chain pto2ReferenceCallerBoundary) bool {
	rows := []int{
		chain.call8bf0.line,
		chain.call8650.line,
		chain.entry8c8b.line,
		chain.dma.line,
		chain.dec8cb9.line,
		chain.ret8cd5.line,
		chain.ret8653.line,
		chain.ret8bf3.line,
	}
	for i, line := range rows {
		if line == 0 {
			return false
		}
		if i > 0 && line <= rows[i-1] {
			return false
		}
	}
	return true
}

func pto2ReferenceCallerBoundaryLabel(chain pto2ReferenceCallerBoundary) string {
	return strings.Join([]string{
		pto2ReferenceCallerBoundaryRowLabel("8BF0", chain.call8bf0),
		pto2ReferenceCallerBoundaryRowLabel("8650", chain.call8650),
		pto2ReferenceCallerBoundaryRowLabel("8C8B", chain.entry8c8b),
		fmt.Sprintf("dma line=%d f=%d cy=%d vmaddr=%04X das=%04X src=%02X:%04X hc=%d vc=%d field=%d",
			chain.dma.line, chain.dma.frame, chain.dma.cycles, chain.dma.vmaddr,
			chain.dma.das, chain.dma.a1b, chain.dma.a1t,
			chain.dma.hcounter, chain.dma.vcounter, chain.dma.field),
		pto2ReferenceCallerBoundaryRowLabel("8CB9", chain.dec8cb9),
		pto2ReferenceCallerBoundaryRowLabel("8CD5", chain.ret8cd5),
		pto2ReferenceCallerBoundaryRowLabel("8653", chain.ret8653),
		pto2ReferenceCallerBoundaryRowLabel("8BF3", chain.ret8bf3),
	}, "; ")
}

func pto2ReferenceCallerBoundaryRowLabel(name string, row pto2ReferenceCallerBoundaryRow) string {
	if row.line == 0 {
		return fmt.Sprintf("%s missing", name)
	}
	return fmt.Sprintf("%s line=%d f=%d cy=%d op=%02X hc=%d vc=%d field=%d",
		name, row.line, row.frame, row.cycles, row.opcode, row.hcounter, row.vcounter, row.field)
}
