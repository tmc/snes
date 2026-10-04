package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	AcceptedBranchCaseSHA256     = "fd5e7b3fad28fd5a48d38407f8a371997fb55ddc806a4ba69536665fb5520a71"
	AcceptedBranchTimelineSHA256 = "21ff00a64865a77fc496e897d5a84ea80f07464844c03b356d2aebb963ce3d77"
	AcceptedBranchReceiptSHA256  = "dd8e181478c1415b9bbb3d02d2080f1d0256d33ca79969943656e8bb773db951"

	BranchNode0CanonicalID = "95400ac30236ab3a1727d8364eb2ed488051893b366576bb2b26f93b8bc11ff1" // $0CC120 LDA $11
	BranchNode1CanonicalID = "4375dc20216ba6eb9e6174673cf64a484aca623ea6bf7a87b387734d1f8dec29" // $0CC122 CMP #$08
	BranchNode2CanonicalID = "c6632c1a5d92cb20ad50631c6c223e081332fbcd82bf8e61094c9929a4d99c00" // $0CC124 BCC $C133

	ExpectedBranchNode0RetirementID = 29897
	ExpectedBranchNode0Seq          = 8487
	ExpectedBranchNode1RetirementID = 29900
	ExpectedBranchNode1Seq          = 8488
	ExpectedBranchNode2RetirementID = 29903
	ExpectedBranchNode2Seq          = 8489
	ExpectedBranchBusID             = 29896
)

type BranchBundleManifest struct {
	BundleID                string            `json:"bundle_id"`
	BlockAddress            string            `json:"block_address"`
	Qualification           string            `json:"qualification"`
	StreamSHA256            string            `json:"stream_sha256"`
	ROMSHA256               string            `json:"rom_sha256"`
	DocumentSHA256          string            `json:"document_sha256"`
	CanonicalInstructionIDs []string          `json:"canonical_instruction_ids"`
	RetirementEventIDs      []uint64          `json:"retirement_event_ids"`
	RetirementSeqs          []uint64          `json:"retirement_seqs"`
	OperandBusIDs           []uint64          `json:"operand_bus_ids,omitempty"`
	ArtifactDigests         map[string]string `json:"artifact_digests"`
}

