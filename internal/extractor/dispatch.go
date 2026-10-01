package extractor

import (
	"fmt"
)

// validateDispatchContract verifies that a Candidate's DispatchContract is internally
// consistent and matches the actual Jump Table and instruction entries in the loaded ROM.
func validateDispatchContract(d *DispatchContract, cand *Candidate, romBytes []byte) error {
	if d == nil {
		return fmt.Errorf("nil dispatch contract")
	}
	if d.DispatcherPC == 0 || d.JumpTablePC == 0 || d.ContinuationPC == 0 {
		return fmt.Errorf("dispatch contract has unmapped or zero addresses")
	}
	if d.ExpectedEntryS == 0 || d.ExpectedReturnS == 0 {
		return fmt.Errorf("dispatch contract requires explicit stack expectations")
	}
	if d.ExpectedReturnS != d.ExpectedEntryS+2 {
		return fmt.Errorf("dispatch_invalid_stack_delta: return S (0x%04X) != entry S (0x%04X) + 2", d.ExpectedReturnS, d.ExpectedEntryS)
	}
	if len(d.StackReturnBytes) != 2 {
		return fmt.Errorf("dispatch contract requires 2 stack return bytes")
	}

	// Verify stack return bytes match the continuation address minus 1 (for RTS semantics).
	wantLow := uint8((d.ContinuationPC - 1) & 0xFF)
	wantHigh := uint8(((d.ContinuationPC - 1) >> 8) & 0xFF)
	if d.StackReturnBytes[0] != wantLow || d.StackReturnBytes[1] != wantHigh {
		return fmt.Errorf("dispatch_stack_return_bytes_mismatch: got [0x%02X, 0x%02X], want [0x%02X, 0x%02X]",
			d.StackReturnBytes[0], d.StackReturnBytes[1], wantLow, wantHigh)
	}

	// Verify Jump Table in ROM at d.JumpTablePC + SelectorIndex*2.
	tableEntryAddr := d.JumpTablePC + uint32(d.SelectorIndex)*2
	off, err := deriveLoROMOffset(tableEntryAddr)
	if err != nil {
		return fmt.Errorf("dispatch jump table address not mapped in LoROM: %w", err)
	}
	if int(off+1) >= len(romBytes) {
		return fmt.Errorf("dispatch jump table offset out of ROM bounds: 0x%X", off)
	}
	targetAddr := uint16(romBytes[off]) | (uint16(romBytes[off+1]) << 8)
	expectedEntry := (d.JumpTablePC & 0xFF0000) | uint32(targetAddr)
	if expectedEntry != cand.Entry {
		return fmt.Errorf("dispatch_target_mismatch: table points to 0x%06X, but candidate entry is 0x%06X (selector %d)",
			expectedEntry, cand.Entry, d.SelectorIndex)
	}

	// Verify caller instruction in ROM if specified.
	if d.CallerPC != 0 {
		cOff, err := deriveLoROMOffset(d.CallerPC)
		if err != nil {
			return fmt.Errorf("dispatch caller address not mapped in LoROM: %w", err)
		}
		if int(cOff+2) >= len(romBytes) {
			return fmt.Errorf("dispatch caller offset out of ROM bounds: 0x%X", cOff)
		}
		if d.CallerOpcode != 0 && romBytes[cOff] != d.CallerOpcode {
			return fmt.Errorf("dispatch caller opcode mismatch: got 0x%02X, want 0x%02X", romBytes[cOff], d.CallerOpcode)
		}
		if romBytes[cOff] == 0x20 { // JSR abs
			t := uint16(romBytes[cOff+1]) | (uint16(romBytes[cOff+2]) << 8)
			if uint32(t) != (d.DispatcherPC & 0xFFFF) {
				return fmt.Errorf("dispatch caller JSR target 0x%04X != dispatcher 0x%04X", t, d.DispatcherPC&0xFFFF)
			}
		}
	}

	// Verify dispatcher call in ROM if specified.
	if d.DispatcherCallPC != 0 {
		dcOff, err := deriveLoROMOffset(d.DispatcherCallPC)
		if err != nil {
			return fmt.Errorf("dispatch dispatcher_call address not mapped in LoROM: %w", err)
		}
		if int(dcOff+3) >= len(romBytes) {
			return fmt.Errorf("dispatch dispatcher_call offset out of ROM bounds: 0x%X", dcOff)
		}
		if romBytes[dcOff] != 0x22 {
			return fmt.Errorf("dispatch dispatcher_call opcode 0x%02X != 0x22 (JSL)", romBytes[dcOff])
		}
		if d.HelperEntryPC != 0 {
			t := uint32(romBytes[dcOff+1]) | (uint32(romBytes[dcOff+2]) << 8) | (uint32(romBytes[dcOff+3]) << 16)
			if t != d.HelperEntryPC {
				return fmt.Errorf("dispatch dispatcher_call JSL target 0x%06X != helper 0x%06X", t, d.HelperEntryPC)
			}
		}
	}

	// Verify helper exit in ROM if specified.
	if d.HelperExitPC != 0 {
		heOff, err := deriveLoROMOffset(d.HelperExitPC)
		if err != nil {
			return fmt.Errorf("dispatch helper_exit address not mapped in LoROM: %w", err)
		}
		if int(heOff) >= len(romBytes) {
			return fmt.Errorf("dispatch helper_exit offset out of ROM bounds: 0x%X", heOff)
		}
		if romBytes[heOff] != 0xDC {
			return fmt.Errorf("dispatch helper_exit opcode 0x%02X != 0xDC (JML [abs])", romBytes[heOff])
		}
	}

	return nil
}

