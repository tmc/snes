package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SignedWordCompanionPacket contains project-local computation and evidence records
// explaining observed signed-word occurrences.
type SignedWordCompanionPacket struct {
	StreamSHA256 string                    `json:"stream_sha256"`
	ROMSHA256    string                    `json:"rom_sha256,omitempty"`
	Scope        string                    `json:"scope,omitempty"`
	Cases        []SignedWordCompanionCase `json:"cases"`
}

// SignedWordCompanionCase represents one anchored high-byte store occurrence
// with its paired memory word, 4-instruction walkthrough, explanation, and
// separately labeled execution qualification.
type SignedWordCompanionCase struct {
	CaseID           string                 `json:"case_id"` // "positive" or "negative"
	TraceFrame       int                    `json:"trace_frame"`
	RetirementID     uint64                 `json:"retirement_id"`
	Seq              uint64                 `json:"seq"`
	InstructionID    string                 `json:"instruction_id"`
	Address          uint32                 `json:"address"`
	AddressStr       string                 `json:"address_str"` // e.g. "09:F88F"

	// 1. Recorded Evidence
	TableByte        uint8                  `json:"table_byte"`
	SignedValue      int                    `json:"signed_value"` // 20 or -61
	WordHex          string                 `json:"word_hex"`     // "0014" or "FFC3"
	PhysicalWordAddr string                 `json:"physical_word_addr"` // "7E:1F54" or "7E:1F56"
	LowByteStore     ByteStoreWitness       `json:"low_byte_store"`
	HighByteStore    ByteStoreWitness       `json:"high_byte_store"`
	ExitA            uint16                 `json:"exit_a"` // 0xFF00 or 0xFFFF
	ExitP            uint8                  `json:"exit_p"` // 0x32 or 0xB1

	// 2. Explanatory Interpretation
	ExplanationTitle string                 `json:"explanation_title"`
	Explanation      string                 `json:"explanation"`
	Walkthrough      []WalkthroughRow       `json:"walkthrough"`

	// 3. Isolated Execution Qualification
	Qualification    CompanionQualification `json:"qualification"`
}

// ByteStoreWitness captures an observed physical byte store in WRAM.
type ByteStoreWitness struct {
	Address     string `json:"address"`     // e.g. "7E:1F54"
	Value       uint8  `json:"value"`       // e.g. 0x14
	Instruction string `json:"instruction"` // e.g. "09:F887"
	RecordID    uint64 `json:"record_id"`   // e.g. 52086
	Seq         uint64 `json:"seq"`         // e.g. 13201
	BusID       uint64 `json:"bus_id"`      // e.g. 52085
	Note        string `json:"note"`        // e.g. "observed prior low-byte write (outside 4-instruction replay)"
}

// WalkthroughRow describes one step of the 4-instruction sequence.
type WalkthroughRow struct {
	PC          string `json:"pc"`          // e.g. "09:F889"
	Mnemonic    string `json:"mnemonic"`    // e.g. "CMP #$80"
	RecordID    uint64 `json:"record_id"`   // e.g. 52089
	Seq         uint64 `json:"seq"`         // e.g. 13202
	BusAccess   string `json:"bus_access"`  // e.g. "read 7E:1F54 = $14" or "-"
	EntryA      uint16 `json:"entry_a"`
	EntryP      uint8  `json:"entry_p"`
	ExitA       uint16 `json:"exit_a"`
	ExitP       uint8  `json:"exit_p"`
	Explanation string `json:"explanation"` // e.g. "Carry=0 ($14 < $80), Neg=1"
}

// CompanionQualification contains compiler and differential execution qualification.
type CompanionQualification struct {
	Status              string `json:"status"` // "verified" or "unavailable"
	Reason              string `json:"reason,omitempty"`
	DifferentialMatched bool   `json:"differential_matched"`
	GeneratedCSource    string `json:"generated_c_source,omitempty"`
	GeneratedCHash      string `json:"generated_c_hash,omitempty"`
	RunnerBinaryHash    string `json:"runner_binary_hash,omitempty"`
	Compiler            string `json:"compiler,omitempty"`
	CompilerFlags       string `json:"compiler_flags,omitempty"`
	Scope               string `json:"scope,omitempty"`
	ReceiptPath         string `json:"receipt_path,omitempty"`
}

// SignedWordCompanionIndex indexes companion cases by anchor identity.
type SignedWordCompanionIndex struct {
	Packet   *SignedWordCompanionPacket
	byAnchor map[string]*SignedWordCompanionCase
}

