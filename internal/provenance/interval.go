package provenance

import (
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/trace"
)

// ByteInterval represents the continuous lifespan of a physical byte version
// from its initial store to replacement write or window end.
type ByteInterval struct {
	Schema               string                `json:"schema"`
	PhysicalAddress      uint32                `json:"physical_address"`
	PhysicalAddressHex   string                `json:"physical_address_hex"`
	Value                uint8                 `json:"value"`
	InitialStore         IntervalTransaction   `json:"initial_store"`
	Readers              []IntervalTransaction `json:"readers"`
	Replacement          *IntervalTransaction  `json:"replacement,omitempty"`
	Termination          string                `json:"termination"` // "overwritten" or "window_end"
	HostFrames            FrameSpan             `json:"host_frames"`
	HostFrameOffset       int                   `json:"host_frame_offset"`
	PPUFrames             FrameSpan             `json:"ppu_frames"`
	Cycles                CycleSpan             `json:"cycles"`
	CapturedProofEligible bool                  `json:"captured_proof_eligible"`
	CorrespondenceStatus  string                `json:"correspondence_status"` // "complete", "partial", "unavailable"
	Limitations           []string              `json:"limitations"`
}

// FrameSpan records beginning and ending frame boundaries.
type FrameSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// CycleSpan records beginning and ending cycle boundaries.
type CycleSpan struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}

// IntervalTransaction records one memory bus transaction during the byte's lifespan,
// correlated with its CPU retirement sequence if available.
type IntervalTransaction struct {
	Event                 Event     `json:"event"`
	PhysicalAddress       uint32    `json:"physical_address"`
	Op                    string    `json:"op"`
	Value                 uint8     `json:"value"`
	Cycle                 uint64    `json:"cycle"`
	HostFrame             int       `json:"host_frame"`
	PPUFrame              int       `json:"ppu_frame"`
	Actor                 string    `json:"actor,omitempty"`
	Correlated            bool      `json:"correlated"`
	TraceBusID            uint64    `json:"trace_bus_id,omitempty"`
	RetirementID          uint64    `json:"retirement_id,omitempty"`
	Seq                   uint64    `json:"seq,omitempty"`
	PrecedingRetirementID uint64    `json:"preceding_retirement_id,omitempty"`
	Instruction           string    `json:"instruction,omitempty"`
	InstructionID         string    `json:"instruction_id,omitempty"`
	InstructionAddress    uint32    `json:"instruction_address,omitempty"`
	InstructionBytes      string    `json:"instruction_bytes,omitempty"`
	InstructionCycles     CycleSpan `json:"instruction_cycles,omitempty"`
	RegisterChanges       []string  `json:"register_changes,omitempty"`
	Status                string    `json:"status,omitempty"`
}

// RetirementCorrespondence supplies the CPU retirement context for a bus access.
type RetirementCorrespondence struct {
	TraceBusID            uint64
	RetirementID          uint64
	Seq                   uint64
	PrecedingRetirementID uint64
	Instruction           string
	InstructionID         string
	InstructionAddress    uint32
	InstructionBytes      string
	InstructionCycles     CycleSpan
	RegisterChanges       []string
	Status                string
}

// OccurrenceCorrelator matches physical bus events to CPU retirement records.
type OccurrenceCorrelator interface {
	CorrelateBus(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool)
}

// IntervalProjectionChecker validates that the complete physical byte projection
// from an admitted trace matches the continuous lifespan interval.
type IntervalProjectionChecker interface {
	CheckProjection(addr uint32, startCycle, endCycle uint64, expectedCycles []uint64) (bool, string)
	CheckProjectionEvents(addr uint32, startCycle, endCycle uint64, expectedEvents []Event) (bool, string)
}

// OccurrenceCorrelatorFunc adapts an ordinary function to OccurrenceCorrelator.
type OccurrenceCorrelatorFunc func(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool)

// CorrelateBus implements OccurrenceCorrelator.
func (f OccurrenceCorrelatorFunc) CorrelateBus(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool) {
	return f(cycle, addr, op, val)
}

