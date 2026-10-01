package extractor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIndependentMissingFixtureAndROM verifies that when a fixture contains only
// an engine revision header and zero instruction records, the extractor refuses
// the occurrence rather than producing an ungrounded false-complete case.
func TestIndependentMissingFixtureAndROM(t *testing.T) {
	d := t.TempDir()
	write := func(n string, b []byte) string {
		p := filepath.Join(d, n)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	rb := make([]byte, 32768)
	rb[0] = 0xea
	rb[1] = 0x60
	rb[0x1000] = 0x20
	rom := write("rom", rb)
	fix := write("fixture", []byte("{\"run\":{\"engine_revision\":\"test\"}}\n"))
	hist := write("history", []byte("{\"kind\":\"header\"}\n"))

	base := CPUState{S: 0x1fd, PC: 0x8000, Cycles: 10}
	call := base
	call.PC = 0x9000
	call.S += 2
	call.Cycles = 5
	mid := base
	mid.PC++
	mid.Cycles = 12
	exit := mid
	exit.PC = 0x9003
	exit.S += 2
	exit.Cycles = 18

	records := []RawInsn{
		{Seq: 9, Entry: call, Exit: base, Length: 3, Status: "retired", Fetches: []FetchRec{{Addr: 0x9000, Value: 0x20, Role: "opcode", ROMOffset: 4096}}},
		{Seq: 10, Entry: base, Exit: mid, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8000, Value: 0xea, Role: "opcode", ROMOffset: 0}}},
		{Seq: 11, Entry: mid, Exit: exit, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8001, Value: 0x60, Role: "opcode", ROMOffset: 1}}},
		{Seq: 12, Entry: exit, Exit: exit, Length: 1, Status: "retired"},
	}

	var b []byte
	for _, r := range records {
		v, e := json.Marshal(RawEvent{Kind: "cpu_insn", Insn: &r})
		if e != nil {
			t.Fatal(e)
		}
		b = append(b, v...)
		b = append(b, '\n')
	}
	capPath := write("capture", b)

	cfg := Config{
		Candidate: Candidate{
			ID:               "probe",
			Kind:             "leaf",
			Entry:            0x8000,
			Returns:          []uint32{0x8001},
			InstructionCount: 2,
		},
		FixturePath: fix,
		CapturePath: capPath,
		HistoryPath: hist,
		ROMPath:     rom,
	}
	addIndependentProducerCoverage(t, &cfg)
	res, err := Extract(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("complete=%d refused=%d reasons=%v",
		res.Receipt.CompleteExecutions, res.Receipt.RejectedExecutions, res.Receipt.RefusalReasons)

	// Fixture lacked the instruction records; occurrence must be refused.
	if len(res.Cases) != 0 {
		t.Fatalf("expected 0 completed cases with missing fixture instructions, got %d", len(res.Cases))
	}
	if res.Receipt.CompleteExecutions != 0 {
		t.Fatalf("expected 0 complete executions, got %d", res.Receipt.CompleteExecutions)
	}
	if res.Receipt.RejectedExecutions != 1 {
		t.Fatalf("expected 1 rejected execution, got %d", res.Receipt.RejectedExecutions)
	}
	if res.Receipt.RefusalReasons["missing_fixture_caller_insn"] == 0 && res.Receipt.RefusalReasons["missing_fixture_body_insn"] == 0 {
		t.Fatalf("expected missing_fixture_caller_insn or missing_fixture_body_insn refusal, got %v", res.Receipt.RefusalReasons)
	}
}

// TestIndependentROMOffsetOutOfBounds verifies that instruction fetches with out-of-bounds
// ROM offsets are refused rather than bypassed.
func TestIndependentROMOffsetOutOfBounds(t *testing.T) {
	d := t.TempDir()
	write := func(n string, b []byte) string {
		p := filepath.Join(d, n)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	rb := make([]byte, 1024) // 1KB ROM
	rom := write("rom", rb)
	hist := write("history", []byte("{\"kind\":\"header\"}\n"))

	base := CPUState{S: 0x1fd, PC: 0x8000, Cycles: 10}
	call := base
	call.PC = 0x9000
	call.S += 2
	call.Cycles = 5
	mid := base
	mid.PC++
	mid.Cycles = 12
	exit := mid
	exit.PC = 0x9003
	exit.S += 2
	exit.Cycles = 18

	// ROMOffset 99999 is out-of-bounds for a 1KB ROM.
	records := []RawInsn{
		{Seq: 9, Entry: call, Exit: base, Length: 3, Status: "retired", Fetches: []FetchRec{{Addr: 0x9000, Value: 0x20, Role: "opcode", ROMOffset: 0}}},
		{Seq: 10, Entry: base, Exit: mid, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8000, Value: 0x00, Role: "opcode", ROMOffset: 99999}}},
		{Seq: 11, Entry: mid, Exit: exit, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8001, Value: 0x60, Role: "opcode", ROMOffset: 1}}},
		{Seq: 12, Entry: exit, Exit: exit, Length: 1, Status: "retired"},
	}

	var b []byte
	for _, r := range records {
		v, _ := json.Marshal(RawEvent{Kind: "cpu_insn", Insn: &r})
		b = append(b, v...)
		b = append(b, '\n')
	}
	capPath := write("capture", b)

	var fb []byte
	fb = append(fb, []byte("{\"run\":{\"engine_revision\":\"test\"}}\n")...)
	fb = append(fb, b...)
	fixPath := write("fixture", fb)

	cfg := Config{
		Candidate: Candidate{
			ID:               "oob-probe",
			Kind:             "leaf",
			Entry:            0x8000,
			Returns:          []uint32{0x8001},
			InstructionCount: 2,
		},
		FixturePath: fixPath,
		CapturePath: capPath,
		HistoryPath: hist,
		ROMPath:     rom,
	}
	addIndependentProducerCoverage(t, &cfg)
	res, err := Extract(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Cases) != 0 {
		t.Fatalf("expected 0 completed cases with OOB ROM offset, got %d", len(res.Cases))
	}
	hasOOB := false
	for reason := range res.Receipt.RefusalReasons {
		if strings.HasPrefix(reason, "rom_offset_out_of_bounds") {
			hasOOB = true
			break
		}
	}
	if !hasOOB {
		t.Fatalf("expected rom_offset_out_of_bounds refusal reason, got %v", res.Receipt.RefusalReasons)
	}
}