type BranchStateRegisters struct {
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

type BranchReceiptCaseResult struct {
	CaseID              string               `json:"case_id"`
	InputWRAM11         uint8                `json:"input_wram_11"`
	Kind                string               `json:"kind"`
	ExpectedSuccessorPC string               `json:"expected_successor_pc"`
	EmuSuccessorPC      string               `json:"emu_successor_pc"`
	CSuccessorPC        string               `json:"c_successor_pc"`
	EmuWrites           int                  `json:"emu_writes"`
	CWrites             int                  `json:"c_writes"`
	EmuMatchesC         bool                 `json:"emu_matches_c"`
	MatchesExpected     bool                 `json:"matches_expected"`
	Verified            bool                 `json:"verified"`
	EmuState            BranchStateRegisters `json:"emu_state"`
	CState              BranchStateRegisters `json:"c_state"`
}

type BranchReceiptSummary struct {
	DualBackendVerified bool                      `json:"dual_backend_verified"`
	BaselineRawVerified bool                      `json:"baseline_raw_verified"`
	ZeroWritesVerified  bool                      `json:"zero_writes_verified"`
	Results             []BranchReceiptCaseResult `json:"results,omitempty"`
}

type BranchPredictStepSummary struct {
	InputVal    uint8  `json:"input_val"`
	EntryA      string `json:"entry_a"`
	EntryP      string `json:"entry_p"`
	ExitA       string `json:"exit_a"`
	ExitP       string `json:"exit_p"`
	ExitPC      string `json:"exit_pc"`
	CarrySet    bool   `json:"carry_set"`
	ZeroSet     bool   `json:"zero_set"`
	NegativeSet bool   `json:"negative_set"`
	FlagSummary string `json:"flag_summary"`
	Action      string `json:"action"`
	Writes      int    `json:"writes"`
	Annotation  string `json:"annotation"`
}

type BranchTimelineStepSummary struct {
	StepIndex       int                      `json:"step_index"`
	Address         string                   `json:"address"`
	ROMOffset       string                   `json:"rom_offset"`
	Mnemonic        string                   `json:"mnemonic"`
	RecordedInput   uint8                    `json:"recorded_input"`
	RecordedEntryA  string                   `json:"recorded_entry_a"`
	RecordedEntryP  string                   `json:"recorded_entry_p"`
	RecordedExitA   string                   `json:"recorded_exit_a"`
	RecordedExitP   string                   `json:"recorded_exit_p"`
	RecordedExitPC  string                   `json:"recorded_exit_pc"`
	RecordedCarry   bool                     `json:"recorded_carry"`
	RecordedZero    bool                     `json:"recorded_zero"`
	RecordedNeg     bool                     `json:"recorded_neg"`
	RecordedFlags   string                   `json:"recorded_flags"`
	RecordedAction  string                   `json:"recorded_action"`
	RecordedWrites  int                      `json:"recorded_writes"`
	RecordedNote    string                   `json:"recorded_annotation"`
	Prediction7     BranchPredictStepSummary `json:"prediction_7"`
	Prediction8     BranchPredictStepSummary `json:"prediction_8"`
	Prediction9     BranchPredictStepSummary `json:"prediction_9"`
}

type BranchCaseSummary struct {
	CaseID              string                   `json:"case_id"`
	InputVal            uint8                    `json:"input_val"`
	Kind                string                   `json:"kind"`
	ExpectedSuccessorPC string                   `json:"expected_successor_pc"`
	BranchTaken         bool                     `json:"branch_taken"`
	CarrySet            bool                     `json:"carry_set"`
	ZeroSet             bool                     `json:"zero_set"`
	NegativeSet         bool                     `json:"negative_set"`
	FlagSummary         string                   `json:"flag_summary"`
	BranchAction        string                   `json:"branch_action"`
	ActualSuccessorPC   string                   `json:"actual_successor_pc,omitempty"`
	WritesCount         int                      `json:"writes_count"`
	ActualResult        *BranchReceiptCaseResult `json:"actual_result,omitempty"`
}

type BranchComparisonCard struct {
	Status              string                      `json:"status"` // "available" or "unavailable"
	Reason              string                      `json:"reason,omitempty"`
	Qualification       string                      `json:"qualification,omitempty"`
	BlockAddress        string                      `json:"block_address,omitempty"`
	PhysicalReadAddress string                      `json:"physical_read_address,omitempty"`
	BaselineInput       uint8                       `json:"baseline_input"`
	BaselinePC          string                      `json:"baseline_successor_pc"`
	BaselineTaken       bool                        `json:"baseline_branch_taken"`
	BaselineCarry       bool                        `json:"baseline_carry"`
	BaselineFlags       string                      `json:"baseline_flags"`
	BaselineAction      string                      `json:"baseline_action"`
	BaselineWrites      int                         `json:"baseline_writes"`
	Cases               []BranchCaseSummary         `json:"cases,omitempty"`
	Timeline            []BranchTimelineStepSummary `json:"timeline,omitempty"`
	ReceiptSummary      *BranchReceiptSummary       `json:"receipt_summary,omitempty"`
	Manifest            *BranchBundleManifest       `json:"manifest,omitempty"`
}

func deriveFlagAnnotation(p uint8) (carrySet bool, zeroSet bool, negSet bool, flagStr string) {
	carrySet = (p & 0x01) != 0
	zeroSet = (p & 0x02) != 0
	negSet = (p & 0x80) != 0
	cVal := 0
	if carrySet {
		cVal = 1
	}
	flagStr = fmt.Sprintf("C=%d", cVal)
	if zeroSet {
		flagStr += ", Z=1"
	} else if negSet {
		flagStr += ", N=1"
	} else {
		flagStr += ", Z=0"
	}
	return
}

// matchOccurrenceRegisters compares all noncycle registers (A, X, Y, S, D, DB, PB, PC, P, E)
// between two OccurrenceRegisters.
func matchOccurrenceRegisters(r1, r2 OccurrenceRegisters) (bool, string) {
	if r1.A != r2.A {
		return false, fmt.Sprintf("A mismatch: %04X vs %04X", r1.A, r2.A)
	}
	if r1.X != r2.X {
		return false, fmt.Sprintf("X mismatch: %04X vs %04X", r1.X, r2.X)
	}
	if r1.Y != r2.Y {
		return false, fmt.Sprintf("Y mismatch: %04X vs %04X", r1.Y, r2.Y)
	}
	if r1.S != r2.S {
		return false, fmt.Sprintf("S mismatch: %04X vs %04X", r1.S, r2.S)
	}
	if r1.D != r2.D {
		return false, fmt.Sprintf("D mismatch: %04X vs %04X", r1.D, r2.D)
	}
	if r1.DB != r2.DB {
		return false, fmt.Sprintf("DB mismatch: %02X vs %02X", r1.DB, r2.DB)
	}
	if r1.PB != r2.PB {
		return false, fmt.Sprintf("PB mismatch: %02X vs %02X", r1.PB, r2.PB)
	}
	if r1.PC != r2.PC {
		return false, fmt.Sprintf("PC mismatch: %04X vs %04X", r1.PC, r2.PC)
	}
	if r1.P != r2.P {
		return false, fmt.Sprintf("P mismatch: %02X vs %02X", r1.P, r2.P)
	}
	if r1.E != r2.E {
		return false, fmt.Sprintf("E mismatch: %v vs %v", r1.E, r2.E)
	}
	return true, ""
}

// matchOccurrenceToBranchState compares all noncycle registers between an OccurrenceRegisters
// and a BranchStateRegisters.
func matchOccurrenceToBranchState(r OccurrenceRegisters, s BranchStateRegisters) (bool, string) {
	if r.A != s.A {
		return false, fmt.Sprintf("A mismatch: %04X vs %04X", r.A, s.A)
	}
	if r.X != s.X {
		return false, fmt.Sprintf("X mismatch: %04X vs %04X", r.X, s.X)
	}
	if r.Y != s.Y {
		return false, fmt.Sprintf("Y mismatch: %04X vs %04X", r.Y, s.Y)
	}
	if r.S != s.S {
		return false, fmt.Sprintf("S mismatch: %04X vs %04X", r.S, s.S)
	}
	if r.D != s.D {
		return false, fmt.Sprintf("D mismatch: %04X vs %04X", r.D, s.D)
	}
	if r.DB != s.DB {
		return false, fmt.Sprintf("DB mismatch: %02X vs %02X", r.DB, s.DB)
	}
	if r.PB != s.PB {
		return false, fmt.Sprintf("PB mismatch: %02X vs %02X", r.PB, s.PB)
	}
	if r.PC != s.PC {
		return false, fmt.Sprintf("PC mismatch: %04X vs %04X", r.PC, s.PC)
	}
	if r.P != s.P {
		return false, fmt.Sprintf("P mismatch: %02X vs %02X", r.P, s.P)
	}
	if r.E != s.E {
		return false, fmt.Sprintf("E mismatch: %v vs %v", r.E, s.E)
	}
	return true, ""
}

func (s *Server) LoadBranchComparisonBundle() *BranchComparisonCard {
	card := &BranchComparisonCard{
		Status: "unavailable",
	}

	if s == nil || s.Document == nil || s.Occurrences == nil {
		card.Reason = "server, document, or occurrence index unavailable"
		return card
	}

	// 1. Locate bundle directory strictly within ProjectDir
	if s.ProjectDir == "" {
		card.Reason = "project directory not specified"
		return card
	}
	bundleDir := filepath.Join(s.ProjectDir, "evidence", "bundles", "branch_0cc124")
	fi, err := os.Stat(bundleDir)
	if err != nil || !fi.IsDir() {
		card.Reason = "project-local branch comparison bundle not found at evidence/bundles/branch_0cc124"
		return card
	}

	// 2. Validate admitted active dataset identities against pinned constants
	activeStream := s.Occurrences.StreamSHA256
	activeROM := s.Document.ROM.NormalizedSHA256
	activeDoc := s.DocumentSHA256

	if activeStream != PinnedStreamSHA256 {
		card.Reason = fmt.Sprintf("unadmitted active stream SHA-256 %q, require pinned %q", activeStream, PinnedStreamSHA256)
		return card
	}
	if activeROM != PinnedROMSHA256 {
		card.Reason = fmt.Sprintf("unadmitted active ROM SHA-256 %q, require pinned %q", activeROM, PinnedROMSHA256)
		return card
	}
	if activeDoc != PinnedDocSHA256 {
		card.Reason = fmt.Sprintf("unadmitted active document SHA-256 %q, require pinned %q", activeDoc, PinnedDocSHA256)
		return card
	}

	// 3. Read and parse manifest.json
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		card.Reason = fmt.Sprintf("read manifest.json: %v", err)
		return card
	}
	var manifest BranchBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		card.Reason = fmt.Sprintf("decode manifest.json: %v", err)
		return card
	}

	if manifest.StreamSHA256 != activeStream {
		card.Reason = fmt.Sprintf("stream SHA-256 mismatch: bundle=%s active=%s", manifest.StreamSHA256, activeStream)
		return card
	}
	if manifest.ROMSHA256 != activeROM {
		card.Reason = fmt.Sprintf("ROM SHA-256 mismatch: bundle=%s active=%s", manifest.ROMSHA256, activeROM)
		return card
	}
	if manifest.DocumentSHA256 != activeDoc {
		card.Reason = fmt.Sprintf("document SHA-256 mismatch: bundle=%s active=%s", manifest.DocumentSHA256, activeDoc)
		return card
	}
	if manifest.BlockAddress != "$0CC120" {
		card.Reason = fmt.Sprintf("block address mismatch: %s", manifest.BlockAddress)
		return card
	}

	// Verify exact canonical instruction IDs
	expectedInstIDs := []string{
		BranchNode0CanonicalID,
		BranchNode1CanonicalID,
		BranchNode2CanonicalID,
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

	// Verify exact retirement IDs
	expectedRetIDs := []uint64{
		ExpectedBranchNode0RetirementID,
		ExpectedBranchNode1RetirementID,
		ExpectedBranchNode2RetirementID,
	}
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

	// Verify exact authentic retirement sequences (8487, 8488, 8489)
	expectedSeqs := []uint64{
		ExpectedBranchNode0Seq,
		ExpectedBranchNode1Seq,
		ExpectedBranchNode2Seq,
	}
	if len(manifest.RetirementSeqs) != len(expectedSeqs) {
		card.Reason = "manifest retirement seqs count mismatch"
		return card
	}
	for i, seq := range expectedSeqs {
		if manifest.RetirementSeqs[i] != seq {
			card.Reason = fmt.Sprintf("retirement seq mismatch at %d: %d vs %d", i, manifest.RetirementSeqs[i], seq)
			return card
		}
	}

	if len(manifest.OperandBusIDs) != 1 || manifest.OperandBusIDs[0] != ExpectedBranchBusID {
		card.Reason = fmt.Sprintf("manifest operand bus IDs mismatch: %v vs [%d]", manifest.OperandBusIDs, ExpectedBranchBusID)
		return card
	}

	// 4. Verify artifact content digests against manifest and accepted constants
	requiredArtifacts := []struct {
		name string
		key  string
	}{
		{"case.json", "case_sha256"},
		{"timeline.json", "timeline_sha256"},
		{"receipt.json", "receipt_sha256"},
	}

	acceptedDigests := map[string]string{
		"case.json":     AcceptedBranchCaseSHA256,
		"timeline.json": AcceptedBranchTimelineSHA256,
		"receipt.json":  AcceptedBranchReceiptSHA256,
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
			card.Reason = fmt.Sprintf("artifact digest tamper detected for %s: got %s, want %s", art.name, digest, expectedDigest)
			return card
		}
		if accepted, ok := acceptedDigests[art.name]; ok && digest != accepted {
			card.Reason = fmt.Sprintf("artifact %s content digest %s does not match accepted pin %s", art.name, digest, accepted)
			return card
		}
		artifactData[art.name] = b
	}

	// 5. Unmarshal and verify receipt.json contents against active stream/ROM and execution checks
	var rawReceipt struct {
		Status              string                    `json:"status"`
		StreamSHA256        string                    `json:"stream_sha256"`
		ROMSHA256           string                    `json:"rom_sha256"`
		BlockAddress        string                    `json:"block_address"`
		DispatchSeq         uint64                    `json:"dispatch_seq"`
		TargetSeq           uint64                    `json:"target_seq"`
		PhysicalReadAddress string                    `json:"physical_read_address"`
		RecordedReadValue   uint8                     `json:"recorded_read_value"`
		BaselineRawVerified bool                      `json:"baseline_raw_verified"`
		DualBackendVerified bool                      `json:"dual_backend_verified"`
		ZeroWritesVerified  bool                      `json:"zero_writes_verified"`
		Results             []BranchReceiptCaseResult `json:"results"`
	}
	if err := json.Unmarshal(artifactData["receipt.json"], &rawReceipt); err != nil {
		card.Reason = fmt.Sprintf("decode receipt.json: %v", err)
		return card
	}

	// Pinned receipt SHA checks: ensure active stream and ROM match receipt identities
	if rawReceipt.StreamSHA256 != activeStream {
		card.Reason = fmt.Sprintf("receipt stream SHA-256 mismatch against active: receipt=%s active=%s", rawReceipt.StreamSHA256, activeStream)
		return card
	}
	if rawReceipt.ROMSHA256 != activeROM {
		card.Reason = fmt.Sprintf("receipt ROM SHA-256 mismatch against active: receipt=%s active=%s", rawReceipt.ROMSHA256, activeROM)
		return card
	}
	if rawReceipt.BlockAddress != "$0CC120" {
		card.Reason = fmt.Sprintf("receipt block address mismatch: %s", rawReceipt.BlockAddress)
		return card
	}
	if rawReceipt.PhysicalReadAddress != "$000011" || rawReceipt.RecordedReadValue != 3 {
		card.Reason = fmt.Sprintf("receipt read address/value mismatch: addr=%s val=%d", rawReceipt.PhysicalReadAddress, rawReceipt.RecordedReadValue)
		return card
	}

	if rawReceipt.Status != "success" ||
		!rawReceipt.DualBackendVerified ||
		!rawReceipt.BaselineRawVerified ||
		!rawReceipt.ZeroWritesVerified {
		card.Reason = fmt.Sprintf("receipt verification failed: status=%s dual=%v raw=%v zero_writes=%v",
			rawReceipt.Status, rawReceipt.DualBackendVerified, rawReceipt.BaselineRawVerified,
			rawReceipt.ZeroWritesVerified)
		return card
	}
	if len(rawReceipt.Results) != 4 {
		card.Reason = fmt.Sprintf("receipt results count %d != 4", len(rawReceipt.Results))
		return card
	}
	receiptByCaseID := make(map[string]BranchReceiptCaseResult)
	for _, res := range rawReceipt.Results {
		if !res.Verified || !res.EmuMatchesC || !res.MatchesExpected || res.EmuWrites != 0 || res.CWrites != 0 {
			card.Reason = fmt.Sprintf("case %s not verified in receipt (verified=%v emuMatchesC=%v writes=%d/%d)",
				res.CaseID, res.Verified, res.EmuMatchesC, res.EmuWrites, res.CWrites)
			return card
		}
		receiptByCaseID[res.CaseID] = res
	}

	// 6. Query and validate actual live occurrences from s.Occurrences in trace frame 0
	rep0 := s.Occurrences.Lookup(0, BranchNode0CanonicalID, 0x0CC120)
	if rep0 == nil || rep0.Status != "available" {
		card.Reason = fmt.Sprintf("live occurrence unavailable for node 0 ($0CC120): %v", rep0)
		return card
	}
	rep1 := s.Occurrences.Lookup(0, BranchNode1CanonicalID, 0x0CC122)
	if rep1 == nil || rep1.Status != "available" {
		card.Reason = fmt.Sprintf("live occurrence unavailable for node 1 ($0CC122): %v", rep1)
		return card
	}
	rep2 := s.Occurrences.Lookup(0, BranchNode2CanonicalID, 0x0CC124)
	if rep2 == nil || rep2.Status != "available" {
		card.Reason = fmt.Sprintf("live occurrence unavailable for node 2 ($0CC124): %v", rep2)
		return card
	}

	// Check exact retirement and sequence IDs on live occurrences
	if rep0.RetirementID != ExpectedBranchNode0RetirementID || rep0.Seq != ExpectedBranchNode0Seq {
		card.Reason = fmt.Sprintf("node 0 live occurrence retirement/seq mismatch: got %d/%d, want %d/%d",
			rep0.RetirementID, rep0.Seq, ExpectedBranchNode0RetirementID, ExpectedBranchNode0Seq)
		return card
	}
	if rep1.RetirementID != ExpectedBranchNode1RetirementID || rep1.Seq != ExpectedBranchNode1Seq {
		card.Reason = fmt.Sprintf("node 1 live occurrence retirement/seq mismatch: got %d/%d, want %d/%d",
			rep1.RetirementID, rep1.Seq, ExpectedBranchNode1RetirementID, ExpectedBranchNode1Seq)
		return card
	}
	if rep2.RetirementID != ExpectedBranchNode2RetirementID || rep2.Seq != ExpectedBranchNode2Seq {
		card.Reason = fmt.Sprintf("node 2 live occurrence retirement/seq mismatch: got %d/%d, want %d/%d",
			rep2.RetirementID, rep2.Seq, ExpectedBranchNode2RetirementID, ExpectedBranchNode2Seq)
		return card
	}

	// Check live operand bus witness for Node 0 (physical read 29896=$000011 value 3 within LDA interval)
	if rep0.OperandBus == nil || rep0.OperandBus.ID != ExpectedBranchBusID || rep0.OperandBus.Address != 0x11 || rep0.OperandBus.Value != 3 {
		card.Reason = fmt.Sprintf("node 0 live operand bus mismatch: %+v (want ID %d, addr 0x11, val 3)", rep0.OperandBus, ExpectedBranchBusID)
		return card
	}
	if rep0.OperandBus.Cycle < rep0.Cycles.Entry || rep0.OperandBus.Cycle > rep0.Cycles.Exit {
		card.Reason = fmt.Sprintf("node 0 operand cycle %d not in LDA interval [%d, %d]", rep0.OperandBus.Cycle, rep0.Cycles.Entry, rep0.Cycles.Exit)
		return card
	}

	// Validate full noncycle register state continuity across the 3 instructions
	if ok, diff := matchOccurrenceRegisters(rep0.Exit, rep1.Entry); !ok {
		card.Reason = fmt.Sprintf("state continuity broken between node 0 and node 1: %s", diff)
		return card
	}
	if ok, diff := matchOccurrenceRegisters(rep1.Exit, rep2.Entry); !ok {
		card.Reason = fmt.Sprintf("state continuity broken between node 1 and node 2: %s", diff)
		return card
	}

	readAddrStr := rawReceipt.PhysicalReadAddress

	// 7. Unmarshal timeline.json and extract step comparisons with derived actual annotations
	var rawTimeline []struct {
		StepIndex int    `json:"step_index"`
		Address   string `json:"address"`
		Mnemonic  string `json:"mnemonic"`
		Recorded  struct {
			InputVal uint8                `json:"input_val"`
			EntryA   string               `json:"entry_a"`
			EntryP   string               `json:"entry_p"`
			ExitA    string               `json:"exit_a"`
			ExitP    string               `json:"exit_p"`
			ExitPC   string               `json:"exit_pc"`
			State    BranchStateRegisters `json:"state"`
		} `json:"recorded"`
		Predictions []struct {
			InputVal uint8                `json:"input_val"`
			EntryA   string               `json:"entry_a"`
			EntryP   string               `json:"entry_p"`
			ExitA    string               `json:"exit_a"`
			ExitP    string               `json:"exit_p"`
			ExitPC   string               `json:"exit_pc"`
			State    BranchStateRegisters `json:"state"`
		} `json:"predictions"`
	}
	if err := json.Unmarshal(artifactData["timeline.json"], &rawTimeline); err != nil {
		card.Reason = fmt.Sprintf("decode timeline.json: %v", err)
		return card
	}

	if len(rawTimeline) != 3 {
		card.Reason = fmt.Sprintf("timeline steps count %d != 3", len(rawTimeline))
		return card
	}

	baseResult := receiptByCaseID["baseline_3"]
	pred7Result := receiptByCaseID["prediction_7"]
	pred8Result := receiptByCaseID["prediction_8"]
	pred9Result := receiptByCaseID["prediction_9"]
	// Unmarshal case.json to access admitted initial_cpu_state and case definitions
	var rawCaseData struct {
		InitialCPUState BranchStateRegisters `json:"initial_cpu_state"`
		Cases []struct {
			CaseID              string `json:"case_id"`
			InputVal            uint8  `json:"input_val"`
			Kind                string `json:"kind"`
			ExpectedSuccessorPC string `json:"expected_successor_pc"`
			WantNextPC          uint32 `json:"want_next_pc"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(artifactData["case.json"], &rawCaseData); err != nil {
		card.Reason = fmt.Sprintf("decode case.json: %v", err)
		return card
	}

	// Join admitted live occurrence node 0 entry state to case.json initial_cpu_state for all ten noncycle registers
	if ok, diff := matchOccurrenceToBranchState(rep0.Entry, rawCaseData.InitialCPUState); !ok {
		card.Reason = fmt.Sprintf("initial cpu state mismatch against live occurrence node 0 entry: %s", diff)
		return card
	}

	// Join pinned recorded baseline execution exit state from receipt to live occurrence node 2 exit
	if ok, diff := matchOccurrenceToBranchState(rep2.Exit, baseResult.EmuState); !ok {
		card.Reason = fmt.Sprintf("baseline receipt emu_state mismatch against live occurrence node 2 exit: %s", diff)
		return card
	}
	if ok, diff := matchOccurrenceToBranchState(rep2.Exit, baseResult.CState); !ok {
		card.Reason = fmt.Sprintf("baseline receipt c_state mismatch against live occurrence node 2 exit: %s", diff)
		return card
	}

	// Join recorded timeline step states to admitted live occurrences
	// Step 0 entry state
	entryAVal, errA := strconv.ParseUint(strings.TrimPrefix(rawTimeline[0].Recorded.EntryA, "$"), 16, 16)
	entryPVal, errP := strconv.ParseUint(strings.TrimPrefix(rawTimeline[0].Recorded.EntryP, "$"), 16, 8)
	if errA != nil || errP != nil || rep0.Entry.A != uint16(entryAVal) || rep0.Entry.P != uint8(entryPVal) || rep0.Entry.PC != 0xC120 || rep0.Entry.PB != 0x0C {
		card.Reason = fmt.Sprintf("timeline step 0 entry mismatch against live occurrence node 0 entry: live(A=%04X,P=%02X,PC=%04X,PB=%02X) vs timeline(A=%s,P=%s,PC=C120,PB=0C)",
			rep0.Entry.A, rep0.Entry.P, rep0.Entry.PC, rep0.Entry.PB, rawTimeline[0].Recorded.EntryA, rawTimeline[0].Recorded.EntryP)
		return card
	}
	// Step 0 exit state
	if ok, diff := matchOccurrenceToBranchState(rep0.Exit, rawTimeline[0].Recorded.State); !ok {
		card.Reason = fmt.Sprintf("timeline step 0 recorded state mismatch against live occurrence node 0 exit: %s", diff)
		return card
	}
	// Step 1 exit state
	if ok, diff := matchOccurrenceToBranchState(rep1.Exit, rawTimeline[1].Recorded.State); !ok {
		card.Reason = fmt.Sprintf("timeline step 1 recorded state mismatch against live occurrence node 1 exit: %s", diff)
		return card
	}
	// Step 2 exit state
	if ok, diff := matchOccurrenceToBranchState(rep2.Exit, rawTimeline[2].Recorded.State); !ok {
		card.Reason = fmt.Sprintf("timeline step 2 recorded state mismatch against live occurrence node 2 exit: %s", diff)
		return card
	}

	var timelineSteps []BranchTimelineStepSummary
	for _, st := range rawTimeline {
		var pred7, pred8, pred9 BranchPredictStepSummary
		for _, p := range st.Predictions {
			carry, zero, neg, flags := deriveFlagAnnotation(p.State.P)
			action := "Taken"
			if carry {
				action = "Fallthrough"
			}
			caseID := fmt.Sprintf("prediction_%d", p.InputVal)
			predRes, ok := receiptByCaseID[caseID]
			writes := 0
			if ok {
				writes = predRes.EmuWrites
			}
			var annot string
			switch st.StepIndex {
			case 1: // LDA $11
				annot = fmt.Sprintf("Read %s=%d → A=%s (unrecorded)", readAddrStr, p.InputVal, p.ExitA)
			case 2: // CMP #$08
				annot = fmt.Sprintf("CMP #$08 → P=%s (%s)", p.ExitP, flags)
			case 3: // BCC $C133
				annot = fmt.Sprintf("%s → %s (%d writes)", action, p.ExitPC, writes)
			}

			s := BranchPredictStepSummary{
				InputVal:    p.InputVal,
				EntryA:      p.EntryA,
				EntryP:      p.EntryP,
				ExitA:       p.ExitA,
				ExitP:       p.ExitP,
				ExitPC:      p.ExitPC,
				CarrySet:    carry,
				ZeroSet:     zero,
				NegativeSet: neg,
				FlagSummary: flags,
				Action:      action,
				Writes:      writes,
				Annotation:  annot,
			}
			switch p.InputVal {
			case 7:
				pred7 = s
			case 8:
				pred8 = s
			case 9:
				pred9 = s
			}
		}

		addrVal, _ := strconv.ParseUint(strings.TrimPrefix(st.Address, "$"), 16, 32)
		physOff := ((addrVal >> 16) & 0x7F) << 15 | (addrVal & 0x7FFF)
		romOffStr := fmt.Sprintf("$%06X", physOff)

		recCarry, recZero, recNeg, recFlags := deriveFlagAnnotation(st.Recorded.State.P)
		recAction := "Taken"
		if recCarry {
			recAction = "Fallthrough"
		}
		recWrites := baseResult.EmuWrites
		var recAnnot string
		switch st.StepIndex {
		case 1: // LDA $11
			recAnnot = fmt.Sprintf("Read %s=%d → A=%s", readAddrStr, st.Recorded.InputVal, st.Recorded.ExitA)
		case 2: // CMP #$08
			recAnnot = fmt.Sprintf("CMP #$08 → P=%s (%s)", st.Recorded.ExitP, recFlags)
		case 3: // BCC $C133
			recAnnot = fmt.Sprintf("%s → %s (%d writes)", recAction, st.Recorded.ExitPC, recWrites)
		}

		timelineSteps = append(timelineSteps, BranchTimelineStepSummary{
			StepIndex:      st.StepIndex,
			Address:        st.Address,
			ROMOffset:      romOffStr,
			Mnemonic:       st.Mnemonic,
			RecordedInput:  st.Recorded.InputVal,
			RecordedEntryA: st.Recorded.EntryA,
			RecordedEntryP: st.Recorded.EntryP,
			RecordedExitA:  st.Recorded.ExitA,
			RecordedExitP:  st.Recorded.ExitP,
			RecordedExitPC: st.Recorded.ExitPC,
			RecordedCarry:  recCarry,
			RecordedZero:   recZero,
			RecordedNeg:    recNeg,
			RecordedFlags:  recFlags,
			RecordedAction: recAction,
			RecordedWrites: recWrites,
			RecordedNote:   recAnnot,
			Prediction7:    pred7,
			Prediction8:    pred8,
			Prediction9:    pred9,
		})
	}

	// 8. Derive case summaries from actual execution results
	var cases []BranchCaseSummary
	for _, c := range rawCaseData.Cases {
		res, ok := receiptByCaseID[c.CaseID]
		var carrySet, zeroSet, negSet bool
		var flagStr string
		var branchAction string
		var writesCount int
		actualSuccessorPC := ""
		var resPtr *BranchReceiptCaseResult
		if ok {
			resCopy := res
			resPtr = &resCopy
			carrySet, zeroSet, negSet, flagStr = deriveFlagAnnotation(res.EmuState.P)
			actualSuccessorPC = res.EmuSuccessorPC
			writesCount = res.EmuWrites
			if carrySet {
				branchAction = "FALLTHROUGH"
			} else {
				branchAction = "TAKEN"
			}
		}

		cases = append(cases, BranchCaseSummary{
			CaseID:              c.CaseID,
			InputVal:            c.InputVal,
			Kind:                c.Kind,
			ExpectedSuccessorPC: c.ExpectedSuccessorPC,
			BranchTaken:         !carrySet,
			CarrySet:            carrySet,
			ZeroSet:             zeroSet,
			NegativeSet:         negSet,
			FlagSummary:         flagStr,
			BranchAction:        branchAction,
			ActualSuccessorPC:   actualSuccessorPC,
			WritesCount:         writesCount,
			ActualResult:        resPtr,
		})
	}

	// All checks strictly verified
	baseCarry, _, _, baseFlags := deriveFlagAnnotation(baseResult.EmuState.P)
	baseAction := "TAKEN"
	if baseCarry {
		baseAction = "FALLTHROUGH"
	}

	card.Status = "available"
	card.Reason = ""
	card.Qualification = manifest.Qualification
	card.BlockAddress = manifest.BlockAddress
	card.PhysicalReadAddress = readAddrStr
	card.BaselineInput = baseResult.InputWRAM11
	card.BaselinePC = baseResult.EmuSuccessorPC
	card.BaselineTaken = !baseCarry
	card.BaselineCarry = baseCarry
	card.BaselineFlags = baseFlags
	card.BaselineAction = baseAction
	card.BaselineWrites = baseResult.EmuWrites
	card.Cases = cases
	card.Timeline = timelineSteps
	card.ReceiptSummary = &BranchReceiptSummary{
		DualBackendVerified: rawReceipt.DualBackendVerified,
		BaselineRawVerified: rawReceipt.BaselineRawVerified,
		ZeroWritesVerified:  rawReceipt.ZeroWritesVerified,
		Results:             rawReceipt.Results,
	}
	card.Manifest = &manifest

	_ = pred7Result
	_ = pred8Result
	_ = pred9Result

	return card
}
