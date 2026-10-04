package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
)

// OccurrenceReport represents the dynamic retirement and operand witness for an instruction occurrence.
type OccurrenceReport struct {
	Status                    string                   `json:"status"` // "available" or "unavailable"
	Reason                    string                   `json:"reason,omitempty"`
	StreamSHA256              string                   `json:"stream_sha256,omitempty"`
	PPUFrame                  *int                     `json:"ppu_frame,omitempty"`
	TraceFrame                *int                     `json:"trace_frame,omitempty"`
	Instruction               string                   `json:"instruction,omitempty"` // e.g. "09:F882"
	InstructionID             string                   `json:"instruction_id,omitempty"`
	Address                   uint32                   `json:"address,omitempty"` // e.g. 653442
	RetirementID              uint64                   `json:"retirement_id,omitempty"`
	Seq                       uint64                   `json:"seq,omitempty"`
	TotalMatches              int                      `json:"total_matches,omitempty"`
	MatchIndex                int                      `json:"match_index,omitempty"`
	MatchingRetirements       int                      `json:"matching_retirements,omitempty"`
	GlobalMatchingRetirements int                      `json:"global_matching_retirements,omitempty"`
	GlobalFirstSeq            uint64                   `json:"global_first_seq,omitempty"`
	Cycles                    Interval                 `json:"cycles,omitempty"`
	Entry                     OccurrenceRegisters      `json:"entry,omitempty"`
	Exit                      OccurrenceRegisters      `json:"exit,omitempty"`
	Changes                   []string                 `json:"changes,omitempty"`
	OperandBus                *OperandWitness          `json:"operand_bus,omitempty"`
	Companion                 *SignedWordCompanionCase `json:"companion,omitempty"`
	ValueChain                *ValueChainCard          `json:"value_chain,omitempty"`
	BranchComparison          *BranchComparisonCard    `json:"branch_comparison,omitempty"`
	StackComparison           *StackComparisonCard     `json:"stack_comparison,omitempty"`
}

// Interval represents a cycle range.
type Interval struct {
	Entry uint64 `json:"entry"`
	Exit  uint64 `json:"exit"`
}

// OccurrenceRegisters contains CPU register values.
type OccurrenceRegisters struct {
	A      uint16 `json:"a"`
	X      uint16 `json:"x"`
	Y      uint16 `json:"y"`
	S      uint16 `json:"s"`
	D      uint16 `json:"d"`
	DB     uint8  `json:"db"`
	PB     uint8  `json:"pb"`
	PC     uint16 `json:"pc"`
	P      uint8  `json:"p"`
	E      bool   `json:"e"`
	Cycles uint64 `json:"cycles"`
}

// OperandWitness describes the bus data access associated with an instruction occurrence.
type OperandWitness struct {
	ID           uint64 `json:"id"`
	Cycle        uint64 `json:"cycle"`
	Op           string `json:"op"`
	Space        string `json:"space"`
	Address      uint32 `json:"address"`
	EffectiveHex string `json:"effective_hex"`
	PhysicalHex  string `json:"physical_hex"`
	Value        uint8  `json:"value"`
	Before       *uint8 `json:"before,omitempty"`
	After        *uint8 `json:"after,omitempty"`
	SourceSpace  string `json:"source_space,omitempty"`
	SourceAddr   uint32 `json:"source_addr,omitempty"`
	Description  string `json:"description"`
}

// OccurrenceIndex holds admitted dynamic occurrences.
type OccurrenceIndex struct {
	StreamSHA256       string
	RunInfo            *trace.RunInfo
	byFrameAndID       map[int]map[string][]*OccurrenceReport
	byFrameAndAddr     map[int]map[uint32][]*OccurrenceReport
	frameCountsByID    map[int]map[string]int
	frameCountsByAddr  map[int]map[uint32]int
	globalCountsByID   map[string]int
	globalFirstSeqByID map[string]uint64
	reports            []*OccurrenceReport
	retainedEvents     map[uint64]trace.Event
	wramAccesses       map[uint32][]trace.Event
}

