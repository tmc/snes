package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/snes/internal/trace"
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

// companionReferencedRanges returns event ID ranges that should be retained from the trace
// to support physical evidence validation for signed word companions.
func companionReferencedRanges(projectDir string) [][2]uint64 {
	ranges := [][2]uint64{
		{30135, 30155},   // $7E:1F05 initial store (30147) and retirement (30148)
		{52070, 52138},   // baseline authentic range + LDY 09:F882 (52076/52077)
		{52130, 52305},   // authentic scaling calculation explainer anchors & walkthroughs (52132..52296)
		{139200, 139225}, // $7E:1F05 read (139209/139210) and overwrite (139219/139220)
	}
	candidates := []string{
		filepath.Join(projectDir, "signed_words.json"),
		filepath.Join(projectDir, "computation_evidence.json"),
		filepath.Join(projectDir, "export", "signed_words.json"),
	}
	for _, cand := range candidates {
		data, err := os.ReadFile(cand)
		if err != nil {
			continue
		}
		var pkt SignedWordCompanionPacket
		if err := json.Unmarshal(data, &pkt); err != nil {
			continue
		}
		for _, c := range pkt.Cases {
			minID := c.LowByteStore.BusID
			if minID == 0 || (c.LowByteStore.RecordID > 0 && c.LowByteStore.RecordID < minID) {
				minID = c.LowByteStore.RecordID
			}
			maxID := c.HighByteStore.RecordID
			if c.HighByteStore.BusID > maxID {
				maxID = c.HighByteStore.BusID
			}
			if c.RetirementID > maxID {
				maxID = c.RetirementID
			}
			if minID > 0 && maxID >= minID {
				start := minID
				if start > 15 {
					start -= 15
				}
				ranges = append(ranges, [2]uint64{start, maxID + 15})
			}
		}
		break
	}
	return ranges
}

func parsePhysicalAddr(s string) uint32 {
	s = strings.TrimPrefix(s, "$")
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		var b, o uint32
		fmt.Sscanf(parts[0], "%x", &b)
		fmt.Sscanf(parts[1], "%x", &o)
		return (b << 16) | (o & 0xFFFF)
	}
	var a uint32
	fmt.Sscanf(s, "%x", &a)
	return a
}

func busCanonicalAddr(addr uint32) uint32 {
	a := addr & 0xFFFFFF
	bank := (a >> 16) & 0xFF
	offset := a & 0xFFFF
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000 {
		return 0x7E0000 | offset
	}
	return a
}

