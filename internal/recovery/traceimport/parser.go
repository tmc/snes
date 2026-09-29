package traceimport

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
)

// ParseOptions configures trace observation stream parsing.
type ParseOptions struct {
	// ExpectedStreamSHA256, if non-empty, overrides the computed stream SHA-256 for receipt comparison.
	ExpectedStreamSHA256 string
}

type parseState struct {
	instMap       map[string]*recovery.Instruction
	instOrder     []string
	edgeMap       map[string]*recovery.Edge
	edgeOrder     []string
	evMap         map[string]bool
	issueSeen     map[string]bool
	coverage      *coverage.Builder
	lastSeq       uint64
	lastSeqInstID string
	hasSeq        bool
}

// Parse reads and validates an observation stream and optional receipt against rom.
// If the stream begins with gzip magic bytes (0x1f 0x8b), it is transparently decompressed
// while computing the stream SHA-256 over the compressed bytes.
func Parse(streamReader io.Reader, receiptReader io.Reader, rom []byte, expectedROMHash string) (*ImportResult, error) {
	return ParseWithOptions(streamReader, receiptReader, rom, expectedROMHash, ParseOptions{})
}

// ParseWithOptions reads and validates an observation stream and optional receipt against rom with options.
func ParseWithOptions(streamReader io.Reader, receiptReader io.Reader, rom []byte, expectedROMHash string, opts ParseOptions) (*ImportResult, error) {
	if streamReader == nil {
		return nil, errors.New("traceimport: stream reader cannot be nil")
	}

	res := &ImportResult{
		Instructions: []recovery.Instruction{},
		Edges:        []recovery.Edge{},
		Evidence:     []recovery.Evidence{},
		Issues:       []recovery.Issue{},
	}

	state := &parseState{
		instMap:   make(map[string]*recovery.Instruction),
		instOrder: make([]string, 0),
		edgeMap:   make(map[string]*recovery.Edge),
		edgeOrder: make([]string, 0),
		evMap:     make(map[string]bool),
		issueSeen: make(map[string]bool),
		coverage:  coverage.NewBuilder(),
	}

	// 1. Process receipt if provided.
	if receiptReader != nil {
		var rc Receipt
		dec := json.NewDecoder(receiptReader)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rc); err != nil {
			return nil, fmt.Errorf("traceimport: parse receipt: %w", err)
		}
		if rc.Schema != SchemaVersion {
			return nil, fmt.Errorf("traceimport: unsupported receipt schema %d (expected %d)", rc.Schema, SchemaVersion)
		}
		res.Receipt = &rc
		if rc.Outcome == "complete" {
			res.IsComplete = true
		} else {
			res.IsComplete = false
			res.Issues = append(res.Issues, recovery.Issue{
				ID:       fmt.Sprintf("iss-receipt-outcome-%s", rc.Outcome),
				Reason:   fmt.Sprintf("trace execution not complete (outcome %q, truncation %q)", rc.Outcome, rc.TruncationReason),
				Blocking: false,
			})
		}
	} else {
		res.IsComplete = false
		res.Issues = append(res.Issues, recovery.Issue{
			ID:       "iss-receipt-missing",
			Reason:   "trace missing completion receipt: replay completeness unverified",
			Blocking: false,
		})
	}

	// 2. Stream parsing and hash computation.
	br := bufio.NewReader(streamReader)
	header, _ := br.Peek(2)
	isGzip := len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b

	var (
		recordReader io.Reader
		streamHasher = sha256.New()
		tee          = io.TeeReader(br, streamHasher)
	)

	if isGzip {
		gz, err := gzip.NewReader(tee)
		if err != nil {
			return nil, fmt.Errorf("traceimport: open gzip stream: %w", err)
		}
		defer gz.Close()
		recordReader = gz
	} else {
		recordReader = tee
	}

	scanner := bufio.NewScanner(recordReader)
	const maxLineBuf = 10 * 1024 * 1024 // 10MB line buffer
	scanner.Buffer(make([]byte, 64*1024), maxLineBuf)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var rec StreamRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("traceimport: line %d: unmarshal stream record: %w", lineNum, err)
		}

		if rec.Schema != SchemaVersion {
			return nil, fmt.Errorf("traceimport: line %d: unsupported schema version %d (expected %d)", lineNum, rec.Schema, SchemaVersion)
		}

		// First record contract: MUST be kind == "run"
		if res.RunMetadata == nil {
			if rec.Kind != "run" || rec.Run == nil {
				return nil, fmt.Errorf("traceimport: first record must be run header, got %q", rec.Kind)
			}
			res.RunMetadata = rec.Run
			res.LogicalRunID = ComputeLogicalRunID(rec.Run)

			// Validate ROM hash
			if expectedROMHash != "" && !strings.EqualFold(rec.Run.ROM_SHA256, expectedROMHash) {
				return nil, fmt.Errorf("traceimport: trace ROM hash %q does not match document ROM hash %q", rec.Run.ROM_SHA256, expectedROMHash)
			}
			continue
		}

		res.TotalRecords++

		switch rec.Kind {
		case "cpu_insn":
			if err := processCPUInsn(rec, res, state, rom, expectedROMHash); err != nil {
				return nil, fmt.Errorf("traceimport: line %d: %w", lineNum, err)
			}
		case "cpu_transition":
			processCPUTransition(rec, res, state)
		case "gap":
			if rec.Gap != nil {
				gap := coverage.Gap{
					FirstSeq: rec.Gap.FirstSeq,
					LastSeq:  rec.Gap.LastSeq,
					Reason:   rec.Gap.Reason,
				}
				res.Gaps = append(res.Gaps, gap)
				res.IsComplete = false
				state.addIssue(res, recovery.Issue{
					ID:       fmt.Sprintf("iss-gap-%d-%d", gap.FirstSeq, gap.LastSeq),
					Reason:   fmt.Sprintf("trace gap in sequence [%d, %d]: %s", gap.FirstSeq, gap.LastSeq, gap.Reason),
					Blocking: false,
				})
				if state.hasSeq && gap.FirstSeq < state.lastSeq {
					return nil, fmt.Errorf("traceimport: line %d: non-monotonic gap: first_seq %d < previous seq %d", lineNum, gap.FirstSeq, state.lastSeq)
				}
				if gap.LastSeq >= gap.FirstSeq {
					state.lastSeq = gap.LastSeq
					state.lastSeqInstID = ""
					state.hasSeq = true
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("traceimport: scan stream: %w", err)
	}

	if isGzip {
		// Ensure any remaining bytes from underlying compressed stream are read into streamHasher.
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return nil, fmt.Errorf("traceimport: drain compressed stream: %w", err)
		}
	}

	if res.RunMetadata == nil {
		return nil, errors.New("traceimport: empty stream: no run record found")
	}

	if opts.ExpectedStreamSHA256 != "" {
		res.StreamSHA256 = opts.ExpectedStreamSHA256
	} else {
		res.StreamSHA256 = hex.EncodeToString(streamHasher.Sum(nil))
	}

	// Verify stream hash matches receipt if present
	if res.Receipt != nil && res.Receipt.StreamSHA256 != "" {
		if !strings.EqualFold(res.StreamSHA256, res.Receipt.StreamSHA256) {
			return nil, fmt.Errorf("traceimport: computed stream hash %q does not match receipt hash %q",
				res.StreamSHA256, res.Receipt.StreamSHA256)
		}
	}

	// Populate Instructions in observation order
	res.Instructions = make([]recovery.Instruction, 0, len(state.instOrder))
	for _, id := range state.instOrder {
		res.Instructions = append(res.Instructions, *state.instMap[id])
	}

	// Populate Edges in observation order
	res.Edges = make([]recovery.Edge, 0, len(state.edgeOrder))
	for _, id := range state.edgeOrder {
		res.Edges = append(res.Edges, *state.edgeMap[id])
	}

	// Sort Evidence deterministically
	sort.Slice(res.Evidence, func(i, j int) bool {
		return res.Evidence[i].ID < res.Evidence[j].ID
	})

	runID := res.LogicalRunID
	if runID == "" {
		runID = res.StreamSHA256
	}
	res.Sites = state.coverage.Sites(runID)

	return res, nil
}

