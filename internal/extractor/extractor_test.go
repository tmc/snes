package extractor_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/internal/extractor"
)

func TestCandidateSchema(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		targetID  string
		wantEntry uint32
		wantInsns int
		wantSpan  int
		wantCtxE  string
	}{
		{
			name: "top-level fields",
			input: `{
				"id": "leaf-test1",
				"kind": "leaf",
				"entry": 65536,
				"returns": [65538],
				"instruction_count": 2,
				"byte_span": 3,
				"entry_contexts": [{"e": "clear", "m": "set", "x": "set"}]
			}`,
			wantEntry: 65536,
			wantInsns: 2,
			wantSpan:  3,
			wantCtxE:  "clear",
		},
		{
			name: "nested proposal fields",
			input: `{
				"id": "author-leaf",
				"kind": "leaf",
				"proposal": {
					"entry": 65536,
					"start": 65536,
					"end": 65539,
					"instruction_ids": ["id1", "id2"],
					"entry_contexts": [{"e": "clear", "m": "set", "x": "set", "c": "set"}]
				}
			}`,
			wantEntry: 65536,
			wantInsns: 2,
			wantSpan:  3,
			wantCtxE:  "clear",
		},
		{
			name: "proposals container selection by id",
			input: `{
				"candidates": [
					{"id": "cand-other", "entry": 12345, "instruction_count": 3},
					{
						"id": "author-leaf",
						"kind": "leaf",
						"proposal": {
							"start": 65536,
							"end": 65539,
							"instruction_ids": ["a", "b"],
							"entry_contexts": [{"e": "clear"}]
						}
					}
				]
			}`,
			targetID:  "author-leaf",
			wantEntry: 65536,
			wantInsns: 2,
			wantSpan:  3,
			wantCtxE:  "clear",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cand, err := extractor.LoadCandidate([]byte(tt.input), tt.targetID)
			if err != nil {
				t.Fatalf("LoadCandidate failed: %v", err)
			}
			if cand.Entry != tt.wantEntry {
				t.Errorf("Entry = %d, want %d", cand.Entry, tt.wantEntry)
			}
			if cand.InstructionCount != tt.wantInsns {
				t.Errorf("InstructionCount = %d, want %d", cand.InstructionCount, tt.wantInsns)
			}
			if cand.ByteSpan != tt.wantSpan {
				t.Errorf("ByteSpan = %d, want %d", cand.ByteSpan, tt.wantSpan)
			}
			if len(cand.EntryContexts) == 0 || cand.EntryContexts[0].E != tt.wantCtxE {
				t.Errorf("EntryContexts[0].E = %v, want %s", cand.EntryContexts, tt.wantCtxE)
			}
		})
	}
}

func TestFileDigests(t *testing.T) {
	t.Run("plain file", func(t *testing.T) {
		tmpDir := t.TempDir()
		p := filepath.Join(tmpDir, "plain.txt")
		if err := os.WriteFile(p, []byte("snes test data"), 0644); err != nil {
			t.Fatal(err)
		}
		raw, dec, err := extractor.FileDigests(p)
		if err != nil {
			t.Fatalf("FileDigests failed: %v", err)
		}
		if raw != dec {
			t.Errorf("plain file raw (%s) != dec (%s)", raw, dec)
		}

		recFile := filepath.Join(tmpDir, "plain.receipt.json")
		_ = os.WriteFile(recFile, []byte(`{"schema": 2, "stream_sha256": "`+raw+`"}`), 0644)
		if _, err := extractor.VerifyReceiptDigest(recFile, p, raw, dec); err != nil {
			t.Errorf("VerifyReceiptDigest plain should pass: %v", err)
		}

		_ = os.WriteFile(recFile, []byte(`{"schema": 2, "stream_sha256": "badhash"}`), 0644)
		if _, err := extractor.VerifyReceiptDigest(recFile, p, raw, dec); err == nil {
			t.Errorf("VerifyReceiptDigest plain should fail on mismatch")
		}
	})

	t.Run("gzip compressed file", func(t *testing.T) {
		tmpDir := t.TempDir()
		p := filepath.Join(tmpDir, "stream.gz")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		decompressed := []byte("decompressed snes trace contents line 1\nline 2\n")
		if _, err := gw.Write(decompressed); err != nil {
			t.Fatal(err)
		}
		gw.Close()
		f.Close()

		raw, dec, err := extractor.FileDigests(p)
		if err != nil {
			t.Fatalf("FileDigests on gz failed: %v", err)
		}
		if raw == dec {
			t.Errorf("gzip raw and dec hashes should differ for compressed data")
		}

		recFile := filepath.Join(tmpDir, "trace.receipt.json")
		// Receipt matching decompressed hash per Schema 2 stream semantics.
		_ = os.WriteFile(recFile, []byte(`{"schema": 2, "stream_sha256": "`+dec+`"}`), 0644)
		if _, err := extractor.VerifyReceiptDigest(recFile, p, raw, dec); err != nil {
			t.Errorf("VerifyReceiptDigest dec should pass: %v", err)
		}

		// Receipt matching raw container hash.
		_ = os.WriteFile(recFile, []byte(`{"schema": 2, "stream_sha256": "`+raw+`"}`), 0644)
		if _, err := extractor.VerifyReceiptDigest(recFile, p, raw, dec); err != nil {
			t.Errorf("VerifyReceiptDigest raw should pass: %v", err)
		}

		// Receipt mismatching both.
		_ = os.WriteFile(recFile, []byte(`{"schema": 2, "stream_sha256": "0000000000000000000000000000000000000000000000000000000000000000"}`), 0644)
		if _, err := extractor.VerifyReceiptDigest(recFile, p, raw, dec); err == nil {
			t.Errorf("VerifyReceiptDigest should fail on mismatch")
		}
	})
}

