package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery/analysis"
	"github.com/tmc/snes/internal/trace"
)

const (
	AcceptedStackCaseSHA256       = "65f6df51e1626c52e5d73132e0072c722c7a6417079d1e9387bb9613b77dce2a"
	AcceptedStackTimelineSHA256   = "27a1b55351e232dd6b03127e8e25c7d37b4606e52569bad822235de63697bc65"
	AcceptedStackReceiptSHA256    = "993b35d636df46643f6b064beacd65b0dbc950a5f150d1fbb39ff8192efb7008"
	AcceptedStackGeneratedCSHA256 = "78df7a557aba7820fb6523dbbdd141d1242562f4ca972df0f61067d1dcc9d9b1"
	AdmittedDocumentSHA256        = "cb6f4a1af5d6ec5f2d906596ea7e3d61f05d17f6709477133afc551837f51836"

	StackNode0CanonicalID = "b8ada5111a6770c7c31706b7132feddf82d73cbd35f025527632f370332d24fb" // $0CC404 PHB
	StackNode1CanonicalID = "4822592d95b274cfbb0423cb73f8a30fb1a9b53bbf414aa814b47eb80f8db550" // $0CC405 PHK
	StackNode2CanonicalID = "6d78c881fcf4aa1526d4c7c0666bf1beb03a2a1ba7833b134368da3447481769" // $0CC406 PLB
	StackNode3CanonicalID = "9e4b50e1dead1c3aba9f2604e59629481035d51df50a885bca792a2780cb9b77" // $0CC407 INC $1E0A

	ExpectedStackNode0RetirementID = 30003
	ExpectedStackNode0Seq          = 8512
)

type StackBundleManifest struct {
	BundleID                string            `json:"bundle_id"`
	BlockAddress            string            `json:"block_address"`
	Qualification           string            `json:"qualification"`
	StreamSHA256            string            `json:"stream_sha256"`
	ROMSHA256               string            `json:"rom_sha256"`
	DocumentSHA256          string            `json:"document_sha256"`
	CanonicalInstructionIDs []string          `json:"canonical_instruction_ids"`
	RetirementEventIDs      []uint64          `json:"retirement_event_ids"`
	RetirementSeqs          []uint64          `json:"retirement_seqs"`
	OperandBusIDs           []uint64          `json:"operand_bus_ids"`
	ArtifactDigests         map[string]string `json:"artifact_digests"`
}

type StackStateRegisters struct {
	A  uint16 `json:"a"`
	X  uint16 `json:"x"`
	Y  uint16 `json:"y"`
	S  uint16 `json:"s"`
	D  uint16 `json:"d"`
	DB uint8  `json:"db"`
	PB uint8  `json:"pb"`
	P  uint8  `json:"p"`
	E  bool   `json:"e"`
	PC uint16 `json:"pc"`
}

type StackMemoryWrite struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

type StackReceiptCaseResult struct {
	CaseID              string               `json:"case_id"`
	InputVal            uint8                `json:"input_val"`
	OutputVal           uint8                `json:"output_val"`
	Kind                string               `json:"kind"`
	ExpectedSuccessorPC string               `json:"expected_successor_pc"`
	EmuSuccessorPC      string               `json:"emu_successor_pc"`
	CSuccessorPC        string               `json:"c_successor_pc"`
	EmuP                string               `json:"emu_p"`
	CP                  string               `json:"c_p"`
	EmuMatchesC         bool                 `json:"emu_matches_c"`
	MatchesExpected     bool                 `json:"matches_expected"`
	EmuWrites           int                  `json:"emu_writes"`
	CWrites             int                  `json:"c_writes"`
	Writes              []StackMemoryWrite   `json:"writes"`
	TotalWrites         uint32               `json:"total_writes"`
	WriteOverflow       bool                 `json:"write_overflow"`
	MissingRead         bool                 `json:"missing_read"`
	MissingAddr         uint32               `json:"missing_addr,omitempty"`
	MMIOAccess          bool                 `json:"mmio_access"`
	MMIOAddr            uint32               `json:"mmio_addr,omitempty"`
	Discrepancy         string               `json:"discrepancy,omitempty"`
	Verified            bool                 `json:"verified"`
	EmuState            StackStateRegisters  `json:"emu_state"`
	CState              StackStateRegisters  `json:"c_state"`
}