// addIssue appends iss to res.Issues unless an issue with the same ID was
// already recorded, keeping memory bounded by distinct issues rather than
// executions.
func (s *parseState) addIssue(res *ImportResult, iss recovery.Issue) {
	if s.issueSeen[iss.ID] {
		return
	}
	s.issueSeen[iss.ID] = true
	res.Issues = append(res.Issues, iss)
}

func processCPUInsn(rec StreamRecord, res *ImportResult, s *parseState, rom []byte, expectedROMHash string) error {
	insn := rec.Insn
	if insn == nil {
		return errors.New("missing insn payload on cpu_insn record")
	}

	instAddr := (uint32(insn.Entry.PB) << 16) | uint32(insn.Entry.PC)

	if insn.Status != "retired" {
		s.addIssue(res, recovery.Issue{
			ID:       fmt.Sprintf("iss-%06x-status-%s", instAddr, insn.Status),
			Address:  instAddr,
			Reason:   fmt.Sprintf("instruction at seq %d did not retire normally (status: %s, fault: %s)", insn.Seq, insn.Status, insn.Fault),
			Blocking: false,
		})
		return nil
	}

	if len(insn.Fetches) == 0 {
		return nil
	}

	firstFetch := insn.Fetches[0]
	if firstFetch.ROMOffset == nil {
		s.addIssue(res, recovery.Issue{
			ID:       fmt.Sprintf("iss-%06x-nonrom", firstFetch.Addr),
			Address:  firstFetch.Addr,
			Reason:   fmt.Sprintf("instruction at seq %d executed from non-ROM address $%06X", insn.Seq, firstFetch.Addr),
			Blocking: false,
		})
		return nil
	}

	firstOffset := *firstFetch.ROMOffset
	if int(firstOffset) >= len(rom) {
		s.addIssue(res, recovery.Issue{
			ID:       fmt.Sprintf("iss-%06x-oob", firstFetch.Addr),
			Address:  firstFetch.Addr,
			Offset:   firstOffset,
			Reason:   fmt.Sprintf("instruction fetch at seq %d has out-of-bounds ROM offset %d", insn.Seq, firstOffset),
			Blocking: true,
		})
		return nil
	}

	fetchBytes := make([]byte, len(insn.Fetches))
	for i, f := range insn.Fetches {
		fetchBytes[i] = f.Value
		if f.ROMOffset != nil {
			off := *f.ROMOffset
			if int(off) >= len(rom) || rom[off] != f.Value {
				s.addIssue(res, recovery.Issue{
					ID:       fmt.Sprintf("iss-%06x-fetchmismatch", f.Addr),
					Address:  f.Addr,
					Offset:   off,
					Reason:   fmt.Sprintf("fetch at seq %d byte %d ($%02X) does not match ROM byte at offset %d", insn.Seq, i, f.Value, off),
					Blocking: true,
				})
				return nil
			}
		}
	}

	hexBytes := hex.EncodeToString(fetchBytes)
	opcode := fetchBytes[0]
	op := cpu.Opcodes[opcode]

	// Extract processor status context
	ctx := recovery.Context{
		E: "clear",
		M: "clear",
		X: "clear",
		C: "clear",
	}
	if insn.Entry.E {
		ctx.E = "set"
		ctx.M = "set"
		ctx.X = "set"
	} else {
		if insn.Entry.P&0x20 != 0 {
			ctx.M = "set"
		}
		if insn.Entry.P&0x10 != 0 {
			ctx.X = "set"
		}
	}
	if insn.Entry.P&0x01 != 0 {
		ctx.C = "set"
	}

	mnemonic := strings.ToLower(op.Name)
	modeStr := addressingModeName(op.Mode)
	if isMDependent(opcode) {
		if len(fetchBytes) == 3 {
			mnemonic += ".w"
		} else {
			mnemonic += ".b"
		}
	} else if isXDependent(opcode) {
		if len(fetchBytes) == 3 {
			mnemonic += ".w"
		} else {
			mnemonic += ".b"
		}
	}

	instID := recovery.ComputeInstructionID(expectedROMHash, instAddr, firstOffset, hexBytes, ctx)

	if insn.Seq != 0 {
		if s.hasSeq {
			if insn.Seq == s.lastSeq {
				if instID == s.lastSeqInstID {
					// Duplicate sequence event for the same instruction: deduplicate idempotently
					return nil
				}
				return fmt.Errorf("traceimport: conflicting duplicate sequence %d for instruction %s", insn.Seq, instID)
			}
			if insn.Seq < s.lastSeq {
				return fmt.Errorf("traceimport: non-monotonic sequence: seq %d < previous %d", insn.Seq, s.lastSeq)
			}
		}
		s.lastSeq = insn.Seq
		s.lastSeqInstID = instID
		s.hasSeq = true
	}

	if res.FirstSeq == 0 || insn.Seq < res.FirstSeq {
		res.FirstSeq = insn.Seq
	}
	res.LastSeq = max(res.LastSeq, insn.Seq)
	if res.MinFrame == 0 || rec.Frame < res.MinFrame {
		res.MinFrame = rec.Frame
	}
	res.MaxFrame = max(res.MaxFrame, rec.Frame)

	runPrefix := "unknown"
	if res.LogicalRunID != "" {
		if len(res.LogicalRunID) >= 8 {
			runPrefix = res.LogicalRunID[:8]
		} else {
			runPrefix = res.LogicalRunID
		}
	} else if res.RunMetadata != nil && res.RunMetadata.EngineRevision != "" {
		runPrefix = res.RunMetadata.EngineRevision
	}

	evID := fmt.Sprintf("ev-inst-%s-%s", runPrefix, instID[:min(16, len(instID))])
	if !s.evMap[evID] {
		s.evMap[evID] = true
		res.Evidence = append(res.Evidence, recovery.Evidence{
			ID:      evID,
			Kind:    "observed",
			Details: fmt.Sprintf("event:%d seq:%d frame:%d addr:$%06X offset:$%06X context:e=%s,m=%s,x=%s", rec.ID, insn.Seq, rec.Frame, instAddr, firstOffset, ctx.E, ctx.M, ctx.X),
		})
	}

	if _, ok := s.instMap[instID]; !ok {
		inst := recovery.Instruction{
			ID:           instID,
			Architecture: "wdc65816",
			Address:      instAddr,
			Offset:       firstOffset,
			Bytes:        hexBytes,
			Opcode:       opcode,
			Mnemonic:     mnemonic,
			Mode:         modeStr,
			Context:      ctx,
			Evidence:     []string{evID},
		}
		s.instMap[instID] = &inst
		s.instOrder = append(s.instOrder, instID)
	}

	if err := s.coverage.Add(coverage.Event{
		Seq:           insn.Seq,
		Frame:         rec.Frame,
		Address:       instAddr,
		Offset:        firstOffset,
		HasROMOffset:  true,
		InstructionID: instID,
		Context:       ctx,
	}); err != nil {
		return err
	}

	// Successor Edge
	destAddr := insn.SuccessorPC.Address()
	edgeKind := "fallthrough"
	switch opcode {
	case 0x20, 0x22:
		edgeKind = "call"
	case 0x60, 0x6B, 0x40:
		edgeKind = "return"
	case 0x80, 0x82, 0x10, 0x30, 0x50, 0x70, 0x90, 0xB0, 0xD0, 0xF0:
		edgeKind = "branch"
	case 0x4C, 0x5C, 0x6C, 0x7C, 0xDC:
		edgeKind = "jump"
	default:
		seqAddr := insn.SequentialPC.Address()
		if destAddr == seqAddr {
			edgeKind = "fallthrough"
		} else {
			edgeKind = "jump"
		}
	}

	edgeTuple := fmt.Sprintf(`["edge",%q,%q,%d]`, instID, edgeKind, destAddr)
	edgeSum := sha256.Sum256([]byte(edgeTuple))
	edgeID := hex.EncodeToString(edgeSum[:])

	edgeEvID := fmt.Sprintf("ev-edge-%s-%s", runPrefix, edgeID[:min(16, len(edgeID))])
	if !s.evMap[edgeEvID] {
		s.evMap[edgeEvID] = true
		res.Evidence = append(res.Evidence, recovery.Evidence{
			ID:      edgeEvID,
			Kind:    "observed",
			Details: fmt.Sprintf("event:%d seq:%d frame:%d kind:%s source:%s dest:$%06X", rec.ID, insn.Seq, rec.Frame, edgeKind, instID[:min(16, len(instID))], destAddr),
		})
	}

	if _, ok := s.edgeMap[edgeID]; !ok {
		s.edgeMap[edgeID] = &recovery.Edge{
			ID:          edgeID,
			Kind:        edgeKind,
			Source:      instID,
			Destination: destAddr,
			Evidence:    []string{edgeEvID},
		}
		s.edgeOrder = append(s.edgeOrder, edgeID)
	}

	return nil
}

