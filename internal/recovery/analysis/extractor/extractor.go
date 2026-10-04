package extractor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

const (
	// PinnedStreamSHA256 is the cryptographic SHA256 of the authenticated USA natural producer capture trace.
	PinnedStreamSHA256 = "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421"

	// PinnedROMSHA256 is the cryptographic SHA256 of the authentic USA Zelda 3 ROM.
	PinnedROMSHA256 = "66871d66be19ad2c34c927d6b14cd8eb6fc3181965b6e517cb361f7316009cfb"

	// AdmittedEngineRevision is the admitted engine revision from authentic run headers.
	AdmittedEngineRevision = "4bc98a31c53b7936b287e569dd776b49e10194da"

	// AdmittedInitialStateSHA256 is the admitted initial state digest from authentic run headers.
	AdmittedInitialStateSHA256 = "4a91f6ebf622a98480629f71306dc5ffc2b91da88b1a41d31ecab44d5de9cbf7"
)

// Config controls span extraction and replay qualification.
type Config struct {
	TracePath       string
	ROMPath         string
	ProjectDir      string
	MinSpanLength   int
	MaxSpans        int
	CandidateBudget int
	StepBudget      int
}

// DefaultConfig returns the standard configuration targeting the natural producer capture.
func DefaultConfig() Config {
	return Config{
		TracePath:       "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl",
		ROMPath:         "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc",
		ProjectDir:      "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project",
		MinSpanLength:   2,
		MaxSpans:        20,
		CandidateBudget: 250,
		StepBudget:      5000,
	}
}

// QualifiedSpan holds the complete verified artifacts of an admitted straight-line trace span.
type QualifiedSpan struct {
	BlockID          string              `json:"block_id"`
	StartAddress     uint32              `json:"start_address"`
	EndAddress       uint32              `json:"end_address"`
	ROMBank          uint8               `json:"rom_bank"` // Physical 32K bank (ROM offset / 0x8000)
	InstructionCount int                 `json:"instruction_count"`
	PhysicalStarts   []uint32            `json:"physical_starts"` // Admitted fetched physical ROM offsets
	StartSeq         uint64              `json:"start_seq"`
	EndSeq           uint64              `json:"end_seq"`
	StartEventID     uint64              `json:"start_event_id"`
	EndEventID       uint64              `json:"end_event_id"`
	ReplayCase       decomp.ReplayCase   `json:"replay_case"`
	InitialMemory    []decomp.MemoryCell `json:"initial_memory"`
	GeneratedC       string              `json:"generated_c"`
	EmulatorResult   decomp.ExecResult   `json:"emulator_result"`
	CompiledCResult  decomp.ExecResult   `json:"compiled_c_result"`
}

// RefusedSpan records an attempted span that was refused due to unknown memory or invalid contract.
type RefusedSpan struct {
	StartAddress uint32 `json:"start_address"`
	Length       int    `json:"length"`
	StartSeq     uint64 `json:"start_seq"`
	Reason       string `json:"reason"`
}

// MismatchedSpan records an attempted span where differential execution diverged.
type MismatchedSpan struct {
	StartAddress uint32 `json:"start_address"`
	Length       int    `json:"length"`
	StartSeq     uint64 `json:"start_seq"`
	Reason       string `json:"reason"`
}

// Accounting summarizes extraction, filtering, qualification, and refusal totals.
type Accounting struct {
	StreamSHA256         string           `json:"stream_sha256"`
	ROMSHA256            string           `json:"rom_sha256"`
	TotalEventsScanned   int              `json:"total_events_scanned"`
	TotalRetirements     int              `json:"total_retirements"`
	CandidateSpansFound  int              `json:"candidate_spans_found"`
	SelectedSpans        int              `json:"selected_spans"`
	RejectedSpans        int              `json:"rejected_spans"`
	RefusedSpans         []RefusedSpan    `json:"refused_spans"`
	MismatchedSpans      []MismatchedSpan `json:"mismatched_spans"`
	QualifiedSpans       []QualifiedSpan  `json:"qualified_spans"`
	UniquePhysicalStarts []uint32         `json:"unique_physical_starts"` // Unique physical ROM offsets
	UniqueROMBanks       []uint8          `json:"unique_rom_banks"`       // Unique 32K physical banks
}