type StackReceiptSummary struct {
	Status                string                   `json:"status"`
	StreamSHA256          string                   `json:"stream_sha256"`
	ROMSHA256             string                   `json:"rom_sha256"`
	BlockAddress          string                   `json:"block_address"`
	DispatchSeq           uint64                   `json:"dispatch_seq"`
	TargetSeq             uint64                   `json:"target_seq"`
	PhysicalReadAddress   string                   `json:"physical_read_address"`
	PhysicalWriteAddress  string                   `json:"physical_write_address"`
	RecordedReadValue     uint8                    `json:"recorded_read_value"`
	RecordedWriteValue    uint8                    `json:"recorded_write_value"`
	BaselineRawVerified   bool                     `json:"baseline_raw_verified"`
	DualBackendVerified   bool                     `json:"dual_backend_verified"`
	ExpectedMatchVerified bool                     `json:"expected_match_verified"`
	ThreeWritesVerified   bool                     `json:"three_writes_verified"`
	Results               []StackReceiptCaseResult `json:"results,omitempty"`
}

type StackStepRecordSummary struct {
	InputVal uint8                `json:"input_val"`
	EntryA   string               `json:"entry_a"`
	EntryS   string               `json:"entry_s"`
	EntryDB  string               `json:"entry_db"`
	EntryP   string               `json:"entry_p"`
	ExitA    string               `json:"exit_a"`
	ExitS    string               `json:"exit_s"`
	ExitDB   string               `json:"exit_db"`
	ExitP    string               `json:"exit_p"`
	ExitPC   string               `json:"exit_pc"`
	Reads    []StackMemoryWrite   `json:"reads,omitempty"`
	Writes   []StackMemoryWrite   `json:"writes,omitempty"`
	State    StackStateRegisters  `json:"state"`
	CState   StackStateRegisters  `json:"c_state"`
}

type StackTimelineStepSummary struct {
	StepIndex     int                    `json:"step_index"`
	Address       string                 `json:"address"`
	ROMOffset     string                 `json:"rom_offset"`
	Mnemonic      string                 `json:"mnemonic"`
	Recorded      StackStepRecordSummary `json:"recorded"`
	Prediction127 StackStepRecordSummary `json:"prediction_127"`
	Prediction255 StackStepRecordSummary `json:"prediction_255"`
}

type StackCaseSummary struct {
	CaseID              string                  `json:"case_id"`
	InputVal            uint8                   `json:"input_val"`
	Kind                string                  `json:"kind"`
	ExpectedOutput      uint8                   `json:"expected_output"`
	ExpectedSuccessorPC string                  `json:"expected_successor_pc"`
	ExpectedP           string                  `json:"expected_p"`
	ActualSuccessorPC   string                  `json:"actual_successor_pc,omitempty"`
	ActualP             string                  `json:"actual_p,omitempty"`
	WritesCount         int                     `json:"writes_count"`
	ActualResult        *StackReceiptCaseResult `json:"actual_result,omitempty"`
}