func TestMemoryTrace(t *testing.T) {
	insns := []*extractor.RawInsn{
		{
			Seq:   10,
			Entry: extractor.CPUState{Cycles: 100, PB: 0x0E, PC: 0xD60B, S: 0x01F8},
			Exit:  extractor.CPUState{Cycles: 110, PB: 0x0E, PC: 0xD60D, S: 0x01F8},
		},
		{
			Seq:   11,
			Entry: extractor.CPUState{Cycles: 110, PB: 0x0E, PC: 0xD60D, S: 0x01F8},
			Exit:  extractor.CPUState{Cycles: 120, PB: 0x0E, PC: 0xD60F, S: 0x01F8},
		},
	}

	tests := []struct {
		name         string
		bus          extractor.BusEventList
		transitions  []*extractor.RawEvent
		startCycle   uint64
		endCycle     uint64
		wantRefusal  string
		wantInitLen  int
		wantWriteLen int
	}{
		{
			name: "pure wram read and write",
			bus: extractor.BusEventList{
				{Cycle: 105, ID: 1, Space: "wram", Addr: 0x01F6, Value: 0x42, Op: "read"},
				{Cycle: 115, ID: 2, Space: "wram", Addr: 0x009C, Value: 0x20, Op: "write"},
			},
			startCycle:   100,
			endCycle:     120,
			wantInitLen:  1,
			wantWriteLen: 1,
		},
		{
			name: "unsupported mmio access refused",
			bus: extractor.BusEventList{
				{Cycle: 105, ID: 1, Space: "mmio", Addr: 0x2100, Value: 0x0F, Op: "write"},
			},
			startCycle:  100,
			endCycle:    120,
			wantRefusal: "unsupported_memory_space:mmio",
		},
		{
			name: "intervening dma refused",
			bus:  extractor.BusEventList{},
			transitions: []*extractor.RawEvent{
				{Cycle: 112, Kind: "dma"},
			},
			startCycle:  100,
			endCycle:    120,
			wantRefusal: "hardware_event_in_interval:dma",
		},
		{
			name: "intervening interrupt/transition refused",
			bus:  extractor.BusEventList{},
			transitions: []*extractor.RawEvent{
				{Cycle: 112, Kind: "cpu_transition"},
			},
			startCycle:  100,
			endCycle:    120,
			wantRefusal: "interrupt_or_transition_in_interval",
		},
		{
			name: "read after write mismatch refused",
			bus: extractor.BusEventList{
				{Cycle: 105, ID: 1, Space: "wram", Addr: 0x009C, Value: 0x20, Op: "write"},
				{Cycle: 115, ID: 2, Space: "wram", Addr: 0x009C, Value: 0x99, Op: "read"},
			},
			startCycle:  100,
			endCycle:    120,
			wantRefusal: "read_after_write_mismatch:addr=0x7e009c,cur=32,read=153",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := extractor.ExtractMemoryTrace(tt.bus, tt.transitions, tt.startCycle, tt.endCycle, insns)
			if tt.wantRefusal != "" {
				if mt.RefusalReason != tt.wantRefusal {
					t.Fatalf("RefusalReason = %q, want %q", mt.RefusalReason, tt.wantRefusal)
				}
				return
			}
			if mt.RefusalReason != "" {
				t.Fatalf("unexpected refusal: %s", mt.RefusalReason)
			}
			if len(mt.InitialMemory) != tt.wantInitLen {
				t.Errorf("len(InitialMemory) = %d, want %d", len(mt.InitialMemory), tt.wantInitLen)
			}
			if len(mt.ObservedWrites) != tt.wantWriteLen {
				t.Errorf("len(ObservedWrites) = %d, want %d", len(mt.ObservedWrites), tt.wantWriteLen)
			}
		})
	}
}

