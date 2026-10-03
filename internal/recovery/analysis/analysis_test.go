package analysis

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func createSyntheticROM(resetBytes []byte) []byte {
	rom := make([]byte, 32*1024)
	// LoROM header is at $7FC0
	// Emulation reset vector is at $7FC0 + $3C = $7FFC
	resetAddr := uint16(0x8000)
	binary.LittleEndian.PutUint16(rom[0x7FC0+0x3C:], resetAddr)

	// Copy reset routine at offset 0 ($8000 in bank 0)
	copy(rom[0:], resetBytes)
	return rom
}

func TestAnalyzeLoROM_ResetRoutine(t *testing.T) {
	// Simple reset routine:
	// SEI (78)
	// CLC (18)
	// XCE (FB)
	// REP #$30 (C2 30) -> M=0, X=0
	// LDA #$1234 (A9 34 12) -> 16-bit immediate
	// SEP #$30 (E2 30) -> M=1, X=1
	// LDA #$56 (A9 56) -> 8-bit immediate
	// STP (DB)
	code := []byte{
		0x78,       // SEI
		0x18,       // CLC
		0xFB,       // XCE
		0xC2, 0x30, // REP #$30
		0xA9, 0x34, 0x12, // LDA #$1234 (16-bit)
		0xE2, 0x30, // SEP #$30
		0xA9, 0x56, // LDA #$56 (8-bit)
		0xDB, // STP
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{
		ROM: recovery.ROMIdentity{
			NormalizedSHA256: "dummy-hash",
		},
	}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	if len(res.Instructions) != 8 {
		t.Fatalf("expected 8 instructions, got %d", len(res.Instructions))
	}

	// Verify M/X context propagation
	// Inst 4 is LDA #$1234, should have M=clear, X=clear, size 3
	lda16 := res.Instructions[4]
	if lda16.Context.M != "clear" || lda16.Context.X != "clear" {
		t.Errorf("expected M=clear, X=clear for 16-bit LDA, got %+v", lda16.Context)
	}
	if lda16.Bytes != "a93412" {
		t.Errorf("expected bytes a93412, got %s", lda16.Bytes)
	}

	// Inst 6 is LDA #$56, should have M=set, X=set, size 2
	lda8 := res.Instructions[6]
	if lda8.Context.M != "set" || lda8.Context.X != "set" {
		t.Errorf("expected M=set, X=set for 8-bit LDA, got %+v", lda8.Context)
	}
	if lda8.Bytes != "a956" {
		t.Errorf("expected bytes a956, got %s", lda8.Bytes)
	}
}

func TestAnalyzeLoROM_CallStopsLinearTrace(t *testing.T) {
	// Routine with JSR:
	// JSR $8005 (20 05 80)
	// NOP (EA) -> should NOT be analyzed because return context is unknown
	// Target at $8005:
	// RTS (60)
	code := []byte{
		0x20, 0x05, 0x80, // JSR $8005
		0xEA, // NOP at $8003
		0xEA, // NOP at $8004
		0x60, // RTS at $8005
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	// Should analyze JSR ($8000) and RTS ($8005). Should NOT analyze $8003.
	if len(res.Instructions) != 2 {
		t.Fatalf("expected 2 instructions (JSR and RTS), got %d", len(res.Instructions))
	}
	if res.Instructions[0].Address != 0x8000 || res.Instructions[1].Address != 0x8005 {
		t.Errorf("unexpected instruction addresses: %v", res.Instructions)
	}

	// Should have recorded issue about call fallthrough return context not assumed
	foundIssue := false
	for _, iss := range res.Issues {
		if iss.Reason == "call fallthrough return context not assumed" {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Errorf("expected call fallthrough issue to be recorded")
	}
}