// LoadSignedWordCompanion loads a project-local signed_words.json or computation_evidence.json packet,
// validating the packet against the admitted active ROM and stream identities.
func LoadSignedWordCompanion(projectDir string, activeROMSHA string, activeStreamSHA string) (*SignedWordCompanionIndex, error) {
	candidates := []string{
		filepath.Join(projectDir, "signed_words.json"),
		filepath.Join(projectDir, "computation_evidence.json"),
		filepath.Join(projectDir, "export", "signed_words.json"),
	}

	var data []byte
	var packetPath string
	for _, cand := range candidates {
		if b, err := os.ReadFile(cand); err == nil {
			data = b
			packetPath = cand
			break
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no signed word companion packet found in %s", projectDir)
	}

	var packet SignedWordCompanionPacket
	if err := json.Unmarshal(data, &packet); err != nil {
		return nil, fmt.Errorf("unmarshal signed word packet %s: %w", packetPath, err)
	}

	// Validate packet ROM and Stream identities against active admitted project
	if packet.ROMSHA256 != "" && activeROMSHA != "" && packet.ROMSHA256 != activeROMSHA {
		return nil, fmt.Errorf("companion packet ROM mismatch: packet has %s, project has %s", packet.ROMSHA256, activeROMSHA)
	}
	if packet.StreamSHA256 != "" && activeStreamSHA != "" && packet.StreamSHA256 != activeStreamSHA {
		return nil, fmt.Errorf("companion packet stream mismatch: packet has %s, project has %s", packet.StreamSHA256, activeStreamSHA)
	}

	idx := &SignedWordCompanionIndex{
		Packet:   &packet,
		byAnchor: make(map[string]*SignedWordCompanionCase),
	}

	for i := range packet.Cases {
		c := &packet.Cases[i]

		// 1. Validate recorded evidence (witness bytes, derived word, exit registers)
		if !validateRecordedEvidence(c) {
			continue // Withhold invalid/contradictory recorded companion
		}

		// 2. Validate qualification against referenced receipt
		validateCaseQualification(projectDir, packet.StreamSHA256, c)

		// Key by streamSHA:traceFrame:retirementID:instID
		if c.InstructionID != "" {
			keyWithID := fmt.Sprintf("%s:%d:%d:%s", packet.StreamSHA256, c.TraceFrame, c.RetirementID, c.InstructionID)
			idx.byAnchor[keyWithID] = c
		}

		// Key by streamSHA:traceFrame:retirementID:address (only used when instruction ID is unspecified)
		if c.Address != 0 {
			keyWithAddr := fmt.Sprintf("%s:%d:%d:%06X", packet.StreamSHA256, c.TraceFrame, c.RetirementID, c.Address)
			idx.byAnchor[keyWithAddr] = c
		}
	}

	return idx, nil
}

func validateRecordedEvidence(c *SignedWordCompanionCase) bool {
	// 1. Derive word and signed value directly from actual physical low and high store witness bytes
	derivedWord := uint16(c.LowByteStore.Value) | (uint16(c.HighByteStore.Value) << 8)
	expectedWordHex := fmt.Sprintf("%04X", derivedWord)
	expectedSignedValue := int(int16(derivedWord))

	if !strings.EqualFold(c.WordHex, expectedWordHex) || c.SignedValue != expectedSignedValue {
		return false // Contradictory authored word does not match validated witness bytes
	}

	// 2. Validate physical word address matches low byte store address
	if c.PhysicalWordAddr != c.LowByteStore.Address {
		return false
	}

	// 3. Validate exit registers against the final walkthrough step
	if len(c.Walkthrough) == 0 {
		return false
	}
	lastRow := c.Walkthrough[len(c.Walkthrough)-1]
	if c.ExitA != lastRow.ExitA || c.ExitP != lastRow.ExitP {
		return false
	}
	if c.HighByteStore.RecordID != lastRow.RecordID {
		return false
	}

	return true
}

func validateCaseQualification(projectDir string, packetStreamSHA string, c *SignedWordCompanionCase) {
	if c.Qualification.ReceiptPath == "" {
		// No receipt specified: cannot claim verified qualification
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "no qualification receipt specified"
		c.Qualification.DifferentialMatched = false
		return
	}

	receiptPath := c.Qualification.ReceiptPath
	if !filepath.IsAbs(receiptPath) {
		receiptPath = filepath.Join(projectDir, receiptPath)
	}

	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = fmt.Sprintf("qualification receipt unreadable (%v)", err)
		c.Qualification.DifferentialMatched = false
		return
	}

	type receiptCompiledResult struct {
		State struct {
			A uint16 `json:"a"`
			P uint8  `json:"p"`
		} `json:"state"`
		MissingRead       bool `json:"missing_read"`
		UninitializedRead bool `json:"uninitialized_read"`
		WriteOverflow     bool `json:"write_overflow"`
		MMIOAccess        bool `json:"mmio_access"`
	}

	type receiptCase struct {
		Name                 string                `json:"name"`
		SourceStreamSHA256   string                `json:"source_stream_sha256"`
		SourceEventIDs       []uint64              `json:"source_event_ids"`
		GeneratedCHash       string                `json:"generated_c_hash"`
		RunnerBinaryHash     string                `json:"runner_binary_hash"`
		DifferentialMatched  bool                  `json:"differential_matched"`
		EffectsRefusalsClear *bool                 `json:"effects_refusals_clear"`
		Compiler             string                `json:"compiler"`
		CompilerFlags        string                `json:"compiler_flags"`
		GeneratedCSource     string                `json:"generated_c_source"`
		CompiledCResult      receiptCompiledResult `json:"compiled_c_result"`
	}

	var receipt struct {
		CaseCount int           `json:"case_count"`
		Cases     []receiptCase `json:"cases"`
	}
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = fmt.Sprintf("qualification receipt invalid JSON (%v)", err)
		c.Qualification.DifferentialMatched = false
		return
	}

	var foundCase *receiptCase
	for j := range receipt.Cases {
		rc := &receipt.Cases[j]
		if rc.Name == c.CaseID {
			foundCase = rc
			break
		}
	}

	if foundCase == nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = fmt.Sprintf("case %q not found in receipt", c.CaseID)
		c.Qualification.DifferentialMatched = false
		return
	}

	// Check stream binding
	if foundCase.SourceStreamSHA256 != "" && packetStreamSHA != "" && foundCase.SourceStreamSHA256 != packetStreamSHA {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt belongs to different stream"
		c.Qualification.DifferentialMatched = false
		return
	}

	// Check retirement event IDs binding against walkthrough steps
	if len(foundCase.SourceEventIDs) != len(c.Walkthrough) {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt retirement IDs mismatch selected sequence"
		c.Qualification.DifferentialMatched = false
		return
	}
	for k := range c.Walkthrough {
		if foundCase.SourceEventIDs[k] != c.Walkthrough[k].RecordID {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt retirement IDs mismatch selected sequence"
			c.Qualification.DifferentialMatched = false
			return
		}
	}

	// Check differential match
	if !foundCase.DifferentialMatched {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
		c.Qualification.DifferentialMatched = false
		return
	}

	// Check refusal effects and result state
	res := foundCase.CompiledCResult
	if res.MissingRead || res.UninitializedRead || res.WriteOverflow || res.MMIOAccess || (foundCase.EffectsRefusalsClear != nil && !*foundCase.EffectsRefusalsClear) {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}
	if res.State.A != c.ExitA || res.State.P != c.ExitP {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}

	// Check inline generated C source against receipt
	if c.Qualification.GeneratedCSource != "" && foundCase.GeneratedCSource != "" && c.Qualification.GeneratedCSource != foundCase.GeneratedCSource {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "inline generated source differs from pinned receipt"
		c.Qualification.DifferentialMatched = false
		return
	}

	if c.Qualification.GeneratedCHash != "" && foundCase.GeneratedCHash != c.Qualification.GeneratedCHash {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "generated C hash mismatch with receipt"
		c.Qualification.DifferentialMatched = false
		return
	}

	if c.Qualification.RunnerBinaryHash != "" && foundCase.RunnerBinaryHash != c.Qualification.RunnerBinaryHash {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "runner binary hash mismatch with receipt"
		c.Qualification.DifferentialMatched = false
		return
	}

	// Verify generated C source hash
	if foundCase.GeneratedCSource != "" {
		computedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(foundCase.GeneratedCSource)))
		if foundCase.GeneratedCHash != "" && computedHash != foundCase.GeneratedCHash {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt generated C source hash mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
	}

	c.Qualification.Status = "verified"
	c.Qualification.DifferentialMatched = true
	c.Qualification.GeneratedCSource = foundCase.GeneratedCSource
	c.Qualification.GeneratedCHash = foundCase.GeneratedCHash
	c.Qualification.RunnerBinaryHash = foundCase.RunnerBinaryHash
	if c.Qualification.Compiler == "" {
		c.Qualification.Compiler = foundCase.Compiler
	}
	if c.Qualification.CompilerFlags == "" {
		c.Qualification.CompilerFlags = foundCase.CompilerFlags
	}
}

// Lookup finds a matching companion case by stream SHA, trace frame, retirement ID, and instruction ID / address.
// An explicit instruction ID mismatch returns nil and does not substitute address fallback.
func (idx *SignedWordCompanionIndex) Lookup(streamSHA string, traceFrame int, retirementID uint64, instID string, addr uint32) *SignedWordCompanionCase {
	if idx == nil || idx.Packet == nil {
		return nil
	}
	if idx.Packet.StreamSHA256 != "" && streamSHA != "" && idx.Packet.StreamSHA256 != streamSHA {
		return nil
	}

	if instID != "" {
		key := fmt.Sprintf("%s:%d:%d:%s", streamSHA, traceFrame, retirementID, instID)
		c, ok := idx.byAnchor[key]
		if !ok {
			return nil // Explicit canonical miss must not substitute address fallback
		}
		if c.InstructionID != instID {
			return nil
		}
		return c
	}

	if addr != 0 {
		key := fmt.Sprintf("%s:%d:%d:%06X", streamSHA, traceFrame, retirementID, addr)
		if c, ok := idx.byAnchor[key]; ok {
			return c
		}
	}

	return nil
}