func TestWriteHistory(t *testing.T) {
	histJSON := `{"kind":"bus","op":"write","space":"wram","addr":502,"after":104,"cycle":50}
{"kind":"bus","op":"write","space":"wram","addr":503,"after":131,"cycle":60}
{"kind":"bus","op":"write","space":"wram","addr":504,"after":6,"cycle":70}
`
	scanner := bufio.NewScanner(bytes.NewBufferString(histJSON))
	h, err := extractor.NewWriteHistory(scanner)
	if err != nil {
		t.Fatalf("NewWriteHistory failed: %v", err)
	}

	cells := []extractor.MemoryCell{
		{Address: 0x7E01F6, Value: 104},
		{Address: 0x7E01F7, Value: 131},
		{Address: 0x7E01F8, Value: 6},
	}

	// 1. Confirmed by prior write before cycle 100.
	sources, err := h.ClassifyInitialMemory(cells, 100, "")
	if err != nil {
		t.Fatalf("ClassifyInitialMemory failed: %v", err)
	}
	for i, s := range sources {
		if s.Source != "confirmed_by_prior_write" {
			t.Errorf("sources[%d].Source = %s, want confirmed_by_prior_write", i, s.Source)
		}
	}

	// 2. Unwritten cell with bare string "power_on" MUST BE REFUSED (no unverified fallback).
	unwritten := []extractor.MemoryCell{{Address: 0x7E9999, Value: 0}}
	if _, err := h.ClassifyInitialMemory(unwritten, 100, "power_on"); err == nil {
		t.Errorf("missing writes under power_on without pinned state must be refused")
	}

	// 3. Unwritten cell with pinned checkpoint WRAM produces restored_checkpoint_wram (NOT power_on_wram).
	mockWRAM := make([]byte, 131072)
	mockWRAM[0x9999] = 42
	h.SetPinnedWRAM(mockWRAM)
	ckptCell := []extractor.MemoryCell{{Address: 0x7E9999, Value: 42}}
	cSources, err := h.ClassifyInitialMemory(ckptCell, 100, "restored_state")
	if err != nil {
		t.Fatalf("pinned checkpoint state should admit cell: %v", err)
	}
	if cSources[0].Source != "restored_checkpoint_wram" {
		t.Errorf("expected restored_checkpoint_wram, got %s", cSources[0].Source)
	}

	// 4. Pinned checkpoint state mismatch must be refused.
	ckptMismatch := []extractor.MemoryCell{{Address: 0x7E9999, Value: 99}}
	if _, err := h.ClassifyInitialMemory(ckptMismatch, 100, "restored_state"); err == nil {
		t.Errorf("pinned checkpoint value mismatch must be refused")
	}

	// 5. Value mismatch against prior write must be refused.
	mismatch := []extractor.MemoryCell{{Address: 0x7E01F6, Value: 99}}
	if _, err := h.ClassifyInitialMemory(mismatch, 100, ""); err == nil {
		t.Errorf("value mismatch against prior write must be refused")
	}
}

func TestZeroCycleHeaderMissingWritesMustRefuse(t *testing.T) {
	// Zero-cycle write to addr 100, but cell 200 has no writes.
	histJSON := `{"kind":"bus","op":"write","space":"wram","addr":100,"after":55,"cycle":0}`
	scanner := bufio.NewScanner(bytes.NewBufferString(histJSON))
	h, err := extractor.NewWriteHistory(scanner)
	if err != nil {
		t.Fatalf("NewWriteHistory failed: %v", err)
	}

	// Address 200 with value 0 or 55 must not be admitted via fallback; must refuse.
	missing := []extractor.MemoryCell{{Address: 0x7E0200, Value: 0}}
	if _, err := h.ClassifyInitialMemory(missing, 1000, "power_on"); err == nil {
		t.Errorf("zero-cycle header + missing writes must refuse")
	}
}

