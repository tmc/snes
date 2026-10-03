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

func TestAnalyzeLoROM_CallPreservesFlags(t *testing.T) {
	// Routine with JSR to acyclic callee preserving flags:
	// Reset:
	//   CLC (18)
	//   XCE (FB)
	//   JSR $8009 (20 09 80)
	//   NOP (EA) at $8005 -> Should be analyzed!
	//   NOP (EA) at $8006
	//   STP (DB) at $8007
	// Callee at $8009:
	//   NOP (EA)
	//   RTS (60)
	code := []byte{
		0x18,             // $8000: CLC
		0xFB,             // $8001: XCE
		0x20, 0x09, 0x80, // $8002: JSR $8009
		0xEA,             // $8005: NOP
		0xEA,             // $8006: NOP
		0xDB,             // $8007: STP
		0xEA,             // $8008: padding NOP
		0xEA,             // $8009: NOP (callee)
		0x60,             // $800A: RTS
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	found8005 := false
	found8007 := false
	for _, inst := range res.Instructions {
		if inst.Address == 0x8005 {
			found8005 = true
		}
		if inst.Address == 0x8007 {
			found8007 = true
		}
	}
	if !found8005 {
		t.Errorf("expected decoding to continue past JSR to $8005")
	}
	if !found8007 {
		t.Errorf("expected decoding to reach STP at $8007")
	}

	for _, iss := range res.Issues {
		if iss.Reason == "call fallthrough return context not assumed" {
			t.Errorf("unexpected call fallthrough issue: %+v", iss)
		}
	}
}

func TestAnalyzeLoROM_CallSetsFlags(t *testing.T) {
	// Routine where callee switches M to 16-bit:
	// Reset:
	//   CLC (18)
	//   XCE (FB)
	//   JSR $8009 (20 09 80)
	//   LDA #$1234 (A9 34 12) at $8005 -> 16-bit immediate because callee set M=clear!
	//   STP (DB) at $8008
	// Callee at $8009:
	//   REP #$20 (C2 20) -> M=clear
	//   RTS (60)
	code := []byte{
		0x18,             // $8000: CLC
		0xFB,             // $8001: XCE
		0x20, 0x09, 0x80, // $8002: JSR $8009
		0xA9, 0x34, 0x12, // $8005: LDA #$1234 (16-bit)
		0xDB,             // $8008: STP
		0xC2, 0x20,       // $8009: REP #$20
		0x60,             // $800B: RTS
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	var ldaInst *recovery.Instruction
	for i := range res.Instructions {
		if res.Instructions[i].Address == 0x8005 {
			ldaInst = &res.Instructions[i]
			break
		}
	}
	if ldaInst == nil {
		t.Fatalf("expected instruction at $8005 to be decoded")
	}
	if ldaInst.Context.M != "clear" {
		t.Errorf("expected M=clear at $8005, got %s", ldaInst.Context.M)
	}
	if ldaInst.Bytes != "a93412" {
		t.Errorf("expected 16-bit LDA bytes a93412, got %s", ldaInst.Bytes)
	}
}

func TestAnalyzeLoROM_CyclicOrUnresolvedCallStopsTrace(t *testing.T) {
	// Routine with JSR to cyclically complex callee:
	// Reset:
	//   JSR $8006 (20 06 80)
	//   NOP (EA) at $8003 -> Should NOT be analyzed!
	//   NOP (EA) at $8004
	//   STP (DB) at $8005
	// Callee at $8006:
	//   BRA $8006 (80 FE) -> Infinite loop!
	code := []byte{
		0x20, 0x06, 0x80, // $8000: JSR $8006
		0xEA,             // $8003: NOP
		0xEA,             // $8004: NOP
		0xDB,             // $8005: STP
		0x80, 0xFE,       // $8006: BRA $8006 (-2)
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	for _, inst := range res.Instructions {
		if inst.Address == 0x8003 || inst.Address == 0x8004 || inst.Address == 0x8005 {
			t.Errorf("unexpected instruction decoded at $%06X after unresolved call", inst.Address)
		}
	}

	foundIssue := false
	for _, iss := range res.Issues {
		if iss.Reason == "call fallthrough return context not assumed" {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Errorf("expected call fallthrough issue to be recorded for cyclic callee")
	}
}

func TestAnalyzeLoROM_EmulationModeREP(t *testing.T) {
	// In emulation mode (reset default: E=set, M=set, X=set),
	// REP #$30 cannot clear M/X flags.
	code := []byte{
		0xC2, 0x30, // $8000: REP #$30 (in emulation mode)
		0xA9, 0x56, // $8002: LDA #$56 (still 8-bit!)
		0xDB, // $8004: STP
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	if len(res.Instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(res.Instructions))
	}

	lda := res.Instructions[1]
	if lda.Context.M != "set" || lda.Context.X != "set" {
		t.Errorf("expected M=set, X=set after REP in emulation mode, got %+v", lda.Context)
	}
	if lda.Bytes != "a956" {
		t.Errorf("expected 8-bit LDA bytes a956, got %s", lda.Bytes)
	}
}

func TestAnalyzeLoROM_ArithmeticInvalidatesCarry(t *testing.T) {
	// CLC sets C=clear; subsequent ADC or CMP should invalidate C to unknown.
	code := []byte{
		0x18,       // $8000: CLC -> C=clear
		0x69, 0x05, // $8001: ADC #$05 -> C=unknown
		0xDB, // $8003: STP
	}

	rom := createSyntheticROM(code)
	doc := &recovery.Document{}

	res, err := AnalyzeLoROM(rom, doc, Config{MaxInstructions: 100})
	if err != nil {
		t.Fatalf("AnalyzeLoROM failed: %v", err)
	}

	if len(res.Instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(res.Instructions))
	}

	adc := res.Instructions[1]
	if adc.Context.C != "clear" {
		t.Errorf("entry to ADC should have C=clear, got %s", adc.Context.C)
	}

	stp := res.Instructions[2]
	if stp.Context.C != "unknown" {
		t.Errorf("successor to ADC should have C=unknown, got %s", stp.Context.C)
	}
}