// LoadSignedWordCompanion loads a project-local signed_words.json or computation_evidence.json packet,
// validating the packet against the admitted active ROM, stream identities, and retained physical trace evidence.
func LoadSignedWordCompanion(projectDir string, activeROMSHA string, activeStreamSHA string, occ ...*OccurrenceIndex) (*SignedWordCompanionIndex, error) {
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

	var occIndex *OccurrenceIndex
	if len(occ) > 0 {
		occIndex = occ[0]
	}

	idx := &SignedWordCompanionIndex{
		Packet:   &packet,
		byAnchor: make(map[string]*SignedWordCompanionCase),
	}

	for i := range packet.Cases {
		c := &packet.Cases[i]

		// 1. Validate recorded evidence (witness bytes, derived word, exit registers, physical trace)
		if !validateRecordedEvidence(c, occIndex, activeROMSHA, activeStreamSHA) {
			continue // Withhold invalid/contradictory recorded companion
		}

		// 2. Validate qualification against referenced receipt and full execution state
		validateCaseQualification(projectDir, packet.StreamSHA256, c, occIndex)

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

const (
	AcceptedExecutionSealSHA256 = "2f022f3d8c18dde25879ed446ca6f65bfd3367a6cebb13e8c4e13f5b279a4d7d"
	AcceptedWorkerReceiptSHA256  = "ce7fd7b58322d3b058c0cc761e0e47da6fcb0ba745e4645723cad68c07778e77"
	AcceptedOriginalInputSHA256  = "5cc9b07c136c9ccb49fd4bd587c0425a8a28e6d35f8b3192ec65611e442a939b"
	AcceptedSourceRevision       = "fbd4a6697a976c56402ca15d3c93fd7e3aa8b45f"
)

// findPrecedingRetirement finds the immediately preceding retired instruction (Seq == owning.Seq - 1)
// in the retained trace events. Returns false if no predecessor retirement is found.
func findPrecedingRetirement(occIndex *OccurrenceIndex, owningEv trace.Event) (trace.Event, bool) {
	if occIndex == nil || occIndex.retainedEvents == nil || owningEv.Insn == nil || owningEv.Insn.Seq == 0 {
		return trace.Event{}, false
	}
	wantSeq := owningEv.Insn.Seq - 1
	for id := owningEv.ID - 1; id > 0 && owningEv.ID-id < 50; id-- {
		ev, ok := occIndex.retainedEvents[id]
		if ok && ev.Kind == "cpu_insn" && ev.Insn != nil && ev.Insn.Status == "retired" {
			if ev.Insn.Seq == wantSeq {
				return ev, true
			}
			if ev.Insn.Seq < wantSeq {
				return trace.Event{}, false
			}
		}
	}
	return trace.Event{}, false
}

// findUniqueRetainedOperand locates the unique physical bus operand transaction
// bounded by previousRetirementID < busID < owningRetirementID within the instruction's
// cycle interval, validating the frame, operation, width, address and value.
func findUniqueRetainedOperand(occIndex *OccurrenceIndex, owningEv, prevEv trace.Event, expectedOp string, expectedAddr uint32, expectedVal uint8) (*trace.Event, bool) {
	if occIndex == nil || occIndex.retainedEvents == nil || owningEv.Insn == nil {
		return nil, false
	}
	insn := owningEv.Insn
	fetchAddrs := make(map[uint32]bool)
	for _, f := range insn.Fetches {
		fetchAddrs[f.Addr] = true
	}

	var candidates []trace.Event
	for id := prevEv.ID + 1; id < owningEv.ID; id++ {
		b, ok := occIndex.GetRetainedEvent(id)
		if !ok || b.Kind != "bus" {
			continue
		}
		if b.Frame != owningEv.Frame {
			continue
		}
		if b.Cycle < insn.Entry.Cycles || b.Cycle > insn.Exit.Cycles {
			continue
		}
		// Direct physical bus PC equality with owning Entry.PB:PC
		if b.PC == nil || b.PC.Bank != insn.Entry.PB || b.PC.Addr != insn.Entry.PC {
			continue
		}
		// Require bus CPU snapshot and match relevant PBR/DP/P/E/MWidth/XWidth against owning entry
		if b.CPU == nil {
			continue
		}
		expMWidth := 16
		if insn.Entry.E || insn.Entry.P&0x20 != 0 {
			expMWidth = 8
		}
		expXWidth := 16
		if insn.Entry.E || insn.Entry.P&0x10 != 0 {
			expXWidth = 8
		}
		if b.CPU.PBR != insn.Entry.PB || b.CPU.DP != insn.Entry.D || b.CPU.P != insn.Entry.P || b.CPU.E != insn.Entry.E ||
			b.CPU.MWidth != expMWidth || b.CPU.XWidth != expXWidth {
			continue
		}
		if b.Space == "cpu" && fetchAddrs[b.Addr] {
			continue
		}
		if b.Space != "wram" || b.Width != 1 || b.Op != expectedOp {
			continue
		}
		if busCanonicalAddr(b.Addr) != expectedAddr {
			continue
		}
		if uint8(b.Value) != expectedVal {
			continue
		}
		candidates = append(candidates, b)
	}
	if len(candidates) != 1 {
		return nil, false
	}
	return &candidates[0], true
}

func validateRecordedEvidence(c *SignedWordCompanionCase, occIndex *OccurrenceIndex, activeROM, activeStream string) bool {
	// A missing admitted trace must withhold recorded attachment, not promote authored internal consistency.
	if occIndex == nil || len(occIndex.retainedEvents) == 0 {
		return false
	}

	// 1. Validate physical word address matches low byte store address
	if c.PhysicalWordAddr != c.LowByteStore.Address {
		return false
	}

	// 2. Validate exit registers against the final walkthrough step
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

	// 3. Anchor validation
	anchorEv, ok := occIndex.GetRetainedEvent(c.RetirementID)
	if !ok || anchorEv.Kind != "cpu_insn" || anchorEv.Insn == nil || anchorEv.Insn.Status != "retired" {
		return false
	}
	if int(anchorEv.Frame) != c.TraceFrame || anchorEv.Insn.Seq != c.Seq {
		return false
	}
	anchorAddr := uint32(anchorEv.Insn.Entry.PB)<<16 | uint32(anchorEv.Insn.Entry.PC)
	if anchorAddr != c.Address {
		return false
	}
	if c.InstructionID != "" && activeROM != "" {
		canID := computeCanonicalInstructionID(activeROM, anchorEv.Insn)
		if canID != "" && canID != c.InstructionID {
			return false
		}
	}

	// 4. Low-byte store CPU instruction and physical bus write validation
	lowInsnEv, ok := occIndex.GetRetainedEvent(c.LowByteStore.RecordID)
	if !ok || lowInsnEv.Kind != "cpu_insn" || lowInsnEv.Insn == nil || lowInsnEv.Insn.Status != "retired" {
		return false
	}
	if int(lowInsnEv.Frame) != c.TraceFrame || lowInsnEv.Insn.Seq != c.LowByteStore.Seq {
		return false
	}
	pcLowStr := fmt.Sprintf("%02X:%04X", lowInsnEv.Insn.Entry.PB, lowInsnEv.Insn.Entry.PC)
	if pcLowStr != c.LowByteStore.Instruction {
		return false
	}
	// STA dp: opcode 133 ($85)
	if len(lowInsnEv.Insn.Fetches) < 2 || lowInsnEv.Insn.Fetches[0].Value != 133 {
		return false
	}
	// CPU mode and context: Bank 9, DP $1F00, M=1, X=1
	if lowInsnEv.Insn.Entry.PB != 9 || lowInsnEv.Insn.Entry.D != 7936 ||
		(lowInsnEv.Insn.Entry.P&0x20) == 0 || (lowInsnEv.Insn.Entry.P&0x10) == 0 {
		return false
	}

	effLowAddr := uint32(lowInsnEv.Insn.Entry.D + uint16(lowInsnEv.Insn.Fetches[1].Value))
	if busCanonicalAddr(effLowAddr) != parsePhysicalAddr(c.LowByteStore.Address) {
		return false
	}
	actualLowByte := c.LowByteStore.Value
	if c.TableByte != 0 && c.TableByte != actualLowByte {
		return false
	}

	lowPrevEv, ok := findPrecedingRetirement(occIndex, lowInsnEv)
	if !ok {
		return false
	}
	lowOperand, ok := findUniqueRetainedOperand(occIndex, lowInsnEv, lowPrevEv, "write", parsePhysicalAddr(c.LowByteStore.Address), actualLowByte)
	if !ok || lowOperand.ID != c.LowByteStore.BusID {
		return false
	}

	// 5. High-byte store CPU instruction and physical bus write validation
	highInsnEv, ok := occIndex.GetRetainedEvent(c.HighByteStore.RecordID)
	if !ok || highInsnEv.Kind != "cpu_insn" || highInsnEv.Insn == nil || highInsnEv.Insn.Status != "retired" {
		return false
	}
	if int(highInsnEv.Frame) != c.TraceFrame || highInsnEv.Insn.Seq != c.HighByteStore.Seq {
		return false
	}
	if c.HighByteStore.RecordID != c.RetirementID {
		return false
	}
	pcHighStr := fmt.Sprintf("%02X:%04X", highInsnEv.Insn.Entry.PB, highInsnEv.Insn.Entry.PC)
	if pcHighStr != c.HighByteStore.Instruction {
		return false
	}
	if len(highInsnEv.Insn.Fetches) < 2 || highInsnEv.Insn.Fetches[0].Value != 133 {
		return false
	}
	if highInsnEv.Insn.Entry.PB != 9 || highInsnEv.Insn.Entry.D != 7936 ||
		(highInsnEv.Insn.Entry.P&0x20) == 0 || (highInsnEv.Insn.Entry.P&0x10) == 0 {
		return false
	}

	effHighAddr := uint32(highInsnEv.Insn.Entry.D + uint16(highInsnEv.Insn.Fetches[1].Value))
	if busCanonicalAddr(effHighAddr) != parsePhysicalAddr(c.HighByteStore.Address) {
		return false
	}
	actualHighByte := c.HighByteStore.Value

	highPrevEv, ok := findPrecedingRetirement(occIndex, highInsnEv)
	if !ok {
		return false
	}
	highOperand, ok := findUniqueRetainedOperand(occIndex, highInsnEv, highPrevEv, "write", parsePhysicalAddr(c.HighByteStore.Address), actualHighByte)
	if !ok || highOperand.ID != c.HighByteStore.BusID {
		return false
	}

	// 6. Same-byte reread during SBC retirement (walkthrough step 1)
	if len(c.Walkthrough) < 2 {
		return false
	}
	sbcEv, ok := occIndex.GetRetainedEvent(c.Walkthrough[1].RecordID)
	if !ok || sbcEv.Kind != "cpu_insn" || sbcEv.Insn == nil || sbcEv.Insn.Status != "retired" {
		return false
	}
	if len(sbcEv.Insn.Fetches) < 2 || sbcEv.Insn.Fetches[0].Value != 229 { // SBC dp ($E5)
		return false
	}
	effSBCAddr := uint32(sbcEv.Insn.Entry.D + uint16(sbcEv.Insn.Fetches[1].Value))
	if effSBCAddr != effLowAddr {
		return false
	}

	sbcPrevEv, ok := findPrecedingRetirement(occIndex, sbcEv)
	if !ok {
		return false
	}
	_, ok = findUniqueRetainedOperand(occIndex, sbcEv, sbcPrevEv, "read", parsePhysicalAddr(c.LowByteStore.Address), actualLowByte)
	if !ok {
		return false
	}

	// 7. Four walkthrough CPU transitions validation
	for _, s := range c.Walkthrough {
		stepEv, ok := occIndex.GetRetainedEvent(s.RecordID)
		if !ok || stepEv.Kind != "cpu_insn" || stepEv.Insn == nil || stepEv.Insn.Status != "retired" {
			return false
		}
		if stepEv.Insn.Seq != s.Seq {
			return false
		}
		stepPCStr := fmt.Sprintf("%02X:%04X", stepEv.Insn.Entry.PB, stepEv.Insn.Entry.PC)
		if stepPCStr != s.PC {
			return false
		}
		if stepEv.Insn.Entry.A != s.EntryA || stepEv.Insn.Entry.P != s.EntryP {
			return false
		}
		if stepEv.Insn.Exit.A != s.ExitA || stepEv.Insn.Exit.P != s.ExitP {
			return false
		}
	}

	// 8. Derive word directly from joined actual physical witness bytes
	derivedWord := uint16(actualLowByte) | (uint16(actualHighByte) << 8)
	expectedWordHex := fmt.Sprintf("%04X", derivedWord)
	expectedSignedValue := int(int16(derivedWord))
	if !strings.EqualFold(c.WordHex, expectedWordHex) || c.SignedValue != expectedSignedValue {
		return false
	}

	return true
}

func validateCaseQualification(projectDir string, packetStreamSHA string, c *SignedWordCompanionCase, occIndex *OccurrenceIndex) {
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

	// 1. Verify receipt content hash against accepted worker receipt identity
	receiptHash := fmt.Sprintf("%x", sha256.Sum256(receiptBytes))
	if receiptHash != AcceptedWorkerReceiptSHA256 {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "qualification receipt content hash mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 2. Check against project sealed manifest
	manifestPath := filepath.Join(projectDir, "sealed-manifest.json")
	mBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "sealed manifest missing"
		c.Qualification.DifferentialMatched = false
		return
	}
	if fmt.Sprintf("%x", sha256.Sum256(mBytes)) != AcceptedExecutionSealSHA256 {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "sealed manifest hash mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 3. Check against project original input artifact
	inputPath := filepath.Join(projectDir, "original-input.json")
	inBytes, err := os.ReadFile(inputPath)
	if err != nil {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "original input missing"
		c.Qualification.DifferentialMatched = false
		return
	}
	if fmt.Sprintf("%x", sha256.Sum256(inBytes)) != AcceptedOriginalInputSHA256 {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "original input hash mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}

	type receiptCPUState struct {
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

	type receiptWrite struct {
		Address uint32 `json:"address"`
		Value   uint8  `json:"value"`
	}

	type receiptExecResult struct {
		State             receiptCPUState `json:"state"`
		NextPC            uint32          `json:"next_pc"`
		TotalWrites       uint32          `json:"total_writes"`
		Writes            []receiptWrite  `json:"writes"`
		MissingRead       bool            `json:"missing_read"`
		UninitializedRead bool            `json:"uninitialized_read"`
		WriteOverflow     bool            `json:"write_overflow"`
		MMIOAccess        bool            `json:"mmio_access"`
	}

	type receiptIRInstruction struct {
		ID      string `json:"id"`
		Address uint32 `json:"address"`
		Bytes   string `json:"bytes"`
		Opcode  uint8  `json:"opcode"`
	}

	type receiptIR struct {
		Instructions []receiptIRInstruction `json:"instructions"`
	}

	type receiptCase struct {
		Name                 string            `json:"name"`
		SourceStreamSHA256   string            `json:"source_stream_sha256"`
		SourceEventIDs       []uint64          `json:"source_event_ids"`
		GeneratedCHash       string            `json:"generated_c_hash"`
		RunnerBinaryHash     string            `json:"runner_binary_hash"`
		DifferentialMatched  bool              `json:"differential_matched"`
		EffectsRefusalsClear *bool             `json:"effects_refusals_clear"`
		Compiler             string            `json:"compiler"`
		CompilerFlags        string            `json:"compiler_flags"`
		GeneratedCSource     string            `json:"generated_c_source"`
		IR                   receiptIR         `json:"ir"`
		InitialState         receiptCPUState   `json:"initial_state"`
		ExpectedState        receiptCPUState   `json:"expected_state"`
		CompiledCResult      receiptExecResult `json:"compiled_c_result"`
		EmulatorResult       receiptExecResult `json:"emulator_result"`
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

	// 1. Check stream binding
	if foundCase.SourceStreamSHA256 != "" && packetStreamSHA != "" && foundCase.SourceStreamSHA256 != packetStreamSHA {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt belongs to different stream"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 2. Check retirement event IDs binding against walkthrough steps
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

	// 3. Check differential match
	if !foundCase.DifferentialMatched {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 4. Check refusal effects
	res := foundCase.CompiledCResult
	if res.MissingRead || res.UninitializedRead || res.WriteOverflow || res.MMIOAccess || (foundCase.EffectsRefusalsClear != nil && !*foundCase.EffectsRefusalsClear) {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 5. Check IR input instructions against admitted trace
	if len(foundCase.IR.Instructions) > 0 {
		if len(foundCase.IR.Instructions) != len(c.Walkthrough) {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt result state and refusal effects mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
		if occIndex != nil {
			for k, w := range c.Walkthrough {
				stepEv, ok := occIndex.GetRetainedEvent(w.RecordID)
				if ok && stepEv.Insn != nil {
					var fetchedHex strings.Builder
					for _, f := range stepEv.Insn.Fetches {
						fmt.Fprintf(&fetchedHex, "%02x", f.Value)
					}
					if !strings.EqualFold(foundCase.IR.Instructions[k].Bytes, fetchedHex.String()) {
						c.Qualification.Status = "unavailable"
						c.Qualification.Reason = "receipt result state and refusal effects mismatch"
						c.Qualification.DifferentialMatched = false
						return
					}
				}
			}
		}
	}

	// 6. Check InitialState, ExitState, NextPC, and Writes directly against admitted trace records
	if occIndex != nil {
		// Entry state from walkthrough step 0
		step0, ok0 := occIndex.GetRetainedEvent(c.Walkthrough[0].RecordID)
		if ok0 && step0.Insn != nil {
			e := step0.Insn.Entry
			init := foundCase.InitialState
			if init.A != e.A || init.X != e.X || init.Y != e.Y || init.S != e.S ||
				init.D != e.D || init.DB != e.DB || init.PB != e.PB || init.P != e.P ||
				init.E != e.E || init.PC != e.PC {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
		}

		// Exit state from walkthrough step 3 (last step)
		step3, ok3 := occIndex.GetRetainedEvent(c.Walkthrough[len(c.Walkthrough)-1].RecordID)
		if ok3 && step3.Insn != nil {
			ex := step3.Insn.Exit
			if res.State.A != ex.A || res.State.X != ex.X || res.State.Y != ex.Y || res.State.S != ex.S ||
				res.State.D != ex.D || res.State.DB != ex.DB || res.State.PB != ex.PB || res.State.P != ex.P ||
				res.State.E != ex.E || res.State.PC != ex.PC {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
			expNextPC := uint32(ex.PB)<<16 | uint32(ex.PC)
			if res.NextPC != expNextPC {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
		}

		// High store write
		highBus, okH := occIndex.GetRetainedEvent(c.HighByteStore.BusID)
		if okH {
			if res.TotalWrites != 1 || len(res.Writes) != 1 {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
			if res.Writes[0].Address != busCanonicalAddr(highBus.Addr) || res.Writes[0].Value != uint8(highBus.Value) {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
		}
	} else {
		// Fallback when trace records not present
		if res.State.A != c.ExitA || res.State.P != c.ExitP {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt result state and refusal effects mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
	}

	// 7. Check EmulatorResult matches CompiledCResult
	em := foundCase.EmulatorResult
	if em.State != res.State || em.NextPC != res.NextPC || em.TotalWrites != res.TotalWrites || len(em.Writes) != len(res.Writes) {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
		c.Qualification.DifferentialMatched = false
		return
	}
	if len(em.Writes) > 0 && em.Writes[0] != res.Writes[0] {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 8. Source code and hashes
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
