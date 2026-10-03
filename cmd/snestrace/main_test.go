package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/framecap"
	"github.com/tmc/snes/internal/trace"
)

func newTraceTestROM(code []byte) []byte {
	rom := make([]byte, 0x8000)
	copy(rom, code)
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func TestParseButtons(t *testing.T) {
	got, err := parseButtons("Right+Start")
	if err != nil {
		t.Fatalf("parseButtons: %v", err)
	}
	if got == 0 {
		t.Fatal("parseButtons returned zero")
	}
}

func TestBuildRevision(t *testing.T) {
	if got := buildRevision(); got == "" || got == "unknown" {
		t.Fatalf("buildRevision = %q, want VCS revision or git fallback", got)
	}
}

func TestStartBoundary(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "fresh", want: "fresh_frame"},
		{name: "restored", path: "checkpoint.state", want: "restored_state"},
	}
	for _, tt := range tests {
		if got := startBoundary(tt.path); got != tt.want {
			t.Fatalf("%s: startBoundary(%q) = %q, want %q", tt.name, tt.path, got, tt.want)
		}
	}
}

func TestQueryWriters(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":0,"space":"wram","addr":34,"op":"write","value":91}
{"id":2,"schema":1,"kind":"bus","frame":0,"space":"wram","addr":35,"op":"read","value":9}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "writers", "--trace", tracePath, "--addr", "wram:0x22", "--format", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 1`) {
		t.Fatalf("query output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "writers", "--trace", tracePath, "--addr", "wram:0x22", "--format", "text"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), `unsupported format "text"`) {
		t.Fatalf("run unsupported format code=%d stderr=%s", code, stderr.String())
	}
}

func TestQueryDMAForDest(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":3,"schema":1,"kind":"dma","frame":0,"dest":{"space":"vram","start":16384,"end":18431}}
{"id":4,"schema":1,"kind":"hdma","frame":0,"dest":{"space":"vram","start":18432,"end":18435}}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "dma-for-dest", "--trace", tracePath, "--dest", "vram:0x4000-0x4803"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"kind": "dma"`) {
		t.Fatalf("query output = %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"kind": "hdma"`) {
		t.Fatalf("query output = %s", stdout.String())
	}
}

func TestQueryBusForPC(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":4,"schema":1,"kind":"bus","frame":0,"pc":{"bank":128,"addr":33059},"space":"wram","addr":34,"op":"write"}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "bus-for-pc", "--trace", tracePath, "--addr", "cpu:80:8120-cpu:80:812f"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 4`) {
		t.Fatalf("query output = %s", stdout.String())
	}
}

func TestQueryReadersMatchesROMSource(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":4,"schema":1,"kind":"bus","frame":0,"space":"cpu","addr":8388608,"source":{"space":"rom","start":0,"end":0},"op":"read"}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "readers", "--trace", tracePath, "--addr", "rom:0x0"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 4`) {
		t.Fatalf("query output = %s", stdout.String())
	}
}

func TestQueryMMIOReadersAndWriters(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"mmio","frame":0,"space":"ppu","addr":8448,"op":"write","value":128}
{"id":2,"schema":1,"kind":"apu","frame":0,"space":"apu","addr":8512,"op":"read","value":18}
{"id":3,"schema":1,"kind":"input","frame":0,"space":"cpu","addr":16920,"op":"read","value":52}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "writers", "--trace", tracePath, "--addr", "ppu:0x2100"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run writers code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 1`) {
		t.Fatalf("writers output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "readers", "--trace", tracePath, "--addr", "apu:0x2140"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run apu readers code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 2`) {
		t.Fatalf("apu readers output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "readers", "--trace", tracePath, "--addr", "cpu:00:4218"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run input readers code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 3`) {
		t.Fatalf("input readers output = %s", stdout.String())
	}
}

func TestQueryExplainWriter(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":1,"cycle":10,"pc":{"bank":128,"addr":33059},"space":"wram","addr":34,"op":"write","value":91}
{"id":2,"schema":1,"kind":"dma","frame":2,"cycle":20,"source":{"space":"cpu","start":8323072,"end":8323327},"dest":{"space":"wram","start":32,"end":47}}
{"id":3,"schema":1,"kind":"bus","frame":3,"space":"wram","addr":34,"op":"write","value":92}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "explain-writer", "--trace", tracePath, "--addr", "wram:0x22", "--frame-start", "1", "--frame-end", "2"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"semantic_hash"`) || !strings.Contains(text, `"event_count": 2`) || strings.Contains(text, `"id": 3`) {
		t.Fatalf("query output = %s", text)
	}
}

func TestQueryLastWriterAtFrame(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":1,"space":"wram","addr":34,"op":"write","value":91}
{"id":2,"schema":1,"kind":"bus","frame":2,"space":"wram","addr":34,"op":"write","value":92}
{"id":3,"schema":1,"kind":"bus","frame":3,"space":"wram","addr":34,"op":"write","value":93}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "last-writer-at-frame", "--trace", tracePath, "--addr", "wram:0x22", "--frame", "2"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"event_count": 1`) || !strings.Contains(text, `"id": 2`) || strings.Contains(text, `"id": 3`) {
		t.Fatalf("query output = %s", text)
	}
}