// IsExcludedROMOffset checks if an offset falls in accepted lookup, stack, 139220, C120,
// or B5 qualified C435/C45B/C47B connected regions.
func IsExcludedROMOffset(off uint32) bool {
	// Bank 0 connected region: [00:8781..00:87BD] -> LoROM offset [0x00781..0x007BD]
	if off >= 0x00781 && off <= 0x007BD {
		return true
	}
	// Bank 9 lookup & B5 profile: [09:F864..09:F8DF] -> LoROM offset [0x4F864..0x4F8DF]
	if off >= 0x4F864 && off <= 0x4F8DF {
		return true
	}
	// Bank 0C C120 spans: [0C:C120..0C:C134] -> LoROM offset [0x64120..0x64134]
	if off >= 0x64120 && off <= 0x64134 {
		return true
	}
	// Bank 0C lookup/stack/139220 and B5 C435/C45B/C47B connected regions:
	// [0C:C404..0C:C47B] -> LoROM offset [0x64404..0x6447B]
	if off >= 0x64404 && off <= 0x6447B {
		return true
	}
	return false
}

// IsExcludedAddress checks if an address maps to an excluded ROM offset.
func IsExcludedAddress(addr uint32) bool {
	romOff, ok := SnesLoROMOffset(addr, 0x400000)
	if ok && IsExcludedROMOffset(uint32(romOff)) {
		return true
	}
	return false
}

// IsStopInstruction checks if an opcode is a subroutine call, indirect jump, return, or branch.
func IsStopInstruction(op uint8) bool {
	switch op {
	case 0x20, 0xFC, 0x22: // JSR, JSL
		return true
	case 0x6C, 0x7C, 0xDC: // JMP (abs), JMP (abs,X), JML [long]
		return true
	case 0x60, 0x6B, 0x40: // RTS, RTL, RTI
		return true
	case 0x4C, 0x5C: // JMP abs, JML long
		return true
	case 0x80, 0x82, 0x90, 0xB0, 0xF0, 0xD0, 0x30, 0x10, 0x50, 0x70: // BRA, BRL, BCC, BCS, BEQ, BNE, BMI, BPL, BVC, BVS
		return true
	}
	return false
}

// SnesLoROMOffset maps a 24-bit SNES CPU address to its corresponding file offset in a LoROM image.
func SnesLoROMOffset(addr uint32, romSize int) (int, bool) {
	if addr > 0xFFFFFF || addr&0xFFFF < 0x8000 || (addr>>16) == 0x7E || (addr>>16) == 0x7F {
		return 0, false
	}
	off := int(((addr>>16)&0x7F)*0x8000 + (addr & 0x7FFF))
	return off, off < romSize
}

// StorageCanonicalAddr normalizes an event address based on its explicit address space.
// For WRAM space, offsets up to 17 bits (0x00000..0x1FFFF) map physical WRAM directly ($7E0000..$7FFFFF).
// For CPU space, mirrored ranges $0000..$1FFF in banks $00..$3F and $80..$BF map to physical WRAM $7E0000..$7E1FFF.
func StorageCanonicalAddr(space string, addr uint32) uint32 {
	if space == "wram" {
		return 0x7E0000 | (addr & 0x1FFFF)
	}
	a := addr & 0xFFFFFF
	bank := uint8((a >> 16) & 0xFF)
	offset := uint16(a & 0xFFFF)
	if bank == 0x7E || bank == 0x7F {
		return 0x7E0000 | (a & 0x1FFFF)
	}
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset < 0x2000 {
		return 0x7E0000 | uint32(offset)
	}
	return a
}

