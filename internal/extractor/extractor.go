package extractor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Config defines the input configuration for the extractor.
type Config struct {
	Candidate               Candidate
	ExpectedROMSHA256       string
	FixturePath             string
	FixtureReceiptPath      string
	FixtureSummaryPath      string
	CapturePath             string
	CaptureReceiptPath      string
	CaptureSummaryPath      string
	HistoryPath             string
	HistoryReceiptPath      string
	HistorySummaryPath      string
	CheckpointPath          string
	CheckpointAbsoluteFrame int
	ROMPath                 string
	InputsPath              string
	StartBoundary           string
	MinFrame                int
	CasePrefix              string
	CorpusLabel             string
	CorpusName              string
}

// ExtractionResult contains the complete output of an extraction run.
type ExtractionResult struct {
	Cases            []RoutineCaseV1
	TrustRoot        ProposedTrustRoot
	Receipt          ExtractionReceipt
	NegativeControls []RoutineCaseV1
	SwapControls     []SwapControlSpec
}

func instructionsEqual(a, b *RawInsn) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Seq != b.Seq || a.Length != b.Length || a.Status != b.Status {
		return false
	}
	if a.Entry != b.Entry || a.Exit != b.Exit {
		return false
	}
	if len(a.Fetches) != len(b.Fetches) {
		return false
	}
	for i := range a.Fetches {
		if a.Fetches[i] != b.Fetches[i] {
			return false
		}
	}
	return true
}

// deriveLoROMOffset maps a 24-bit SNES CPU address to its corresponding LoROM file offset.
// LoROM maps CPU banks 00-7D and 80-FF with addresses 8000-FFFF to 32KB ROM banks.
// Addresses < 0x8000 or banks 7E/7F (WRAM) do not map to ROM and return an error.
func deriveLoROMOffset(cpuAddr uint32) (uint32, error) {
	bank := uint8(cpuAddr >> 16)
	addr := uint16(cpuAddr & 0xFFFF)
	if bank == 0x7E || bank == 0x7F {
		return 0, fmt.Errorf("cpu address 0x%06X is in WRAM, not ROM", cpuAddr)
	}
	if addr < 0x8000 {
		return 0, fmt.Errorf("cpu address 0x%06X offset 0x%04X is below 0x8000 (not LoROM ROM area)", cpuAddr, addr)
	}
	loromBank := bank & 0x7F
	offset := uint32(loromBank)*0x8000 + uint32(addr&0x7FFF)
	return offset, nil
}

// validateInstructionFetches strictly verifies that instruction fetches:
// 1. Have contiguous addresses matching Entry.PC
// 2. Have valid roles ("opcode" for fetch 0, "operand" for subsequent fetches)
// 3. Derive correct LoROM offsets without relying on unvalidated trace metadata
// 4. If trace supplies ROMOffset, ensure it strictly matches the derived LoROM offset
// 5. Are within ROM slice bounds
// 6. Exactly match the actual bytes in the loaded ROM image at the derived offset
func validateInstructionFetches(insn *RawInsn, romBytes []byte) error {
	if insn == nil {
		return fmt.Errorf("nil instruction")
	}
	if len(insn.Fetches) == 0 {
		return fmt.Errorf("missing instruction fetches")
	}
	for idx, fetch := range insn.Fetches {
		expectedAddr := (uint32(insn.Entry.PB) << 16) | uint32(insn.Entry.PC+uint16(idx))
		if fetch.Addr != expectedAddr {
			return fmt.Errorf("fetch_addr_mismatch:got=0x%06X,want=0x%06X", fetch.Addr, expectedAddr)
		}
		if fetch.Role != "" {
			if idx == 0 && fetch.Role != "opcode" {
				return fmt.Errorf("invalid_fetch_role:got=%s,want=opcode", fetch.Role)
			}
			if idx > 0 && fetch.Role != "operand" {
				return fmt.Errorf("invalid_fetch_role:got=%s,want=operand", fetch.Role)
			}
		}

		derivedOffset, err := deriveLoROMOffset(fetch.Addr)
		if err != nil {
			return fmt.Errorf("lorom_mapping_error:%w", err)
		}
		if fetch.ROMOffset >= uint32(len(romBytes)) || int(derivedOffset) >= len(romBytes) {
			return fmt.Errorf("rom_offset_out_of_bounds:0x%X>=0x%X", derivedOffset, len(romBytes))
		}
		if fetch.ROMOffset != 0 && fetch.ROMOffset != derivedOffset {
			return fmt.Errorf("forged_or_mismatched_rom_offset:supplied=0x%X,derived=0x%X", fetch.ROMOffset, derivedOffset)
		}
		if romBytes[derivedOffset] != fetch.Value {
			return fmt.Errorf("rom_fetch_mismatch:offset=0x%X,rom=0x%02X,fetch=0x%02X", derivedOffset, romBytes[derivedOffset], fetch.Value)
		}
	}
	return nil
}

func makeFileRef(p string) (*FileRef, error) {
	if p == "" {
		return nil, nil
	}
	h, _, err := FileDigests(p)
	if err != nil {
		return nil, fmt.Errorf("file ref %q: %w", p, err)
	}
	return &FileRef{Path: p, SHA256: h}, nil
}