type StackComparisonCard struct {
	Status                  string                     `json:"status"` // "available" or "unavailable"
	Reason                  string                     `json:"reason,omitempty"`
	Qualification           string                     `json:"qualification,omitempty"`
	BlockAddress            string                     `json:"block_address,omitempty"`
	ScopeStop               string                     `json:"scope_stop,omitempty"`
	LogicalCounterAddress   string                     `json:"logical_counter_address,omitempty"`
	PhysicalCounterAddress  string                     `json:"physical_counter_address,omitempty"`
	StackPushPHBAddress     string                     `json:"stack_push_phb_address,omitempty"`
	StackPushPHKAddress     string                     `json:"stack_push_phk_address,omitempty"`
	BaselineInput           uint8                      `json:"baseline_input"`
	BaselineOutput          uint8                      `json:"baseline_output"`
	BaselineSuccessorPC     string                     `json:"baseline_successor_pc"`
	BaselineS               string                     `json:"baseline_s"`
	BaselineDB              string                     `json:"baseline_db"`
	BaselineP               string                     `json:"baseline_p"`
	BaselineWrites          int                        `json:"baseline_writes"`
	Cases                   []StackCaseSummary         `json:"cases,omitempty"`
	Timeline                []StackTimelineStepSummary `json:"timeline,omitempty"`
	ReceiptSummary          *StackReceiptSummary       `json:"receipt_summary,omitempty"`
	Manifest                *StackBundleManifest       `json:"manifest,omitempty"`
}

