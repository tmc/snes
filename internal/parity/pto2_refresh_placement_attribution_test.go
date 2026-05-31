package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type pto2RefreshAttributionHit struct {
	prev    pto2CPUCompareRow
	start   pto2CPUCompareRow
	next    pto2CPUCompareRow
	refresh []pto2CPUCompareRow
}

func TestPTO2Frame0RefreshPlacementAttribution(t *testing.T) {
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

	goRows := readPTO2Frame0RefreshAttributionRows(t, goRaw, "go")
	refRows := readPTO2Frame0RefreshAttributionRows(t, refRaw, "ref")
	goHit, ok := findPTO2RefreshAttributionHit(goRows, false)
	if !ok {
		t.Fatalf("%s has no frame-0 C0:8156->C0:8151->C0:8153 window with refresh-attributed row timing", goPath)
	}
	refHit, ok := findPTO2RefreshAttributionHit(refRows, true)
	if !ok {
		t.Fatalf("%s has no frame-0 C0:8156->C0:8151 refresh rows before C0:8153", refPath)
	}

	if detail := pto2CPUStateDiff(goHit.prev, refHit.prev); detail != "" {
		t.Fatalf("previous rows differ: %s", detail)
	}
	if detail := pto2CPUStateDiff(goHit.start, refHit.start); detail != "" {
		t.Fatalf("C0:8151 rows differ: %s", detail)
	}
	if detail := pto2CPUStateDiff(goHit.next, refHit.next); detail != "" {
		t.Fatalf("C0:8153 rows differ: %s", detail)
	}

	baseDelta := int64(goHit.prev.cycles) - int64(refHit.prev.cycles)
	startDelta := int64(goHit.start.cycles) - int64(refHit.start.cycles)
	nextDelta := int64(goHit.next.cycles) - int64(refHit.next.cycles)
	if baseDelta != 6 {
		t.Fatalf("base delta at C0:8156 = %d, want 6", baseDelta)
	}
	if startDelta-baseDelta != 40 {
		t.Fatalf("C0:8151 delta moved by %d cycles from base, want 40-cycle refresh attribution", startDelta-baseDelta)
	}
	if nextDelta != baseDelta {
		t.Fatalf("C0:8153 delta = %d, want restored base delta %d", nextDelta, baseDelta)
	}

	goPrevToStart := goHit.start.cycles - goHit.prev.cycles
	refPrevToStart := refHit.start.cycles - refHit.prev.cycles
	if goPrevToStart != refPrevToStart+40 {
		t.Fatalf("C0:8156->C0:8151 duration go=%d ref=%d, want Go to carry one 40-cycle refresh",
			goPrevToStart, refPrevToStart)
	}
	goStartToNext := goHit.next.cycles - goHit.start.cycles
	refStartToNext := refHit.next.cycles - refHit.start.cycles
	if refStartToNext != goStartToNext+40 {
		t.Fatalf("C0:8151->C0:8153 duration go=%d ref=%d, want reference to carry one 40-cycle refresh",
			goStartToNext, refStartToNext)
	}
	if len(refHit.refresh) != 12 {
		t.Fatalf("reference refresh rows = %d, want 12 begin/active/inactive/end rows", len(refHit.refresh))
	}
	if first, last := refHit.refresh[0], refHit.refresh[len(refHit.refresh)-1]; first.cycles != 8722 || last.cycles != 8762 {
		t.Fatalf("reference refresh span = %d..%d, want 8722..8762", first.cycles, last.cycles)
	}

	t.Logf("PTO2 refresh attribution artifacts: go=%s sha256=%s ref=%s sha256=%s",
		goPath, hashBytes(goRaw), refPath, hashBytes(refRaw))
	t.Logf("PTO2 refresh attribution: Go rows after opcode fetch move C0:8151 delta from %d to %d, but C0:8153 returns to %d",
		baseDelta, startDelta, nextDelta)
	t.Logf("PTO2 refresh placement: Go C0:8156->C0:8151=%d and C0:8151->C0:8153=%d; ref C0:8156->C0:8151=%d, refresh=%d..%d, C0:8151->C0:8153=%d",
		goPrevToStart, goStartToNext, refPrevToStart,
		refHit.refresh[0].cycles, refHit.refresh[len(refHit.refresh)-1].cycles, refStartToNext)
}