func processCPUTransition(rec StreamRecord, res *ImportResult, s *parseState) {
	tr := rec.Transition
	if tr == nil {
		return
	}

	if tr.Kind == "nmi" || tr.Kind == "irq" {
		handlerAddr := tr.HandlerPC.Address()
		runPrefix := "unknown"
		if res.LogicalRunID != "" {
			if len(res.LogicalRunID) >= 8 {
				runPrefix = res.LogicalRunID[:8]
			} else {
				runPrefix = res.LogicalRunID
			}
		} else if res.RunMetadata != nil && res.RunMetadata.EngineRevision != "" {
			runPrefix = res.RunMetadata.EngineRevision
		}
		evID := fmt.Sprintf("ev-tr-%s-%s-%06x", runPrefix, tr.Kind, tr.VectorAddr)
		if !s.evMap[evID] {
			s.evMap[evID] = true
			res.Evidence = append(res.Evidence, recovery.Evidence{
				ID:      evID,
				Kind:    "observed",
				Details: fmt.Sprintf("event:%d seq:%d frame:%d kind:%s vector:$%06X handler:$%06X", rec.ID, tr.Seq, rec.Frame, tr.Kind, tr.VectorAddr, handlerAddr),
			})
		}

		edgeTuple := fmt.Sprintf(`["interrupt",%d,%d]`, tr.VectorAddr, handlerAddr)
		edgeSum := sha256.Sum256([]byte(edgeTuple))
		edgeID := hex.EncodeToString(edgeSum[:])

		if _, ok := s.edgeMap[edgeID]; !ok {
			s.edgeMap[edgeID] = &recovery.Edge{
				ID:          edgeID,
				Kind:        "interrupt",
				Source:      fmt.Sprintf("vec-%06x", tr.VectorAddr),
				Destination: handlerAddr,
				Evidence:    []string{evID},
			}
			s.edgeOrder = append(s.edgeOrder, edgeID)
		}
	}
}

