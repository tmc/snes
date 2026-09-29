package traceimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

// Parse reads and validates an observation stream and optional receipt against rom.
func Parse(streamReader io.Reader, receiptReader io.Reader, rom []byte, expectedROMHash string) (*ImportResult, error) {
	if streamReader == nil {
		return nil, errors.New("traceimport: stream reader cannot be nil")
	}

	res := &ImportResult{
		Instructions: []recovery.Instruction{},
		Edges:        []recovery.Edge{},
		Evidence:     []recovery.Evidence{},
		Issues:       []recovery.Issue{},
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
	streamHasher := sha256.New()
	tee := io.TeeReader(streamReader, streamHasher)

	scanner := bufio.NewScanner(tee)
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

			// Validate ROM hash
			if expectedROMHash != "" && !strings.EqualFold(rec.Run.ROM_SHA256, expectedROMHash) {
				return nil, fmt.Errorf("traceimport: trace ROM hash %q does not match document ROM hash %q", rec.Run.ROM_SHA256, expectedROMHash)
			}
			continue
		}

		res.TotalRecords++

		switch rec.Kind {
		case "cpu_insn":
			if err := processCPUInsn(rec, res, rom, expectedROMHash); err != nil {
				return nil, fmt.Errorf("traceimport: line %d: %w", lineNum, err)
			}
		case "cpu_transition":
			processCPUTransition(rec, res)
		case "gap":
			// Handled: explicit gap sequence recorded in issues if desired
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("traceimport: scan stream: %w", err)
	}

	if res.RunMetadata == nil {
		return nil, errors.New("traceimport: empty stream: no run record found")
	}

	res.StreamSHA256 = hex.EncodeToString(streamHasher.Sum(nil))

	// Verify stream hash matches receipt if present
	if res.Receipt != nil && res.Receipt.StreamSHA256 != "" {
		if !strings.EqualFold(res.StreamSHA256, res.Receipt.StreamSHA256) {
			return nil, fmt.Errorf("traceimport: computed stream hash %q does not match receipt hash %q",
				res.StreamSHA256, res.Receipt.StreamSHA256)
		}
	}

	return res, nil
}

func processCPUInsn(rec StreamRecord, res *ImportResult, rom []byte, expectedROMHash string) error {
	insn := rec.Insn
	if insn == nil {
		return errors.New("missing insn payload on cpu_insn record")
	}

	instAddr := (uint32(insn.Entry.PB) << 16) | uint32(insn.Entry.PC)

	if insn.Status != "retired" {
		res.Issues = append(res.Issues, recovery.Issue{
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
		res.Issues = append(res.Issues, recovery.Issue{
			ID:       fmt.Sprintf("iss-%06x-nonrom", firstFetch.Addr),
			Address:  firstFetch.Addr,
			Reason:   fmt.Sprintf("instruction at seq %d executed from non-ROM address $%06X", insn.Seq, firstFetch.Addr),
			Blocking: false,
		})
		return nil
	}

	firstOffset := *firstFetch.ROMOffset
	if int(firstOffset) >= len(rom) {
		res.Issues = append(res.Issues, recovery.Issue{
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
				res.Issues = append(res.Issues, recovery.Issue{
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

	evidenceTuple := []any{"observed", rec.ID, insn.Seq, res.RunMetadata.EngineRevision}
	evB, _ := json.Marshal(evidenceTuple)
	evSum := sha256.Sum256(evB)
	evidenceID := hex.EncodeToString(evSum[:])

	res.Evidence = append(res.Evidence, recovery.Evidence{
		ID:      evidenceID,
		Kind:    "observed",
		Details: fmt.Sprintf("event:%d seq:%d frame:%d", rec.ID, insn.Seq, rec.Frame),
	})

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
		Evidence:     []string{evidenceID},
	}
	res.Instructions = append(res.Instructions, inst)

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

	edgeTuple := []any{"edge", instID, edgeKind, destAddr}
	edgeB, _ := json.Marshal(edgeTuple)
	edgeSum := sha256.Sum256(edgeB)
	edgeID := hex.EncodeToString(edgeSum[:])

	res.Edges = append(res.Edges, recovery.Edge{
		ID:          edgeID,
		Kind:        edgeKind,
		Source:      instID,
		Destination: destAddr,
		Evidence:    []string{evidenceID},
	})

	return nil
}

func processCPUTransition(rec StreamRecord, res *ImportResult) {
	tr := rec.Transition
	if tr == nil {
		return
	}

	if tr.Kind == "nmi" || tr.Kind == "irq" {
		handlerAddr := tr.HandlerPC.Address()
		evidenceTuple := []any{"observed", rec.ID, tr.Seq, res.RunMetadata.EngineRevision}
		evB, _ := json.Marshal(evidenceTuple)
		evSum := sha256.Sum256(evB)
		evidenceID := hex.EncodeToString(evSum[:])

		res.Evidence = append(res.Evidence, recovery.Evidence{
			ID:      evidenceID,
			Kind:    "observed",
			Details: fmt.Sprintf("event:%d seq:%d kind:%s", rec.ID, tr.Seq, tr.Kind),
		})

		edgeTuple := []any{"interrupt", tr.VectorAddr, handlerAddr}
		edgeB, _ := json.Marshal(edgeTuple)
		edgeSum := sha256.Sum256(edgeB)
		edgeID := hex.EncodeToString(edgeSum[:])

		res.Edges = append(res.Edges, recovery.Edge{
			ID:          edgeID,
			Kind:        "interrupt",
			Source:      fmt.Sprintf("vec-%06x", tr.VectorAddr),
			Destination: handlerAddr,
			Evidence:    []string{evidenceID},
		})
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