func newOccurrenceIndex(streamSHA string) *OccurrenceIndex {
	return &OccurrenceIndex{
		StreamSHA256:       streamSHA,
		byFrameAndID:       make(map[int]map[string][]*OccurrenceReport),
		byFrameAndAddr:     make(map[int]map[uint32][]*OccurrenceReport),
		frameCountsByID:    make(map[int]map[string]int),
		frameCountsByAddr:  make(map[int]map[uint32]int),
		globalCountsByID:   make(map[string]int),
		globalFirstSeqByID: make(map[string]uint64),
		retainedEvents:     make(map[uint64]trace.Event),
		wramAccesses:       make(map[uint32][]trace.Event),
	}
}

// RecordWRAMAccess records a physical WRAM bus access for projection validation.
func (idx *OccurrenceIndex) RecordWRAMAccess(physAddr uint32, ev trace.Event) {
	if idx == nil {
		return
	}
	if idx.wramAccesses == nil {
		idx.wramAccesses = make(map[uint32][]trace.Event)
	}
	idx.wramAccesses[physAddr] = append(idx.wramAccesses[physAddr], ev)
}

// GetWRAMAccesses returns all physical WRAM bus accesses for the given address.
func (idx *OccurrenceIndex) GetWRAMAccesses(physAddr uint32) []trace.Event {
	if idx == nil || idx.wramAccesses == nil {
		return nil
	}
	return idx.wramAccesses[physAddr]
}

// RetainEvent saves a physical or CPU trace event for companion and evidence validation.
func (idx *OccurrenceIndex) RetainEvent(ev trace.Event) {
	if idx == nil {
		return
	}
	if idx.retainedEvents == nil {
		idx.retainedEvents = make(map[uint64]trace.Event)
	}
	idx.retainedEvents[ev.ID] = ev
}

// GetRetainedEvent retrieves a previously retained trace event by ID.
func (idx *OccurrenceIndex) GetRetainedEvent(id uint64) (trace.Event, bool) {
	if idx == nil || idx.retainedEvents == nil {
		return trace.Event{}, false
	}
	ev, ok := idx.retainedEvents[id]
	return ev, ok
}

func computeCanonicalInstructionID(romHash string, insn *trace.Insn) string {
	if insn == nil || len(insn.Fetches) == 0 {
		return ""
	}
	addr := uint32(insn.Entry.PB)<<16 | uint32(insn.Entry.PC)
	var firstOffset uint32
	if insn.Fetches[0].ROMOffset != nil {
		firstOffset = *insn.Fetches[0].ROMOffset
	}
	var hexBytes strings.Builder
	for _, f := range insn.Fetches {
		fmt.Fprintf(&hexBytes, "%02x", f.Value)
	}
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
	return recovery.ComputeInstructionID(romHash, addr, firstOffset, hexBytes.String(), ctx)
}

func (idx *OccurrenceIndex) recordGlobal(instID string, seq uint64) {
	if instID == "" {
		return
	}
	idx.globalCountsByID[instID]++
	if _, ok := idx.globalFirstSeqByID[instID]; !ok {
		idx.globalFirstSeqByID[instID] = seq
	}
}

func (idx *OccurrenceIndex) countFrame(traceFrame int, instID string, addr uint32) int {
	if idx.frameCountsByID[traceFrame] == nil {
		idx.frameCountsByID[traceFrame] = make(map[string]int)
	}
	if idx.frameCountsByAddr[traceFrame] == nil {
		idx.frameCountsByAddr[traceFrame] = make(map[uint32]int)
	}
	if instID != "" {
		idx.frameCountsByID[traceFrame][instID]++
	}
	idx.frameCountsByAddr[traceFrame][addr]++
	if instID != "" {
		return idx.frameCountsByID[traceFrame][instID]
	}
	return idx.frameCountsByAddr[traceFrame][addr]
}

func (idx *OccurrenceIndex) addFirst(traceFrame int, instID string, addr uint32, rep *OccurrenceReport) {
	if idx.byFrameAndID[traceFrame] == nil {
		idx.byFrameAndID[traceFrame] = make(map[string][]*OccurrenceReport)
	}
	if idx.byFrameAndAddr[traceFrame] == nil {
		idx.byFrameAndAddr[traceFrame] = make(map[uint32][]*OccurrenceReport)
	}
	rep.InstructionID = instID
	rep.MatchIndex = 1
	if instID != "" {
		idx.byFrameAndID[traceFrame][instID] = append(idx.byFrameAndID[traceFrame][instID], rep)
	}
	idx.byFrameAndAddr[traceFrame][addr] = append(idx.byFrameAndAddr[traceFrame][addr], rep)
	idx.reports = append(idx.reports, rep)
}