// Run executes the automatic extraction, export, and qualification pipeline.
func Run(ctx context.Context, cfg Config) (*Accounting, error) {
	if cfg.MinSpanLength < 2 {
		cfg.MinSpanLength = 2
	}
	if cfg.CandidateBudget <= 0 {
		cfg.CandidateBudget = 250
	}
	if cfg.StepBudget <= 0 {
		cfg.StepBudget = 5000
	}

	// 1. Read and validate actual ROM
	romBytes, err := os.ReadFile(cfg.ROMPath)
	if err != nil {
		return nil, fmt.Errorf("read ROM %s: %w", cfg.ROMPath, err)
	}
	romSum := sha256.Sum256(romBytes)
	actualROMSHA := hex.EncodeToString(romSum[:])
	if actualROMSHA != PinnedROMSHA256 {
		return nil, fmt.Errorf("rom sha256 %s does not match pinned %s", actualROMSHA, PinnedROMSHA256)
	}

	// 2. Load and validate project document
	docPath := filepath.Join(cfg.ProjectDir, "recovery.json")
	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		return nil, fmt.Errorf("read project document %s: %w", docPath, err)
	}
	var doc recovery.Document
	if err := json.Unmarshal(docBytes, &doc); err != nil {
		return nil, fmt.Errorf("decode project document %s: %w", docPath, err)
	}
	if doc.ROM.NormalizedSHA256 != actualROMSHA {
		return nil, fmt.Errorf("project doc ROM SHA256 %s differs from actual ROM %s", doc.ROM.NormalizedSHA256, actualROMSHA)
	}

	// Index document instructions by (Address, Context)
	type docContextKey struct {
		addr       uint32
		e, m, x, c string
	}
	docInstByContext := make(map[docContextKey]recovery.Instruction, len(doc.Instructions))
	for _, inst := range doc.Instructions {
		k := docContextKey{
			addr: inst.Address,
			e:    inst.Context.E,
			m:    inst.Context.M,
			x:    inst.Context.X,
			c:    inst.Context.C,
		}
		docInstByContext[k] = inst
	}

	// 3. Scan trace stream, computing stream SHA and tracking prior write history
	traceFile, err := os.Open(cfg.TracePath)
	if err != nil {
		return nil, fmt.Errorf("open trace file %s: %w", cfg.TracePath, err)
	}
	defer traceFile.Close()

	hasher := sha256.New()
	tee := io.TeeReader(traceFile, hasher)
	scanner := bufio.NewScanner(tee)
	scanBuf := make([]byte, 1024*1024)
	scanner.Buffer(scanBuf, 16*1024*1024)

	accounting := &Accounting{
		ROMSHA256: actualROMSHA,
	}

	// WRAM write history tracker: canonical address -> value
	wramHistory := make(map[uint32]uint8)

	// Stream parsing structures
	type parsedRetirement struct {
		Event              trace.Event
		Retained           decomp.RetainedTraceEvent
		Opcode             uint8
		Address            uint32
		ROMOffset          uint32
		IsExcluded         bool
		IsStop             bool
		InSpanReads        []trace.Event
		InSpanWrites       []trace.Event
		HistorySnapAtEntry map[uint32]uint8
	}

	var allRetirements []parsedRetirement
	var pendingBusEvents []trace.Event
	eventCount := 0
	retirementsCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		eventCount++

		var header struct {
			ID   uint64 `json:"id"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			return nil, fmt.Errorf("line %d: decode event header: %w", eventCount, err)
		}

		if header.Kind == "run" {
			var rawRun struct {
				Run struct {
					ROMSHA256          string `json:"rom_sha256"`
					Mapper             string `json:"mapper"`
					EngineRevision     string `json:"engine_revision"`
					EngineDirty        bool   `json:"engine_dirty"`
					Start              string `json:"start"`
					InitialStateSHA256 string `json:"initial_state_sha256"`
				} `json:"run"`
			}
			if err := json.Unmarshal(line, &rawRun); err == nil {
				if rawRun.Run.ROMSHA256 != actualROMSHA {
					return nil, fmt.Errorf("trace run header ROM SHA256 %s differs from actual ROM %s", rawRun.Run.ROMSHA256, actualROMSHA)
				}
				if rawRun.Run.EngineRevision != AdmittedEngineRevision {
					return nil, fmt.Errorf("trace run header engine revision %q != admitted %q", rawRun.Run.EngineRevision, AdmittedEngineRevision)
				}
				if rawRun.Run.EngineDirty {
					return nil, fmt.Errorf("trace run header engine is dirty")
				}
				if rawRun.Run.Start != "checkpoint" {
					return nil, fmt.Errorf("trace run header start %q != checkpoint", rawRun.Run.Start)
				}
				if rawRun.Run.InitialStateSHA256 != AdmittedInitialStateSHA256 {
					return nil, fmt.Errorf("trace run header initial state sha256 mismatch")
				}
			}
			continue
		}

		if header.Kind == "bus" {
			var bEv trace.Event
			if err := json.Unmarshal(line, &bEv); err != nil {
				return nil, fmt.Errorf("decode bus event %d: %w", header.ID, err)
			}
			pendingBusEvents = append(pendingBusEvents, bEv)
			continue
		}

		if header.Kind == "cpu_insn" {
			var rEv trace.Event
			if err := json.Unmarshal(line, &rEv); err != nil {
				return nil, fmt.Errorf("decode cpu_insn event %d: %w", header.ID, err)
			}
			if rEv.Insn == nil || rEv.Insn.Status != "retired" {
				continue
			}
			retirementsCount++

			var retained decomp.RetainedTraceEvent
			retained.ID = rEv.ID
			retained.Schema = rEv.Schema
			retained.Kind = rEv.Kind
			retained.Cycle = rEv.Cycle
			retained.Frame = rEv.Frame
			retained.Insn.Seq = rEv.Insn.Seq
			retained.Insn.Status = rEv.Insn.Status
			retained.Insn.Entry.A = rEv.Insn.Entry.A
			retained.Insn.Entry.X = rEv.Insn.Entry.X
			retained.Insn.Entry.Y = rEv.Insn.Entry.Y
			retained.Insn.Entry.S = rEv.Insn.Entry.S
			retained.Insn.Entry.PC = rEv.Insn.Entry.PC
			retained.Insn.Entry.D = rEv.Insn.Entry.D
			retained.Insn.Entry.DB = rEv.Insn.Entry.DB
			retained.Insn.Entry.PB = rEv.Insn.Entry.PB
			retained.Insn.Entry.P = rEv.Insn.Entry.P
			retained.Insn.Entry.E = rEv.Insn.Entry.E
			retained.Insn.Entry.Cycles = rEv.Insn.Entry.Cycles

			retained.Insn.Exit.A = rEv.Insn.Exit.A
			retained.Insn.Exit.X = rEv.Insn.Exit.X
			retained.Insn.Exit.Y = rEv.Insn.Exit.Y
			retained.Insn.Exit.S = rEv.Insn.Exit.S
			retained.Insn.Exit.PC = rEv.Insn.Exit.PC
			retained.Insn.Exit.D = rEv.Insn.Exit.D
			retained.Insn.Exit.DB = rEv.Insn.Exit.DB
			retained.Insn.Exit.PB = rEv.Insn.Exit.PB
			retained.Insn.Exit.P = rEv.Insn.Exit.P
			retained.Insn.Exit.E = rEv.Insn.Exit.E
			retained.Insn.Exit.Cycles = rEv.Insn.Exit.Cycles

			retained.Insn.Length = rEv.Insn.Length
			retained.Insn.SequentialPC.Addr = rEv.Insn.SequentialPC.Addr
			retained.Insn.SequentialPC.Bank = rEv.Insn.SequentialPC.Bank
			retained.Insn.SuccessorPC.Addr = rEv.Insn.SuccessorPC.Addr
			retained.Insn.SuccessorPC.Bank = rEv.Insn.SuccessorPC.Bank

			physROMOffset := uint32(0)
			for _, f := range rEv.Insn.Fetches {
				romOff := uint32(0)
				if f.ROMOffset != nil {
					romOff = *f.ROMOffset
				}
				if physROMOffset == 0 && romOff != 0 {
					physROMOffset = romOff
				}
				retained.Insn.Fetches = append(retained.Insn.Fetches, decomp.TraceRecordFetch{
					Addr:      uint16(f.Addr & 0xFFFF),
					Value:     f.Value,
					Role:      f.Role,
					ROMOffset: romOff,
				})
			}

			op := uint8(0)
			if len(rEv.Insn.Fetches) > 0 {
				op = rEv.Insn.Fetches[0].Value
			}
			addr := (uint32(rEv.Insn.Entry.PB) << 16) | uint32(rEv.Insn.Entry.PC)
			if physROMOffset == 0 {
				if off, ok := SnesLoROMOffset(addr, len(romBytes)); ok {
					physROMOffset = uint32(off)
				}
			}

			// Gate 2: Capture WRAM history snapshot at span ENTRY strictly before this instruction's bus interval.
			histSnapAtEntry := make(map[uint32]uint8, len(wramHistory))
			for k, v := range wramHistory {
				histSnapAtEntry[k] = v
			}

			// Classify bus events belonging to this instruction
			var inReads []trace.Event
			var inWrites []trace.Event
			for _, b := range pendingBusEvents {
				if b.Cycle >= rEv.Insn.Entry.Cycles && b.Cycle <= rEv.Insn.Exit.Cycles {
					isFetch := false
					for _, f := range rEv.Insn.Fetches {
						if b.Addr == f.Addr && b.Op == "read" {
							isFetch = true
							break
						}
					}
					if !isFetch {
						if b.Op == "read" {
							inReads = append(inReads, b)
						} else if b.Op == "write" {
							inWrites = append(inWrites, b)
						}
					}
				}
				// Advance WRAM history for writes
				if b.Op == "write" {
					cAddr := StorageCanonicalAddr(b.Space, b.Addr)
					wramHistory[cAddr] = uint8(b.Value)
				}
			}
			pendingBusEvents = nil

			// Check for MMIO access in instruction bus operations
			hasMMIO := decomp.IsMMIOAddr(addr)
			for _, b := range inReads {
				if b.Space != "wram" && decomp.IsMMIOAddr(b.Addr) {
					hasMMIO = true
					break
				}
			}
			for _, b := range inWrites {
				if b.Space != "wram" && decomp.IsMMIOAddr(b.Addr) {
					hasMMIO = true
					break
				}
			}

			allRetirements = append(allRetirements, parsedRetirement{
				Event:              rEv,
				Retained:           retained,
				Opcode:             op,
				Address:            addr,
				ROMOffset:          physROMOffset,
				IsExcluded:         IsExcludedROMOffset(physROMOffset),
				IsStop:             IsStopInstruction(op) || hasMMIO,
				InSpanReads:        inReads,
				InSpanWrites:       inWrites,
				HistorySnapAtEntry: histSnapAtEntry,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan trace stream: %w", err)
	}

	streamSHA := hex.EncodeToString(hasher.Sum(nil))
	accounting.StreamSHA256 = streamSHA
	accounting.TotalEventsScanned = eventCount
	accounting.TotalRetirements = retirementsCount

	if streamSHA != PinnedStreamSHA256 {
		return nil, fmt.Errorf("stream sha256 %s does not match pinned %s", streamSHA, PinnedStreamSHA256)
	}

	// 4. Form contiguous straight-line candidate spans
	var currentCandidate []parsedRetirement
	var candidateSpans [][]parsedRetirement

	for i := 0; i < len(allRetirements); i++ {
		ret := allRetirements[i]

		// Check straight-line continuity with previous instruction
		isContiguous := false
		if len(currentCandidate) > 0 {
			prev := currentCandidate[len(currentCandidate)-1]
			if ret.Retained.Insn.Seq == prev.Retained.Insn.Seq+1 &&
				ret.Retained.Insn.Entry.PC == prev.Retained.Insn.SequentialPC.Addr &&
				ret.Retained.Insn.Entry.PB == prev.Retained.Insn.SequentialPC.Bank &&
				prev.Retained.Insn.SuccessorPC.Addr == prev.Retained.Insn.SequentialPC.Addr &&
				prev.Retained.Insn.SuccessorPC.Bank == prev.Retained.Insn.SequentialPC.Bank {
				isContiguous = true
			}
		}

		if !isContiguous {
			if len(currentCandidate) >= cfg.MinSpanLength {
				candidateSpans = append(candidateSpans, currentCandidate)
			}
			currentCandidate = nil
		}

		// Stop before stop instructions or excluded ROM offsets
		if ret.IsStop || ret.IsExcluded {
			if len(currentCandidate) >= cfg.MinSpanLength {
				candidateSpans = append(candidateSpans, currentCandidate)
			}
			currentCandidate = nil
			continue
		}

		currentCandidate = append(currentCandidate, ret)
	}
	if len(currentCandidate) >= cfg.MinSpanLength {
		candidateSpans = append(candidateSpans, currentCandidate)
	}

	accounting.CandidateSpansFound = len(candidateSpans)

	// 5. Evaluate and qualify candidate spans up to budget
	physStartsSeen := make(map[uint32]bool)
	romBanksSeen := make(map[uint8]bool)

	evaluatedCandidates := 0
	totalStepsEvaluated := 0

	for _, cand := range candidateSpans {
		if evaluatedCandidates >= cfg.CandidateBudget || totalStepsEvaluated >= cfg.StepBudget {
			break
		}
		if len(accounting.QualifiedSpans) >= cfg.MaxSpans {
			break
		}

		evaluatedCandidates++
		totalStepsEvaluated += len(cand)

		first := cand[0]
		last := cand[len(cand)-1]
		firstAddr := first.Address
		lastExitAddr := (uint32(last.Retained.Insn.SuccessorPC.Bank) << 16) | uint32(last.Retained.Insn.SuccessorPC.Addr)

		// Gate 4: Physical bank metrics derive from physical ROM offsets (32K banks)
		romBank := uint8(first.ROMOffset / 0x8000)

		// 5a. Construct structure.BasicBlock and resolve instructions against document & ROM
		var blockInstructions []recovery.Instruction
		var retainedEvents []decomp.RetainedTraceEvent
		var candPhysicalStarts []uint32
		lookupFailed := false

		for _, r := range cand {
			retainedEvents = append(retainedEvents, r.Retained)
			// Gate 4: PhysicalStarts must use admitted fetched ROM offsets
			candPhysicalStarts = append(candPhysicalStarts, r.ROMOffset)

			eFlag := "clear"
			if r.Retained.Insn.Entry.E {
				eFlag = "set"
			}
			mFlag := "clear"
			if r.Retained.Insn.Entry.P&0x20 != 0 {
				mFlag = "set"
			}
			xFlag := "clear"
			if r.Retained.Insn.Entry.P&0x10 != 0 {
				xFlag = "set"
			}
			cFlag := "clear"
			if r.Retained.Insn.Entry.P&0x01 != 0 {
				cFlag = "set"
			}
			targetKey := docContextKey{addr: r.Address, e: eFlag, m: mFlag, x: xFlag, c: cFlag}
			docInst, ok := docInstByContext[targetKey]
			if !ok {
				romOff, inROM := SnesLoROMOffset(r.Address, len(romBytes))
				if !inROM {
					lookupFailed = true
					break
				}
				var fetchBytes []byte
				for _, f := range r.Retained.Insn.Fetches {
					fetchBytes = append(fetchBytes, f.Value)
				}
				bytesHex := hex.EncodeToString(fetchBytes)
				cInst := recovery.Instruction{
					Address: r.Address,
					Offset:  uint32(romOff),
					Opcode:  r.Opcode,
					Bytes:   bytesHex,
					Context: recovery.Context{E: eFlag, M: mFlag, X: xFlag, C: cFlag},
				}
				cInst.ID = recovery.ComputeInstructionID(actualROMSHA, cInst.Address, cInst.Offset, cInst.Bytes, cInst.Context)
				docInst = cInst
			}
			blockInstructions = append(blockInstructions, docInst)
		}

		if lookupFailed {
			accounting.RejectedSpans++
			continue
		}

		block := &structure.BasicBlock{
			ID:           fmt.Sprintf("span-%06x-%dinsn-seq%d", firstAddr, len(blockInstructions), first.Retained.Insn.Seq),
			StartAddress: firstAddr,
			EndAddress:   lastExitAddr,
			Instructions: blockInstructions,
			Successors:   []uint32{lastExitAddr},
		}

		// 5b. Initial memory binding: first-use read-before-write analysis
		// Using snapshot taken at span ENTRY before first instruction's bus interval.
		initialMem := make(map[uint32]uint8)
		inSpanWritten := make(map[uint32]bool)
		refusalReason := ""
		isRefused := false

		for _, r := range cand {
			for _, readEv := range r.InSpanReads {
				cAddr := StorageCanonicalAddr(readEv.Space, readEv.Addr)
				if inSpanWritten[cAddr] {
					// Read from value written earlier in this span; internal dataflow
					continue
				}
				readVal := uint8(readEv.Value)

				// Check static ROM
				if readEv.Space != "wram" {
					if romOff, inROM := SnesLoROMOffset(readEv.Addr, len(romBytes)); inROM {
						expectedVal := romBytes[romOff]
						if readVal != expectedVal {
							isRefused = true
							refusalReason = fmt.Sprintf("ROM read mismatch at $%06X: got 0x%02X, ROM has 0x%02X", readEv.Addr, readVal, expectedVal)
							break
						}
						initialMem[cAddr] = expectedVal
						initialMem[readEv.Addr] = expectedVal
						continue
					}
				}

				// Check prior WRAM history snapshot at span ENTRY
				if priorVal, ok := first.HistorySnapAtEntry[cAddr]; ok {
					if readVal != priorVal {
						isRefused = true
						refusalReason = fmt.Sprintf("WRAM read mismatch at $%06X: got 0x%02X, prior write had 0x%02X", cAddr, readVal, priorVal)
						break
					}
					initialMem[cAddr] = priorVal
					initialMem[readEv.Addr] = priorVal
					continue
				}

				// Unknown uninitialized memory byte -> explicit refusal
				isRefused = true
				refusalReason = fmt.Sprintf("refused unknown initial memory byte at canonical address $%06X (read value 0x%02X neither in ROM nor written in prior history)", cAddr, readVal)
				break
			}
			if isRefused {
				break
			}

			// Track in-span writes
			for _, writeEv := range r.InSpanWrites {
				cAddr := StorageCanonicalAddr(writeEv.Space, writeEv.Addr)
				inSpanWritten[cAddr] = true
			}
		}

		if isRefused {
			accounting.RefusedSpans = append(accounting.RefusedSpans, RefusedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       refusalReason,
			})
			continue
		}

		// 5c. Lift block into machine IR
		entryCtx := blockInstructions[0].Context
		ir, err := decomp.LiftBlock(block, entryCtx)
		if err != nil {
			accounting.RejectedSpans++
			continue
		}

		// Generate compilable C
		cCode, err := decomp.GenerateCompilableC(ir)
		if err != nil {
			accounting.RejectedSpans++
			continue
		}

		// 5d. Import ReplayCase using existing backend API
		var derivedCells []decomp.DerivedMemoryCell
		for addr, val := range initialMem {
			derivedCells = append(derivedCells, decomp.DerivedMemoryCell{
				Address:          addr,
				Value:            val,
				DerivationReason: "first_use_read_before_write_verified",
			})
		}
		sort.Slice(derivedCells, func(i, j int) bool { return derivedCells[i].Address < derivedCells[j].Address })

		replayCase, err := decomp.ImportReplayCaseFromTraceRecords(
			block.ID, retainedEvents, streamSHA, streamSHA, "", actualROMSHA, derivedCells, block,
		)
		if err != nil {
			accounting.RejectedSpans++
			continue
		}

		initState := replayCase.InitialState
		recordedExit := replayCase.ObservedExit
		recordedNextPC := replayCase.ObservedNextPC

		// Collect recorded writes from trace
		var recordedWrites []decomp.MemoryWrite
		for _, r := range cand {
			for _, w := range r.InSpanWrites {
				recordedWrites = append(recordedWrites, decomp.MemoryWrite{
					Address: StorageCanonicalAddr(w.Space, w.Addr),
					Value:   uint8(w.Value),
				})
			}
		}

		// 5e. Execute Go emulator with step recording
		emuResult, steps, err := decomp.RunEmulatorBlockWithSteps(ctx, ir, initState, initialMem)
		if err != nil {
			accounting.MismatchedSpans = append(accounting.MismatchedSpans, MismatchedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       fmt.Sprintf("emulator error: %v", err),
			})
			continue
		}
		if emuResult.MissingRead || emuResult.MMIOAccess || emuResult.WriteOverflow {
			accounting.RefusedSpans = append(accounting.RefusedSpans, RefusedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       fmt.Sprintf("emulator refusal: missingRead=%v mmio=%v overflow=%v", emuResult.MissingRead, emuResult.MMIOAccess, emuResult.WriteOverflow),
			})
			continue
		}

		// Gate 1: Per-Step Trace Matching against actual recorded trace events
		if len(steps) != len(cand) {
			accounting.MismatchedSpans = append(accounting.MismatchedSpans, MismatchedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       fmt.Sprintf("step count mismatch: emu has %d steps, trace has %d instructions", len(steps), len(cand)),
			})
			continue
		}

		stepTraceMismatch := false
		var stepMismatchReason string
		for sIdx, step := range steps {
			r := cand[sIdx]
			// Verify step Entry state against recorded Entry
			if step.EntryState.A != r.Retained.Insn.Entry.A ||
				step.EntryState.X != r.Retained.Insn.Entry.X ||
				step.EntryState.Y != r.Retained.Insn.Entry.Y ||
				step.EntryState.S != r.Retained.Insn.Entry.S ||
				step.EntryState.D != r.Retained.Insn.Entry.D ||
				step.EntryState.DB != r.Retained.Insn.Entry.DB ||
				step.EntryState.PB != r.Retained.Insn.Entry.PB ||
				step.EntryState.PC != r.Retained.Insn.Entry.PC ||
				step.EntryState.P != r.Retained.Insn.Entry.P ||
				step.EntryState.E != r.Retained.Insn.Entry.E {
				stepTraceMismatch = true
				stepMismatchReason = fmt.Sprintf("step %d entry state mismatch: emu=%+v trace=%+v", sIdx, step.EntryState, r.Retained.Insn.Entry)
				break
			}

			// Verify step Exit state against recorded Exit
			if step.ExitState.A != r.Retained.Insn.Exit.A ||
				step.ExitState.X != r.Retained.Insn.Exit.X ||
				step.ExitState.Y != r.Retained.Insn.Exit.Y ||
				step.ExitState.S != r.Retained.Insn.Exit.S ||
				step.ExitState.D != r.Retained.Insn.Exit.D ||
				step.ExitState.DB != r.Retained.Insn.Exit.DB ||
				step.ExitState.PB != r.Retained.Insn.Exit.PB ||
				step.ExitState.PC != r.Retained.Insn.Exit.PC ||
				step.ExitState.P != r.Retained.Insn.Exit.P ||
				step.ExitState.E != r.Retained.Insn.Exit.E {
				stepTraceMismatch = true
				stepMismatchReason = fmt.Sprintf("step %d exit state mismatch: emu=%+v trace=%+v", sIdx, step.ExitState, r.Retained.Insn.Exit)
				break
			}

			// Verify step Exit PC against expected successor PC
			expectedSucc := (uint32(r.Retained.Insn.SuccessorPC.Bank) << 16) | uint32(r.Retained.Insn.SuccessorPC.Addr)
			stepExitPC := (uint32(step.ExitState.PB) << 16) | uint32(step.ExitState.PC)
			if stepExitPC != expectedSucc {
				stepTraceMismatch = true
				stepMismatchReason = fmt.Sprintf("step %d successor PC mismatch: emu=$%06X, want $%06X", sIdx, stepExitPC, expectedSucc)
				break
			}

			// Verify ordered data writes
			if len(step.Writes) != len(r.InSpanWrites) {
				stepTraceMismatch = true
				stepMismatchReason = fmt.Sprintf("step %d writes count mismatch: emu has %d, trace has %d", sIdx, len(step.Writes), len(r.InSpanWrites))
				break
			}
			for wIdx, sw := range step.Writes {
				tw := r.InSpanWrites[wIdx]
				cAddr := StorageCanonicalAddr(tw.Space, tw.Addr)
				if sw.Address != cAddr || sw.Value != uint8(tw.Value) {
					stepTraceMismatch = true
					stepMismatchReason = fmt.Sprintf("step %d write[%d] mismatch: emu=$%06X=0x%02X, trace=$%06X=0x%02X",
						sIdx, wIdx, sw.Address, sw.Value, cAddr, tw.Value)
					break
				}
			}
			if stepTraceMismatch {
				break
			}

			// Verify ordered data reads
			if len(step.Reads) != len(r.InSpanReads) {
				stepTraceMismatch = true
				stepMismatchReason = fmt.Sprintf("step %d reads count mismatch: emu has %d, trace has %d", sIdx, len(step.Reads), len(r.InSpanReads))
				break
			}
			for rIdx, sr := range step.Reads {
				tr := r.InSpanReads[rIdx]
				cAddr := StorageCanonicalAddr(tr.Space, tr.Addr)
				if sr.Address != cAddr || sr.Value != uint8(tr.Value) {
					stepTraceMismatch = true
					stepMismatchReason = fmt.Sprintf("step %d read[%d] mismatch: emu=$%06X=0x%02X, trace=$%06X=0x%02X",
						sIdx, rIdx, sr.Address, sr.Value, cAddr, tr.Value)
					break
				}
			}
			if stepTraceMismatch {
				break
			}
		}

		if stepTraceMismatch {
			accounting.MismatchedSpans = append(accounting.MismatchedSpans, MismatchedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       stepMismatchReason,
			})
			continue
		}

		// 5f. Execute compiled C block
		cResult, err := decomp.RunCompiledCBlock(ctx, ir, initState, initialMem)
		if err != nil {
			accounting.MismatchedSpans = append(accounting.MismatchedSpans, MismatchedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       fmt.Sprintf("compiled C error: %v", err),
			})
			continue
		}
		if cResult.MissingRead || cResult.MMIOAccess || cResult.WriteOverflow {
			accounting.RefusedSpans = append(accounting.RefusedSpans, RefusedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason:       fmt.Sprintf("compiled C refusal: missingRead=%v mmio=%v overflow=%v", cResult.MissingRead, cResult.MMIOAccess, cResult.WriteOverflow),
			})
			continue
		}

		// 5g. Final noncycle CPU state, endpoint, and ordered writes agreement between compiled C, Go emulator, and recorded trace
		stateMatch := (cResult.State.A == recordedExit.A && cResult.State.X == recordedExit.X &&
			cResult.State.Y == recordedExit.Y && cResult.State.S == recordedExit.S &&
			cResult.State.D == recordedExit.D && cResult.State.DB == recordedExit.DB &&
			cResult.State.PB == recordedExit.PB && cResult.State.P == recordedExit.P &&
			cResult.State.E == recordedExit.E)

		endpointMatch := (cResult.NextPC == recordedNextPC) && (emuResult.NextPC == recordedNextPC)

		writesMatch := len(recordedWrites) == len(cResult.Writes)
		if writesMatch {
			for wIdx := range recordedWrites {
				if recordedWrites[wIdx] != cResult.Writes[wIdx] {
					writesMatch = false
					break
				}
			}
		}

		if !stateMatch || !endpointMatch || !writesMatch {
			accounting.MismatchedSpans = append(accounting.MismatchedSpans, MismatchedSpan{
				StartAddress: firstAddr,
				Length:       len(cand),
				StartSeq:     first.Retained.Insn.Seq,
				Reason: fmt.Sprintf("compiled C mismatch: stateMatch=%v endpointMatch=%v writesMatch=%v",
					stateMatch, endpointMatch, writesMatch),
			})
			continue
		}

		// The span is QUALIFIED!
		accounting.SelectedSpans++
		var memCells []decomp.MemoryCell
		for addr, val := range initialMem {
			memCells = append(memCells, decomp.MemoryCell{Address: addr, Value: val})
		}
		sort.Slice(memCells, func(i, j int) bool { return memCells[i].Address < memCells[j].Address })

		for _, p := range candPhysicalStarts {
			physStartsSeen[p] = true
		}
		romBanksSeen[romBank] = true

		accounting.QualifiedSpans = append(accounting.QualifiedSpans, QualifiedSpan{
			BlockID:          block.ID,
			StartAddress:     firstAddr,
			EndAddress:       lastExitAddr,
			ROMBank:          romBank,
			InstructionCount: len(cand),
			PhysicalStarts:   candPhysicalStarts,
			StartSeq:         first.Retained.Insn.Seq,
			EndSeq:           last.Retained.Insn.Seq,
			StartEventID:     first.Retained.ID,
			EndEventID:       last.Retained.ID,
			ReplayCase:       replayCase,
			InitialMemory:    memCells,
			GeneratedC:       cCode,
			EmulatorResult:   emuResult,
			CompiledCResult:  cResult,
		})
	}

	for p := range physStartsSeen {
		accounting.UniquePhysicalStarts = append(accounting.UniquePhysicalStarts, p)
	}
	sort.Slice(accounting.UniquePhysicalStarts, func(i, j int) bool {
		return accounting.UniquePhysicalStarts[i] < accounting.UniquePhysicalStarts[j]
	})

	for b := range romBanksSeen {
		accounting.UniqueROMBanks = append(accounting.UniqueROMBanks, b)
	}
	sort.Slice(accounting.UniqueROMBanks, func(i, j int) bool {
		return accounting.UniqueROMBanks[i] < accounting.UniqueROMBanks[j]
	})

	return accounting, nil
}
