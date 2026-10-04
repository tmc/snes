package server

import (
	"fmt"

	"github.com/tmc/snes/internal/trace"
)

// ClassifiedBusEvent represents an admitted and classified bus event in the slice.
type ClassifiedBusEvent struct {
	Event         trace.Event `json:"event"`
	Role          string      `json:"role"` // "opcode_fetch", "operand_fetch", "data_read", "data_write"
	InstructionID string      `json:"instruction_id"`
	InstructionPC uint32      `json:"instruction_pc"`
	FetchIndex    int         `json:"fetch_index,omitempty"`
	LogicalAddr   uint32      `json:"logical_addr,omitempty"`
	PhysicalAddr  uint32      `json:"physical_addr,omitempty"`
	EffectiveAddr uint32      `json:"effective_addr,omitempty"`
}

// SliceAdmissionResult contains the admitted and classified events for the 139220 slice.
type SliceAdmissionResult struct {
	PrecedingRetirement trace.Event          `json:"preceding_retirement"`
	SliceInstructions   []trace.Event        `json:"slice_instructions"`
	ClassifiedBusEvents []ClassifiedBusEvent `json:"classified_bus_events"`
	InputRead           trace.Event          `json:"input_read"`
	OutputWrite         trace.Event          `json:"output_write"`
	InputLogicalAddr    uint32               `json:"input_logical_addr"`
	InputPhysicalAddr   uint32               `json:"input_physical_addr"`
	OutputLogicalAddr   uint32               `json:"output_logical_addr"`
	OutputPhysicalAddr  uint32               `json:"output_physical_addr"`
	TotalFetches        int                  `json:"total_fetches"`
	TotalBusEvents      int                  `json:"total_bus_events"`
}