func (idx *OccurrenceIndex) finalize() {
	for _, rep := range idx.reports {
		if rep.TraceFrame == nil {
			continue
		}
		tf := *rep.TraceFrame
		if rep.InstructionID != "" {
			rep.TotalMatches = idx.frameCountsByID[tf][rep.InstructionID]
			rep.MatchingRetirements = rep.TotalMatches
			rep.GlobalMatchingRetirements = idx.globalCountsByID[rep.InstructionID]
			rep.GlobalFirstSeq = idx.globalFirstSeqByID[rep.InstructionID]
		} else {
			rep.TotalMatches = idx.frameCountsByAddr[tf][rep.Address]
			rep.MatchingRetirements = rep.TotalMatches
		}
	}
}

// Lookup returns the first matching occurrence report for an instruction in a trace frame.
func (idx *OccurrenceIndex) Lookup(traceFrame int, instID string, addr uint32) *OccurrenceReport {
	if idx == nil {
		return &OccurrenceReport{
			Status: "unavailable",
			Reason: "no occurrence index available",
		}
	}
	if instID != "" {
		if fMap := idx.byFrameAndID[traceFrame]; fMap != nil {
			if reps := fMap[instID]; len(reps) > 0 {
				return reps[0]
			}
		}
		return &OccurrenceReport{
			Status: "unavailable",
			Reason: fmt.Sprintf("no occurrence found for canonical instruction %s in trace frame %d", instID, traceFrame),
		}
	}
	if addr != 0 {
		if fMap := idx.byFrameAndAddr[traceFrame]; fMap != nil {
			if reps := fMap[addr]; len(reps) > 0 {
				return reps[0]
			}
		}
		return &OccurrenceReport{
			Status: "unavailable",
			Reason: fmt.Sprintf("no occurrence found for address $%06X in trace frame %d", addr, traceFrame),
		}
	}
	return &OccurrenceReport{
		Status: "unavailable",
		Reason: fmt.Sprintf("no occurrence found in trace frame %d", traceFrame),
	}
}