// Extract executes the complete generic extraction pipeline.
func Extract(cfg Config) (*ExtractionResult, error) {
	cfg.Candidate.Normalize()
	if cfg.Candidate.Entry == 0 {
		return nil, fmt.Errorf("candidate %q has invalid entry address 0", cfg.Candidate.ID)
	}
	if cfg.Candidate.InstructionCount <= 0 {
		return nil, fmt.Errorf("candidate %q has invalid instruction count %d", cfg.Candidate.ID, cfg.Candidate.InstructionCount)
	}

	// 1. Measure and verify ROM digest in a single read (owned in-memory bytes).
	romBytes, err := os.ReadFile(cfg.ROMPath)
	if err != nil {
		return nil, fmt.Errorf("read ROM %q: %w", cfg.ROMPath, err)
	}
	romSum := sha256.Sum256(romBytes)
	romSHA := hex.EncodeToString(romSum[:])
	if cfg.ExpectedROMSHA256 != "" && romSHA != cfg.ExpectedROMSHA256 {
		return nil, fmt.Errorf("rom digest mismatch: got %s, want %s", romSHA, cfg.ExpectedROMSHA256)
	}

	if cfg.Candidate.Dispatch != nil {
		if err := validateDispatchContract(cfg.Candidate.Dispatch, &cfg.Candidate, romBytes); err != nil {
			return nil, fmt.Errorf("dispatch contract validation: %w", err)
		}
	}
	if cfg.Candidate.Connected != nil {
		if err := validateConnectedContract(cfg.Candidate.Connected, &cfg.Candidate, romBytes); err != nil {
			return nil, fmt.Errorf("connected contract validation: %w", err)
		}
	}

	// 2. Ingest History stream and compute digests in a single stream pass (zero reopen).
	histScanner, histFinalize, err := OpenHashedStream(cfg.HistoryPath)
	if err != nil {
		return nil, fmt.Errorf("open history stream %q: %w", cfg.HistoryPath, err)
	}
	history, err := NewWriteHistory(histScanner)
	if err != nil {
		histFinalize()
		return nil, fmt.Errorf("load write history: %w", err)
	}
	histRawSHA, histDecSHA, err := histFinalize()
	if err != nil {
		return nil, fmt.Errorf("finalize history stream digests: %w", err)
	}
	if _, err := VerifyReceiptDigest(cfg.HistoryReceiptPath, cfg.HistoryPath, histRawSHA, histDecSHA); err != nil {
		return nil, fmt.Errorf("verify history receipt: %w", err)
	}

	historyCoverage, err := loadProducerCoverage(cfg.HistorySummaryPath, cfg.HistoryReceiptPath, histRawSHA, histDecSHA, romSHA, false)
	if err != nil {
		return nil, err
	}

	// 3. Ingest Checkpoint if provided, verifying ROM hash, version, exact 128KB WRAM, and deep copying (single read).
	var checkpointRef *CheckpointRef
	if cfg.CheckpointPath != "" {
		wram, cpSHA, err := LoadCheckpointWRAM(cfg.CheckpointPath, romSHA)
		if err != nil {
			return nil, fmt.Errorf("load checkpoint %q: %w", cfg.CheckpointPath, err)
		}
		history.SetPinnedWRAM(wram)

		powRef, err := makeFileRef(cfg.HistorySummaryPath)
		if err != nil {
			return nil, fmt.Errorf("checkpoint power-on history summary: %w", err)
		}
		checkpointRef = &CheckpointRef{
			Path:                  cfg.CheckpointPath,
			SHA256:                cpSHA,
			AbsoluteFrame:         cfg.CheckpointAbsoluteFrame,
			PowerOnHistorySummary: powRef,
		}
	}

	// 4. Ingest Capture stream and compute digests in a single stream pass.
	capScanner, capFinalize, err := OpenHashedStream(cfg.CapturePath)
	if err != nil {
		return nil, fmt.Errorf("open capture stream %q: %w", cfg.CapturePath, err)
	}

	var (
		insnsBySeq    = make(map[uint64]*RawEvent)
		transitions   []*RawEvent
		busEvents     BusEventList
		lastInsnSeq   uint64
		hasLastSeq    bool
		capCount      int
		captureHeader *producerHeader
	)

	for capScanner.Scan() {
		capCount++
		if capCount > MaxCaptureEvents {
			capFinalize()
			return nil, fmt.Errorf("capture event limit exceeded: %d > %d", capCount, MaxCaptureEvents)
		}

		line := capScanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev RawEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			capFinalize()
			return nil, fmt.Errorf("unmarshal capture event: %w", err)
		}
		if ev.Kind == "run" {
			if captureHeader != nil || capCount != 1 {
				capFinalize()
				return nil, fmt.Errorf("producer coverage: duplicate run header")
			}
			var h producerHeader
			if err := json.Unmarshal(line, &h); err != nil {
				capFinalize()
				return nil, err
			}
			captureHeader = &h
		}
		switch ev.Kind {
		case "cpu_insn":
			if ev.Insn != nil {
				seq := ev.Insn.Seq
				if hasLastSeq && seq <= lastInsnSeq {
					if seq == lastInsnSeq {
						if existing, ok := insnsBySeq[seq]; ok && !instructionsEqual(existing.Insn, ev.Insn) {
							capFinalize()
							return nil, fmt.Errorf("conflicting duplicate cpu_insn at seq %d", seq)
						}
						continue
					}
					capFinalize()
					return nil, fmt.Errorf("non-monotonic cpu_insn seq: %d follows %d", seq, lastInsnSeq)
				}
				if existing, ok := insnsBySeq[seq]; ok {
					if !instructionsEqual(existing.Insn, ev.Insn) {
						capFinalize()
						return nil, fmt.Errorf("conflicting duplicate cpu_insn at seq %d", seq)
					}
					continue
				}
				insnsBySeq[seq] = &ev
				lastInsnSeq = seq
				hasLastSeq = true
			}
		case "cpu_transition", "dma", "hdma", "mmio":
			transitions = append(transitions, &ev)
		case "bus":
			ev.Raw = append([]byte(nil), line...)
			busEvents = append(busEvents, &ev)
		}
	}
	if err := capScanner.Err(); err != nil {
		capFinalize()
		return nil, fmt.Errorf("scan capture: %w", err)
	}

	capRawSHA, capDecSHA, err := capFinalize()
	if err != nil {
		return nil, fmt.Errorf("finalize capture stream digests: %w", err)
	}
	if _, err := VerifyReceiptDigest(cfg.CaptureReceiptPath, cfg.CapturePath, capRawSHA, capDecSHA); err != nil {
		return nil, fmt.Errorf("verify capture receipt: %w", err)
	}

	captureCoverage, err := loadProducerCoverage(cfg.CaptureSummaryPath, cfg.CaptureReceiptPath, capRawSHA, capDecSHA, romSHA, true)
	if err != nil {
		return nil, err
	}
	if err := captureCoverage.checkHeader(captureHeader); err != nil {
		return nil, err
	}
	if err := captureCoverage.checkEngine(historyCoverage); err != nil {
		return nil, err
	}
	sort.Sort(busEvents)

	candPB := uint8((cfg.Candidate.Entry >> 16) & 0xFF)
	candPC := uint16(cfg.Candidate.Entry & 0xFFFF)
	expectedInsnCount := cfg.Candidate.InstructionCount

	// Find all candidate entry sequences.
	var candidateSeqs []uint64
	for seq, ev := range insnsBySeq {
		if ev.Frame < cfg.MinFrame {
			continue
		}
		if ev.Insn != nil && ev.Insn.Entry.PB == candPB && ev.Insn.Entry.PC == candPC {
			candidateSeqs = append(candidateSeqs, seq)
		}
	}
	sort.Slice(candidateSeqs, func(i, j int) bool { return candidateSeqs[i] < candidateSeqs[j] })

	// Build sets of required seq numbers:
	// - body seqs to verify against fixture
	// - call predecessor seqs (seq - 1)
	// - return successor seqs (seq + expectedInsnCount)
	neededSeqs := make(map[uint64]bool)
	bodySeqs := make(map[uint64]bool)
	callSeqToEntry := make(map[uint64]uint64)
	retSeqToEntry := make(map[uint64]uint64)

	for _, entrySeq := range candidateSeqs {
		if cfg.Candidate.Dispatch != nil {
			disp := cfg.Candidate.Dispatch
			predCount := 1 + 2 + disp.HelperCount
			callSeq := entrySeq - uint64(predCount)
			retSeq := entrySeq + uint64(expectedInsnCount)
			for s := callSeq; s < entrySeq; s++ {
				neededSeqs[s] = true
			}
			neededSeqs[retSeq] = true
			callSeqToEntry[callSeq] = entrySeq
			retSeqToEntry[retSeq] = entrySeq
			for i := 0; i < expectedInsnCount; i++ {
				bSeq := entrySeq + uint64(i)
				neededSeqs[bSeq] = true
				bodySeqs[bSeq] = true
			}
		} else if cfg.Candidate.Connected != nil {
			callSeq := entrySeq - 1
			neededSeqs[callSeq] = true
			callSeqToEntry[callSeq] = entrySeq
			maxLen := 50
			for i := 0; i <= maxLen; i++ {
				bSeq := entrySeq + uint64(i)
				neededSeqs[bSeq] = true
				bodySeqs[bSeq] = true
			}
		} else {
			callSeq := entrySeq - 1
			retSeq := entrySeq + uint64(expectedInsnCount)
			neededSeqs[callSeq] = true
			neededSeqs[retSeq] = true
			callSeqToEntry[callSeq] = entrySeq
			retSeqToEntry[retSeq] = entrySeq
			for i := 0; i < expectedInsnCount; i++ {
				bSeq := entrySeq + uint64(i)
				neededSeqs[bSeq] = true
				bodySeqs[bSeq] = true
			}
		}
	}

	// 5. Ingest Fixture stream in a single streaming pass:
	// - Verify header and extract engine revision
	// - Verify all candidate body instructions against corresponding pinned fixture instructions
	// - Retrieve predecessor call and successor return instructions when not present in capture
	fixScanner, fixFinalize, err := OpenHashedStream(cfg.FixturePath)
	if err != nil {
		return nil, fmt.Errorf("open fixture stream %q: %w", cfg.FixturePath, err)
	}
	if !fixScanner.Scan() {
		fixFinalize()
		return nil, fmt.Errorf("fixture %q is empty", cfg.FixturePath)
	}
	var fixHeader struct {
		Run struct {
			EngineRevision string `json:"engine_revision"`
		} `json:"run"`
	}
	if err := json.Unmarshal(fixScanner.Bytes(), &fixHeader); err != nil {
		fixFinalize()
		return nil, fmt.Errorf("parse fixture header %q: %w", cfg.FixturePath, err)
	}
	if fixHeader.Run.EngineRevision == "" {
		fixFinalize()
		return nil, fmt.Errorf("fixture header %q missing run.engine_revision", cfg.FixturePath)
	}
	engineRev := fixHeader.Run.EngineRevision

	fixtureInsnsBySeq := make(map[uint64]*RawInsn) // keyed by seq

	for fixScanner.Scan() {
		line := fixScanner.Bytes()
		if len(line) == 0 || !bytes.Contains(line, []byte(`"cpu_insn"`)) {
			continue
		}
		var ev RawEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			fixFinalize()
			return nil, fmt.Errorf("unmarshal fixture event: %w", err)
		}
		if ev.Kind != "cpu_insn" || ev.Insn == nil {
			continue
		}
		seq := ev.Insn.Seq
		if !neededSeqs[seq] {
			continue
		}

		insnCopy := *ev.Insn
		fixtureInsnsBySeq[seq] = &insnCopy
	}
	if err := fixScanner.Err(); err != nil {
		fixFinalize()
		return nil, fmt.Errorf("scan fixture: %w", err)
	}

	fixRawSHA, fixDecSHA, err := fixFinalize()
	if err != nil {
		return nil, fmt.Errorf("finalize fixture stream digests: %w", err)
	}
	if _, err := VerifyReceiptDigest(cfg.FixtureReceiptPath, cfg.FixturePath, fixRawSHA, fixDecSHA); err != nil {
		return nil, fmt.Errorf("verify fixture receipt: %w", err)
	}

	// Build file references with explicit error propagation.
	fixReceiptRef, err := makeFileRef(cfg.FixtureReceiptPath)
	if err != nil {
		return nil, fmt.Errorf("fixture receipt ref: %w", err)
	}
	fixSummaryRef, err := makeFileRef(cfg.FixtureSummaryPath)
	if err != nil {
		return nil, fmt.Errorf("fixture summary ref: %w", err)
	}
	capReceiptRef := captureCoverage.receiptRef
	capSummaryRef := captureCoverage.summaryRef
	histReceiptRef := historyCoverage.receiptRef
	histSummaryRef := historyCoverage.summaryRef
	inputsRef, err := makeFileRef(cfg.InputsPath)
	if err != nil {
		return nil, fmt.Errorf("inputs ref: %w", err)
	}

	fixtureRef := FixtureRef{
		Path:               cfg.FixturePath,
		SHA256:             fixRawSHA,
		DecompressedSHA256: fixDecSHA,
		EngineRevision:     engineRev,
		Receipt:            fixReceiptRef,
		Summary:            fixSummaryRef,
	}
	captureRef := StreamRef{
		Path:    cfg.CapturePath,
		SHA256:  capRawSHA,
		Receipt: capReceiptRef,
		Summary: capSummaryRef,
	}
	historyRef := StreamRef{
		Path:    cfg.HistoryPath,
		SHA256:  histRawSHA,
		Receipt: histReceiptRef,
		Summary: histSummaryRef,
	}

	var (
		emittedCases   []RoutineCaseV1
		totalEntryHits int
		refusalReasons = make(map[string]int)
	)

	recordRefusal := func(reason string) {
		refusalReasons[reason]++
	}

	startBoundary := cfg.StartBoundary
	if startBoundary == "" && cfg.CheckpointPath != "" {
		startBoundary = "restored_state"
	}

	// Validate each candidate entry occurrence.
	for _, seq := range candidateSeqs {
		entryEv := insnsBySeq[seq]
		totalEntryHits++

		if cfg.Candidate.Connected != nil {
			conn := cfg.Candidate.Connected

			if entryEv.Insn == nil {
				recordRefusal("missing_candidate_entry_insn")
				continue
			}
			if entryEv.Insn.Status != "retired" {
				recordRefusal("unretired_entry_insn")
				continue
			}
			if entryEv.Insn.Entry.PB != candPB || entryEv.Insn.Entry.PC != candPC {
				recordRefusal("entry_pc_mismatch")
				continue
			}
			if entryEv.Insn.Entry.S != conn.ExpectedEntryS {
				recordRefusal(fmt.Sprintf("entry_s_mismatch: got 0x%04X, want 0x%04X", entryEv.Insn.Entry.S, conn.ExpectedEntryS))
				continue
			}

			// Verify caller stack write in history (real history-bus outer boundary)
			addrLow := uint32(0x7E0000) | uint32(conn.ExpectedEntryS+1)  // 0x7E01FA
			addrHigh := uint32(0x7E0000) | uint32(conn.ExpectedEntryS+2) // 0x7E01FB
			wLow, hasLow := history.latestWriteBefore(addrLow, entryEv.Insn.Entry.Cycles)
			wHigh, hasHigh := history.latestWriteBefore(addrHigh, entryEv.Insn.Entry.Cycles)
			if !hasLow || !hasHigh {
				recordRefusal("connected_missing_stack_history")
				continue
			}
			if wLow.Value != conn.StackReturnBytes[0] || wHigh.Value != conn.StackReturnBytes[1] {
				recordRefusal(fmt.Sprintf("connected_stack_history_value_mismatch: got [0x%02X, 0x%02X], want [0x%02X, 0x%02X]",
					wLow.Value, wHigh.Value, conn.StackReturnBytes[0], conn.StackReturnBytes[1]))
				continue
			}
			// Order: high then low (push order: JSR pushes high then low)
			if wHigh.Cycle > wLow.Cycle || wHigh.ID >= wLow.ID {
				recordRefusal("connected_stack_write_order_mismatch")
				continue
			}
			// Actor must be CPU (not DMA or unknown)
			if wHigh.Actor != "cpu" || wLow.Actor != "cpu" {
				recordRefusal("connected_stack_actor_not_cpu")
				continue
			}
			callerBank := byte(conn.CallerPC >> 16)
			callerPC16 := uint16(conn.CallerPC & 0xFFFF)
			if wHigh.CPUPBR != callerBank || wHigh.CPUPC != callerPC16 || wLow.CPUPBR != callerBank || wLow.CPUPC != callerPC16 {
				recordRefusal("connected_caller_pc_mismatch")
				continue
			}
			if wHigh.CPUOpcode != 0x20 || wLow.CPUOpcode != 0x20 {
				recordRefusal("connected_caller_opcode_mismatch")
				continue
			}
			if wHigh.CPUS != conn.ExpectedEntryS+2 || wLow.CPUS != conn.ExpectedEntryS+2 {
				recordRefusal("connected_caller_s_mismatch")
				continue
			}
			callerBytes := make([]byte, 3)
			for i := range callerBytes {
				off, err := deriveLoROMOffset(conn.CallerPC&0xff0000 | uint32(uint16(conn.CallerPC)+uint16(i)))
				if err != nil {
					return nil, fmt.Errorf("connected caller ROM: %w", err)
				}
				if int(off) >= len(romBytes) {
					return nil, fmt.Errorf("connected caller ROM out of bounds")
				}
				callerBytes[i] = romBytes[off]
			}
			if !bytes.Equal(wHigh.CPUBytes, callerBytes) || !bytes.Equal(wLow.CPUBytes, callerBytes) {
				recordRefusal("connected_caller_bytes_mismatch")
				continue
			}
			if !historyContextMatches(wHigh, entryEv.Insn.Entry) || !historyContextMatches(wLow, entryEv.Insn.Entry) {
				recordRefusal("connected_caller_entry_context_mismatch")
				continue
			}

			// Trace body
			var body []*RawInsn
			bodyValid := true
			reachedTerminal := false
			maxSteps := 50

			for i := 0; i < maxSteps; i++ {
				bSeq := seq + uint64(i)
				fixInsn, hasFix := fixtureInsnsBySeq[bSeq]
				if !hasFix {
					recordRefusal("missing_fixture_body_insn")
					bodyValid = false
					break
				}
				capEv, hasCap := insnsBySeq[bSeq]
				if !hasCap || capEv.Insn == nil {
					recordRefusal("missing_capture_body_insn")
					bodyValid = false
					break
				}
				if capEv.Insn.Status != "retired" || fixInsn.Status != "retired" {
					recordRefusal("unretired_body_insn")
					bodyValid = false
					break
				}
				if !instructionsEqual(capEv.Insn, fixInsn) {
					recordRefusal("conflicting_fixture_capture_body_insn")
					bodyValid = false
					break
				}
				insn := capEv.Insn
				insnPC := (uint32(insn.Entry.PB) << 16) | uint32(insn.Entry.PC)

				inSpan := false
				for _, s := range conn.Spans {
					if insnPC >= s.Start && insnPC < s.End {
						inSpan = true
						break
					}
				}
				if !inSpan {
					recordRefusal("connected_out_of_bounds_pc")
					bodyValid = false
					break
				}

				if len(insn.Fetches) > 0 && insn.Fetches[0].Value == 0xDC {
					target := (uint32(insn.Exit.PB) << 16) | uint32(insn.Exit.PC)
					allowedTargets, ok := conn.AllowedIndirectTargets[insnPC]
					if !ok {
						recordRefusal("connected_unmodeled_indirect_site")
						bodyValid = false
						break
					}
					targetAllowed := false
					for _, at := range allowedTargets {
						if target == at {
							targetAllowed = true
							break
						}
					}
					if !targetAllowed {
						recordRefusal("connected_unmodeled_indirect_target")
						bodyValid = false
						break
					}
				}

				body = append(body, insn)

				if insnPC == conn.TerminalReturnPC {
					reachedTerminal = true
					break
				}
			}

			if !bodyValid {
				continue
			}
			if !reachedTerminal {
				recordRefusal("connected_incomplete_closure")
				continue
			}

			pathLen := len(body)
			lenAllowed := false
			for _, l := range conn.AllowedPathLengths {
				if pathLen == l {
					lenAllowed = true
					break
				}
			}
			if !lenAllowed {
				recordRefusal(fmt.Sprintf("connected_unexpected_path_length:%d", pathLen))
				continue
			}

			lastInsn := body[len(body)-1]
			lastInsnPC := (uint32(lastInsn.Entry.PB) << 16) | uint32(lastInsn.Entry.PC)
			if lastInsnPC != conn.TerminalReturnPC {
				recordRefusal("terminal_return_address_mismatch")
				continue
			}
			if len(lastInsn.Fetches) == 0 || lastInsn.Fetches[0].Value != 0x60 {
				recordRefusal("illegal_return_opcode")
				continue
			}
			if lastInsn.Exit.S != lastInsn.Entry.S+2 {
				recordRefusal("invalid_rts_stack_delta")
				continue
			}
			if lastInsn.Exit.S != conn.ExpectedReturnS {
				recordRefusal("return_stack_mismatch")
				continue
			}

			continuationPC := conn.ContinuationPC
			lastExitPC := (uint32(lastInsn.Exit.PB) << 16) | uint32(lastInsn.Exit.PC)
			if lastExitPC != continuationPC {
				recordRefusal("return_successor_mismatch")
				continue
			}

			// If continuation instruction is captured, verify it matches
			var retSeq uint64
			if fixRet, hasFixRet := fixtureInsnsBySeq[lastInsn.Seq+1]; hasFixRet && fixRet.Status == "retired" {
				if capRet, hasCapRet := insnsBySeq[lastInsn.Seq+1]; hasCapRet && capRet.Insn != nil {
					if !instructionsEqual(capRet.Insn, fixRet) {
						recordRefusal("conflicting_fixture_capture_continuation_insn")
						continue
					}
				}
				contPC := (uint32(fixRet.Entry.PB) << 16) | uint32(fixRet.Entry.PC)
				if contPC != conn.ContinuationPC {
					recordRefusal("return_successor_mismatch")
					continue
				}
				retSeq = fixRet.Seq
			}

			for i := 0; i < len(body); i++ {
				insn := body[i]
				if err := validateInstructionFetches(insn, romBytes); err != nil {
					bodyValid = false
					recordRefusal(err.Error())
					break
				}
				if i < len(body)-1 {
					next := body[i+1]
					if insn.Exit.PB != next.Entry.PB || insn.Exit.PC != next.Entry.PC ||
						insn.Exit.A != next.Entry.A || insn.Exit.X != next.Entry.X || insn.Exit.Y != next.Entry.Y ||
						insn.Exit.S != next.Entry.S || insn.Exit.D != next.Entry.D || insn.Exit.DB != next.Entry.DB ||
						insn.Exit.P != next.Entry.P || insn.Exit.E != next.Entry.E {
						bodyValid = false
						recordRefusal("cpu_continuity_break")
						break
					}
					if insn.Exit.Cycles != next.Entry.Cycles {
						bodyValid = false
						recordRefusal("cycle_monotonicity_break")
						break
					}
				}
			}
			if !bodyValid {
				continue
			}

			if conn.DispatcherCallPC != 0 {
				for k, insn := range body {
					insnPC := (uint32(insn.Entry.PB) << 16) | uint32(insn.Entry.PC)
					if insnPC == conn.DispatcherCallPC {
						if k+16 <= len(body) {
							if err := verifyHelperBus(body[k:k+16], busEvents, romBytes); err != nil {
								bodyValid = false
								recordRefusal(fmt.Sprintf("connected_helper_bus_error: %v", err))
								break
							}
						}
						break
					}
				}
				if !bodyValid {
					continue
				}
			}

			checkEnd := lastInsn.Seq
			if retSeq != 0 {
				checkEnd = retSeq
			}
			if err := captureCoverage.checkInterval(insnsBySeq, seq, checkEnd); err != nil {
				recordRefusal(err.Error())
				continue
			}
			if err := checkCoveredAccesses(body, busEvents); err != nil {
				recordRefusal(err.Error())
				continue
			}

			startCycles := body[0].Entry.Cycles
			endCycles := lastInsn.Exit.Cycles

			memTrace := ExtractMemoryTrace(busEvents, transitions, startCycles, endCycles, body)
			if memTrace.RefusalReason != "" {
				recordRefusal(memTrace.RefusalReason)
				continue
			}
			if err := historyCoverage.checkMemory(memTrace.InitialMemory); err != nil {
				recordRefusal(err.Error())
				continue
			}
			memSources, err := history.ClassifyInitialMemory(memTrace.InitialMemory, startCycles, startBoundary)
			if err != nil {
				recordRefusal(err.Error())
				continue
			}

			caseID := fmt.Sprintf("%s%s_seq_%d", cfg.CasePrefix, cfg.Candidate.ID, seq)
			rc := RoutineCaseV1{
				SchemaVersion:           "snes-routine-case-v1",
				CaseID:                  caseID,
				RoutineID:               cfg.Candidate.ID,
				EntryPC:                 cfg.Candidate.Entry,
				ReturnInsnPC:            lastInsnPC,
				ROMSHA256:               romSHA,
				RunID:                   fixRawSHA,
				StreamSHA256:            fixRawSHA,
				EngineRevision:          engineRev,
				Frame:                   entryEv.Frame,
				CallSeq:                 0,
				CallPC:                  conn.CallerPC,
				EntrySeq:                seq,
				ExitSeq:                 lastInsn.Seq,
				ReturnSeq:               retSeq,
				ObservedNextPC:          continuationPC,
				InstructionCount:        len(body),
				InitialState:            body[0].Entry,
				ObservedExitState:       lastInsn.Exit,
				InitialMemory:           memTrace.InitialMemory,
				ObservedWrites:          memTrace.ObservedWrites,
				ObservedEffectsCaptured: false,
				Evidence: CaseEvidence{
					Label:               cfg.CorpusLabel,
					Corpus:              cfg.CorpusName,
					Fixture:             fixtureRef,
					Capture:             captureRef,
					History:             historyRef,
					Inputs:              inputsRef,
					StartBoundary:       startBoundary,
					Checkpoint:          checkpointRef,
					InitialMemorySource: memSources,
				},
			}
			emittedCases = append(emittedCases, rc)
			continue
		}

		// 1. Verify contiguous instruction occurrence in both fixture and capture, and exact equality.
		var body []*RawInsn
		bodyValid := true
		for i := 0; i < expectedInsnCount; i++ {
			bSeq := seq + uint64(i)
			fixInsn, hasFix := fixtureInsnsBySeq[bSeq]
			if !hasFix {
				recordRefusal("missing_fixture_body_insn")
				bodyValid = false
				break
			}
			capEv, hasCap := insnsBySeq[bSeq]
			if !hasCap || capEv.Insn == nil {
				recordRefusal("missing_capture_body_insn")
				bodyValid = false
				break
			}
			if capEv.Insn.Status != "retired" || fixInsn.Status != "retired" {
				recordRefusal("unretired_body_insn")
				bodyValid = false
				break
			}
			if !instructionsEqual(capEv.Insn, fixInsn) {
				recordRefusal("conflicting_fixture_capture_body_insn")
				bodyValid = false
				break
			}
			body = append(body, capEv.Insn)
		}
		if !bodyValid {
			continue
		}

		lastInsn := body[len(body)-1]
		lastInsnPC := (uint32(lastInsn.Entry.PB) << 16) | uint32(lastInsn.Entry.PC)

		var (
			callInsn *RawInsn
			retInsn  *RawInsn
			callSeq  uint64
			retSeq   uint64
		)
		if cfg.Candidate.Dispatch != nil {
			disp := cfg.Candidate.Dispatch
			predCount := 1 + 2 + disp.HelperCount
			callSeq = seq - uint64(predCount)
			retSeq = seq + uint64(expectedInsnCount)

			var predecessors []*RawInsn
			predsValid := true
			for s := callSeq; s < seq; s++ {
				fixPred, hasFix := fixtureInsnsBySeq[s]
				if !hasFix {
					recordRefusal("missing_fixture_predecessor_insn")
					predsValid = false
					break
				}
				if fixPred.Status != "retired" {
					recordRefusal("unretired_predecessor_insn")
					predsValid = false
					break
				}
				if capPred, hasCap := insnsBySeq[s]; hasCap && capPred.Insn != nil {
					if !instructionsEqual(capPred.Insn, fixPred) {
						recordRefusal("conflicting_fixture_capture_predecessor_insn")
						predsValid = false
						break
					}
				}
				predecessors = append(predecessors, fixPred)
			}
			if !predsValid {
				continue
			}

			if err := verifyDispatchOccurrence(cfg.Candidate.Dispatch, predecessors, entryEv, body, history, romBytes, busEvents); err != nil {
				recordRefusal(err.Error())
				continue
			}
			callInsn = predecessors[0]
			fixRet, hasFixRet := fixtureInsnsBySeq[retSeq]
			if hasFixRet && fixRet.Status == "retired" {
				retInsn = fixRet
			}
		} else {
			// 2. Caller predecessor verification (must exist in fixture; if present in capture, must be equal).
			callSeq = seq - 1
			fixCaller, hasFixCall := fixtureInsnsBySeq[callSeq]
			if !hasFixCall {
				recordRefusal("missing_fixture_caller_insn")
				continue
			}
			if fixCaller.Status != "retired" {
				recordRefusal("unretired_caller_insn")
				continue
			}
			if capCaller, hasCapCall := insnsBySeq[callSeq]; hasCapCall && capCaller.Insn != nil {
				if !instructionsEqual(capCaller.Insn, fixCaller) {
					recordRefusal("conflicting_fixture_capture_caller_insn")
					continue
				}
			}
			callInsn = fixCaller

			// Require fetched JSR ($20 / $22) or JSL ($22) opcode and stack/continuation push semantics.
			if len(callInsn.Fetches) == 0 {
				recordRefusal("missing_call_fetches")
				continue
			}
			if err := validateInstructionFetches(callInsn, romBytes); err != nil {
				recordRefusal(err.Error())
				continue
			}
			callOpcode := callInsn.Fetches[0].Value
			switch callOpcode {
			case 0x22: // JSL: pushes 3 bytes (PB, PC-1 high, PC-1 low); S decreases by 3.
				if callInsn.Exit.S != callInsn.Entry.S-3 {
					recordRefusal("invalid_jsl_call_stack_delta")
					continue
				}
				if callInsn.Exit.PB != candPB || callInsn.Exit.PC != candPC {
					recordRefusal("call_predecessor_mismatch")
					continue
				}
			case 0x20: // JSR absolute: pushes 2 bytes (PC-1 high, PC-1 low); S decreases by 2.
				if callInsn.Exit.S != callInsn.Entry.S-2 {
					recordRefusal("invalid_jsr_call_stack_delta")
					continue
				}
				if callInsn.Exit.PB != candPB || callInsn.Exit.PC != candPC {
					recordRefusal("call_predecessor_mismatch")
					continue
				}
			default:
				// Non-call predecessor (e.g. SEP, branch, fallthrough, JMP).
				recordRefusal(fmt.Sprintf("illegal_call_opcode:0x%02X", callOpcode))
				continue
			}

			// 3. Continuation / Return successor verification (must exist in fixture; if present in capture, must be equal).
			retSeq = seq + uint64(expectedInsnCount)
			fixRet, hasFixRet := fixtureInsnsBySeq[retSeq]
			if !hasFixRet {
				recordRefusal("missing_fixture_continuation_insn")
				continue
			}
			if fixRet.Status != "retired" {
				recordRefusal("unretired_continuation_insn")
				continue
			}
			if capRet, hasCapRet := insnsBySeq[retSeq]; hasCapRet && capRet.Insn != nil {
				if !instructionsEqual(capRet.Insn, fixRet) {
					recordRefusal("conflicting_fixture_capture_continuation_insn")
					continue
				}
			}
			retInsn = fixRet
			if len(retInsn.Fetches) > 0 {
				if err := validateInstructionFetches(retInsn, romBytes); err != nil {
					recordRefusal(err.Error())
					continue
				}
			}

			// Verify return instruction opcode and stack delta semantics.
			if len(lastInsn.Fetches) == 0 {
				recordRefusal("missing_return_fetches")
				continue
			}
			retOpcode := lastInsn.Fetches[0].Value
			switch retOpcode {
			case 0x6B: // RTL pulls 3 bytes (PC-1 high, PC-1 low, PB); S increases by 3.
				if lastInsn.Exit.S != lastInsn.Entry.S+3 {
					recordRefusal("invalid_rtl_stack_delta")
					continue
				}
			case 0x60: // RTS pulls 2 bytes (PC-1 high, PC-1 low); S increases by 2.
				if lastInsn.Exit.S != lastInsn.Entry.S+2 {
					recordRefusal("invalid_rts_stack_delta")
					continue
				}
			default:
				recordRefusal(fmt.Sprintf("illegal_return_opcode:0x%02X", retOpcode))
				continue
			}

			// Verify return site matches candidate contract.
			lastInsnPC := (uint32(lastInsn.Entry.PB) << 16) | uint32(lastInsn.Entry.PC)
			if len(cfg.Candidate.Returns) > 0 {
				matchedReturn := false
				for _, r := range cfg.Candidate.Returns {
					if r == lastInsnPC {
						matchedReturn = true
						break
					}
				}
				if !matchedReturn {
					recordRefusal("return_address_mismatch")
					continue
				}
			}

			// Confirm return instruction retired to the successor entry address.
			if retInsn.Entry.PB != lastInsn.Exit.PB || retInsn.Entry.PC != lastInsn.Exit.PC {
				recordRefusal("return_successor_mismatch")
				continue
			}
		}

		// Verify full exit-to-next-entry CPU register/flag continuity and ROM fetches across body.
		bodyValid = true
		for i := 0; i < len(body); i++ {
			insn := body[i]
			// Verify ROM byte match for every instruction fetch.
			if err := validateInstructionFetches(insn, romBytes); err != nil {
				bodyValid = false
				recordRefusal(err.Error())
				break
			}
			if !bodyValid {
				break
			}

			// Verify continuity to next instruction in body.
			if i < len(body)-1 {
				next := body[i+1]
				if insn.Exit.PB != next.Entry.PB || insn.Exit.PC != next.Entry.PC ||
					insn.Exit.A != next.Entry.A || insn.Exit.X != next.Entry.X || insn.Exit.Y != next.Entry.Y ||
					insn.Exit.S != next.Entry.S || insn.Exit.D != next.Entry.D || insn.Exit.DB != next.Entry.DB ||
					insn.Exit.P != next.Entry.P || insn.Exit.E != next.Entry.E {
					bodyValid = false
					recordRefusal("cpu_continuity_break")
					break
				}
				if insn.Exit.Cycles > next.Entry.Cycles {
					bodyValid = false
					recordRefusal("cycle_monotonicity_break")
					break
				}
			}
		}
		if !bodyValid {
			continue
		}

		// Straight-line leaf verification: ensure no branches or calls inside leaf routine.
		if cfg.Candidate.Kind == "leaf" {
			leafShapeValid := true
			for i := 0; i < len(body)-1; i++ {
				// Body instructions prior to return must retire to sequential PC.
				expectedSequentialPC := body[i].Entry.PC + uint16(body[i].Length)
				if body[i].Exit.PB != body[i].Entry.PB || body[i].Exit.PC != expectedSequentialPC {
					leafShapeValid = false
					recordRefusal("leaf_branch_or_call_unsupported")
					break
				}
			}
			if !leafShapeValid {
				continue
			}
		}

		var (
			callPC         uint32
			callSeqNum     uint64
			observedNextPC uint32
			returnSeqNum   uint64
			exitSeq        = seq + uint64(expectedInsnCount) - 1
		)
		if cfg.Candidate.Dispatch != nil {
			callPC = cfg.Candidate.Dispatch.CallerPC
			if callInsn != nil {
				callPC = (uint32(callInsn.Entry.PB) << 16) | uint32(callInsn.Entry.PC)
				callSeqNum = callInsn.Seq
			}
			observedNextPC = cfg.Candidate.Dispatch.ContinuationPC
			if retInsn != nil {
				returnSeqNum = retInsn.Seq
			}
			checkEnd := exitSeq
			if returnSeqNum != 0 {
				checkEnd = returnSeqNum
			}
			if err := captureCoverage.checkInterval(insnsBySeq, callSeqNum, checkEnd); err != nil {
				recordRefusal(err.Error())
				continue
			}
			if err := checkCoveredAccesses(append([]*RawInsn{callInsn}, body...), busEvents); err != nil {
				recordRefusal(err.Error())
				continue
			}
		} else {
			callPC = (uint32(callInsn.Entry.PB) << 16) | uint32(callInsn.Entry.PC)
			callSeqNum = callInsn.Seq
			observedNextPC = (uint32(lastInsn.Exit.PB) << 16) | uint32(lastInsn.Exit.PC)
			returnSeqNum = retSeq
			if err := captureCoverage.checkInterval(insnsBySeq, callSeq, retSeq); err != nil {
				recordRefusal(err.Error())
				continue
			}
			if err := checkCoveredAccesses(append([]*RawInsn{callInsn}, body...), busEvents); err != nil {
				recordRefusal(err.Error())
				continue
			}
		}

		// Join memory trace within [entry.Cycles, exit.Cycles].
		startCycles := body[0].Entry.Cycles
		endCycles := lastInsn.Exit.Cycles

		memTrace := ExtractMemoryTrace(busEvents, transitions, startCycles, endCycles, body)
		if memTrace.RefusalReason != "" {
			recordRefusal(memTrace.RefusalReason)
			continue
		}

		if err := historyCoverage.checkMemory(memTrace.InitialMemory); err != nil {
			recordRefusal(err.Error())
			continue
		}

		// Classify initial memory against write history and pinned state.
		memSources, err := history.ClassifyInitialMemory(memTrace.InitialMemory, startCycles, startBoundary)
		if err != nil {
			recordRefusal(err.Error())
			continue
		}

		caseID := fmt.Sprintf("%s%s_seq_%d", cfg.CasePrefix, cfg.Candidate.ID, seq)

		rc := RoutineCaseV1{
			SchemaVersion:           "snes-routine-case-v1",
			CaseID:                  caseID,
			RoutineID:               cfg.Candidate.ID,
			EntryPC:                 cfg.Candidate.Entry,
			ReturnInsnPC:            lastInsnPC,
			ROMSHA256:               romSHA,
			RunID:                   fixRawSHA,
			StreamSHA256:            fixRawSHA,
			EngineRevision:          engineRev,
			Frame:                   entryEv.Frame,
			CallSeq:                 callSeqNum,
			CallPC:                  callPC,
			EntrySeq:                seq,
			ExitSeq:                 exitSeq,
			ReturnSeq:               returnSeqNum,
			ObservedNextPC:          observedNextPC,
			InstructionCount:        expectedInsnCount,
			InitialState:            body[0].Entry,
			ObservedExitState:       lastInsn.Exit,
			InitialMemory:           memTrace.InitialMemory,
			ObservedWrites:          memTrace.ObservedWrites,
			ObservedEffectsCaptured: false,
			Evidence: CaseEvidence{
				Label:               cfg.CorpusLabel,
				Corpus:              cfg.CorpusName,
				Fixture:             fixtureRef,
				Capture:             captureRef,
				History:             historyRef,
				Inputs:              inputsRef,
				StartBoundary:       startBoundary,
				Checkpoint:          checkpointRef,
				InitialMemorySource: memSources,
			},
		}

		emittedCases = append(emittedCases, rc)
	}

	rejectedExecutions := totalEntryHits - len(emittedCases)
	var (
		negControls  []RoutineCaseV1
		swapControls []SwapControlSpec
	)
	if len(emittedCases) > 0 {
		negControls = GenerateNegativeControls(emittedCases[0])
		swapControls = GenerateSwapControls(emittedCases, 10)
	}

	trustRoot := ProposedTrustRoot{
		Label:      cfg.CorpusLabel,
		Corpus:     cfg.CorpusName,
		ROMSHA256:  romSHA,
		Fixture:    fixtureRef,
		Capture:    captureRef,
		History:    historyRef,
		Checkpoint: checkpointRef,
		Inputs:     inputsRef,
		Status:     "PROPOSED_NOT_CONSUMER_ADMITTED",
	}

	receipt := ExtractionReceipt{
		RoutineID:          cfg.Candidate.ID,
		ROMSHA256:          romSHA,
		FixtureSHA256:      fixRawSHA,
		FixtureDecSHA256:   fixDecSHA,
		CaptureSHA256:      capRawSHA,
		HistorySHA256:      histRawSHA,
		TotalEntryHits:     totalEntryHits,
		CompleteExecutions: len(emittedCases),
		RejectedExecutions: rejectedExecutions,
		RefusalReasons:     refusalReasons,
		NegativeControls:   len(negControls),
		SwapControls:       len(swapControls),
	}

	return &ExtractionResult{
		Cases:            emittedCases,
		TrustRoot:        trustRoot,
		Receipt:          receipt,
		NegativeControls: negControls,
		SwapControls:     swapControls,
	}, nil
}

func historyContextMatches(w HistoryWrite, e CPUState) bool {
	return w.CPUA == e.A && w.CPUX == e.X && w.CPUY == e.Y && w.CPUD == e.D && w.CPUDB == e.DB && w.CPUP == e.P && w.CPUE == e.E
}