// AdmitReplaySliceEvents enforces exact physical ownership, instruction fetch classification,
// and in-span projection bounds for the 139220 slice using companion and occurrence validations.
func AdmitReplaySliceEvents(events []trace.Event, streamSHA string) (*SliceAdmissionResult, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("admit slice events: empty event list")
	}

	occIndex := newOccurrenceIndex(streamSHA)
	for _, ev := range events {
		occIndex.RetainEvent(ev)
	}

	// 1. Locate the 4 slice retirement instructions
	expectedPCs := []uint16{0xC468, 0xC46B, 0xC46C, 0xC46E}
	var sliceInsns []trace.Event
	for _, ev := range events {
		if ev.Kind == "cpu_insn" && ev.Insn != nil && ev.Insn.Status == "retired" {
			if ev.Insn.Entry.PB == 0x0C && len(sliceInsns) < len(expectedPCs) {
				if ev.Insn.Entry.PC == expectedPCs[len(sliceInsns)] {
					sliceInsns = append(sliceInsns, ev)
				}
			}
		}
	}
	if len(sliceInsns) != 4 {
		return nil, fmt.Errorf("admit slice events: expected 4 slice retirement instructions, found %d", len(sliceInsns))
	}

	// Verify sequential Seq order of slice instructions
	for i := 1; i < len(sliceInsns); i++ {
		if sliceInsns[i].Insn.Seq != sliceInsns[i-1].Insn.Seq+1 {
			return nil, fmt.Errorf("admit slice events: non-consecutive Seq between insn %d (seq %d) and %d (seq %d)",
				i-1, sliceInsns[i-1].Insn.Seq, i, sliceInsns[i].Insn.Seq)
		}
	}

	// 2. Locate and validate preceding retirement using companion helper
	insn0 := sliceInsns[0]
	prevEv, ok := findPrecedingRetirement(occIndex, insn0)
	if !ok {
		return nil, fmt.Errorf("admit slice events: failed to locate preceding retirement for instruction %d", insn0.ID)
	}
	if prevEv.Insn.SuccessorPC.Bank != insn0.Insn.Entry.PB || prevEv.Insn.SuccessorPC.Addr != insn0.Insn.Entry.PC {
		return nil, fmt.Errorf("admit slice events: preceding retirement %d successor PC %02X:%04X does not match insn 0 entry %02X:%04X",
			prevEv.ID, prevEv.Insn.SuccessorPC.Bank, prevEv.Insn.SuccessorPC.Addr, insn0.Insn.Entry.PB, insn0.Insn.Entry.PC)
	}
	if prevEv.Insn.Exit.A != insn0.Insn.Entry.A || prevEv.Insn.Exit.X != insn0.Insn.Entry.X ||
		prevEv.Insn.Exit.Y != insn0.Insn.Entry.Y || prevEv.Insn.Exit.P != insn0.Insn.Entry.P ||
		prevEv.Insn.Exit.S != insn0.Insn.Entry.S || prevEv.Insn.Exit.D != insn0.Insn.Entry.D ||
		prevEv.Insn.Exit.DB != insn0.Insn.Entry.DB || prevEv.Insn.Exit.PB != insn0.Insn.Entry.PB ||
		prevEv.Insn.Exit.E != insn0.Insn.Entry.E {
		return nil, fmt.Errorf("admit slice events: preceding retirement %d exit state does not match insn 0 entry state", prevEv.ID)
	}

	// 3. Physical operand derivation and ownership for Step 0 (LDA $0CC468)
	if len(insn0.Insn.Fetches) < 3 || insn0.Insn.Fetches[0].Value != 0xAD {
		return nil, fmt.Errorf("admit slice events: insn 0 opcode mismatch (expected 0xAD with 3 fetches)")
	}
	isM8 := insn0.Insn.Entry.E || (insn0.Insn.Entry.P&0x20 != 0)
	if !isM8 {
		return nil, fmt.Errorf("admit slice events: insn 0 expected 8-bit accumulator mode")
	}
	ldaAbsAddr := uint32(insn0.Insn.Fetches[1].Value) | uint32(insn0.Insn.Fetches[2].Value)<<8
	ldaLogicalAddr := (uint32(insn0.Insn.Entry.DB) << 16) | ldaAbsAddr
	ldaExpectedSpace, ldaExpectedPhysAddr := trace.CPUSpace(ldaLogicalAddr)
	if ldaExpectedSpace != "wram" {
		return nil, fmt.Errorf("admit slice events: insn 0 expected wram space for logical addr $%06X, got %s", ldaLogicalAddr, ldaExpectedSpace)
	}
	ldaExpectedCanonical := busCanonicalAddr(ldaExpectedPhysAddr)
	expectedReadVal := uint8(insn0.Insn.Exit.A)

	// Reuse findUniqueRetainedOperand from companion.go
	readOperand, ok := findUniqueRetainedOperand(occIndex, insn0, prevEv, "read", ldaExpectedCanonical, expectedReadVal)
	if !ok || readOperand == nil {
		return nil, fmt.Errorf("admit slice events: failed to locate unique retained read operand for insn 0")
	}
	// Add bus CPU effective-address join against derived logical address
	if readOperand.CPU == nil || readOperand.CPU.EffectiveAddr == nil || *readOperand.CPU.EffectiveAddr != ldaLogicalAddr {
		return nil, fmt.Errorf("admit slice events: read operand %d effective address mismatch: got %v, want $%06X",
			readOperand.ID, readOperand.CPU, ldaLogicalAddr)
	}
	if readOperand.ID <= prevEv.ID || readOperand.ID >= insn0.ID {
		return nil, fmt.Errorf("admit slice events: read operand %d not strictly between prev %d and insn 0 %d",
			readOperand.ID, prevEv.ID, insn0.ID)
	}
	if readOperand.Space != ldaExpectedSpace || readOperand.Addr != ldaExpectedPhysAddr {
		return nil, fmt.Errorf("admit slice events: read operand %d space/addr mismatch: got %s:$%04X, want %s:$%04X",
			readOperand.ID, readOperand.Space, readOperand.Addr, ldaExpectedSpace, ldaExpectedPhysAddr)
	}

	// 4. Physical operand derivation and ownership for Step 3 (STA $0CC46E)
	insn2 := sliceInsns[2]
	insn3 := sliceInsns[3]
	if len(insn3.Insn.Fetches) < 3 || insn3.Insn.Fetches[0].Value != 0x8D {
		return nil, fmt.Errorf("admit slice events: insn 3 opcode mismatch (expected 0x8D with 3 fetches)")
	}
	isM8 = insn3.Insn.Entry.E || (insn3.Insn.Entry.P&0x20 != 0)
	if !isM8 {
		return nil, fmt.Errorf("admit slice events: insn 3 expected 8-bit accumulator mode")
	}
	staAbsAddr := uint32(insn3.Insn.Fetches[1].Value) | uint32(insn3.Insn.Fetches[2].Value)<<8
	staLogicalAddr := (uint32(insn3.Insn.Entry.DB) << 16) | staAbsAddr
	staExpectedSpace, staExpectedPhysAddr := trace.CPUSpace(staLogicalAddr)
	if staExpectedSpace != "wram" {
		return nil, fmt.Errorf("admit slice events: insn 3 expected wram space for logical addr $%06X, got %s", staLogicalAddr, staExpectedSpace)
	}
	staExpectedCanonical := busCanonicalAddr(staExpectedPhysAddr)
	expectedWriteVal := uint8(insn3.Insn.Entry.A)

	// Reuse findUniqueRetainedOperand from companion.go
	writeOperand, ok := findUniqueRetainedOperand(occIndex, insn3, insn2, "write", staExpectedCanonical, expectedWriteVal)
	if !ok || writeOperand == nil {
		return nil, fmt.Errorf("admit slice events: failed to locate unique retained write operand for insn 3")
	}
	// Add bus CPU effective-address join against derived logical address
	if writeOperand.CPU == nil || writeOperand.CPU.EffectiveAddr == nil || *writeOperand.CPU.EffectiveAddr != staLogicalAddr {
		return nil, fmt.Errorf("admit slice events: write operand %d effective address mismatch: got %v, want $%06X",
			writeOperand.ID, writeOperand.CPU, staLogicalAddr)
	}
	if writeOperand.ID <= insn2.ID || writeOperand.ID >= insn3.ID {
		return nil, fmt.Errorf("admit slice events: write operand %d not strictly between insn 2 %d and insn 3 %d",
			writeOperand.ID, insn2.ID, insn3.ID)
	}
	if writeOperand.Cycle != insn3.Insn.Exit.Cycles {
		return nil, fmt.Errorf("admit slice events: write operand %d cycle %d does not match insn 3 inclusive exit cycle %d",
			writeOperand.ID, writeOperand.Cycle, insn3.Insn.Exit.Cycles)
	}
	if writeOperand.Space != staExpectedSpace || writeOperand.Addr != staExpectedPhysAddr {
		return nil, fmt.Errorf("admit slice events: write operand %d space/addr mismatch: got %s:$%04X, want %s:$%04X",
			writeOperand.ID, writeOperand.Space, writeOperand.Addr, staExpectedSpace, staExpectedPhysAddr)
	}

	// 5. Complete classification and projection of ALL in-span bus events
	fetchCounters := make([]int, len(sliceInsns))
	var classifiedBus []ClassifiedBusEvent

	for _, ev := range events {
		if ev.ID < prevEv.ID || ev.ID > insn3.ID {
			continue
		}

		if ev.Kind == "cpu_insn" {
			// Must be one of the admitted retirements
			isAdmitted := ev.ID == prevEv.ID || ev.ID == insn0.ID || ev.ID == sliceInsns[1].ID ||
				ev.ID == insn2.ID || ev.ID == insn3.ID
			if !isAdmitted {
				return nil, fmt.Errorf("admit slice events: unadmitted cpu_insn event %d in span", ev.ID)
			}
			continue
		}

		if ev.Kind != "bus" {
			return nil, fmt.Errorf("admit slice events: unsupported event kind %q (id %d) in span", ev.Kind, ev.ID)
		}

		// Width must be strictly 1; refuse width 0 or > 1
		if ev.Width != 1 {
			return nil, fmt.Errorf("admit slice events: bus event %d has invalid width %d (require width 1)", ev.ID, ev.Width)
		}
		if ev.Value > 255 {
			return nil, fmt.Errorf("admit slice events: bus event %d has value %d > 255", ev.ID, ev.Value)
		}

		if ev.ID == readOperand.ID {
			classifiedBus = append(classifiedBus, ClassifiedBusEvent{
				Event:         ev,
				Role:          "data_read",
				InstructionID: fmt.Sprintf("$%02X:%04X", insn0.Insn.Entry.PB, insn0.Insn.Entry.PC),
				InstructionPC: (uint32(insn0.Insn.Entry.PB) << 16) | uint32(insn0.Insn.Entry.PC),
				LogicalAddr:   ldaLogicalAddr,
				PhysicalAddr:  ldaExpectedCanonical,
				EffectiveAddr: ldaLogicalAddr,
			})
			continue
		}

		if ev.ID == writeOperand.ID {
			classifiedBus = append(classifiedBus, ClassifiedBusEvent{
				Event:         ev,
				Role:          "data_write",
				InstructionID: fmt.Sprintf("$%02X:%04X", insn3.Insn.Entry.PB, insn3.Insn.Entry.PC),
				InstructionPC: (uint32(insn3.Insn.Entry.PB) << 16) | uint32(insn3.Insn.Entry.PC),
				LogicalAddr:   staLogicalAddr,
				PhysicalAddr:  staExpectedCanonical,
				EffectiveAddr: staLogicalAddr,
			})
			continue
		}

		// Otherwise, it must be an instruction fetch for the active instruction
		var owningIdx int
		switch {
		case ev.ID > prevEv.ID && ev.ID < insn0.ID:
			owningIdx = 0
		case ev.ID > insn0.ID && ev.ID < sliceInsns[1].ID:
			owningIdx = 1
		case ev.ID > sliceInsns[1].ID && ev.ID < insn2.ID:
			owningIdx = 2
		case ev.ID > insn2.ID && ev.ID < insn3.ID:
			owningIdx = 3
		default:
			return nil, fmt.Errorf("admit slice events: bus event %d out of instruction intervals", ev.ID)
		}

		owningInsn := sliceInsns[owningIdx]
		fIdx := fetchCounters[owningIdx]
		if fIdx >= len(owningInsn.Insn.Fetches) {
			return nil, fmt.Errorf("admit slice events: excess bus event %d for instruction %d (fetches exhausted)",
				ev.ID, owningInsn.ID)
		}

		expectedFetch := owningInsn.Insn.Fetches[fIdx]
		if ev.Space != "cpu" || ev.Op != "read" {
			return nil, fmt.Errorf("admit slice events: fetch bus event %d expected cpu read, got space %s op %s",
				ev.ID, ev.Space, ev.Op)
		}
		if ev.Addr != expectedFetch.Addr || uint8(ev.Value) != expectedFetch.Value {
			return nil, fmt.Errorf("admit slice events: fetch bus event %d addr/val ($%06X=%d) does not match expected fetch $%06X=%d",
				ev.ID, ev.Addr, ev.Value, expectedFetch.Addr, expectedFetch.Value)
		}

		role := "operand_fetch"
		if fIdx == 0 {
			role = "opcode_fetch"
		}

		classifiedBus = append(classifiedBus, ClassifiedBusEvent{
			Event:         ev,
			Role:          role,
			InstructionID: fmt.Sprintf("$%02X:%04X", owningInsn.Insn.Entry.PB, owningInsn.Insn.Entry.PC),
			InstructionPC: (uint32(owningInsn.Insn.Entry.PB) << 16) | uint32(owningInsn.Insn.Entry.PC),
			FetchIndex:    fIdx,
			LogicalAddr:   expectedFetch.Addr,
			PhysicalAddr:  expectedFetch.Addr,
		})
		fetchCounters[owningIdx]++
	}

	// Verify all fetches were observed and matched
	totalFetches := 0
	for i, c := range fetchCounters {
		if c != len(sliceInsns[i].Insn.Fetches) {
			return nil, fmt.Errorf("admit slice events: instruction %d has %d observed fetches, want %d",
				sliceInsns[i].ID, c, len(sliceInsns[i].Insn.Fetches))
		}
		totalFetches += c
	}

	res := &SliceAdmissionResult{
		PrecedingRetirement: prevEv,
		SliceInstructions:   sliceInsns,
		ClassifiedBusEvents: classifiedBus,
		InputRead:           *readOperand,
		OutputWrite:         *writeOperand,
		InputLogicalAddr:    ldaLogicalAddr,
		InputPhysicalAddr:   ldaExpectedCanonical,
		OutputLogicalAddr:   staLogicalAddr,
		OutputPhysicalAddr:  staExpectedCanonical,
		TotalFetches:        totalFetches,
		TotalBusEvents:      len(classifiedBus),
	}

	return res, nil
}