// BuildByteInterval joins a selected WRAM write to subsequent reads and replacement write
// across an observation window, correlating memory bus events with CPU retirements.
func BuildByteInterval(w Window, pin string, writerID uint64, correlator OccurrenceCorrelator) (ByteInterval, error) {
	var out ByteInterval
	if err := validateWindow(w, pin); err != nil {
		return out, err
	}
	if writerID >= uint64(len(w.Events)) {
		return out, fmt.Errorf("writer event is outside window")
	}
	writer := w.Events[writerID]
	address, ok, err := wramEvent(writer)
	if err != nil {
		return out, err
	}
	if !ok || writer.Op != "write" || (writer.Actor != "cpu" && writer.Actor != "dma_or_hdma") {
		return out, fmt.Errorf("selected event is not an attributed WRAM write")
	}
	// Validate every bus event before publishing
	for _, e := range w.Events {
		if _, _, err := wramEvent(e); err != nil {
			return out, err
		}
	}

	initialTx := makeIntervalTransaction(writer, address, correlator)
	out = ByteInterval{
		Schema:             "snes-byte-interval-v1",
		PhysicalAddress:    address,
		PhysicalAddressHex: fmt.Sprintf("$%06X", address),
		Value:                 writer.Value,
		InitialStore:          initialTx,
		Readers:               []IntervalTransaction{},
		Termination:           "window_end",
		HostFrameOffset:       108,
		CapturedProofEligible: false,
		Limitations: []string{
			"writer completeness is the declared producer contract, not independently established by absence of events",
			"byte reads do not establish arithmetic propagation or a dependency on subsequent writes",
			"cross-execution correspondence establishes matching observable transactions across repetitions, not same-run causality",
			"separately produced pinned repeated executions: establishes matching observable transactions, not same-run identity or whole-program equivalence",
			"mixed trace has no explicit actor; exact local CPU operand association supports these links",
			"window host frames 108..110 vs mixed logical frames 0..2 use explicit 108 offset; PPU frames 332..334 stay distinct",
			"captured_proof_eligible is false; no arithmetic propagation or pixel ownership claimed",
			"pixel ownership and readers outside the recorded window are unknown",
		},
	}

	totalEvents := 1
	correlatedEvents := 0
	if initialTx.Correlated {
		correlatedEvents++
	}

	for _, e := range w.Events[writerID+1:] {
		a, memory, _ := wramEvent(e)
		if !memory || a != address {
			continue
		}
		if e.Op == "write" {
			repTx := makeIntervalTransaction(e, address, correlator)
			out.Replacement = &repTx
			out.Termination = "overwritten"
			totalEvents++
			if repTx.Correlated {
				correlatedEvents++
			}
			break
		}
		if e.Value != writer.Value {
			return ByteInterval{}, fmt.Errorf("event %d: read differs from selected WRAM byte version", e.ID)
		}
		rTx := makeIntervalTransaction(e, address, correlator)
		out.Readers = append(out.Readers, rTx)
		totalEvents++
		if rTx.Correlated {
			correlatedEvents++
		}
	}

	out.Cycles = CycleSpan{
		Start: writer.Cycle,
	}
	if out.Replacement != nil {
		out.Cycles.End = out.Replacement.Cycle
	} else if len(out.Readers) > 0 {
		out.Cycles.End = out.Readers[len(out.Readers)-1].Cycle
	} else {
		out.Cycles.End = writer.Cycle
	}

	// Dual frame domains: Host Frame and PPU Frame
	out.HostFrames.Start = writer.Frame
	out.PPUFrames.Start = writer.PPUFrame

	if out.Replacement != nil {
		out.HostFrames.End = out.Replacement.HostFrame
		out.PPUFrames.End = out.Replacement.PPUFrame
	} else {
		if w.To > writer.Frame {
			out.HostFrames.End = w.To - 1
		} else {
			out.HostFrames.End = writer.Frame
		}
		if len(w.Frames) > 0 {
			out.PPUFrames.End = int(w.Frames[len(w.Frames)-1].PPUFrame)
		} else {
			out.PPUFrames.End = writer.PPUFrame
		}
	}

	if correlatedEvents == totalEvents && totalEvents > 0 {
		out.CorrespondenceStatus = "complete"
	} else if correlatedEvents > 0 {
		out.CorrespondenceStatus = "partial"
	} else {
		out.CorrespondenceStatus = "unavailable"
	}

	if checker, ok := correlator.(IntervalProjectionChecker); ok {
		var expectedEvents []Event
		expectedEvents = append(expectedEvents, initialTx.Event)
		for _, r := range out.Readers {
			expectedEvents = append(expectedEvents, r.Event)
		}
		if out.Replacement != nil {
			expectedEvents = append(expectedEvents, out.Replacement.Event)
		}
		if ok, reason := checker.CheckProjectionEvents(address, out.Cycles.Start, out.Cycles.End, expectedEvents); !ok {
			out.CorrespondenceStatus = "partial"
			if reason != "" {
				out.Limitations = append(out.Limitations, reason)
			}
		}
	}

	return out, nil
}

func makeIntervalTransaction(e Event, address uint32, correlator OccurrenceCorrelator) IntervalTransaction {
	tx := IntervalTransaction{
		Event:           e,
		PhysicalAddress: address,
		Op:              e.Op,
		Value:           e.Value,
		Cycle:           e.Cycle,
		HostFrame:       e.Frame,
		PPUFrame:        e.PPUFrame,
		Actor:           e.Actor,
		Status:          "observed_window_event",
	}
	if correlator != nil {
		if corr, ok := correlator.CorrelateBus(e.Cycle, address, e.Op, e.Value); ok && corr != nil {
			tx.Correlated = true
			tx.TraceBusID = corr.TraceBusID
			tx.RetirementID = corr.RetirementID
			tx.Seq = corr.Seq
			tx.PrecedingRetirementID = corr.PrecedingRetirementID
			tx.Instruction = corr.Instruction
			tx.InstructionID = corr.InstructionID
			tx.InstructionAddress = corr.InstructionAddress
			tx.InstructionBytes = corr.InstructionBytes
			tx.InstructionCycles = corr.InstructionCycles
			tx.RegisterChanges = corr.RegisterChanges
			if corr.Status != "" {
				tx.Status = corr.Status
			} else {
				tx.Status = "correlated_retirement"
			}
		}
	}
	return tx
}

