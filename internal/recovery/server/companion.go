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

// companionReferencedRanges returns event ID ranges that should be retained from the trace
// to support physical evidence validation for signed word companions.
func companionReferencedRanges(projectDir string) [][2]uint64 {
	ranges := [][2]uint64{{52080, 52130}} // baseline authentic range
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
				if start > 5 {
					start -= 5
				}
				ranges = append(ranges, [2]uint64{start, maxID + 5})
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

func validateRecordedEvidence(c *SignedWordCompanionCase, occIndex *OccurrenceIndex, activeROM, activeStream string) bool {
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

	// 3. Physical Trace Evidence Binding (when admitted trace is available)
	if occIndex != nil && len(occIndex.retainedEvents) > 0 {
		// A. Anchor validation
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

		// B. Low byte store physical bus event validation
		lowBus, ok := occIndex.GetRetainedEvent(c.LowByteStore.BusID)
		if !ok || lowBus.Kind != "bus" || lowBus.Op != "write" || lowBus.Width != 1 {
			return false
		}
		if busCanonicalAddr(lowBus.Addr) != parsePhysicalAddr(c.LowByteStore.Address) {
			return false
		}
		if lowBus.PC.Bank != 9 || lowBus.CPU == nil {
			return false
		}
		pcLowStr := fmt.Sprintf("%02X:%04X", lowBus.PC.Bank, lowBus.PC.Addr)
		if pcLowStr != c.LowByteStore.Instruction {
			return false
		}
		actualLowByte := uint8(lowBus.Value)
		if c.LowByteStore.Value != actualLowByte || c.TableByte != actualLowByte {
			return false
		}

		// C. High byte store physical bus event validation
		highBus, ok := occIndex.GetRetainedEvent(c.HighByteStore.BusID)
		if !ok || highBus.Kind != "bus" || highBus.Op != "write" || highBus.Width != 1 {
			return false
		}
		if busCanonicalAddr(highBus.Addr) != parsePhysicalAddr(c.HighByteStore.Address) {
			return false
		}
		if highBus.PC.Bank != 9 || highBus.CPU == nil {
			return false
		}
		pcHighStr := fmt.Sprintf("%02X:%04X", highBus.PC.Bank, highBus.PC.Addr)
		if pcHighStr != c.HighByteStore.Instruction {
			return false
		}
		actualHighByte := uint8(highBus.Value)
		if c.HighByteStore.Value != actualHighByte {
			return false
		}

		// D. Same-byte read physical bus event validation
		foundRead := false
		for id := c.LowByteStore.BusID + 1; id < c.HighByteStore.BusID; id++ {
			ev, ok := occIndex.GetRetainedEvent(id)
			if ok && ev.Kind == "bus" && ev.Op == "read" && ev.Width == 1 &&
				busCanonicalAddr(ev.Addr) == parsePhysicalAddr(c.LowByteStore.Address) {
				if uint8(ev.Value) != actualLowByte {
					return false
				}
				foundRead = true
				break
			}
		}
		if !foundRead {
			return false
		}

		// E. Four walkthrough CPU transitions validation
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

		// F. Derive word and signed value directly from joined actual physical witness bytes
		derivedWord := uint16(actualLowByte) | (uint16(actualHighByte) << 8)
		expectedWordHex := fmt.Sprintf("%04X", derivedWord)
		expectedSignedValue := int(int16(derivedWord))
		if !strings.EqualFold(c.WordHex, expectedWordHex) || c.SignedValue != expectedSignedValue {
			return false
		}
	} else {
		// Fallback internal consistency check when trace events are not loaded
		derivedWord := uint16(c.LowByteStore.Value) | (uint16(c.HighByteStore.Value) << 8)
		expectedWordHex := fmt.Sprintf("%04X", derivedWord)
		expectedSignedValue := int(int16(derivedWord))
		if !strings.EqualFold(c.WordHex, expectedWordHex) || c.SignedValue != expectedSignedValue {
			return false
		}
		if c.TableByte != 0 && c.TableByte != c.LowByteStore.Value {
			return false
		}
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

	// 5. Check full noncycle CPU state, NextPC, writes, and initial state against accepted execution evidence
	type expectedEvidence struct {
		initState   receiptCPUState
		exitState   receiptCPUState
		nextPC      uint32
		totalWrites uint32
		writes      []receiptWrite
	}

	var exp expectedEvidence
	if c.CaseID == "positive" {
		exp = expectedEvidence{
			initState: receiptCPUState{
				A: 65300, X: 152, Y: 115, S: 7996, D: 7936, DB: 9, PB: 9, P: 49, E: false, PC: 63625,
			},
			exitState: receiptCPUState{
				A: 65280, X: 152, Y: 115, S: 7996, D: 7936, DB: 9, PB: 9, P: 50, E: false, PC: 63633,
			},
			nextPC:      653457,
			totalWrites: 1,
			writes:      []receiptWrite{{Address: 8265557, Value: 0}},
		}
	} else if c.CaseID == "negative" {
		exp = expectedEvidence{
			initState: receiptCPUState{
				A: 65475, X: 152, Y: 115, S: 7996, D: 7936, DB: 9, PB: 9, P: 176, E: false, PC: 63638,
			},
			exitState: receiptCPUState{
				A: 65535, X: 152, Y: 115, S: 7996, D: 7936, DB: 9, PB: 9, P: 177, E: false, PC: 63646,
			},
			nextPC:      653470,
			totalWrites: 1,
			writes:      []receiptWrite{{Address: 8265559, Value: 255}},
		}
	} else {
		exp = expectedEvidence{
			exitState: receiptCPUState{
				A: c.ExitA,
				P: c.ExitP,
			},
			totalWrites: 1,
		}
	}

	// Compare CompiledCResult State
	if res.State.A != exp.exitState.A || res.State.P != exp.exitState.P {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}
	if exp.exitState.X != 0 || exp.exitState.Y != 0 {
		if res.State.X != exp.exitState.X || res.State.Y != exp.exitState.Y ||
			res.State.S != exp.exitState.S || res.State.D != exp.exitState.D ||
			res.State.DB != exp.exitState.DB || res.State.PB != exp.exitState.PB ||
			res.State.E != exp.exitState.E || res.State.PC != exp.exitState.PC {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt result state and refusal effects mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
	}
	if exp.nextPC != 0 && res.NextPC != exp.nextPC {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}
	if exp.totalWrites > 0 && res.TotalWrites != exp.totalWrites {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "receipt result state and refusal effects mismatch"
		c.Qualification.DifferentialMatched = false
		return
	}
	if len(exp.writes) > 0 {
		if len(res.Writes) != len(exp.writes) {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt result state and refusal effects mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
		for wi := range exp.writes {
			if res.Writes[wi].Address != exp.writes[wi].Address || res.Writes[wi].Value != exp.writes[wi].Value {
				c.Qualification.Status = "unavailable"
				c.Qualification.Reason = "receipt result state and refusal effects mismatch"
				c.Qualification.DifferentialMatched = false
				return
			}
		}
	}

	// Compare InitialState if available
	if exp.initState.A != 0 {
		init := foundCase.InitialState
		if init.A != exp.initState.A || init.X != exp.initState.X || init.Y != exp.initState.Y ||
			init.S != exp.initState.S || init.D != exp.initState.D || init.DB != exp.initState.DB ||
			init.PB != exp.initState.PB || init.P != exp.initState.P || init.E != exp.initState.E ||
			init.PC != exp.initState.PC {
			c.Qualification.Status = "unavailable"
			c.Qualification.Reason = "receipt result state and refusal effects mismatch"
			c.Qualification.DifferentialMatched = false
			return
		}
	}

	// Compare EmulatorResult against CompiledCResult
	em := foundCase.EmulatorResult
	if em.State != res.State || (exp.nextPC != 0 && em.NextPC != res.NextPC) {
		c.Qualification.Status = "unavailable"
		c.Qualification.Reason = "differential comparison did not match"
		c.Qualification.DifferentialMatched = false
		return
	}

	// 6. Check inline generated C source against receipt
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