// TestReviewForgedROMOffsetMappingRefused reproduces the defect where an attacker-supplied
// ROMOffset on an unmapped/divergent CPU address was accepted instead of deriving the LoROM
// mapping from physical fetch address and validating ROM bytes.
func TestReviewForgedROMOffsetMappingRefused(t *testing.T) {
	d := t.TempDir()
	write := func(n string, b []byte) string {
		p := filepath.Join(d, n)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	rb := make([]byte, 32768)
	rb[0] = 0xff     // physical LoROM address 00:8000 maps to offset 0 (contains 0xFF)
	rb[0x100] = 0xea // forged offset 0x100 contains 0xEA
	rb[1] = 0x60
	rb[0x1000] = 0x20
	rom := write("rom", rb)
	hist := write("history", []byte("{\"kind\":\"header\"}\n"))

	base := CPUState{S: 0x1fd, PC: 0x8000, Cycles: 10}
	call := base
	call.PC = 0x9000
	call.S += 2
	call.Cycles = 5
	mid := base
	mid.PC++
	mid.Cycles = 12
	exit := mid
	exit.PC = 0x9003
	exit.S += 2
	exit.Cycles = 18

	// Seq 10 has Addr 0x8000 (LoROM offset 0), but claims ROMOffset 0x100.
	records := []RawInsn{
		{Seq: 9, Entry: call, Exit: base, Length: 3, Status: "retired", Fetches: []FetchRec{{Addr: 0x9000, Value: 0x20, Role: "opcode", ROMOffset: 4096}}},
		{Seq: 10, Entry: base, Exit: mid, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8000, Value: 0xea, Role: "opcode", ROMOffset: 0x100}}},
		{Seq: 11, Entry: mid, Exit: exit, Length: 1, Status: "retired", Fetches: []FetchRec{{Addr: 0x8001, Value: 0x60, Role: "opcode", ROMOffset: 1}}},
		{Seq: 12, Entry: exit, Exit: exit, Length: 1, Status: "retired"},
	}

	var b []byte
	for _, r := range records {
		v, e := json.Marshal(RawEvent{Kind: "cpu_insn", Insn: &r})
		if e != nil {
			t.Fatal(e)
		}
		b = append(b, v...)
		b = append(b, '\n')
	}
	fetchOnly, _ := json.Marshal(RawEvent{Kind: "bus", Cycle: 11, ID: 1, Space: "cpu", Op: "read", Source: &EventSource{Space: "rom"}})
	b = append(b, fetchOnly...)
	b = append(b, '\n')
	capPath := write("capture", b)
	fb := append([]byte("{\"run\":{\"engine_revision\":\"test\"}}\n"), b...)
	fix := write("fixture", fb)

	cfg := Config{
		Candidate: Candidate{
			ID:               "probe",
			Kind:             "leaf",
			Entry:            0x8000,
			Returns:          []uint32{0x8001},
			InstructionCount: 2,
		},
		FixturePath: fix,
		CapturePath: capPath,
		HistoryPath: hist,
		ROMPath:     rom,
	}
	addIndependentProducerCoverage(t, &cfg)
	res, err := Extract(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("complete=%d refused=%d reasons=%v",
		res.Receipt.CompleteExecutions, res.Receipt.RejectedExecutions, res.Receipt.RefusalReasons)

	// Forged mapping MUST be refused.
	if len(res.Cases) != 0 || res.Receipt.CompleteExecutions != 0 {
		t.Fatalf("forged ROM offset was accepted! got %d complete cases", len(res.Cases))
	}
	if res.Receipt.RejectedExecutions != 1 {
		t.Fatalf("expected 1 rejected execution, got %d", res.Receipt.RejectedExecutions)
	}
	found := false
	for reason := range res.Receipt.RefusalReasons {
		if strings.HasPrefix(reason, "forged_or_mismatched_rom_offset:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected forged ROM mapping refusal, got %v", res.Receipt.RefusalReasons)
	}
}

