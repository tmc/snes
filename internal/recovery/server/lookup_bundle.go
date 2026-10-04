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
	AcceptedCaseSHA256     = "a1ba4acf93973869b5e11c0df37dc49532f21dc917778a3b4d3b5b662d4e60aa"
	AcceptedTimelineSHA256 = "ca9236af75fc1c13018b500379ab0f80a2ee77691b4c5252b2d9a605dbf7eb93"
	AcceptedReceiptSHA256  = "7b275efd71e1e68c5426c985b076c765eae348ce8c1809ee1833ee91ac28db7b"
)

type LookupBundleManifest struct {
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

type LookupReceiptSummary struct {
	DualBackendVerified  bool `json:"dual_backend_verified"`
	BaselineRawVerified  bool `json:"baseline_raw_verified"`
	SingleWriteVerified  bool `json:"single_write_verified"`
	StepAccessesVerified bool `json:"step_accesses_verified"`
}

type LookupPredictSummary struct {
	InputVal         uint8  `json:"input_val"`
	EffectiveAddress string `json:"effective_address"`
	ROMOffset        string `json:"rom_offset,omitempty"`
	BusValue         uint8  `json:"bus_value"`
	BusOp            string `json:"bus_op"`
	ExitY            string `json:"exit_y"`
	ExitA            string `json:"exit_a"`
	ExitPC           string `json:"exit_pc"`
}

type LookupTimelineStepSummary struct {
	StepIndex       int                  `json:"step_index"`
	Address         string               `json:"address"`
	ROMOffset       string               `json:"rom_offset"`
	Mnemonic        string               `json:"mnemonic"`
	RecordedInput   uint8                `json:"recorded_input"`
	RecordedOperand uint64               `json:"recorded_operand"`
	RecordedEffAddr string               `json:"recorded_effective_address"`
	RecordedROMOff  string               `json:"recorded_rom_offset,omitempty"`
	RecordedBusVal  uint8                `json:"recorded_bus_value"`
	RecordedBusOp   string               `json:"recorded_bus_op"`
	RecordedEntryY  string               `json:"recorded_entry_y"`
	RecordedExitY   string               `json:"recorded_exit_y"`
	RecordedExitA   string               `json:"recorded_exit_a"`
	RecordedExitPC  string               `json:"recorded_exit_pc"`
	Prediction114   LookupPredictSummary `json:"prediction_114"`
	Prediction116   LookupPredictSummary `json:"prediction_116"`
}

type LookupCaseSummary struct {
	CaseID            string `json:"case_id"`
	InputVal          uint8  `json:"input_val"`
	Kind              string `json:"kind"`
	ExpectedFullA     string `json:"expected_full_a"`
	ExpectedY         uint16 `json:"expected_y"`
	ExpectedROMAddr   string `json:"expected_rom_addr"`
	ExpectedROMOff    string `json:"expected_rom_offset"`
	ExpectedROMVal    uint8  `json:"expected_rom_val"`
	ExpectedWriteAddr string `json:"expected_write_addr"`
	ExpectedWriteVal  uint8  `json:"expected_write_val"`
	NextPC            string `json:"next_pc"`
}

type LookupReplayCard struct {
	Status         string                      `json:"status"` // "available" or "unavailable"
	Reason         string                      `json:"reason,omitempty"`
	Qualification  string                      `json:"qualification,omitempty"`
	BlockAddress   string                      `json:"block_address,omitempty"`
	BaselineInput  uint8                       `json:"baseline_input"`
	BaselineOutput uint8                       `json:"baseline_output"`
	BaselineFullA  string                      `json:"baseline_full_a"`
	Cases          []LookupCaseSummary         `json:"cases,omitempty"`
	Timeline       []LookupTimelineStepSummary `json:"timeline,omitempty"`
	ReceiptSummary *LookupReceiptSummary       `json:"receipt_summary,omitempty"`
	Manifest       *LookupBundleManifest       `json:"manifest,omitempty"`
}

func (s *Server) LoadLookupReplayBundle() *LookupReplayCard {
	card := &LookupReplayCard{
		Status: "unavailable",
	}

	if s == nil || s.Document == nil || s.Occurrences == nil {
		card.Reason = "server, document, or occurrence index unavailable"
		return card
	}

	// 1. Locate bundle directory strictly within ProjectDir (no cwd or ancestor fallback)
	if s.ProjectDir == "" {
		card.Reason = "project directory not specified"
		return card
	}
	bundleDir := filepath.Join(s.ProjectDir, "evidence", "bundles", "lookup_09f882")
	fi, err := os.Stat(bundleDir)
	if err != nil || !fi.IsDir() {
		card.Reason = "project-local lookup replay bundle not found at evidence/bundles/lookup_09f882"
		return card
	}

	// 2. Read and parse manifest.json
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		card.Reason = fmt.Sprintf("read manifest.json: %v", err)
		return card
	}
	var manifest LookupBundleManifest
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
	if manifest.BlockAddress != "$09F882" {
		card.Reason = fmt.Sprintf("block address mismatch: %s", manifest.BlockAddress)
		return card
	}

	// Verify exact canonical instruction IDs
	expectedInstIDs := []string{
		ValueChainNode0CanonicalID,
		ValueChainNode1CanonicalID,
		ValueChainNode2CanonicalID,
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
		ExpectedNode0RetirementID,
		ExpectedNode1RetirementID,
		ExpectedNode2RetirementID,
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

	// 4. Verify artifact content digests against manifest (anti-tamper guard)
	requiredArtifacts := []struct {
		name string
		key  string
	}{
		{"case.json", "case_sha256"},
		{"timeline.json", "timeline_sha256"},
		{"receipt.json", "receipt_sha256"},
	}

	acceptedDigests := map[string]string{
		"case.json":     AcceptedCaseSHA256,
		"timeline.json": AcceptedTimelineSHA256,
		"receipt.json":  AcceptedReceiptSHA256,
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
		Status               string `json:"status"`
		StreamSHA256         string `json:"stream_sha256"`
		ROMSHA256            string `json:"rom_sha256"`
		BlockAddress         string `json:"block_address"`
		DualBackendVerified  bool   `json:"dual_backend_verified"`
		BaselineRawVerified  bool   `json:"baseline_raw_verified"`
		SingleWriteVerified  bool   `json:"single_write_verified"`
		StepAccessesVerified bool   `json:"step_accesses_verified"`
		Results              []struct {
			CaseID          string `json:"case_id"`
			Verified        bool   `json:"verified"`
			EmuMatchesC     bool   `json:"emu_matches_c"`
			MatchesExpected bool   `json:"matches_expected"`
		} `json:"results"`
	}
	if err := json.Unmarshal(artifactData["receipt.json"], &rawReceipt); err != nil {
		card.Reason = fmt.Sprintf("decode receipt.json: %v", err)
		return card
	}

	if rawReceipt.Status != "success" ||
		!rawReceipt.DualBackendVerified ||
		!rawReceipt.BaselineRawVerified ||
		!rawReceipt.SingleWriteVerified ||
		!rawReceipt.StepAccessesVerified {
		card.Reason = fmt.Sprintf("receipt verification failed: status=%s dual=%v raw=%v write=%v accesses=%v",
			rawReceipt.Status, rawReceipt.DualBackendVerified, rawReceipt.BaselineRawVerified,
			rawReceipt.SingleWriteVerified, rawReceipt.StepAccessesVerified)
		return card
	}
	if len(rawReceipt.Results) != 3 {
		card.Reason = fmt.Sprintf("receipt results count %d != 3", len(rawReceipt.Results))
		return card
	}
	for _, res := range rawReceipt.Results {
		if !res.Verified || !res.EmuMatchesC || !res.MatchesExpected {
			card.Reason = fmt.Sprintf("case %s not verified in receipt", res.CaseID)
			return card
		}
	}

	// 6. Unmarshal timeline.json and extract step comparisons
	var rawTimeline []struct {
		StepIndex int    `json:"step_index"`
		Address   string `json:"address"`
		Mnemonic  string `json:"mnemonic"`
		Recorded  struct {
			InputVal       uint8  `json:"input_val"`
			OperandEventID uint64 `json:"operand_event_id"`
			EffAddr        string `json:"effective_address"`
			ROMOffset      string `json:"rom_offset"`
			BusVal         uint8  `json:"bus_value"`
			BusOp          string `json:"bus_op"`
			EntryY         string `json:"entry_y"`
			ExitY          string `json:"exit_y"`
			ExitA          string `json:"exit_a"`
			ExitPC         string `json:"exit_pc"`
		} `json:"recorded"`
		Predictions []struct {
			InputVal  uint8  `json:"input_val"`
			EffAddr   string `json:"effective_address"`
			ROMOffset string `json:"rom_offset"`
			BusVal    uint8  `json:"bus_value"`
			BusOp     string `json:"bus_op"`
			ExitY     string `json:"exit_y"`
			ExitA     string `json:"exit_a"`
			ExitPC    string `json:"exit_pc"`
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

	var timelineSteps []LookupTimelineStepSummary
	for _, st := range rawTimeline {
		var pred114, pred116 LookupPredictSummary
		for _, p := range st.Predictions {
			switch p.InputVal {
			case 114:
				pred114 = LookupPredictSummary{
					InputVal:         114,
					EffectiveAddress: p.EffAddr,
					ROMOffset:        p.ROMOffset,
					BusValue:         p.BusVal,
					BusOp:            p.BusOp,
					ExitY:            p.ExitY,
					ExitA:            p.ExitA,
					ExitPC:           p.ExitPC,
				}
			case 116:
				pred116 = LookupPredictSummary{
					InputVal:         116,
					EffectiveAddress: p.EffAddr,
					ROMOffset:        p.ROMOffset,
					BusValue:         p.BusVal,
					BusOp:            p.BusOp,
					ExitY:            p.ExitY,
					ExitA:            p.ExitA,
					ExitPC:           p.ExitPC,
				}
			}
		}

		addrVal, _ := strconv.ParseUint(strings.TrimPrefix(st.Address, "$"), 16, 32)
		physOff := ((addrVal >> 16) & 0x7F) << 15 | (addrVal & 0x7FFF)
		romOffStr := fmt.Sprintf("$%06X", physOff)

		timelineSteps = append(timelineSteps, LookupTimelineStepSummary{
			StepIndex:       st.StepIndex,
			Address:         st.Address,
			ROMOffset:       romOffStr,
			Mnemonic:        st.Mnemonic,
			RecordedInput:   st.Recorded.InputVal,
			RecordedOperand: st.Recorded.OperandEventID,
			RecordedEffAddr: st.Recorded.EffAddr,
			RecordedROMOff:  st.Recorded.ROMOffset,
			RecordedBusVal:  st.Recorded.BusVal,
			RecordedBusOp:   st.Recorded.BusOp,
			RecordedEntryY:  st.Recorded.EntryY,
			RecordedExitY:   st.Recorded.ExitY,
			RecordedExitA:   st.Recorded.ExitA,
			RecordedExitPC:  st.Recorded.ExitPC,
			Prediction114:   pred114,
			Prediction116:   pred116,
		})
	}

	// 7. Unmarshal case.json
	var rawCaseData struct {
		Cases []struct {
			CaseID            string `json:"case_id"`
			InputWRAM05       uint8  `json:"input_wram_05"`
			Kind              string `json:"kind"`
			ExpectedFullA     string `json:"expected_full_a"`
			ExpectedY         uint16 `json:"expected_y"`
			ExpectedROMAddr   string `json:"expected_rom_addr"`
			ExpectedROMOffset string `json:"expected_rom_offset"`
			ExpectedROMVal    uint8  `json:"expected_rom_val"`
			ExpectedWriteAddr string `json:"expected_write_addr"`
			ExpectedWriteVal  uint8  `json:"expected_write_val"`
			WantNextPC        uint32 `json:"want_next_pc"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(artifactData["case.json"], &rawCaseData); err != nil {
		card.Reason = fmt.Sprintf("decode case.json: %v", err)
		return card
	}

	var cases []LookupCaseSummary
	for _, c := range rawCaseData.Cases {
		cases = append(cases, LookupCaseSummary{
			CaseID:            c.CaseID,
			InputVal:          c.InputWRAM05,
			Kind:              c.Kind,
			ExpectedFullA:     c.ExpectedFullA,
			ExpectedY:         c.ExpectedY,
			ExpectedROMAddr:   c.ExpectedROMAddr,
			ExpectedROMOff:    c.ExpectedROMOffset,
			ExpectedROMVal:    c.ExpectedROMVal,
			ExpectedWriteAddr: c.ExpectedWriteAddr,
			ExpectedWriteVal:  c.ExpectedWriteVal,
			NextPC:            fmt.Sprintf("$%06X", c.WantNextPC),
		})
	}

	// All checks strictly verified
	card.Status = "available"
	card.Reason = ""
	card.Qualification = manifest.Qualification
	card.BlockAddress = manifest.BlockAddress
	for _, c := range cases {
		if c.Kind == "baseline" || c.CaseID == "baseline_115" {
			card.BaselineInput = c.InputVal
			card.BaselineOutput = c.ExpectedWriteVal
			card.BaselineFullA = c.ExpectedFullA
			break
		}
	}
	card.Cases = cases
	card.Timeline = timelineSteps
	card.ReceiptSummary = &LookupReceiptSummary{
		DualBackendVerified:  rawReceipt.DualBackendVerified,
		BaselineRawVerified:  rawReceipt.BaselineRawVerified,
		SingleWriteVerified:  rawReceipt.SingleWriteVerified,
		StepAccessesVerified: rawReceipt.StepAccessesVerified,
	}
	card.Manifest = &manifest

	return card
}
