package extractor

import (
	"fmt"
	"sort"
)

// validateConnectedContract verifies that a Candidate's ConnectedContract is internally
// consistent and matches the actual code structure, call sites, and indirect targets in the loaded ROM.
func validateConnectedContract(conn *ConnectedContract, cand *Candidate, romBytes []byte) error {
	if conn == nil {
		return fmt.Errorf("nil connected contract")
	}
	if cand.Entry == 0 || conn.CallerPC == 0 || conn.ContinuationPC == 0 {
		return fmt.Errorf("connected contract has zero or unmapped entry/caller/continuation addresses")
	}
	if conn.ExpectedEntryS == 0 || conn.ExpectedReturnS == 0 {
		return fmt.Errorf("connected contract requires explicit stack expectations")
	}
	if conn.ExpectedReturnS != conn.ExpectedEntryS+2 {
		return fmt.Errorf("connected_invalid_stack_delta: return S (0x%04X) != entry S (0x%04X) + 2", conn.ExpectedReturnS, conn.ExpectedEntryS)
	}
	if len(conn.StackReturnBytes) != 2 {
		return fmt.Errorf("connected contract requires 2 stack return bytes")
	}

	// Verify stack return bytes match the continuation address minus 1 (for RTS semantics).
	wantLow := uint8((conn.ContinuationPC - 1) & 0xFF)
	wantHigh := uint8(((conn.ContinuationPC - 1) >> 8) & 0xFF)
	if conn.StackReturnBytes[0] != wantLow || conn.StackReturnBytes[1] != wantHigh {
		return fmt.Errorf("connected_stack_return_bytes_mismatch: got [0x%02X, 0x%02X], want [0x%02X, 0x%02X]",
			conn.StackReturnBytes[0], conn.StackReturnBytes[1], wantLow, wantHigh)
	}

	if len(conn.Spans) == 0 {
		return fmt.Errorf("connected contract requires at least one span")
	}
	spansCopy := append([]AddressRange(nil), conn.Spans...)
	sort.Slice(spansCopy, func(i, j int) bool { return spansCopy[i].Start < spansCopy[j].Start })
	for i, s := range spansCopy {
		if s.Start >= s.End || s.Start>>16 != (s.End-1)>>16 {
			return fmt.Errorf("connected span %d [0x%06X, 0x%06X) crosses bank or is inverted", i, s.Start, s.End)
		}
		if i > 0 && spansCopy[i-1].End > s.Start {
			return fmt.Errorf("connected overlapping spans at index %d", i)
		}
	}

	// Verify caller instruction in ROM.
	if conn.CallerPC != 0 {
		cOff, err := deriveLoROMOffset(conn.CallerPC)
		if err != nil {
			return fmt.Errorf("connected caller address not mapped in LoROM: %w", err)
		}
		if int(cOff+2) >= len(romBytes) {
			return fmt.Errorf("connected caller offset out of ROM bounds: 0x%X", cOff)
		}
		if conn.CallerOpcode != 0x20 {
			return fmt.Errorf("connected caller opcode mismatch: got 0x%02X, want 0x20 (JSR)", conn.CallerOpcode)
		}
		if romBytes[cOff] != 0x20 {
			return fmt.Errorf("connected caller ROM opcode mismatch: got 0x%02X, want 0x20", romBytes[cOff])
		}
		if conn.CallerPC>>16 != cand.Entry>>16 {
			return fmt.Errorf("connected caller bank 0x%02X != entry bank 0x%02X", conn.CallerPC>>16, cand.Entry>>16)
		}
		t := uint32(conn.CallerPC&0xff0000) | uint32(romBytes[cOff+1]) | (uint32(romBytes[cOff+2]) << 8)
		if t != cand.Entry {
			return fmt.Errorf("connected caller JSR target 0x%06X != entry 0x%06X", t, cand.Entry)
		}
		if conn.ContinuationPC != conn.CallerPC+3 {
			return fmt.Errorf("connected continuation PC 0x%06X != caller PC 0x%06X + 3", conn.ContinuationPC, conn.CallerPC)
		}
	}

	// Verify outer JSR in ROM.
	if conn.OuterJSRPC != 0 {
		ojOff, err := deriveLoROMOffset(conn.OuterJSRPC)
		if err != nil {
			return fmt.Errorf("connected outer JSR address not mapped in LoROM: %w", err)
		}
		if int(ojOff+2) >= len(romBytes) {
			return fmt.Errorf("connected outer JSR offset out of ROM bounds: 0x%X", ojOff)
		}
		if romBytes[ojOff] != 0x20 {
			return fmt.Errorf("connected outer JSR opcode 0x%02X != 0x20", romBytes[ojOff])
		}
	}

	// Verify dispatcher call in ROM.
	if conn.DispatcherCallPC != 0 {
		dcOff, err := deriveLoROMOffset(conn.DispatcherCallPC)
		if err != nil {
			return fmt.Errorf("connected dispatcher call address not mapped in LoROM: %w", err)
		}
		if int(dcOff+3) >= len(romBytes) {
			return fmt.Errorf("connected dispatcher call offset out of ROM bounds: 0x%X", dcOff)
		}
		if romBytes[dcOff] != 0x22 {
			return fmt.Errorf("connected dispatcher call opcode 0x%02X != 0x22 (JSL)", romBytes[dcOff])
		}
		if conn.HelperEntryPC != 0 {
			t := uint32(romBytes[dcOff+1]) | (uint32(romBytes[dcOff+2]) << 8) | (uint32(romBytes[dcOff+3]) << 16)
			if t != conn.HelperEntryPC {
				return fmt.Errorf("connected dispatcher call target 0x%06X != helper 0x%06X", t, conn.HelperEntryPC)
			}
		}
	}

	// Verify helper exit in ROM.
	if conn.HelperExitPC != 0 {
		heOff, err := deriveLoROMOffset(conn.HelperExitPC)
		if err != nil {
			return fmt.Errorf("connected helper exit address not mapped in LoROM: %w", err)
		}
		if int(heOff) >= len(romBytes) {
			return fmt.Errorf("connected helper exit offset out of ROM bounds: 0x%X", heOff)
		}
		if romBytes[heOff] != 0xDC {
			return fmt.Errorf("connected helper exit opcode 0x%02X != 0xDC (JML [abs])", romBytes[heOff])
		}
	}

	// Verify indirect targets allowlist.
	if conn.HelperExitPC != 0 {
		targets, ok := conn.AllowedIndirectTargets[conn.HelperExitPC]
		if !ok || len(targets) == 0 {
			return fmt.Errorf("connected contract missing allowed indirect targets for 0x%06X", conn.HelperExitPC)
		}
		hasHandler := false
		for _, t := range targets {
			if t == conn.HandlerEntryPC {
				hasHandler = true
				break
			}
		}
		if !hasHandler {
			return fmt.Errorf("connected contract allowed indirect targets do not include handler entry 0x%06X", conn.HandlerEntryPC)
		}
	}

	// Verify terminal return in ROM.
	if conn.TerminalReturnPC != 0 {
		trOff, err := deriveLoROMOffset(conn.TerminalReturnPC)
		if err != nil {
			return fmt.Errorf("connected terminal return address not mapped in LoROM: %w", err)
		}
		if int(trOff) >= len(romBytes) {
			return fmt.Errorf("connected terminal return offset out of ROM bounds: 0x%X", trOff)
		}
		if romBytes[trOff] != 0x60 {
			return fmt.Errorf("connected terminal return opcode 0x%02X != 0x60 (RTS)", romBytes[trOff])
		}
	}

	return nil
}