func readPTO2Frame0RefreshAttributionRows(t *testing.T, raw []byte, source string) []pto2CPUCompareRow {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("empty PTO2 %s CPU trace", source)
	}
	var rows []pto2CPUCompareRow
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fields pto2CPUCompareJSONRow
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("decode PTO2 %s CPU trace line %d: %v", source, lineNo, err)
		}
		cycles := fields.Cycles
		if cycles == 0 {
			cycles = fields.Cycle
		}
		if fields.Frame > 0 || cycles > 8900 {
			break
		}
		if cycles < 8400 {
			continue
		}
		kind := pto2CPUCompareKind(fields.Kind, fields.Event)
		if kind != "instruction" && kind != "refresh" {
			continue
		}
		rows = append(rows, pto2CPUCompareRow{
			line:                lineNo,
			kind:                kind,
			frame:               fields.Frame,
			cycles:              cycles,
			pb:                  uint8(fields.PB),
			pc:                  uint16(fields.PC),
			opcode:              uint8(fields.Opcode),
			operand0:            uint8(fields.Operand0),
			operand1:            uint8(fields.Operand1),
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
			phase:               fields.Phase,
			clocks:              fields.Clocks,
			dramRefreshPosition: fields.DRAMRefreshPosition,
			dmaCounter:          fields.DMACounter,
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("PTO2 %s trace has no frame-0 rows in refresh attribution window", source)
	}
	return rows
}

func findPTO2RefreshAttributionHit(rows []pto2CPUCompareRow, wantRefresh bool) (pto2RefreshAttributionHit, bool) {
	for i, row := range rows {
		if row.kind != "instruction" || row.pb != 0xc0 || row.pc != 0x8151 || row.opcode != 0x91 {
			continue
		}
		prev, ok := previousPTO2InstructionInRows(rows, i)
		if !ok || prev.pb != 0xc0 || prev.pc != 0x8156 || prev.opcode != 0xd0 {
			continue
		}
		next, ok := nextPTO2InstructionInRows(rows, i)
		if !ok || next.pb != 0xc0 || next.pc != 0x8153 || next.opcode != 0xc8 {
			continue
		}
		refresh := pto2RefreshRowsBetween(rows, row.line, next.line)
		if wantRefresh != (len(refresh) > 0) {
			continue
		}
		if !wantRefresh && row.cycles-prev.cycles != 58 {
			continue
		}
		return pto2RefreshAttributionHit{
			prev:    prev,
			start:   row,
			next:    next,
			refresh: refresh,
		}, true
	}
	return pto2RefreshAttributionHit{}, false
}

func previousPTO2InstructionInRows(rows []pto2CPUCompareRow, i int) (pto2CPUCompareRow, bool) {
	for j := i - 1; j >= 0; j-- {
		if rows[j].kind == "instruction" {
			return rows[j], true
		}
	}
	return pto2CPUCompareRow{}, false
}

func nextPTO2InstructionInRows(rows []pto2CPUCompareRow, i int) (pto2CPUCompareRow, bool) {
	for j := i + 1; j < len(rows); j++ {
		if rows[j].kind == "instruction" {
			return rows[j], true
		}
	}
	return pto2CPUCompareRow{}, false
}

func pto2RefreshRowsBetween(rows []pto2CPUCompareRow, lineLo, lineHi int) []pto2CPUCompareRow {
	var refresh []pto2CPUCompareRow
	for _, row := range rows {
		if row.line <= lineLo || row.line >= lineHi || row.kind != "refresh" {
			continue
		}
		refresh = append(refresh, row)
	}
	return refresh
}