func TestQueryTraceWindowAndFrameSummary(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":0,"space":"wram","addr":34,"op":"write","value":91}
{"id":2,"schema":1,"kind":"watch","frame":1,"name":"link_x","space":"wram","addr":34,"width":2,"value":92}
{"id":3,"schema":1,"kind":"frame","frame":1,"name":"state","hash":"abc"}
{"id":4,"schema":1,"kind":"input","frame":1,"value":256}
{"id":5,"schema":1,"kind":"bus","frame":2,"space":"wram","addr":35,"op":"read","value":9}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "trace-window", "--trace", tracePath, "--event", "3", "--before", "1", "--after", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace-window code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"id": 2`) || !strings.Contains(text, `"id": 3`) || !strings.Contains(text, `"id": 4`) || strings.Contains(text, `"id": 1`) {
		t.Fatalf("trace-window output = %s", text)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "frame-summary", "--trace", tracePath, "--frame", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run frame-summary code=%d stderr=%s", code, stderr.String())
	}
	text = stdout.String()
	for _, want := range []string{`"kind": "watch"`, `"kind": "frame"`, `"kind": "input"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("frame-summary output missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, `"id": 1`) || strings.Contains(text, `"id": 5`) {
		t.Fatalf("frame-summary included non-frame events: %s", text)
	}
}

func TestExplainWriterShapeIncludesCPUAndDMAContext(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":1,"space":"wram","addr":34,"op":"write","value":91,"before":90,"after":91,"cpu":{"pbr":7,"pc":58312,"dbr":7,"dp":0,"x":2,"y":0,"s":495,"p":48,"opcode":149,"bytes":[149,32],"disasm":"STA","addressing":"direct_x","effective_addr":34,"effective_expr":"dp,x"}}
{"id":2,"schema":1,"kind":"dma","frame":2,"source":{"space":"cpu","start":8323072,"end":8323327},"dest":{"space":"wram","start":32,"end":47},"dma":{"channel":0,"mode":1,"count":256,"direction":"a_to_b","target":24,"dest_register":8472}}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "explain-writer", "--trace", tracePath, "--addr", "wram:0x22"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{`"before": 90`, `"after": 91`, `"effective_addr": 34`, `"effective_expr": "dp,x"`, `"channel": 0`, `"dest_register": 8472`} {
		if !strings.Contains(text, want) {
			t.Fatalf("query output missing %s: %s", want, text)
		}
	}
}