func findOperandBusEvent(recentBus []trace.Event, insn *trace.Insn, lastRetirementID, retirementID uint64) *trace.Event {
	if insn == nil {
		return nil
	}
	fetchAddrs := make(map[uint32]bool)
	for _, f := range insn.Fetches {
		fetchAddrs[f.Addr] = true
	}
	var candidates []trace.Event
	for _, b := range recentBus {
		if (lastRetirementID == 0 || b.ID > lastRetirementID) && b.ID < retirementID && insn.Entry.Cycles <= b.Cycle && b.Cycle <= insn.Exit.Cycles {
			if b.Space == "cpu" && fetchAddrs[b.Addr] {
				continue
			}
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 1 {
		res := candidates[0]
		return &res
	}
	return nil
}

func buildOccurrenceReport(ev trace.Event, recentBus []trace.Event, lastRetirementID uint64, streamSHA string, ppuFrame *int, instID string) *OccurrenceReport {
	insn := ev.Insn
	if insn == nil {
		return nil
	}
	addr := uint32(insn.Entry.PB)<<16 | uint32(insn.Entry.PC)
	instStr := fmt.Sprintf("%02X:%04X", insn.Entry.PB, insn.Entry.PC)

	tf := int(ev.Frame)
	rep := &OccurrenceReport{
		Status:        "available",
		StreamSHA256:  streamSHA,
		PPUFrame:      ppuFrame,
		TraceFrame:    &tf,
		Instruction:   instStr,
		InstructionID: instID,
		Address:       addr,
		RetirementID:  ev.ID,
		Seq:           insn.Seq,
		Cycles: Interval{
			Entry: insn.Entry.Cycles,
			Exit:  insn.Exit.Cycles,
		},
		Entry: OccurrenceRegisters{
			A:      insn.Entry.A,
			X:      insn.Entry.X,
			Y:      insn.Entry.Y,
			S:      insn.Entry.S,
			D:      insn.Entry.D,
			DB:     insn.Entry.DB,
			PB:     insn.Entry.PB,
			PC:     insn.Entry.PC,
			P:      insn.Entry.P,
			E:      insn.Entry.E,
			Cycles: insn.Entry.Cycles,
		},
		Exit: OccurrenceRegisters{
			A:      insn.Exit.A,
			X:      insn.Exit.X,
			Y:      insn.Exit.Y,
			S:      insn.Exit.S,
			D:      insn.Exit.D,
			DB:     insn.Exit.DB,
			PB:     insn.Exit.PB,
			PC:     insn.Exit.PC,
			P:      insn.Exit.P,
			E:      insn.Exit.E,
			Cycles: insn.Exit.Cycles,
		},
	}

	var changes []string
	if insn.Entry.A != insn.Exit.A {
		changes = append(changes, fmt.Sprintf("A $%04X → $%04X", insn.Entry.A, insn.Exit.A))
	}
	if insn.Entry.X != insn.Exit.X {
		changes = append(changes, fmt.Sprintf("X $%04X → $%04X", insn.Entry.X, insn.Exit.X))
	}
	if insn.Entry.Y != insn.Exit.Y {
		changes = append(changes, fmt.Sprintf("Y $%04X → $%04X", insn.Entry.Y, insn.Exit.Y))
	}
	if insn.Entry.S != insn.Exit.S {
		changes = append(changes, fmt.Sprintf("S $%04X → $%04X", insn.Entry.S, insn.Exit.S))
	}
	if insn.Entry.D != insn.Exit.D {
		changes = append(changes, fmt.Sprintf("D $%04X → $%04X", insn.Entry.D, insn.Exit.D))
	}
	if insn.Entry.DB != insn.Exit.DB {
		changes = append(changes, fmt.Sprintf("DB $%02X → $%02X", insn.Entry.DB, insn.Exit.DB))
	}
	if insn.Entry.PB != insn.Exit.PB {
		changes = append(changes, fmt.Sprintf("PB $%02X → $%02X", insn.Entry.PB, insn.Exit.PB))
	}
	if insn.Entry.PC != insn.Exit.PC {
		changes = append(changes, fmt.Sprintf("PC $%04X → $%04X", insn.Entry.PC, insn.Exit.PC))
	}
	if insn.Entry.P != insn.Exit.P {
		changes = append(changes, fmt.Sprintf("P $%02X → $%02X", insn.Entry.P, insn.Exit.P))
	}
	if insn.Entry.E != insn.Exit.E {
		changes = append(changes, fmt.Sprintf("E %v → %v", insn.Entry.E, insn.Exit.E))
	}
	rep.Changes = changes

	// Operand witness reconstruction for supported instructions
	if addr == 0x09F882 || addr == 0x09F884 || addr == 0x09F887 || addr == 0x0CC468 || addr == 0x0CC46E || addr == 0x0CC120 {
		busEv := findOperandBusEvent(recentBus, insn, lastRetirementID, ev.ID)
		if busEv != nil {
			effHex, physHex, desc, ok := validateOperand(insn, busEv)
			if ok {
				w := &OperandWitness{
					ID:           busEv.ID,
					Cycle:        busEv.Cycle,
					Op:           busEv.Op,
					Space:        busEv.Space,
					Address:      busEv.Addr,
					Value:        uint8(busEv.Value),
					EffectiveHex: effHex,
					PhysicalHex:  physHex,
					Description:  desc,
				}
				if busEv.Before != nil {
					b := uint8(*busEv.Before)
					w.Before = &b
				}
				if busEv.After != nil {
					a := uint8(*busEv.After)
					w.After = &a
				}
				if busEv.Source.Space != "" {
					w.SourceSpace = busEv.Source.Space
					w.SourceAddr = busEv.Source.Start
				}
				rep.OperandBus = w
			}
		}
	}

	return rep
}

func validateOperand(insn *trace.Insn, busEv *trace.Event) (effectiveHex, physicalHex, desc string, ok bool) {
	if insn == nil || busEv == nil || len(insn.Fetches) == 0 {
		return "", "", "", false
	}
	if busEv.Width != 1 || busEv.Value > 0xFF {
		return "", "", "", false
	}
	if busEv.Before != nil && *busEv.Before > 0xFF {
		return "", "", "", false
	}
	if busEv.After != nil && *busEv.After > 0xFF {
		return "", "", "", false
	}

	addr := uint32(insn.Entry.PB)<<16 | uint32(insn.Entry.PC)
	opcode := insn.Fetches[0].Value

	switch addr {
	case 0x09F882: // LDY dp ($A4)
		if opcode != 0xA4 || len(insn.Fetches) < 2 {
			return "", "", "", false
		}
		isX8 := insn.Entry.E || (insn.Entry.P&0x10 != 0)
		if !isX8 {
			return "", "", "", false
		}
		if busEv.Op != "read" {
			return "", "", "", false
		}
		dp := uint32(insn.Fetches[1].Value)
		logicalAddr := (uint32(insn.Entry.D) + dp) & 0xFFFF
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if expectedSpace != "wram" || busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Exit.Y) {
			return "", "", "", false
		}
		effHex := fmt.Sprintf("$%04X", logicalAddr)
		physHex := fmt.Sprintf("%02X:%04X", 0x7E+(expectedPhysAddr>>16), expectedPhysAddr&0xFFFF)
		desc := fmt.Sprintf("Direct page $%04X + $%02X = $%04X; normalized WRAM byte %d ($%02X)", insn.Entry.D, dp, logicalAddr, busEv.Value, busEv.Value)
		return effHex, physHex, desc, true

	case 0x0CC120: // LDA dp ($A5)
		if opcode != 0xA5 || len(insn.Fetches) < 2 {
			return "", "", "", false
		}
		isM8 := insn.Entry.E || (insn.Entry.P&0x20 != 0)
		if !isM8 {
			return "", "", "", false
		}
		if busEv.Op != "read" {
			return "", "", "", false
		}
		dp := uint32(insn.Fetches[1].Value)
		logicalAddr := (uint32(insn.Entry.D) + dp) & 0xFFFF
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if expectedSpace != "wram" || busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Exit.A) {
			return "", "", "", false
		}
		effHex := fmt.Sprintf("$%04X", logicalAddr)
		physHex := fmt.Sprintf("%02X:%04X", 0x7E+(expectedPhysAddr>>16), expectedPhysAddr&0xFFFF)
		desc := fmt.Sprintf("Direct page $%04X + $%02X = $%04X; normalized WRAM byte %d ($%02X)", insn.Entry.D, dp, logicalAddr, busEv.Value, busEv.Value)
		return effHex, physHex, desc, true

	case 0x09F884: // LDA abs,Y ($B9)
		if opcode != 0xB9 || len(insn.Fetches) < 3 {
			return "", "", "", false
		}
		isM8 := insn.Entry.E || (insn.Entry.P&0x20 != 0)
		if !isM8 {
			return "", "", "", false
		}
		if busEv.Op != "read" {
			return "", "", "", false
		}
		base := uint32(insn.Fetches[1].Value) | uint32(insn.Fetches[2].Value)<<8
		logicalAddr := (uint32(insn.Entry.DB) << 16) | ((base + uint32(insn.Entry.Y)) & 0xFFFF)
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if busEv.Source.Space != "rom" {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Exit.A) {
			return "", "", "", false
		}
		romOff := busEv.Source.Start
		effHex := fmt.Sprintf("$%02X:%04X", logicalAddr>>16, logicalAddr&0xFFFF)
		physHex := fmt.Sprintf("ROM $%06X", romOff)
		desc := fmt.Sprintf("DB $%02X, base $%04X, Y $%02X: CPU $%02X:%04X; ROM offset $%06X, byte %d ($%02X)", insn.Entry.DB, base, insn.Entry.Y, logicalAddr>>16, logicalAddr&0xFFFF, romOff, busEv.Value, busEv.Value)
		return effHex, physHex, desc, true

	case 0x09F887: // STA dp ($85)
		if opcode != 0x85 || len(insn.Fetches) < 2 {
			return "", "", "", false
		}
		isM8 := insn.Entry.E || (insn.Entry.P&0x20 != 0)
		if !isM8 {
			return "", "", "", false
		}
		if busEv.Op != "write" {
			return "", "", "", false
		}
		dp := uint32(insn.Fetches[1].Value)
		logicalAddr := (uint32(insn.Entry.D) + dp) & 0xFFFF
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if expectedSpace != "wram" || busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Entry.A) {
			return "", "", "", false
		}
		effHex := fmt.Sprintf("$%04X", logicalAddr)
		physHex := fmt.Sprintf("%02X:%04X", 0x7E+(expectedPhysAddr>>16), expectedPhysAddr&0xFFFF)
		var desc string
		if busEv.Before != nil && busEv.After != nil {
			desc = fmt.Sprintf("Direct page $%04X + $%02X = $%04X; normalized WRAM byte %d ($%02X) becomes %d ($%02X)", insn.Entry.D, dp, logicalAddr, *busEv.Before, *busEv.Before, *busEv.After, *busEv.After)
		} else {
			desc = fmt.Sprintf("Direct page $%04X + $%02X = $%04X; write WRAM byte %d ($%02X)", insn.Entry.D, dp, logicalAddr, busEv.Value, busEv.Value)
		}
		return effHex, physHex, desc, true

	case 0x0CC468: // LDA abs ($AD)
		if opcode != 0xAD || len(insn.Fetches) < 3 {
			return "", "", "", false
		}
		isM8 := insn.Entry.E || (insn.Entry.P&0x20 != 0)
		if !isM8 {
			return "", "", "", false
		}
		if busEv.Op != "read" {
			return "", "", "", false
		}
		absAddr := uint32(insn.Fetches[1].Value) | uint32(insn.Fetches[2].Value)<<8
		logicalAddr := (uint32(insn.Entry.DB) << 16) | absAddr
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if expectedSpace != "wram" || busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Exit.A) {
			return "", "", "", false
		}
		effHex := fmt.Sprintf("$%04X", absAddr)
		physHex := fmt.Sprintf("%02X:%04X", 0x7E+(expectedPhysAddr>>16), expectedPhysAddr&0xFFFF)
		desc := fmt.Sprintf("Absolute address $%04X; read WRAM byte %d", absAddr, busEv.Value)
		return effHex, physHex, desc, true

	case 0x0CC46E: // STA abs ($8D)
		if opcode != 0x8D || len(insn.Fetches) < 3 {
			return "", "", "", false
		}
		isM8 := insn.Entry.E || (insn.Entry.P&0x20 != 0)
		if !isM8 {
			return "", "", "", false
		}
		if busEv.Op != "write" {
			return "", "", "", false
		}
		absAddr := uint32(insn.Fetches[1].Value) | uint32(insn.Fetches[2].Value)<<8
		logicalAddr := (uint32(insn.Entry.DB) << 16) | absAddr
		expectedSpace, expectedPhysAddr := trace.CPUSpace(logicalAddr)
		if expectedSpace != "wram" || busEv.Space != expectedSpace || busEv.Addr != expectedPhysAddr {
			return "", "", "", false
		}
		if uint8(busEv.Value) != uint8(insn.Entry.A) {
			return "", "", "", false
		}
		effHex := fmt.Sprintf("$%04X", absAddr)
		physHex := fmt.Sprintf("%02X:%04X", 0x7E+(expectedPhysAddr>>16), expectedPhysAddr&0xFFFF)
		var desc string
		if busEv.Before != nil && busEv.After != nil {
			desc = fmt.Sprintf("Absolute address $%04X; write WRAM byte %d (was %d)", absAddr, *busEv.After, *busEv.Before)
		} else {
			desc = fmt.Sprintf("Absolute address $%04X; write WRAM byte %d", absAddr, busEv.Value)
		}
		return effHex, physHex, desc, true

	default:
		return "", "", "", false
	}
}