// verifyDispatchOccurrence checks that a specific candidate execution interval
// satisfies the dispatch contract at runtime entry, body, and terminal RTS exit,
// verifying the complete observed dynamic predecessor chain.
func verifyDispatchOccurrence(d *DispatchContract, predecessors []*RawInsn, entryEv *RawEvent, body []*RawInsn, history *WriteHistory, romBytes []byte, events BusEventList) error {
	if d == nil || entryEv == nil || len(body) == 0 {
		return fmt.Errorf("invalid dispatch occurrence parameters")
	}

	// Causal history is strictly required; nil history is a refusal.
	if history == nil {
		return fmt.Errorf("dispatch_missing_history: causal write history is strictly required")
	}

	// Dynamic predecessor ancestry verification:
	if d.CallerPC != 0 {
		predCount := 1 + 2 + d.HelperCount
		if len(predecessors) < predCount {
			return fmt.Errorf("dispatch_missing_dynamic_ancestry: expected at least %d predecessor instructions, got %d", predCount, len(predecessors))
		}
		for i, p := range predecessors {
			if err := validateInstructionFetches(p, romBytes); err != nil {
				return fmt.Errorf("dispatch_predecessor_fetch_mismatch at %d: %w", i, err)
			}
		}

		// 1. Caller JSR instruction:
		caller := predecessors[0]
		callerPC := (uint32(caller.Entry.PB) << 16) | uint32(caller.Entry.PC)
		if callerPC != d.CallerPC {
			return fmt.Errorf("dispatch_caller_pc_mismatch: got 0x%06X, want 0x%06X", callerPC, d.CallerPC)
		}
		if len(caller.Fetches) > 0 && d.CallerOpcode != 0 && caller.Fetches[0].Value != d.CallerOpcode {
			return fmt.Errorf("dispatch_caller_opcode_mismatch: got 0x%02X, want 0x%02X", caller.Fetches[0].Value, d.CallerOpcode)
		}
		callerSucc := (uint32(caller.Exit.PB) << 16) | uint32(caller.Exit.PC)
		if callerSucc != d.DispatcherPC {
			return fmt.Errorf("dispatch_caller_successor_mismatch: got 0x%06X, want 0x%06X", callerSucc, d.DispatcherPC)
		}
		if caller.Exit.S != d.ExpectedEntryS {
			return fmt.Errorf("dispatch_caller_exit_s_mismatch: got 0x%04X, want 0x%04X", caller.Exit.S, d.ExpectedEntryS)
		}

		// 2. Dispatcher LDA instruction:
		dispInsn := predecessors[1]
		dispPC := (uint32(dispInsn.Entry.PB) << 16) | uint32(dispInsn.Entry.PC)
		if dispPC != d.DispatcherPC {
			return fmt.Errorf("dispatch_dispatcher_pc_mismatch: got 0x%06X, want 0x%06X", dispPC, d.DispatcherPC)
		}
		dispSucc := (uint32(dispInsn.Exit.PB) << 16) | uint32(dispInsn.Exit.PC)
		if d.DispatcherCallPC != 0 && dispSucc != d.DispatcherCallPC {
			return fmt.Errorf("dispatch_dispatcher_successor_mismatch: got 0x%06X, want 0x%06X", dispSucc, d.DispatcherCallPC)
		}

		// 3. Dispatcher Call JSL instruction:
		if d.DispatcherCallPC != 0 {
			callInsn := predecessors[2]
			callPC := (uint32(callInsn.Entry.PB) << 16) | uint32(callInsn.Entry.PC)
			if callPC != d.DispatcherCallPC {
				return fmt.Errorf("dispatch_dispatcher_call_pc_mismatch: got 0x%06X, want 0x%06X", callPC, d.DispatcherCallPC)
			}
			if len(callInsn.Fetches) > 0 && callInsn.Fetches[0].Value != 0x22 {
				return fmt.Errorf("dispatch_dispatcher_call_opcode_mismatch: got 0x%02X, want 0x22 (JSL)", callInsn.Fetches[0].Value)
			}
			callSucc := (uint32(callInsn.Exit.PB) << 16) | uint32(callInsn.Exit.PC)
			if d.HelperEntryPC != 0 && callSucc != d.HelperEntryPC {
				return fmt.Errorf("dispatch_helper_entry_mismatch: got 0x%06X, want 0x%06X", callSucc, d.HelperEntryPC)
			}
		}

		// 4. Helper execution and terminal JML:
		if d.HelperCount > 0 {
			if err := verifyHelperBus(predecessors[2:3+d.HelperCount], events, romBytes); err != nil {
				return err
			}
			helperInsns := predecessors[3 : 3+d.HelperCount]
			firstHelperPC := (uint32(helperInsns[0].Entry.PB) << 16) | uint32(helperInsns[0].Entry.PC)
			if d.HelperEntryPC != 0 && firstHelperPC != d.HelperEntryPC {
				return fmt.Errorf("dispatch_helper_entry_pc_mismatch: got 0x%06X, want 0x%06X", firstHelperPC, d.HelperEntryPC)
			}
			termHelper := helperInsns[len(helperInsns)-1]
			termHelperPC := (uint32(termHelper.Entry.PB) << 16) | uint32(termHelper.Entry.PC)
			if d.HelperExitPC != 0 && termHelperPC != d.HelperExitPC {
				return fmt.Errorf("dispatch_helper_exit_pc_mismatch: got 0x%06X, want 0x%06X", termHelperPC, d.HelperExitPC)
			}
			if len(termHelper.Fetches) > 0 && termHelper.Fetches[0].Value != 0xDC {
				return fmt.Errorf("dispatch_helper_terminal_opcode_mismatch: got 0x%02X, want 0xDC (JML [abs])", termHelper.Fetches[0].Value)
			}
			entryPC := (uint32(entryEv.Insn.Entry.PB) << 16) | uint32(entryEv.Insn.Entry.PC)
			termSucc := (uint32(termHelper.Exit.PB) << 16) | uint32(termHelper.Exit.PC)
			if termSucc != entryPC {
				return fmt.Errorf("dispatch_helper_jump_target_mismatch: got 0x%06X, want entry 0x%06X", termSucc, entryPC)
			}
		}

		// Verify caller stack write event:
		addrLow := uint32(0x7E0000) | uint32(d.ExpectedEntryS+1)
		addrHigh := uint32(0x7E0000) | uint32(d.ExpectedEntryS+2)
		lowVal, errLow := history.lookupWriteBefore(addrLow, entryEv.Cycle)
		highVal, errHigh := history.lookupWriteBefore(addrHigh, entryEv.Cycle)
		if errLow != nil || errHigh != nil {
			return fmt.Errorf("dispatch_missing_stack_history: low_err=%v, high_err=%v", errLow, errHigh)
		}
		if lowVal != d.StackReturnBytes[0] || highVal != d.StackReturnBytes[1] {
			return fmt.Errorf("dispatch_stack_history_value_mismatch: got [0x%02X, 0x%02X], want [0x%02X, 0x%02X]",
				lowVal, highVal, d.StackReturnBytes[0], d.StackReturnBytes[1])
		}

		// Verify caller wrote this stack return address in its cycle window:
		lastWriteLow, hasLow := history.latestWriteBefore(addrLow, entryEv.Cycle)
		if !hasLow || lastWriteLow.Cycle < caller.Entry.Cycles || lastWriteLow.Cycle > caller.Exit.Cycles {
			return fmt.Errorf("dispatch_stack_caller_provenance_mismatch: low byte write not produced by caller JSR")
		}
	}

	// 5. Entry stack pointer must match expected entry S.
	if entryEv.Insn.Entry.S != d.ExpectedEntryS {
		return fmt.Errorf("dispatch_entry_s_mismatch: got=0x%04X, want=0x%04X", entryEv.Insn.Entry.S, d.ExpectedEntryS)
	}

	// 6. Terminal instruction must be RTS (opcode $60).
	lastInsn := body[len(body)-1]
	if len(lastInsn.Fetches) == 0 {
		return fmt.Errorf("missing terminal return fetches")
	}
	if lastInsn.Fetches[0].Value != 0x60 {
		return fmt.Errorf("dispatch_illegal_terminal_opcode: got=0x%02X, want=0x60 (RTS)", lastInsn.Fetches[0].Value)
	}

	// 7. Terminal RTS exit stack pointer must match expected return S.
	if lastInsn.Exit.S != d.ExpectedReturnS {
		return fmt.Errorf("dispatch_return_s_mismatch: got=0x%04X, want=0x%04X", lastInsn.Exit.S, d.ExpectedReturnS)
	}

	// 8. Terminal RTS exit PC must match continuation PC.
	exitPB := lastInsn.Exit.PB
	exitPC := lastInsn.Exit.PC
	exitAddr := (uint32(exitPB) << 16) | uint32(exitPC)
	if exitAddr != d.ContinuationPC {
		return fmt.Errorf("dispatch_continuation_mismatch: got=0x%06X, want=0x%06X", exitAddr, d.ContinuationPC)
	}

	// 9. Verify selector in write history if selector address is specified.
	if d.SelectorAddress != 0 {
		selVal, err := history.lookupWriteBefore(d.SelectorAddress, entryEv.Cycle)
		if err != nil {
			return fmt.Errorf("dispatch_selector_missing_history: %w", err)
		}
		if selVal != d.SelectorIndex {
			return fmt.Errorf("dispatch_selector_mismatch: history had %d, want %d", selVal, d.SelectorIndex)
		}
	}

	return nil
}