func TestMMIOEventsIncludeRegisterLabels(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x80, // LDA #$80
		0x8d, 0x00, 0x21, // STA $2100
		0xa9, 0x34, // LDA #$34
		0x8d, 0x18, 0x21, // STA $2118
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "mmio", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"register":"INIDISP"`, `"category":"display_control"`, `"register":"VMDATAL"`, `"category":"vram_data"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestBusReadEventsIncludeROMSource(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xad, 0x08, 0x80, // LDA $8008
		0x80, 0xfe, // BRA -2
		0x00,
		0x5a,
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "bus", "--addr", "rom:0x8", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"bus"`, `"op":"read"`, `"space":"cpu"`, `"addr":32776`, `"source":{"space":"rom","start":8,"end":8}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestPPUEventsRecordStorageMutations(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x80, // LDA #$80
		0x8d, 0x00, 0x21, // STA $2100 force blank
		0xa9, 0x80, // LDA #$80
		0x8d, 0x15, 0x21, // STA $2115
		0xa9, 0x00, // LDA #$00
		0x8d, 0x16, 0x21, // STA $2116
		0xa9, 0x10, // LDA #$10
		0x8d, 0x17, 0x21, // STA $2117
		0xa9, 0x34, // LDA #$34
		0x8d, 0x18, 0x21, // STA $2118
		0xa9, 0x12, // LDA #$12
		0x8d, 0x19, 0x21, // STA $2119
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "ppu", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"ppu"`, `"register":"VMDATAL"`, `"space":"vram"`, `"addr":8192`, `"after":52`, `"register":"VMDATAH"`, `"addr":8193`, `"after":18`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestInputEventsIncludeControllerRegisterChronology(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x01, // LDA #$01
		0x8d, 0x16, 0x40, // STA $4016
		0xa9, 0x00, // LDA #$00
		0x8d, 0x16, 0x40, // STA $4016
		0xad, 0x16, 0x40, // LDA $4016
		0xad, 0x18, 0x42, // LDA $4218
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "input", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"input"`, `"register":"JOYSER0"`, `"category":"controller_serial"`, `"register":"JOY1L"`, `"category":"auto_joypad"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestAPUEventsIncludePortChronology(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x12, // LDA #$12
		0x8d, 0x40, 0x21, // STA $2140
		0xad, 0x40, 0x21, // LDA $2140
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "apu", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"apu"`, `"register":"APUIO0"`, `"category":"apu_port"`, `"space":"apu"`, `"op":"write"`, `"op":"read"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestInterruptEvents(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x80, // LDA #$80
		0x8d, 0x00, 0x42, // STA $4200
		0x80, 0xfe, // BRA -2
	})
	rom[0x0010] = 0x40 // RTI at NMI vector target.
	rom[0x7ffa] = 0x10
	rom[0x7ffb] = 0x80
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "2", "--events", "interrupt", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"interrupt"`, `"category":"interrupt"`, `"op":"nmi"`, `"op":"rti_exit"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestQueryFirstDifference(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.jsonl")
	rightPath := filepath.Join(dir, "right.jsonl")
	err := os.WriteFile(leftPath, []byte(`{"id":1,"schema":1,"kind":"watch","frame":0,"name":"link_x","space":"wram","addr":34,"width":2,"value":91}
{"id":2,"schema":1,"kind":"frame","frame":0,"name":"state","hash":"aaa"}
`), 0666)
	if err != nil {
		t.Fatalf("write left trace: %v", err)
	}
	err = os.WriteFile(rightPath, []byte(`{"id":1,"schema":1,"kind":"watch","frame":0,"name":"link_x","space":"wram","addr":34,"width":2,"value":92}
{"id":2,"schema":1,"kind":"frame","frame":0,"name":"state","hash":"aaa"}
`), 0666)
	if err != nil {
		t.Fatalf("write right trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "first-difference", "--left", leftPath, "--right", rightPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{`"event_index": 0`, `"reason": "event_mismatch"`, `"semantic_hash"`, `"value": 91`, `"value": 92`} {
		if !strings.Contains(text, want) {
			t.Fatalf("query output missing %s: %s", want, text)
		}
	}
}

func TestQueryPCContextReturnsMatchesWithWindows(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"frame","frame":1,"name":"state","hash":"aaa"}
{"id":2,"schema":1,"kind":"bus","frame":2,"pc":{"bank":128,"addr":33059},"space":"wram","addr":34,"op":"read","value":91}
{"id":3,"schema":1,"kind":"mmio","frame":2,"pc":{"bank":128,"addr":33060},"space":"ppu","addr":8470,"op":"write","value":0}
{"id":4,"schema":1,"kind":"dma","frame":2,"pc":{"bank":128,"addr":33061},"dma":{"channel":4,"mode":1,"count":4096,"target":24},"dest":{"space":"vram","start":0,"end":4095}}
{"id":5,"schema":1,"kind":"frame","frame":3,"name":"state","hash":"bbb"}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "pc-context", "--trace", tracePath, "--pc", "cpu:80:8124-cpu:80:8125", "--before", "1", "--after", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{`"event_count": 2`, `"semantic_hash"`, `"id": 3`, `"id": 4`, `"id": 2`, `"id": 5`} {
		if !strings.Contains(text, want) {
			t.Fatalf("query output missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, `"id": 1`) {
		t.Fatalf("query output included event outside bounded windows: %s", text)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "pc-context", "--trace", tracePath, "--pc", "cpu:80:8124-cpu:80:8125", "--before", "1", "--after", "1", "--max-matches", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run capped query code=%d stderr=%s", code, stderr.String())
	}
	text = stdout.String()
	for _, want := range []string{`"max_matches": 1`, `"truncated": true`, `"event_count": 1`, `"id": 3`} {
		if !strings.Contains(text, want) {
			t.Fatalf("capped query output missing %s: %s", want, text)
		}
	}
}

func TestHashBGR555FrameUsesLittleEndianPixels(t *testing.T) {
	got := hashBGR555Frame([]uint16{0x1234, 0xabcd})
	want := hexHash([]byte{0x34, 0x12, 0xcd, 0xab})
	if got != want {
		t.Fatalf("hashBGR555Frame = %s, want %s", got, want)
	}
}

func TestReplayWriterRangesIncludesExplicitAndWatchRanges(t *testing.T) {
	dir := t.TempDir()
	watchPath := filepath.Join(dir, "watch.txt")
	if err := os.WriteFile(watchPath, []byte("link_x = u16(wram:0x22)\n"), 0666); err != nil {
		t.Fatalf("write watch: %v", err)
	}
	got, err := replayWriterRanges("vram:0x40-0x41", watchPath, "")
	if err != nil {
		t.Fatalf("replayWriterRanges: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if formatRange(got[0]) != "vram:0x40-0x41" || formatRange(got[1]) != "wram:0x22-0x23" {
		t.Fatalf("ranges = %#v", got)
	}
	if lastReplayFrame(0) != 0 || lastReplayFrame(3) != 2 {
		t.Fatalf("lastReplayFrame returned unexpected values")
	}
}

func TestRunFiltersWatchNames(t *testing.T) {
	rom := newTraceTestROM([]byte{0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	watchPath := filepath.Join(dir, "watch.txt")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	if err := os.WriteFile(watchPath, []byte("link_x = u16(wram:0x22)\nlink_y = u16(wram:0x20)\n"), 0666); err != nil {
		t.Fatalf("write watch: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "watch", "--watch", watchPath, "--watch-name", "link_x", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(traceData)
	if !strings.Contains(text, `"name":"link_x"`) || strings.Contains(text, `"name":"link_y"`) {
		t.Fatalf("trace watch filter mismatch: %s", text)
	}
	summaryData, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	summary := string(summaryData)
	if !strings.Contains(summary, `"watch_names": [`) || !strings.Contains(summary, `"link_x"`) || strings.Contains(summary, `"link_y"`) {
		t.Fatalf("summary watch filter mismatch: %s", summary)
	}
	if !strings.Contains(summary, `"start_boundary": "fresh_frame"`) {
		t.Fatalf("summary missing fresh start boundary: %s", summary)
	}
}

func TestRunWatchesDoNotPerturbState(t *testing.T) {
	// Store $5A to $0022, then spin. The watch reads $0022 at each
	// frame boundary, and a bus read would leave $5A on the open bus.
	rom := newTraceTestROM([]byte{0xa9, 0x5a, 0x85, 0x22, 0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	watchPath := filepath.Join(dir, "watch.txt")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	if err := os.WriteFile(watchPath, []byte("v = u8(wram:0x22)\n"), 0666); err != nil {
		t.Fatalf("write watch: %v", err)
	}
	hashes := func(name string, extra ...string) []string {
		summaryPath := filepath.Join(dir, name+".summary.json")
		args := append([]string{"run", "--rom", romPath, "--frames", "2", "--out", filepath.Join(dir, name+".jsonl"), "--summary", summaryPath}, extra...)
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("run %s code=%d stderr=%s", name, code, stderr.String())
		}
		data, err := os.ReadFile(summaryPath)
		if err != nil {
			t.Fatalf("read summary: %v", err)
		}
		var summary struct {
			FrameSummary []struct {
				StateHash string `json:"state_hash"`
			} `json:"frame_summary"`
		}
		if err := json.Unmarshal(data, &summary); err != nil {
			t.Fatalf("decode summary: %v", err)
		}
		var hs []string
		for _, f := range summary.FrameSummary {
			hs = append(hs, f.StateHash)
		}
		return hs
	}
	plain := hashes("plain", "--events", "frame")
	watched := hashes("watched", "--events", "watch", "--watch", watchPath)
	if !slices.Equal(plain, watched) {
		t.Fatalf("state hashes with watches = %v, want %v", watched, plain)
	}
}

func TestReplayPassesWatchNameFilter(t *testing.T) {
	rom := newTraceTestROM([]byte{0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	watchPath := filepath.Join(dir, "watch.txt")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	if err := os.WriteFile(watchPath, []byte("link_x = u16(wram:0x22)\nlink_y = u16(wram:0x20)\n"), 0666); err != nil {
		t.Fatalf("write watch: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "watch", "--watch", watchPath, "--watch-name", "link_x", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"watch_names": [`) || !strings.Contains(string(manifest), `"link_x"`) || strings.Contains(string(manifest), `"link_y"`) {
		t.Fatalf("manifest watch filter mismatch: %s", string(manifest))
	}
	if _, err := os.Stat(filepath.Join(outDir, "writer-02.json")); err == nil {
		t.Fatalf("replay generated writer for filtered watch link_y")
	}
}

func TestReplayManifestRecordsEvents(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, newTraceTestROM([]byte{0x80, 0xfe}), 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "cpu_block,frame", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	traceData, err := os.ReadFile(filepath.Join(outDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(manifest), `"events": "cpu_block,frame"`) {
		t.Fatalf("manifest missing events: %s", string(manifest))
	}
	if !strings.Contains(string(traceData), `"kind":"cpu_block"`) {
		t.Fatalf("trace missing cpu_block: %s", string(traceData))
	}
	if !strings.Contains(string(traceData), `"end_pc"`) || !strings.Contains(string(traceData), `"successor_pc"`) {
		t.Fatalf("trace missing cpu_block successor fields: %s", string(traceData))
	}
	if !strings.Contains(string(traceData), `"branch_kind":"branch_taken"`) {
		t.Fatalf("trace missing branch classification: %s", string(traceData))
	}
	for _, want := range []string{`"m_width":8`, `"x_width":8`} {
		if !strings.Contains(string(traceData), want) {
			t.Fatalf("trace missing CPU width %s: %s", want, string(traceData))
		}
	}
}

func TestReplayWritesProgress(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, newTraceTestROM([]byte{0x80, 0xfe}), 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "frame", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	progress, err := os.ReadFile(filepath.Join(outDir, "progress.jsonl"))
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	text := string(progress)
	for _, want := range []string{`"phase":"run"`, `"status":"start"`, `"status":"done"`, `"phase":"build-index"`, `"phase":"query-writers"`, `"phase":"write-summary"`, `"event_count"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("progress missing %s: %s", want, text)
		}
	}
	if !strings.Contains(stderr.String(), "progress phase=run status=start") {
		t.Fatalf("stderr missing progress line: %s", stderr.String())
	}
}

func TestCPUBlockClassifiesCallAndReturn(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0x20, 0x05, 0x80, // JSR $8005
		0x80, 0xfe, // BRA -2
		0x60, // RTS
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "cpu_block", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"branch_kind":"call"`, `"branch_kind":"return"`, `"successor_pc"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
}

func TestCPUStepEventsIncludeBeforeAndAfterContext(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x12, // LDA #$12
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "cpu_step", "--pc", "cpu:00:8002", "--out", tracePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"kind":"cpu_step"`, `"disasm":"LDA"`, `"bytes":[169,18]`, `"cpu_after"`, `"successor_pc"`, `"m_width":8`, `"x_width":8`} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, `"addr":32768`) {
		t.Fatalf("trace included unfiltered pc: %s", text)
	}
}

func TestRunFiltersCPUBlocksByPC(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0x20, 0x05, 0x80, // JSR $8005
		0x80, 0xfe, // BRA -2
		0x60, // RTS
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "cpu_block", "--pc", "cpu:00:8005", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"addr":32773`) || strings.Contains(text, `"addr":32768`) {
		t.Fatalf("trace pc filter output = %s", text)
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), `"pc_ranges"`) {
		t.Fatalf("summary missing pc_ranges: %s", string(summary))
	}
}

func TestReplayPassesPCFilter(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0x20, 0x05, 0x80, // JSR $8005
		0x80, 0xfe, // BRA -2
		0x60, // RTS
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "cpu_block", "--pc", "cpu:00:8005", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	traceData, err := os.ReadFile(filepath.Join(outDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(traceData), `"addr":32773`) || strings.Contains(string(traceData), `"addr":32768`) {
		t.Fatalf("replay trace pc filter output = %s", string(traceData))
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"pc": "cpu:00:8005"`) {
		t.Fatalf("manifest missing pc filter: %s", string(manifest))
	}
}

func TestRunFiltersBusByOperation(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "bus", "--addr", "wram:0x00", "--op", "write", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"op":"write"`) || strings.Contains(text, `"op":"read"`) {
		t.Fatalf("trace op filter output = %s", text)
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), `"op": "write"`) {
		t.Fatalf("summary missing op filter: %s", string(summary))
	}
}

func TestReplayPassesOperationFilter(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "bus", "--addr", "wram:0x00", "--op", "read", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	traceData, err := os.ReadFile(filepath.Join(outDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(traceData)
	if !strings.Contains(text, `"op":"read"`) || strings.Contains(text, `"op":"write"`) {
		t.Fatalf("replay trace op filter output = %s", text)
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"op": "read"`) {
		t.Fatalf("manifest missing op filter: %s", string(manifest))
	}
}