func isMDependent(opcode byte) bool {
	switch opcode {
	case 0x09, 0x29, 0x49, 0x69, 0x89, 0xA9, 0xC9, 0xE9:
		return true
	default:
		return false
	}
}

func isXDependent(opcode byte) bool {
	switch opcode {
	case 0xA0, 0xA2, 0xC0, 0xE0:
		return true
	default:
		return false
	}
}

func addressingModeName(mode cpu.AddressingMode) string {
	switch mode {
	case cpu.AddrImpl:
		return "implied"
	case cpu.AddrAcc:
		return "accumulator"
	case cpu.AddrImm:
		return "immediate"
	case cpu.AddrAbs:
		return "absolute"
	case cpu.AddrAbsX:
		return "absolute_x"
	case cpu.AddrAbsY:
		return "absolute_y"
	case cpu.AddrDir:
		return "direct_page"
	case cpu.AddrDirX:
		return "direct_page_x"
	case cpu.AddrDirY:
		return "direct_page_y"
	case cpu.AddrInd:
		return "indirect"
	case cpu.AddrIndX:
		return "indirect_x"
	case cpu.AddrIndY:
		return "indirect_y"
	case cpu.AddrLong:
		return "long"
	case cpu.AddrLongX:
		return "long_x"
	case cpu.AddrSr:
		return "stack_relative"
	case cpu.AddrSrIndY:
		return "stack_relative_y"
	case cpu.AddrRel:
		return "relative"
	case cpu.AddrRelL:
		return "relative_long"
	case cpu.AddrDirInd:
		return "direct_indirect"
	case cpu.AddrDirIndL:
		return "direct_indirect_long"
	case cpu.AddrAbsInd:
		return "absolute_indirect"
	case cpu.AddrAbsIndX:
		return "absolute_indirect_x"
	case cpu.AddrAbsIndLong:
		return "absolute_indirect_long"
	case cpu.AddrBlock:
		return "block_move"
	case cpu.AddrDirIndLIdxY:
		return "direct_indirect_long_y"
	default:
		return "unknown"
	}
}