// TestReviewROMFetchDoesNotCertifyMemoryRefused reproduces the defect where an instruction
// sequence with memory stores (STA) and stack reads (RTS) was accepted as complete with zero
// writes and zero initial cells because the bus interval contained a single ROM fetch.
func TestReviewROMFetchDoesNotCertifyMemoryRefused(t *testing.T) {
	insns := []*RawInsn{
		{Fetches: []FetchRec{{Value: 0x85, Role: "opcode"}}}, // STA dp
		{Fetches: []FetchRec{{Value: 0x60, Role: "opcode"}}}, // RTS
	}
	// Bus interval contains only a ROM fetch event, zero WRAM writes, zero WRAM reads.
	events := BusEventList{
		{Kind: "bus", Cycle: 11, ID: 1, Space: "cpu", Op: "read", Source: &EventSource{Space: "rom"}},
	}
	r := ExtractMemoryTrace(events, nil, 10, 20, insns)
	if r.RefusalReason == "" {
		t.Fatalf("expected unverified_bus_completeness refusal on missing memory writes/reads, got empty reason (writes=%d)", len(r.ObservedWrites))
	}
	t.Logf("correctly refused incomplete bus evidence: %s", r.RefusalReason)
}

// TestReviewROMFetchCertifyMemoryPositiveCounterpart provides the valid positive counterpart
// where the bus interval contains the verified STA write and RTS stack reads.
func TestReviewROMFetchCertifyMemoryPositiveCounterpart(t *testing.T) {
	insns := []*RawInsn{
		{Fetches: []FetchRec{{Value: 0x85, Role: "opcode"}}}, // STA dp: requires 1 write
		{Fetches: []FetchRec{{Value: 0x60, Role: "opcode"}}}, // RTS: requires 2 stack reads
	}
	events := BusEventList{
		{Kind: "bus", Cycle: 11, ID: 1, Space: "cpu", Op: "read", Source: &EventSource{Space: "rom"}},
		{Kind: "bus", Cycle: 12, ID: 2, Space: "wram", Op: "write", Addr: 0x009C, Value: 0x42},
		{Kind: "bus", Cycle: 13, ID: 3, Space: "wram", Op: "read", Addr: 0x01F7, Value: 0x34},
		{Kind: "bus", Cycle: 14, ID: 4, Space: "wram", Op: "read", Addr: 0x01F8, Value: 0x12},
	}
	r := ExtractMemoryTrace(events, nil, 10, 20, insns)
	if r.RefusalReason != "" {
		t.Fatalf("unexpected refusal on complete bus trace: %s", r.RefusalReason)
	}
	if len(r.ObservedWrites) != 1 {
		t.Fatalf("len(ObservedWrites) = %d, want 1", len(r.ObservedWrites))
	}
	if len(r.InitialMemory) != 2 {
		t.Fatalf("len(InitialMemory) = %d, want 2", len(r.InitialMemory))
	}
}

// addIndependentProducerCoverage supplies unrestricted producer metadata for
// synthetic negatives, so they reach their intended fixture or ROM checks.
func addIndependentProducerCoverage(t *testing.T, cfg *Config) {
	t.Helper()
	romSHA, _, err := FileDigests(cfg.ROMPath)
	if err != nil {
		t.Fatal(err)
	}
	events := []string{"bus", "mmio", "cpu_insn", "cpu_transition", "dma", "hdma"}
	header := map[string]any{"kind": "run", "run": map[string]any{"rom_sha256": romSHA, "engine_revision": "test", "events": events}}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg.CapturePath)
	if err != nil {
		t.Fatal(err)
	}
	b = append(append(hb, '\n'), b...)
	if err := os.WriteFile(cfg.CapturePath, b, 0600); err != nil {
		t.Fatal(err)
	}
	write := func(path string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, capture := range []bool{true, false} {
		path := cfg.HistoryPath
		summary := producerSummary{ROM: romSHA, Engine: "test", Events: []string{"bus"}, Op: "write"}
		if capture {
			path = cfg.CapturePath
			summary.Events = events
			summary.Op = ""
		}
		raw, decoded, err := FileDigests(path)
		if err != nil {
			t.Fatal(err)
		}
		if raw != decoded {
			t.Fatal("synthetic plain stream hashes differ")
		}
		summary.Trace = decoded
		sp := path + ".summary.json"
		rp := path + ".receipt.json"
		write(sp, summary)
		write(rp, ReceiptData{Schema: 2, Outcome: "complete", StreamSHA256: decoded})
		if capture {
			cfg.CaptureSummaryPath = sp
			cfg.CaptureReceiptPath = rp
		} else {
			cfg.HistorySummaryPath = sp
			cfg.HistoryReceiptPath = rp
		}
	}
}
