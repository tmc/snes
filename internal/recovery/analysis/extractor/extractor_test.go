package extractor

import (
	"context"
	"os"
	"testing"
)

func TestExtractor(t *testing.T) {
	cfg := DefaultConfig()
	if _, err := os.Stat(cfg.TracePath); os.IsNotExist(err) {
		t.Skipf("authentic trace not found at %s", cfg.TracePath)
	}
	if _, err := os.Stat(cfg.ROMPath); os.IsNotExist(err) {
		t.Skipf("authentic ROM not found at %s", cfg.ROMPath)
	}

	cfg.CandidateBudget = 150
	cfg.MaxSpans = 20

	ctx := context.Background()
	acct, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	t.Logf("Total events scanned: %d", acct.TotalEventsScanned)
	t.Logf("Total retirements: %d", acct.TotalRetirements)
	t.Logf("Candidate spans found: %d", acct.CandidateSpansFound)
	t.Logf("Selected spans: %d", acct.SelectedSpans)
	t.Logf("Qualified spans: %d", len(acct.QualifiedSpans))
	t.Logf("Refused spans: %d", len(acct.RefusedSpans))
	t.Logf("Mismatched spans: %d", len(acct.MismatchedSpans))
	t.Logf("Unique physical starts: %d", len(acct.UniquePhysicalStarts))
	t.Logf("Unique ROM banks: %v", acct.UniqueROMBanks)

	if len(acct.QualifiedSpans) == 0 {
		t.Fatalf("expected at least 1 qualified span, got 0")
	}

	for i, q := range acct.QualifiedSpans {
		t.Logf("Span %d: BlockID=%s, Start=$%06X, End=$%06X, Bank=%02X, Insns=%d, PhysicalStarts=%d, StartSeq=%d, EndSeq=%d",
			i, q.BlockID, q.StartAddress, q.EndAddress, q.ROMBank, q.InstructionCount, len(q.PhysicalStarts), q.StartSeq, q.EndSeq)
	}

	for i, r := range acct.RefusedSpans {
		if i < 5 {
			t.Logf("Refused %d: Start=$%06X, Len=%d, Seq=%d, Reason=%s", i, r.StartAddress, r.Length, r.StartSeq, r.Reason)
		}
	}

	for i, m := range acct.MismatchedSpans {
		t.Logf("Mismatched %d: Start=$%06X, Len=%d, Seq=%d, Reason=%s", i, m.StartAddress, m.Length, m.StartSeq, m.Reason)
	}

	// Verify Phase 1 Prerequisite: Machine-select and qualify 1 straight-line span of at least 2 distinct physical starts.
	if len(acct.QualifiedSpans) < 1 {
		t.Fatalf("Phase 1 failed: want >= 1 qualified span, got %d", len(acct.QualifiedSpans))
	}
	if len(acct.QualifiedSpans[0].PhysicalStarts) < 2 {
		t.Fatalf("Phase 1 failed: first span has %d physical starts, want >= 2", len(acct.QualifiedSpans[0].PhysicalStarts))
	}

	// Verify Phase 2 Target: 3 supported spans across 2 physical ROM banks and >= 20 newly qualified physical starts.
	if len(acct.QualifiedSpans) < 3 {
		t.Fatalf("Phase 2 failed: want >= 3 qualified spans, got %d", len(acct.QualifiedSpans))
	}
	if len(acct.UniqueROMBanks) < 2 {
		t.Fatalf("Phase 2 failed: want >= 2 unique ROM banks, got %d (%v)", len(acct.UniqueROMBanks), acct.UniqueROMBanks)
	}
	if len(acct.UniquePhysicalStarts) < 20 {
		t.Fatalf("Phase 2 failed: want >= 20 physical starts, got %d", len(acct.UniquePhysicalStarts))
	}
}

func TestAddressExclusions(t *testing.T) {
	tests := []struct {
		addr uint32
		want bool
	}{
		{0x09F882, true},
		{0x0CC124, true},
		{0x0CC404, true},
		{0x0CC120, true},
		{0x0CC128, true},
		{0x0CC134, true},
		{0x0CC468, true},
		{0x0CC46E, true},
		{0x09F8B5, true},
		{0x008420, false},
		{0x008000, false},
		{0x0CA000, false},
	}
	for _, tc := range tests {
		got := IsExcludedAddress(tc.addr)
		if got != tc.want {
			t.Errorf("IsExcludedAddress($%06X) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestStopInstructions(t *testing.T) {
	tests := []struct {
		op   uint8
		name string
		want bool
	}{
		{0x20, "JSR abs", true},
		{0xFC, "JSR (abs,X)", true},
		{0x22, "JSL long", true},
		{0x6C, "JMP (abs)", true},
		{0x7C, "JMP (abs,X)", true},
		{0xDC, "JML [long]", true},
		{0x60, "RTS", true},
		{0x6B, "RTL", true},
		{0x40, "RTI", true},
		{0xF0, "BEQ", true},
		{0xD0, "BNE", true},
		{0xA9, "LDA #imm", false},
		{0xAD, "LDA abs", false},
		{0x8D, "STA abs", false},
		{0xEA, "NOP", false},
	}
	for _, tc := range tests {
		got := IsStopInstruction(tc.op)
		if got != tc.want {
			t.Errorf("IsStopInstruction(0x%02X %s) = %v, want %v", tc.op, tc.name, got, tc.want)
		}
	}
}

func TestLoROMOffset(t *testing.T) {
	tests := []struct {
		addr    uint32
		romSize int
		wantOff int
		wantOk  bool
	}{
		{0x008000, 0x100000, 0x0000, true},
		{0x00FFFF, 0x100000, 0x7FFF, true},
		{0x018000, 0x100000, 0x8000, true},
		{0x0C8000, 0x100000, 0x60000, true},
		{0x7E0000, 0x100000, 0, false},   // WRAM
		{0x001000, 0x100000, 0, false},   // Lower RAM/MMIO
		{0x008000, 0x4000, 0x0000, true}, // within 0x4000
		{0x018000, 0x4000, 32768, false}, // exceeds romSize
	}
	for _, tc := range tests {
		gotOff, gotOk := SnesLoROMOffset(tc.addr, tc.romSize)
		if gotOk != tc.wantOk || (tc.wantOk && gotOff != tc.wantOff) {
			t.Errorf("SnesLoROMOffset($%06X) = (%d, %v), want (%d, %v)", tc.addr, gotOff, gotOk, tc.wantOff, tc.wantOk)
		}
	}
}