func (s *Server) LoadStackComparisonBundle() *StackComparisonCard {
	card := &StackComparisonCard{
		Status: "unavailable",
	}

	if s == nil || s.Document == nil || s.Occurrences == nil {
		card.Reason = "server, document, or occurrence index unavailable"
		return card
	}

	if s.ProjectDir == "" {
		card.Reason = "project directory not specified"
		return card
	}
	bundleDir := filepath.Join(s.ProjectDir, "evidence", "bundles", "stack_0cc404")
	fi, err := os.Stat(bundleDir)
	if err != nil || !fi.IsDir() {
		card.Reason = "project-local stack replay bundle not found at evidence/bundles/stack_0cc404"
		return card
	}

	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		card.Reason = fmt.Sprintf("read manifest.json: %v", err)
		return card
	}
	var manifest StackBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		card.Reason = fmt.Sprintf("decode manifest.json: %v", err)
		return card
	}

	activeStream := s.Occurrences.StreamSHA256
	activeROM := s.Document.ROM.NormalizedSHA256
	activeDoc := s.DocumentSHA256

	if activeStream != analysis.AdmittedStreamSHA256 || manifest.StreamSHA256 != analysis.AdmittedStreamSHA256 {
		card.Reason = fmt.Sprintf("stream SHA-256 not admitted: active=%s bundle=%s admitted=%s", activeStream, manifest.StreamSHA256, analysis.AdmittedStreamSHA256)
		return card
	}
	if activeROM != analysis.AdmittedROMSHA256 || manifest.ROMSHA256 != analysis.AdmittedROMSHA256 {
		card.Reason = fmt.Sprintf("ROM SHA-256 not admitted: active=%s bundle=%s admitted=%s", activeROM, manifest.ROMSHA256, analysis.AdmittedROMSHA256)
		return card
	}
	if activeDoc != AdmittedDocumentSHA256 || manifest.DocumentSHA256 != AdmittedDocumentSHA256 {
		card.Reason = fmt.Sprintf("document SHA-256 not admitted: active=%s bundle=%s admitted=%s", activeDoc, manifest.DocumentSHA256, AdmittedDocumentSHA256)
		return card
	}
	if manifest.BlockAddress != "$0CC404" {
		card.Reason = fmt.Sprintf("block address mismatch: %s", manifest.BlockAddress)
		return card
	}

	expectedInstIDs := []string{
		StackNode0CanonicalID,
		StackNode1CanonicalID,
		StackNode2CanonicalID,
		StackNode3CanonicalID,
	}
	if len(manifest.CanonicalInstructionIDs) != len(expectedInstIDs) {
		card.Reason = "manifest canonical instruction IDs count mismatch"
		return card
	}
	for i, id := range expectedInstIDs {
		if manifest.CanonicalInstructionIDs[i] != id {
			card.Reason = fmt.Sprintf("canonical instruction ID mismatch at %d: %s vs %s", i, manifest.CanonicalInstructionIDs[i], id)
			return card
		}
	}

	expectedRetIDs := []uint64{30003, 30006, 30009, 30015}
	if len(manifest.RetirementEventIDs) != len(expectedRetIDs) {
		card.Reason = "manifest retirement event IDs count mismatch"
		return card
	}
	for i, id := range expectedRetIDs {
		if manifest.RetirementEventIDs[i] != id {
			card.Reason = fmt.Sprintf("retirement event ID mismatch at %d: %d vs %d", i, manifest.RetirementEventIDs[i], id)
			return card
		}
	}

	expectedRetSeqs := []uint64{8512, 8513, 8514, 8515}
	if len(manifest.RetirementSeqs) != len(expectedRetSeqs) {
		card.Reason = "manifest retirement seqs count mismatch"
		return card
	}
	for i, seq := range expectedRetSeqs {
		if manifest.RetirementSeqs[i] != seq {
			card.Reason = fmt.Sprintf("retirement seq mismatch at %d: %d vs %d", i, manifest.RetirementSeqs[i], seq)
			return card
		}
	}

	expectedOperandIDs := []uint64{30002, 30005, 30008, 30013, 30014}
	if len(manifest.OperandBusIDs) != len(expectedOperandIDs) {
		card.Reason = "manifest operand bus IDs count mismatch"
		return card
	}
	for i, id := range expectedOperandIDs {
		if manifest.OperandBusIDs[i] != id {
			card.Reason = fmt.Sprintf("operand bus ID mismatch at %d: %d vs %d", i, manifest.OperandBusIDs[i], id)
			return card
		}
	}

	if genSHA, ok := manifest.ArtifactDigests["generated_c_sha256"]; !ok || genSHA != AcceptedStackGeneratedCSHA256 {
		card.Reason = fmt.Sprintf("manifest generated_c_sha256 mismatch: got=%s accepted=%s", genSHA, AcceptedStackGeneratedCSHA256)
		return card
	}
	genCPath := filepath.Join(bundleDir, "generated.c")
	if genCBytes, err := os.ReadFile(genCPath); err == nil {
		genCDigest := fmt.Sprintf("%x", sha256.Sum256(genCBytes))
		if genCDigest != AcceptedStackGeneratedCSHA256 {
			card.Reason = fmt.Sprintf("artifact generated.c digest not accepted: got=%s accepted=%s", genCDigest, AcceptedStackGeneratedCSHA256)
			return card
		}
	}

	// Query and validate 4 live canonical occurrences
	type canonicalStepSpec struct {
		instID        string
		addr          uint32
		expectedRetID uint64
		expectedSeq   uint64
		expectedEntry StackStateRegisters
		expectedExit  StackStateRegisters
	}
	canonicalSpecs := []canonicalStepSpec{
		{
			instID:        StackNode0CanonicalID,
			addr:          0x0CC404,
			expectedRetID: 30003,
			expectedSeq:   8512,
			expectedEntry: StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FC, D: 0, DB: 0x00, PB: 0x0C, PC: 0xC404, P: 0x32, E: false},
			expectedExit:  StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FB, D: 0, DB: 0x00, PB: 0x0C, PC: 0xC405, P: 0x32, E: false},
		},
		{
			instID:        StackNode1CanonicalID,
			addr:          0x0CC405,
			expectedRetID: 30006,
			expectedSeq:   8513,
			expectedEntry: StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FB, D: 0, DB: 0x00, PB: 0x0C, PC: 0xC405, P: 0x32, E: false},
			expectedExit:  StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FA, D: 0, DB: 0x00, PB: 0x0C, PC: 0xC406, P: 0x32, E: false},
		},
		{
			instID:        StackNode2CanonicalID,
			addr:          0x0CC406,
			expectedRetID: 30009,
			expectedSeq:   8514,
			expectedEntry: StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FA, D: 0, DB: 0x00, PB: 0x0C, PC: 0xC406, P: 0x32, E: false},
			expectedExit:  StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FB, D: 0, DB: 0x0C, PB: 0x0C, PC: 0xC407, P: 0x30, E: false},
		},
		{
			instID:        StackNode3CanonicalID,
			addr:          0x0CC407,
			expectedRetID: 30015,
			expectedSeq:   8515,
			expectedEntry: StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FB, D: 0, DB: 0x0C, PB: 0x0C, PC: 0xC407, P: 0x30, E: false},
			expectedExit:  StackStateRegisters{A: 0x0CC4, X: 0x00E0, Y: 0x0000, S: 0x01FB, D: 0, DB: 0x0C, PB: 0x0C, PC: 0xC40A, P: 0x30, E: false},
		},
	}

	for i, spec := range canonicalSpecs {
		occRep := s.Occurrences.Lookup(0, spec.instID, spec.addr)
		if occRep == nil || occRep.Status != "available" {
			card.Reason = fmt.Sprintf("canonical step %d (%s) occurrence unavailable", i, spec.instID)
			return card
		}
		if occRep.RetirementID != spec.expectedRetID {
			card.Reason = fmt.Sprintf("canonical step %d retirement ID mismatch: got %d, want %d", i, occRep.RetirementID, spec.expectedRetID)
			return card
		}
		if occRep.Seq != spec.expectedSeq {
			card.Reason = fmt.Sprintf("canonical step %d retirement seq mismatch: got %d, want %d", i, occRep.Seq, spec.expectedSeq)
			return card
		}
		if occRep.TraceFrame == nil || *occRep.TraceFrame != 0 {
			card.Reason = fmt.Sprintf("canonical step %d trace frame mismatch", i)
			return card
		}
		if occRep.PPUFrame != nil && *occRep.PPUFrame != 0 && *occRep.PPUFrame != 332 {
			card.Reason = fmt.Sprintf("canonical step %d PPU frame mismatch", i)
			return card
		}
		if !matchNoncycle(occRep.Entry, spec.expectedEntry) {
			card.Reason = fmt.Sprintf("canonical step %d entry noncycle state mismatch: got %+v, want %+v", i, occRep.Entry, spec.expectedEntry)
			return card
		}
		if !matchNoncycle(occRep.Exit, spec.expectedExit) {
			card.Reason = fmt.Sprintf("canonical step %d exit noncycle state mismatch: got %+v, want %+v", i, occRep.Exit, spec.expectedExit)
			return card
		}
	}

	// Validate the 5 raw physical accesses from live WRAM index
	findWRAMEvent := func(addr uint32, id uint64) (trace.Event, bool) {
		for _, ev := range s.Occurrences.GetWRAMAccesses(addr) {
			if ev.ID == id {
				return ev, true
			}
		}
		return trace.Event{}, false
	}

	ev30002, ok2 := findWRAMEvent(0x7E01FC, 30002)
	ev30005, ok5 := findWRAMEvent(0x7E01FB, 30005)
	ev30008, ok8 := findWRAMEvent(0x7E01FB, 30008)
	ev30013, ok13 := findWRAMEvent(0x7E1E0A, 30013)
	ev30014, ok14 := findWRAMEvent(0x7E1E0A, 30014)

	if !ok2 || !ok5 || !ok8 || !ok13 || !ok14 {
		card.Reason = "missing raw WRAM access events in occurrence index"
		return card
	}

	val30002 := ev30002.Value
	if ev30002.After != nil {
		val30002 = *ev30002.After
	}
	if ev30002.Op != "write" || (ev30002.Width != 0 && ev30002.Width != 1) || val30002 != 0 {
		card.Reason = fmt.Sprintf("raw access 30002 mismatch: op=%s width=%d val=%d", ev30002.Op, ev30002.Width, val30002)
		return card
	}

	if ev30005.Op != "write" || (ev30005.Width != 0 && ev30005.Width != 1) || ev30005.Value != 12 {
		card.Reason = fmt.Sprintf("raw access 30005 mismatch: op=%s width=%d val=%d", ev30005.Op, ev30005.Width, ev30005.Value)
		return card
	}

	if ev30008.Op != "read" || (ev30008.Width != 0 && ev30008.Width != 1) || ev30008.Value != 12 {
		card.Reason = fmt.Sprintf("raw access 30008 mismatch: op=%s width=%d val=%d", ev30008.Op, ev30008.Width, ev30008.Value)
		return card
	}

	if ev30013.Op != "read" || (ev30013.Width != 0 && ev30013.Width != 1) || ev30013.Value != 54 {
		card.Reason = fmt.Sprintf("raw access 30013 mismatch: op=%s width=%d val=%d", ev30013.Op, ev30013.Width, ev30013.Value)
		return card
	}

	val30014 := ev30014.Value
	if ev30014.After != nil {
		val30014 = *ev30014.After
	}
	if ev30014.Op != "write" || (ev30014.Width != 0 && ev30014.Width != 1) || val30014 != 55 {
		card.Reason = fmt.Sprintf("raw access 30014 mismatch: op=%s width=%d val=%d", ev30014.Op, ev30014.Width, val30014)
		return card
	}

	if !(ev30002.ID < ev30005.ID && ev30005.ID < ev30008.ID && ev30008.ID < ev30013.ID && ev30013.ID < ev30014.ID) {
		card.Reason = "raw physical access events not in strict chronological order"
		return card
	}
	if !(ev30002.ID < 30003 && 30003 < ev30005.ID && ev30005.ID < 30006 &&
		30006 < ev30008.ID && ev30008.ID < 30009 &&
		30009 < ev30013.ID && ev30014.ID < 30015) {
		card.Reason = "raw physical access events not bounded by owning retirement intervals"
		return card
	}

	requiredArtifacts := []struct {
		name string
		key  string
	}{
		{"case.json", "case_sha256"},
		{"timeline.json", "timeline_sha256"},
		{"receipt.json", "receipt_sha256"},
	}

	acceptedDigests := map[string]string{
		"case.json":     AcceptedStackCaseSHA256,
		"timeline.json": AcceptedStackTimelineSHA256,
		"receipt.json":  AcceptedStackReceiptSHA256,
	}

	if expGenC, ok := manifest.ArtifactDigests["generated_c_sha256"]; ok && expGenC != AcceptedStackGeneratedCSHA256 {
		card.Reason = fmt.Sprintf("manifest generated_c_sha256 not accepted: got=%s accepted=%s", expGenC, AcceptedStackGeneratedCSHA256)
		return card
	}

	artifactData := make(map[string][]byte)
	for _, art := range requiredArtifacts {
		p := filepath.Join(bundleDir, art.name)
		b, err := os.ReadFile(p)
		if err != nil {
			card.Reason = fmt.Sprintf("missing artifact %s: %v", art.name, err)
			return card
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(b))
		expectedDigest := manifest.ArtifactDigests[art.key]
		if digest != expectedDigest {
			card.Reason = fmt.Sprintf("artifact %s digest mismatch: manifest=%s computed=%s", art.name, expectedDigest, digest)
			return card
		}
		acceptedDigest, ok := acceptedDigests[art.name]
		if !ok || digest != acceptedDigest {
			card.Reason = fmt.Sprintf("artifact %s digest not accepted: got=%s accepted=%s", art.name, digest, acceptedDigest)
			return card
		}
		artifactData[art.name] = b
	}

	if genCBytes, err := os.ReadFile(filepath.Join(bundleDir, "generated.c")); err == nil {
		digest := fmt.Sprintf("%x", sha256.Sum256(genCBytes))
		if digest != AcceptedStackGeneratedCSHA256 {
			card.Reason = fmt.Sprintf("artifact generated.c digest not accepted: got=%s accepted=%s", digest, AcceptedStackGeneratedCSHA256)
			return card
		}
		if expectedDigest, ok := manifest.ArtifactDigests["generated_c_sha256"]; ok && digest != expectedDigest {
			card.Reason = fmt.Sprintf("artifact generated.c digest mismatch: manifest=%s computed=%s", expectedDigest, digest)
			return card
		}
	}

	var rawReceipt StackReceiptSummary
	if err := json.Unmarshal(artifactData["receipt.json"], &rawReceipt); err != nil {
		card.Reason = fmt.Sprintf("decode receipt.json: %v", err)
		return card
	}

	if !rawReceipt.DualBackendVerified {
		card.Reason = "receipt dual-backend verification failed"
		return card
	}
	if !rawReceipt.BaselineRawVerified {
		card.Reason = "receipt baseline raw capture verification failed"
		return card
	}
	if !rawReceipt.ExpectedMatchVerified {
		card.Reason = "receipt expected match verification failed"
		return card
	}
	if !rawReceipt.ThreeWritesVerified {
		card.Reason = "receipt three writes verification failed"
		return card
	}

	type RawCaseSpec struct {
		CaseID              string `json:"case_id"`
		InputVal            uint8  `json:"input_val"`
		Kind                string `json:"kind"`
		ExpectedOutput      uint8  `json:"expected_output"`
		ExpectedSuccessorPC string `json:"expected_successor_pc"`
		ExpectedP           string `json:"expected_p"`
		WantNextPC          uint32 `json:"want_next_pc"`
		WantP               uint8  `json:"want_p"`
	}
	type RawCaseJSON struct {
		Cases []RawCaseSpec `json:"cases"`
	}
	var rawCase RawCaseJSON
	if err := json.Unmarshal(artifactData["case.json"], &rawCase); err != nil {
		card.Reason = fmt.Sprintf("decode case.json: %v", err)
		return card
	}

	receiptResultByID := make(map[string]StackReceiptCaseResult)
	for _, res := range rawReceipt.Results {
		receiptResultByID[res.CaseID] = res
	}

	var cases []StackCaseSummary
	for _, spec := range rawCase.Cases {
		res, hasRes := receiptResultByID[spec.CaseID]
		cSummary := StackCaseSummary{
			CaseID:              spec.CaseID,
			InputVal:            spec.InputVal,
			Kind:                spec.Kind,
			ExpectedOutput:      spec.ExpectedOutput,
			ExpectedSuccessorPC: spec.ExpectedSuccessorPC,
			ExpectedP:           spec.ExpectedP,
		}
		if hasRes {
			cSummary.ActualSuccessorPC = res.EmuSuccessorPC
			cSummary.ActualP = res.EmuP
			cSummary.WritesCount = res.EmuWrites
			resCopy := res
			cSummary.ActualResult = &resCopy

			// Validate 3 writes: 0x7E01FC=00, 0x7E01FB=0C, 0x7E1E0A=output
			if len(res.Writes) != 3 {
				card.Reason = fmt.Sprintf("case %s writes count mismatch: %d != 3", spec.CaseID, len(res.Writes))
				return card
			}
			if res.Writes[0].Address != 0x7E01FC || res.Writes[0].Value != 0x00 {
				card.Reason = fmt.Sprintf("case %s write 0 mismatch: addr=%06X val=%02X", spec.CaseID, res.Writes[0].Address, res.Writes[0].Value)
				return card
			}
			if res.Writes[1].Address != 0x7E01FB || res.Writes[1].Value != 0x0C {
				card.Reason = fmt.Sprintf("case %s write 1 mismatch: addr=%06X val=%02X", spec.CaseID, res.Writes[1].Address, res.Writes[1].Value)
				return card
			}
			if res.Writes[2].Address != 0x7E1E0A || res.Writes[2].Value != spec.ExpectedOutput {
				card.Reason = fmt.Sprintf("case %s write 2 mismatch: addr=%06X val=%02X expected=%02X", spec.CaseID, res.Writes[2].Address, res.Writes[2].Value, spec.ExpectedOutput)
				return card
			}
			// Validate DB transition to 0x0C and next PC 0x0CC40A
			if res.EmuState.DB != 0x0C || res.EmuState.PC != 0xC40A {
				card.Reason = fmt.Sprintf("case %s state mismatch: db=%02X pc=%04X", spec.CaseID, res.EmuState.DB, res.EmuState.PC)
				return card
			}
		}
		cases = append(cases, cSummary)
	}

	type RawTimelineStep struct {
		StepIndex   int                      `json:"step_index"`
		Address     string                   `json:"address"`
		ROMOffset   string                   `json:"rom_offset"`
		Mnemonic    string                   `json:"mnemonic"`
		Recorded    StackStepRecordSummary   `json:"recorded"`
		Predictions []StackStepRecordSummary `json:"predictions"`
	}
	var rawTimeline []RawTimelineStep
	if err := json.Unmarshal(artifactData["timeline.json"], &rawTimeline); err != nil {
		card.Reason = fmt.Sprintf("decode timeline.json: %v", err)
		return card
	}

	var timeline []StackTimelineStepSummary
	for _, step := range rawTimeline {
		tStep := StackTimelineStepSummary{
			StepIndex: step.StepIndex,
			Address:   step.Address,
			ROMOffset: step.ROMOffset,
			Mnemonic:  step.Mnemonic,
			Recorded:  step.Recorded,
		}
		for _, pred := range step.Predictions {
			if pred.InputVal == 127 {
				tStep.Prediction127 = pred
			} else if pred.InputVal == 255 {
				tStep.Prediction255 = pred
			}
		}
		timeline = append(timeline, tStep)
	}

	card.Status = "available"
	card.Reason = ""
	card.Qualification = manifest.Qualification
	card.BlockAddress = manifest.BlockAddress
	card.ScopeStop = "$0CC40A before JSR"
	card.LogicalCounterAddress = "$0C1E0A"
	card.PhysicalCounterAddress = "$7E1E0A"
	card.StackPushPHBAddress = "$7E01FC"
	card.StackPushPHKAddress = "$7E01FB"
	card.BaselineInput = rawReceipt.RecordedReadValue
	card.BaselineOutput = rawReceipt.RecordedWriteValue

	var baselineResult *StackReceiptCaseResult
	for _, res := range rawReceipt.Results {
		if res.Kind == "recorded" || res.CaseID == "baseline_recorded" || res.CaseID == "baseline_54" {
			resCopy := res
			baselineResult = &resCopy
			break
		}
	}
	if baselineResult != nil {
		card.BaselineSuccessorPC = baselineResult.EmuSuccessorPC
		card.BaselineS = fmt.Sprintf("$%04X", baselineResult.EmuState.S)
		card.BaselineDB = fmt.Sprintf("$%02X", baselineResult.EmuState.DB)
		if baselineResult.EmuP != "" {
			card.BaselineP = baselineResult.EmuP
		} else {
			card.BaselineP = fmt.Sprintf("$%02X", baselineResult.EmuState.P)
		}
		card.BaselineWrites = baselineResult.EmuWrites
	} else {
		card.BaselineSuccessorPC = "$0CC40A"
		card.BaselineS = "$01FB"
		card.BaselineDB = "$0C"
		card.BaselineP = "$30"
		card.BaselineWrites = 3
	}

	card.Cases = cases
	card.Timeline = timeline
	card.ReceiptSummary = &rawReceipt
	card.Manifest = &manifest

	return card
}

func matchNoncycle(r OccurrenceRegisters, exp StackStateRegisters) bool {
	return r.A == exp.A &&
		r.X == exp.X &&
		r.Y == exp.Y &&
		r.S == exp.S &&
		r.D == exp.D &&
		r.DB == exp.DB &&
		r.PB == exp.PB &&
		r.PC == exp.PC &&
		r.P == exp.P &&
		r.E == exp.E
}
