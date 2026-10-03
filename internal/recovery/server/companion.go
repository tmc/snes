package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Packet  *SignedWordCompanionPacket
	byAnchor map[string]*SignedWordCompanionCase
}

// LoadSignedWordCompanion loads a project-local signed_words.json or computation_evidence.json packet.
func LoadSignedWordCompanion(projectDir string) (*SignedWordCompanionIndex, error) {
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

	idx := &SignedWordCompanionIndex{
		Packet:   &packet,
		byAnchor: make(map[string]*SignedWordCompanionCase),
	}

	for i := range packet.Cases {
		c := &packet.Cases[i]
		validateCaseQualification(projectDir, c)

		// Key by streamSHA:traceFrame:retirementID:instID
		keyWithID := fmt.Sprintf("%s:%d:%d:%s", packet.StreamSHA256, c.TraceFrame, c.RetirementID, c.InstructionID)
		idx.byAnchor[keyWithID] = c

		// Key by streamSHA:traceFrame:retirementID:address
		keyWithAddr := fmt.Sprintf("%s:%d:%d:%06X", packet.StreamSHA256, c.TraceFrame, c.RetirementID, c.Address)
		idx.byAnchor[keyWithAddr] = c
	}

	return idx, nil
}

func validateCaseQualification(projectDir string, c *SignedWordCompanionCase) {
	if c.Qualification.ReceiptPath == "" {
		// No receipt specified; check if inline qualification fields are already verified
		if c.Qualification.Status == "" {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "no qualification receipt specified"
		}
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

	var receipt struct {
		CaseCount int `json:"case_count"`
		Cases     []struct {
			Name                string `json:"name"`
			GeneratedCHash      string `json:"generated_c_hash"`
			RunnerBinaryHash    string `json:"runner_binary_hash"`
			DifferentialMatched bool   `json:"differential_matched"`
			Compiler            string `json:"compiler"`
			CompilerFlags       string `json:"compiler_flags"`
			GeneratedCSource    string `json:"generated_c_source"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = fmt.Sprintf("qualification receipt invalid JSON (%v)", err)
		c.Qualification.DifferentialMatched = false
		return
	}

	var foundCase *struct {
		Name                string `json:"name"`
		GeneratedCHash      string `json:"generated_c_hash"`
		RunnerBinaryHash    string `json:"runner_binary_hash"`
		DifferentialMatched bool   `json:"differential_matched"`
		Compiler            string `json:"compiler"`
		CompilerFlags       string `json:"compiler_flags"`
		GeneratedCSource    string `json:"generated_c_source"`
	}
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

	if !foundCase.DifferentialMatched {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
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
		if c.Qualification.GeneratedCSource == "" {
			c.Qualification.GeneratedCSource = foundCase.GeneratedCSource
		}
	}

	c.Qualification.Status = "verified"
	c.Qualification.DifferentialMatched = true
	if c.Qualification.Compiler == "" {
		c.Qualification.Compiler = foundCase.Compiler
	}
	if c.Qualification.CompilerFlags == "" {
		c.Qualification.CompilerFlags = foundCase.CompilerFlags
	}
}

// Lookup finds a matching companion case by stream SHA, trace frame, retirement ID, and instruction ID / address.
func (idx *SignedWordCompanionIndex) Lookup(streamSHA string, traceFrame int, retirementID uint64, instID string, addr uint32) *SignedWordCompanionCase {
	if idx == nil || idx.Packet == nil {
		return nil
	}
	if idx.Packet.StreamSHA256 != "" && streamSHA != "" && idx.Packet.StreamSHA256 != streamSHA {
		return nil
	}

	if instID != "" {
		key := fmt.Sprintf("%s:%d:%d:%s", streamSHA, traceFrame, retirementID, instID)
		if c, ok := idx.byAnchor[key]; ok {
			return c
		}
	}

	if addr != 0 {
		key := fmt.Sprintf("%s:%d:%d:%06X", streamSHA, traceFrame, retirementID, addr)
		if c, ok := idx.byAnchor[key]; ok {
			return c
		}
	}

	return nil
}
