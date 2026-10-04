package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	AcceptedStackCaseSHA256     = "86e36332ec6df1b54544dfba6ba6df79c2002e89ce57f6bc1ca9d99a005c0081"
	AcceptedStackTimelineSHA256 = "a84bf947f12b40d34fca91e9a8be38e406e89c3722357f8f2932c3690b872e72"
	AcceptedStackReceiptSHA256  = "933f3f9a0ea7b6ab78e801fbfafaab2c2b521dc887b58622433dbf34220c33ea"

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
	Writes   []StackMemoryWrite   `json:"writes,omitempty"`
	State    StackStateRegisters  `json:"state"`
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
	card.BaselineSuccessorPC = "$0CC40A"
	card.BaselineS = "$01FB"
	card.BaselineDB = "$0C"
	card.BaselineP = "$30"
	card.BaselineWrites = 3
	card.Cases = cases
	card.Timeline = timeline
	card.ReceiptSummary = &rawReceipt
	card.Manifest = &manifest

	return card
}