func TestCheckpointValidation(t *testing.T) {
	tmpDir := t.TempDir()

	encodeState := func(path string, version uint32, romHash [32]byte, wramSize int) {
		type mockState struct {
			Version   uint32
			ROMHash   [32]byte
			BusMDR    uint8
			BusMEMSEL uint8
			WRAM      []byte
		}
		var buf bytes.Buffer
		s := mockState{
			Version: version,
			ROMHash: romHash,
			WRAM:    make([]byte, wramSize),
		}
		if err := gob.NewEncoder(&buf).Encode(s); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Wrong ROM checkpoint -> must refuse.
	wrongROMPath := filepath.Join(tmpDir, "wrong_rom.state")
	var wrongHash [32]byte
	wrongHash[0] = 0xFF
	encodeState(wrongROMPath, 3, wrongHash, 131072)
	if _, _, err := extractor.LoadCheckpointWRAM(wrongROMPath, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Errorf("wrong-ROM checkpoint must refuse")
	}

	// Truncated WRAM -> must refuse.
	truncatedPath := filepath.Join(tmpDir, "truncated.state")
	var goodHash [32]byte
	encodeState(truncatedPath, 3, goodHash, 65536) // 64KB instead of 128KB
	if _, _, err := extractor.LoadCheckpointWRAM(truncatedPath, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Errorf("truncated WRAM must refuse")
	}
}

func TestPostCheckpointWriteGapsMustRefuse(t *testing.T) {
	// Checkpoint sets 0x7E0100 to 0x10.
	mockWRAM := make([]byte, 131072)
	mockWRAM[0x100] = 0x10

	// History has no post-checkpoint write for 0x7E0100.
	histJSON := `{"kind":"bus","op":"write","space":"wram","addr":500,"after":1,"cycle":50}`
	scanner := bufio.NewScanner(bytes.NewBufferString(histJSON))
	h, err := extractor.NewWriteHistory(scanner)
	if err != nil {
		t.Fatalf("NewWriteHistory failed: %v", err)
	}
	h.SetPinnedWRAM(mockWRAM)

	// Routine reads 0x20 at 0x7E0100 (a write occurred between checkpoint and routine but is missing from history).
	gapped := []extractor.MemoryCell{{Address: 0x7E0100, Value: 0x20}}
	if _, err := h.ClassifyInitialMemory(gapped, 100, "restored_state"); err == nil {
		t.Errorf("post-checkpoint write gap / mismatch must refuse")
	}
}

func TestCaptureValidation(t *testing.T) {
	// Table-driven tests for seq monotonicity, conflicting duplicates, and continuity.
	t.Run("non-monotonic seq rejected", func(t *testing.T) {
		tmpDir := t.TempDir()
		capFile := filepath.Join(tmpDir, "bad_seq.jsonl")
		content := `{"kind":"cpu_insn","insn":{"seq":10,"entry":{"cycles":100,"pb":14,"pc":54795},"exit":{"cycles":110,"pb":14,"pc":54797},"status":"retired"}}
{"kind":"cpu_insn","insn":{"seq":9,"entry":{"cycles":110,"pb":14,"pc":54797},"exit":{"cycles":120,"pb":14,"pc":54800},"status":"retired"}}
`
		_ = os.WriteFile(capFile, []byte(content), 0644)
		_, _, err := extractor.FileDigests(capFile)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestControls(t *testing.T) {
	base := extractor.RoutineCaseV1{
		CaseID:    "case_001",
		ROMSHA256: "aabbcc",
		InitialState: extractor.CPUState{
			A: 1, X: 2, Y: 3, S: 500, D: 0, DB: 6, PB: 9, PC: 40000,
		},
		ObservedExitState: extractor.CPUState{
			A: 1, X: 2, Y: 3, S: 503, D: 0, DB: 6, PB: 6, PC: 33000,
		},
		ObservedNextPC: 0x068000,
		InitialMemory: []extractor.MemoryCell{
			{Address: 0x7E01F6, Value: 104},
		},
		ObservedWrites: []extractor.MemoryCell{
			{Address: 0x7E009C, Value: 32},
			{Address: 0x7E009D, Value: 64},
		},
		ExitSeq: 105,
	}

	negs := extractor.GenerateNegativeControls(base)
	if len(negs) != 10 {
		t.Errorf("expected 10 negative controls, got %d", len(negs))
	}

	// When initial memory is empty, initial_memory_flip must not be emitted.
	emptyInitBase := base
	emptyInitBase.InitialMemory = nil
	negsEmptyInit := extractor.GenerateNegativeControls(emptyInitBase)
	for _, c := range negsEmptyInit {
		if c.CaseID == emptyInitBase.CaseID+"_initial_memory_flip" {
			t.Errorf("initial_memory_flip must not be emitted when initial_memory is empty")
		}
	}

	// Swap controls verification: distinct content requirement.
	caseA := base
	caseB := base
	caseB.CaseID = "case_002"
	caseB.ObservedExitState.A = 999 // distinct exit state

	cases := []extractor.RoutineCaseV1{caseA, caseB}
	swaps := extractor.GenerateSwapControls(cases, 10)
	if len(swaps) == 0 {
		t.Fatalf("expected swap control between distinct cases")
	}
	if swaps[0].AdmitCaseID != "case_001" {
		t.Errorf("AdmitCaseID = %s, want case_001", swaps[0].AdmitCaseID)
	}

	// Identical cases must not produce swap controls.
	identicalCases := []extractor.RoutineCaseV1{caseA, caseA}
	identicalCases[1].CaseID = "case_002_identical"
	if len(extractor.GenerateSwapControls(identicalCases, 10)) != 0 {
		t.Errorf("identical cases must not produce swap controls")
	}
}