func TestReplayStopOnDivergence(t *testing.T) {
	rom := newTraceTestROM([]byte{0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	comparePath := filepath.Join(dir, "compare.jsonl")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	if err := os.WriteFile(comparePath, []byte(`{"id":0,"schema":1,"kind":"frame","frame":0,"name":"state","hash":"different"}
`), 0666); err != nil {
		t.Fatalf("write compare: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "frame", "--compare", comparePath, "--stop-on-divergence", "--out-dir", outDir}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("replay code = 0, want divergence failure")
	}
	if !strings.Contains(stderr.String(), "divergence at comparable event") {
		t.Fatalf("stderr missing divergence: %s", stderr.String())
	}
	diff, err := os.ReadFile(filepath.Join(outDir, "first-difference.json"))
	if err != nil {
		t.Fatalf("read first-difference: %v", err)
	}
	if !strings.Contains(string(diff), `"reason": "event_mismatch"`) {
		t.Fatalf("first-difference missing mismatch: %s", string(diff))
	}
}

func TestRunLimitsEvents(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--max-events", "1", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("trace line count = %d, want 1: %s", got, string(data))
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	for _, want := range []string{`"event_count": 1`, `"max_events": 1`, `"truncated": true`} {
		if !strings.Contains(string(summary), want) {
			t.Fatalf("summary missing %s: %s", want, string(summary))
		}
	}
}

func TestReplayPassesMaxEvents(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--max-events", "1", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	traceData, err := os.ReadFile(filepath.Join(outDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if got := strings.Count(string(traceData), "\n"); got != 1 {
		t.Fatalf("replay trace line count = %d, want 1: %s", got, string(traceData))
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"max_events": 1`) {
		t.Fatalf("manifest missing max_events: %s", string(manifest))
	}
}

func TestRunLimitsTraceBytes(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--max-bytes", "8", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("trace bytes = %d, want 0: %s", len(data), string(data))
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	for _, want := range []string{`"event_count": 0`, `"max_bytes": 8`, `"truncated": true`} {
		if !strings.Contains(string(summary), want) {
			t.Fatalf("summary missing %s: %s", want, string(summary))
		}
	}
}

func TestReplayPassesMaxBytes(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0xad, 0x00, 0x00, // LDA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--max-bytes", "8", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	traceData, err := os.ReadFile(filepath.Join(outDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(traceData) != 0 {
		t.Fatalf("replay trace bytes = %d, want 0: %s", len(traceData), string(traceData))
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"max_bytes": 8`) {
		t.Fatalf("manifest missing max_bytes: %s", string(manifest))
	}
}

func TestRunCompressesGzipTraceAndQueryReadsIt(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl.gz")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--compress", "gzip", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		t.Fatalf("trace is not gzip: % x", data[:min(len(data), 4)])
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "writers", "--trace", tracePath, "--addr", "wram:0x00"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("query compressed trace code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"kind": "bus"`) {
		t.Fatalf("query compressed trace output = %s", stdout.String())
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	for _, want := range []string{`"trace_compression": "gzip"`, `"trace_compressed_hash"`, `"trace_compressed_bytes"`} {
		if !strings.Contains(string(summary), want) {
			t.Fatalf("summary missing %s: %s", want, string(summary))
		}
	}
}

func TestReplayCompressesTraceManifest(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x7f, // LDA #$7f
		0x8d, 0x00, 0x00, // STA $0000
		0x80, 0xfe, // BRA -2
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "bus,frame", "--addr", "wram:0x00", "--compress", "gzip", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	tracePath := filepath.Join(outDir, "trace.jsonl.gz")
	if _, err := os.Stat(tracePath); err != nil {
		t.Fatalf("stat compressed trace: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"index", "--trace", tracePath, "--out", filepath.Join(dir, "index2.json")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("index compressed trace code=%d stderr=%s", code, stderr.String())
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, want := range []string{`"path": "` + tracePath + `"`, `"trace_compression": "gzip"`, `"trace_hash"`, `"trace_compressed_hash"`, `"trace_compressed_bytes"`} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("manifest missing %s: %s", want, string(manifest))
		}
	}
}

func TestRunRecordsDMAChannelFilter(t *testing.T) {
	rom := newTraceTestROM([]byte{0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "1", "--events", "frame,dma", "--dma-channel", "3,1", "--out", tracePath, "--summary", summaryPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run trace code=%d stderr=%s", code, stderr.String())
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), `"dma_channels": [`) || !strings.Contains(string(summary), "1") || !strings.Contains(string(summary), "3") {
		t.Fatalf("summary missing dma channel filter: %s", string(summary))
	}
}

func TestReplayWritesFrameViewer(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, newTraceTestROM([]byte{0xea, 0xea, 0x4c, 0x00, 0x80}), 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "replay")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "2", "--events", "frame", "--out-dir", outDir, "--viewer"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	pngPath := filepath.Join(outDir, "frames", "frame_000000.png")
	f, err := os.Open(pngPath)
	if err != nil {
		t.Fatalf("open frame png: %v", err)
	}
	cfg, err := png.DecodeConfig(f)
	f.Close()
	if err != nil {
		t.Fatalf("decode frame png: %v", err)
	}
	if cfg.Width != 256 || cfg.Height == 0 {
		t.Fatalf("png size = %dx%d, want 256xN", cfg.Width, cfg.Height)
	}
	viewer, err := os.ReadFile(filepath.Join(outDir, "viewer.html"))
	if err != nil {
		t.Fatalf("read viewer: %v", err)
	}
	if !strings.Contains(string(viewer), "frame_000000.png") || !strings.Contains(string(viewer), "snestrace replay") {
		t.Fatalf("viewer missing replay data: %s", string(viewer))
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, want := range []string{`"name": "frame-png-dir"`, `"name": "viewer"`, `"viewer": true`} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("manifest missing %s: %s", want, string(manifest))
		}
	}
}

func TestReplayConsumesRequestFile(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, newTraceTestROM([]byte{0xea, 0xea, 0x4c, 0x00, 0x80}), 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	inputsPath := filepath.Join(dir, "inputs.json")
	if err := os.WriteFile(inputsPath, []byte(`[256]`), 0666); err != nil {
		t.Fatalf("write inputs: %v", err)
	}
	watchPath := filepath.Join(dir, "watch.txt")
	if err := os.WriteFile(watchPath, []byte("mode = u8(wram:0x0010)\n"), 0666); err != nil {
		t.Fatalf("write watch: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	requestPath := filepath.Join(dir, "request.json")
	request := `{
  "schema_version": "frontier-replay-request-v1",
  "target": {"target": {"chart": "game_engine", "region": "mode", "from": 7, "to": 5}},
  "rom_path": "` + filepath.ToSlash(romPath) + `",
  "frames": 1,
  "events": "frame,input,watch",
  "compress": "gzip",
  "allow_state_rom_mismatch": true,
  "inputs_path": "` + filepath.ToSlash(inputsPath) + `",
  "watch_path": "` + filepath.ToSlash(watchPath) + `"
}`
	if err := os.WriteFile(requestPath, []byte(request), 0666); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--request", requestPath, "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "trace.jsonl.gz")); err != nil {
		t.Fatalf("stat compressed trace: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, want := range []string{`"request_path": "`, `"request_hash"`, `"request_target"`, `"region": "mode"`, `"trace_compression": "gzip"`} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("manifest missing %s: %s", want, string(manifest))
		}
	}
}

func TestReplayPassesDMAChannelFilter(t *testing.T) {
	rom := newTraceTestROM([]byte{0x80, 0xfe})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"replay", "--rom", romPath, "--frames", "1", "--events", "frame,dma", "--dma-channel", "2", "--out-dir", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run replay code=%d stderr=%s", code, stderr.String())
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"dma_channel": "2"`) {
		t.Fatalf("manifest missing dma_channel: %s", string(manifest))
	}
}

func TestParseDMAChannelsRejectsInvalidChannel(t *testing.T) {
	if _, err := parseDMAChannels("8"); err == nil {
		t.Fatal("parseDMAChannels accepted channel 8")
	}
	if got, err := parseDMAChannels("3,1"); err != nil || !got[1] || !got[3] {
		t.Fatalf("parseDMAChannels = %#v, %v", got, err)
	}
	ctx := runContext{dmaChannels: map[int]bool{1: true, 3: true}}
	if !ctx.matchesDMAChannel(1) || ctx.matchesDMAChannel(2) {
		t.Fatalf("matchesDMAChannel returned unexpected result")
	}
}

func TestQueryWritersFrameRange(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":1,"space":"wram","addr":34,"op":"write","value":91}
{"id":2,"schema":1,"kind":"bus","frame":2,"space":"wram","addr":34,"op":"write","value":92}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "writers", "--trace", tracePath, "--addr", "wram:0x22", "--frame-start", "2", "--frame-end", "2"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run query code=%d stderr=%s", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"id": 2`) || strings.Contains(text, `"id": 1`) {
		t.Fatalf("query output = %s", text)
	}
}

func TestIndex(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	indexPath := filepath.Join(dir, "trace.index.json")
	err := os.WriteFile(tracePath, []byte(`{"id":1,"schema":1,"kind":"bus","frame":0,"pc":{"bank":128,"addr":33059},"space":"wram","addr":34,"op":"write","width":1}
{"id":2,"schema":1,"kind":"dma","frame":1,"dest":{"space":"vram","start":16384,"end":18431}}
`), 0666)
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"index", "--trace", tracePath, "--out", indexPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run index code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"event_count": 2`) || !strings.Contains(text, `"dma"`) || !strings.Contains(text, `"dma_dest"`) || !strings.Contains(text, `"pc_ranges"`) || !strings.Contains(text, `"start": 8421667`) {
		t.Fatalf("index output = %s", text)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "writers", "--trace", indexPath, "--addr", "wram:0x22"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("query index code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": 1`) {
		t.Fatalf("query index output = %s", stdout.String())
	}
}

