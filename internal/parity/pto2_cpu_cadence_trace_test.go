package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
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