func (s *Server) lookupOccurrence(inst recovery.Instruction, traceFrame *int, ppuFrame *int) *OccurrenceReport {
	if s.Occurrences == nil {
		return &OccurrenceReport{
			Status: "unavailable",
			Reason: "dynamic occurrence evidence unavailable for this project/stream",
		}
	}
	var tf int
	if traceFrame != nil {
		tf = *traceFrame
	} else if ppuFrame != nil && s.FrameCapture != nil {
		found := false
		for _, rec := range s.FrameCapture.Records {
			if rec.Number == *ppuFrame && rec.TraceFrame != nil {
				tf = *rec.TraceFrame
				found = true
				break
			}
		}
		if !found {
			return &OccurrenceReport{
				Status: "unavailable",
				Reason: fmt.Sprintf("no trace frame mapped for PPU frame %d", *ppuFrame),
			}
		}
	} else {
		return &OccurrenceReport{
			Status: "unavailable",
			Reason: "no trace frame specified",
		}
	}
	rep := s.Occurrences.Lookup(tf, inst.ID, inst.Address)
	if rep != nil && rep.Status == "available" && s.SignedWords != nil {
		cloned := *rep
		cloned.Companion = s.SignedWords.Lookup(cloned.StreamSHA256, tf, cloned.RetirementID, inst.ID, inst.Address)
		return &cloned
	}
	return rep
}