func TestQueryNegativeWindow(t *testing.T) {
	for _, flag := range []string{"--before", "--after"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"query", "trace-window", flag, "-1"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "nonnegative") {
			t.Fatalf("%s: code %d, error %s", flag, code, stderr.String())
		}
	}
}

func TestRunCapturesFrames(t *testing.T) {
	rom := newTraceTestROM([]byte{
		0xe2, 0x30, // SEP #$30
		0xa9, 0x0f, // LDA #$0f
		0x8d, 0x00, 0x21, // STA $2100
		0x9c, 0x21, 0x21, // loop: STZ $2121
		0xe8,             // INX
		0x8e, 0x22, 0x21, // STX $2122
		0x9c, 0x22, 0x21, // STZ $2122
		0x80, 0xf4, // BRA loop
	})
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, rom, 0666); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	frameDir := filepath.Join(dir, "frames")
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--rom", romPath, "--frames", "4", "--events", "cpu_insn,cpu_transition", "--out", tracePath,
		"--frame-dir", frameDir, "--frame-from", "1", "--frame-max", "2", "--frame-png", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code=%d stderr=%s", code, stderr.String())
	}
	c, err := framecap.Open(frameDir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Receipt == nil || c.Receipt.Outcome != trace.OutcomeLimit || c.Receipt.TruncationReason != "frame_limit" || c.Receipt.Frames != 4 || c.Receipt.Stored != 2 {
		t.Fatalf("receipt = %+v", c.Receipt)
	}
	f, err := os.Open(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events, err := trace.DecodeStrict(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, events[0].Run), mustJSON(t, c.Header.Run); got != want {
		t.Fatalf("run identity differs:\ntrace  %s\nframes %s", got, want)
	}
	entry := map[uint64]uint64{} // seq to entry cycle
	var last uint64
	for _, e := range events {
		switch {
		case e.Insn != nil:
			entry[e.Insn.Seq] = e.Insn.Entry.Cycles
			last = e.Insn.Seq
		case e.Transition != nil:
			entry[e.Transition.Seq] = e.Transition.Before.Cycles
			last = e.Transition.Seq
		}
	}
	// seqAt checks that seq is the first observation at or after cycle.
	seqAt := func(what string, r framecap.Record, seq *uint64, cycle uint64) {
		t.Helper()
		if seq == nil {
			t.Errorf("frame %d: no %s seq", r.Number, what)
			return
		}
		s := *seq
		if s <= last && entry[s] < cycle || s > 1 && entry[s-1] >= cycle {
			t.Errorf("frame %d: %s seq %d (cycle %d, previous %d) for boundary %d", r.Number, what, s, entry[s], entry[s-1], cycle)
		}
	}
	for i, r := range c.Records {
		if r.Number != i || *r.TraceFrame != i {
			t.Errorf("record %d: number %d trace frame %d", i, r.Number, *r.TraceFrame)
		}
		wantStored := i == 1 || i == 2
		if r.Stored != wantStored {
			t.Errorf("frame %d: stored %v skip %q", i, r.Stored, r.Skip)
		}
		if r.Stored && (r.Width != 256 || r.Height != 224 || r.Format != snes.PixelFormatBGR555) {
			t.Errorf("frame %d: %dx%d %s", i, r.Width, r.Height, r.Format)
		}
		seqAt("start", r, r.SeqStart, r.Start)
		seqAt("vblank", r, r.SeqVBlank, r.VBlank)
		if r.End != nil {
			seqAt("end", r, r.SeqEnd, *r.End)
		} else if i != len(c.Records)-1 {
			t.Errorf("frame %d has no end", i)
		}
	}
	if r := c.Records[1]; r.PNG == "" {
		t.Errorf("frame 1: no png")
	} else if _, err := os.Stat(filepath.Join(frameDir, r.PNG)); err != nil {
		t.Error(err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
