package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
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