func (s *Server) handleOccurrence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if s.Document == nil {
		writeJSON(w, &OccurrenceReport{
			Status: "unavailable",
			Reason: "no recovery document loaded",
		})
		return
	}
	var found *recovery.Instruction
	switch {
	case q.Get("instruction") != "":
		id := q.Get("instruction")
		for _, inst := range s.Document.Instructions {
			if inst.ID == id {
				found = &inst
				break
			}
		}
	case q.Get("addr") != "":
		a, err := parseAddress(q.Get("addr"))
		if err == nil {
			for _, inst := range s.Document.Instructions {
				if inst.Address == a {
					found = &inst
					break
				}
			}
		}
	}
	if found == nil {
		writeJSON(w, &OccurrenceReport{
			Status: "unavailable",
			Reason: "instruction not found",
		})
		return
	}
	var tfPtr *int
	if tfStr := q.Get("trace_frame"); tfStr != "" {
		if tfVal, err := strconv.Atoi(tfStr); err == nil {
			tfPtr = &tfVal
		}
	}
	var pfPtr *int
	if pfStr := q.Get("frame"); pfStr != "" {
		if pfVal, err := strconv.Atoi(pfStr); err == nil {
			pfPtr = &pfVal
		}
	}
	rep := s.lookupOccurrence(*found, tfPtr, pfPtr)
	writeJSON(w, s.presentOccurrence(rep))
}