type traceCorrelatorMap struct {
	byBusKey map[busKey]*RetirementCorrespondence
}

type busKey struct {
	cycle uint64
	addr  uint32
	op    string
	val   uint8
}

func (m *traceCorrelatorMap) CorrelateBus(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool) {
	if m == nil || m.byBusKey == nil {
		return nil, false
	}
	k := busKey{cycle: cycle, addr: addr, op: op, val: val}
	if c, ok := m.byBusKey[k]; ok {
		return c, true
	}
	if addr >= 0x7E0000 {
		k16 := busKey{cycle: cycle, addr: addr & 0xFFFF, op: op, val: val}
		if c, ok := m.byBusKey[k16]; ok {
			return c, true
		}
	}
	return nil, false
}

// TraceCorrelatorFromEvents builds an OccurrenceCorrelator from raw trace events.
func TraceCorrelatorFromEvents(events []trace.Event) OccurrenceCorrelator {
	var busEvents []trace.Event
	var cpuEvents []trace.Event
	prevRetirementIDByRetID := make(map[uint64]uint64)

	var lastRetID uint64
	for _, ev := range events {
		if ev.Kind == "bus" {
			busEvents = append(busEvents, ev)
		} else if ev.Kind == "cpu_insn" && ev.Insn != nil && ev.Insn.Status == "retired" {
			if lastRetID > 0 {
				prevRetirementIDByRetID[ev.ID] = lastRetID
			}
			lastRetID = ev.ID
			cpuEvents = append(cpuEvents, ev)
		}
	}

	corrMap := &traceCorrelatorMap{
		byBusKey: make(map[busKey]*RetirementCorrespondence),
	}

	for _, b := range busEvents {
		var owning *trace.Event
		for i := range cpuEvents {
			cand := &cpuEvents[i]
			if cand.ID > b.ID && cand.Insn.Entry.Cycles <= b.Cycle && b.Cycle <= cand.Insn.Exit.Cycles {
				owning = cand
				break
			}
		}
		if owning == nil || owning.Insn == nil {
			continue
		}

		// Exclude instruction fetch reads
		isFetch := false
		for _, f := range owning.Insn.Fetches {
			if b.Op == "read" && b.Addr == f.Addr {
				isFetch = true
				break
			}
		}
		if isFetch {
			continue
		}

		precID := prevRetirementIDByRetID[owning.ID]
		if precID >= b.ID {
			continue
		}

		insn := owning.Insn
		addr := uint32(insn.Entry.PB)<<16 | uint32(insn.Entry.PC)
		instStr := fmt.Sprintf("%02X:%04X", insn.Entry.PB, insn.Entry.PC)

		var hexBytes strings.Builder
		for _, f := range insn.Fetches {
			fmt.Fprintf(&hexBytes, "%02x", f.Value)
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

		if b.Op == "write" {
			if b.Before != nil && b.After != nil {
				changes = append(changes, fmt.Sprintf("write %d (was %d)", *b.After, *b.Before))
			} else {
				changes = append(changes, fmt.Sprintf("write %d", b.Value))
			}
		}

		c := &RetirementCorrespondence{
			TraceBusID:            b.ID,
			RetirementID:          owning.ID,
			Seq:                   insn.Seq,
			PrecedingRetirementID: precID,
			Instruction:           instStr,
			InstructionID:         fmt.Sprintf("%06x-%d", addr, insn.Seq),
			InstructionAddress:    addr,
			InstructionBytes:      hexBytes.String(),
			InstructionCycles: CycleSpan{
				Start: insn.Entry.Cycles,
				End:   insn.Exit.Cycles,
			},
			RegisterChanges: changes,
			Status:          "correlated_trace_retirement",
		}

		normAddr := b.Addr
		if b.Space == "wram" && normAddr < 0x20000 {
			normAddr = 0x7E0000 + normAddr
		}
		kNorm := busKey{cycle: b.Cycle, addr: normAddr, op: b.Op, val: uint8(b.Value)}
		if existing, exists := corrMap.byBusKey[kNorm]; exists && existing.RetirementID != c.RetirementID {
			delete(corrMap.byBusKey, kNorm)
		} else {
			corrMap.byBusKey[kNorm] = c
		}
		kRaw := busKey{cycle: b.Cycle, addr: b.Addr, op: b.Op, val: uint8(b.Value)}
		if existing, exists := corrMap.byBusKey[kRaw]; exists && existing.RetirementID != c.RetirementID {
			delete(corrMap.byBusKey, kRaw)
		} else {
			corrMap.byBusKey[kRaw] = c
		}
	}

	return corrMap
}
