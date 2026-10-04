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
	ExpectedBranchNode1RetirementID = 29900
	ExpectedBranchNode2RetirementID = 29903
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
	InputVal uint8  `json:"input_val"`
	EntryA   string `json:"entry_a"`
	EntryP   string `json:"entry_p"`
	ExitA    string `json:"exit_a"`
	ExitP    string `json:"exit_p"`
	ExitPC   string `json:"exit_pc"`
	CarrySet bool   `json:"carry_set"`
}

type BranchTimelineStepSummary struct {
	StepIndex      int                      `json:"step_index"`
	Address        string                   `json:"address"`
	ROMOffset      string                   `json:"rom_offset"`
	Mnemonic       string                   `json:"mnemonic"`
	RecordedInput  uint8                    `json:"recorded_input"`
	RecordedEntryA string                   `json:"recorded_entry_a"`
	RecordedEntryP string                   `json:"recorded_entry_p"`
	RecordedExitA  string                   `json:"recorded_exit_a"`
	RecordedExitP  string                   `json:"recorded_exit_p"`
	RecordedExitPC string                   `json:"recorded_exit_pc"`
	RecordedCarry  bool                     `json:"recorded_carry"`
	Prediction7    BranchPredictStepSummary `json:"prediction_7"`
	Prediction8    BranchPredictStepSummary `json:"prediction_8"`
	Prediction9    BranchPredictStepSummary `json:"prediction_9"`
}

type BranchCaseSummary struct {
	CaseID              string                   `json:"case_id"`
	InputVal            uint8                    `json:"input_val"`
	Kind                string                   `json:"kind"`
	ExpectedSuccessorPC string                   `json:"expected_successor_pc"`
	BranchTaken         bool                     `json:"branch_taken"`
	CarrySet            bool                     `json:"carry_set"`
	ActualSuccessorPC   string                   `json:"actual_successor_pc,omitempty"`
	WritesCount         int                      `json:"writes_count"`
	ActualResult        *BranchReceiptCaseResult `json:"actual_result,omitempty"`
}

type BranchComparisonCard struct {
	Status         string                      `json:"status"` // "available" or "unavailable"
	Reason         string                      `json:"reason,omitempty"`
	Qualification  string                      `json:"qualification,omitempty"`
	BlockAddress   string                      `json:"block_address,omitempty"`
	BaselineInput  uint8                       `json:"baseline_input"`
	BaselinePC     string                      `json:"baseline_successor_pc"`
	BaselineTaken  bool                        `json:"baseline_branch_taken"`
	Cases          []BranchCaseSummary         `json:"cases,omitempty"`
	Timeline       []BranchTimelineStepSummary `json:"timeline,omitempty"`
	ReceiptSummary *BranchReceiptSummary       `json:"receipt_summary,omitempty"`
	Manifest       *BranchBundleManifest       `json:"manifest,omitempty"`
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

	// 2. Read and parse manifest.json
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

	// 3. Bind manifest content digests and identities to active server state
	activeStream := s.Occurrences.StreamSHA256
	activeROM := s.Document.ROM.NormalizedSHA256
	activeDoc := s.DocumentSHA256

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

	// 5. Unmarshal and verify receipt.json contents
	var rawReceipt struct {
		Status              string                    `json:"status"`
		StreamSHA256        string                    `json:"stream_sha256"`
		ROMSHA256           string                    `json:"rom_sha256"`
		BlockAddress        string                    `json:"block_address"`
		DispatchSeq         uint64                    `json:"dispatch_seq"`
		TargetSeq           uint64                    `json:"target_seq"`
		BaselineRawVerified bool                      `json:"baseline_raw_verified"`
		DualBackendVerified bool                      `json:"dual_backend_verified"`
		ZeroWritesVerified  bool                      `json:"zero_writes_verified"`
		Results             []BranchReceiptCaseResult `json:"results"`
	}
	if err := json.Unmarshal(artifactData["receipt.json"], &rawReceipt); err != nil {
		card.Reason = fmt.Sprintf("decode receipt.json: %v", err)
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
	for _, res := range rawReceipt.Results {
		if !res.Verified || !res.EmuMatchesC || !res.MatchesExpected || res.EmuWrites != 0 || res.CWrites != 0 {
			card.Reason = fmt.Sprintf("case %s not verified in receipt (verified=%v emuMatchesC=%v writes=%d/%d)",
				res.CaseID, res.Verified, res.EmuMatchesC, res.EmuWrites, res.CWrites)
			return card
		}
	}

	// 6. Unmarshal timeline.json and extract step comparisons
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

	var timelineSteps []BranchTimelineStepSummary
	for _, st := range rawTimeline {
		var pred7, pred8, pred9 BranchPredictStepSummary
		for _, p := range st.Predictions {
			carry := (p.State.P & 0x01) != 0
			s := BranchPredictStepSummary{
				InputVal: p.InputVal,
				EntryA:   p.EntryA,
				EntryP:   p.EntryP,
				ExitA:    p.ExitA,
				ExitP:    p.ExitP,
				ExitPC:   p.ExitPC,
				CarrySet: carry,
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

		recCarry := (st.Recorded.State.P & 0x01) != 0

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
			Prediction7:    pred7,
			Prediction8:    pred8,
			Prediction9:    pred9,
		})
	}

	// 7. Unmarshal case.json
	var rawCaseData struct {
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

	receiptByCaseID := make(map[string]BranchReceiptCaseResult)
	for _, res := range rawReceipt.Results {
		receiptByCaseID[res.CaseID] = res
	}

	var cases []BranchCaseSummary
	for _, c := range rawCaseData.Cases {
		res, ok := receiptByCaseID[c.CaseID]
		var carrySet bool
		var branchTaken bool
		var writesCount int
		actualSuccessorPC := ""
		var resPtr *BranchReceiptCaseResult
		if ok {
			resCopy := res
			resPtr = &resCopy
			carrySet = (res.EmuState.P & 0x01) != 0
			actualSuccessorPC = res.EmuSuccessorPC
			writesCount = res.EmuWrites
			// BCC branches when carry is clear
			branchTaken = !carrySet
		}

		cases = append(cases, BranchCaseSummary{
			CaseID:              c.CaseID,
			InputVal:            c.InputVal,
			Kind:                c.Kind,
			ExpectedSuccessorPC: c.ExpectedSuccessorPC,
			BranchTaken:         branchTaken,
			CarrySet:            carrySet,
			ActualSuccessorPC:   actualSuccessorPC,
			WritesCount:         writesCount,
			ActualResult:        resPtr,
		})
	}

	// All checks strictly verified
	card.Status = "available"
	card.Reason = ""
	card.Qualification = manifest.Qualification
	card.BlockAddress = manifest.BlockAddress
	for _, res := range rawReceipt.Results {
		if res.CaseID == "baseline_3" || res.InputWRAM11 == 3 {
			card.BaselineInput = res.InputWRAM11
			card.BaselinePC = res.EmuSuccessorPC
			card.BaselineTaken = (res.EmuState.P & 0x01) == 0
			break
		}
	}
	card.Cases = cases
	card.Timeline = timelineSteps
	card.ReceiptSummary = &BranchReceiptSummary{
		DualBackendVerified: rawReceipt.DualBackendVerified,
		BaselineRawVerified: rawReceipt.BaselineRawVerified,
		ZeroWritesVerified:  rawReceipt.ZeroWritesVerified,
		Results:             rawReceipt.Results,
	}
	card.Manifest = &manifest

	return card
}