// presentOccurrence decorates an admitted occurrence report with contextual analysis cards
// (such as the verified value chain card) without mutating the underlying cached index record
// or introducing recursive nesting.
func (s *Server) presentOccurrence(rep *OccurrenceReport) *OccurrenceReport {
	if rep == nil || rep.Status != "available" {
		return rep
	}
	cloned := *rep
	if rep.InstructionID == ValueChainNode0CanonicalID ||
		rep.InstructionID == ValueChainNode1CanonicalID ||
		rep.InstructionID == ValueChainNode2CanonicalID {
		cloned.ValueChain = s.BuildValueChainCard()
	}
	if rep.InstructionID == BranchNode2CanonicalID &&
		rep.RetirementID == ExpectedBranchNode2RetirementID &&
		rep.Seq == ExpectedBranchNode2Seq &&
		(rep.TraceFrame != nil && *rep.TraceFrame == 0) &&
		(rep.PPUFrame == nil || *rep.PPUFrame == 332) {
		cloned.BranchComparison = s.LoadBranchComparisonBundle()
	}
	if rep.InstructionID == StackNode0CanonicalID &&
		rep.RetirementID == ExpectedStackNode0RetirementID &&
		rep.Seq == ExpectedStackNode0Seq &&
		(rep.TraceFrame != nil && *rep.TraceFrame == 0) &&
		(rep.PPUFrame == nil || *rep.PPUFrame == 332) {
		cloned.StackComparison = s.LoadStackComparisonBundle()
	}
	return &cloned
}
