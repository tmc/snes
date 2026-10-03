package gsu

import "testing"

// TestROMAtBankingWindows pins the GSU-internal opcode/data ROM-bus
// dispatcher (romAt, alu.go) across all three address windows defined by
// bsnes/sfc/coprocessor/superfx/memory.cpp:1-30 SuperFX::read:
//
//	$00-3F:0000-FFFF  ((bank<<15) | (off & $7FFF)) & romMask    ROM (LoROM fold)
//	$40-5F:0000-FFFF  addr & romMask                            ROM (linear)
//	$60-7F:0000-FFFF  addr & ramMask                            shared RAM
//
// Both opcode fetch (PBR:R15 via readOpcode in memory.cpp:43-71) and
// data fetch (ROMBR:R14 via GETB-family) traverse this same dispatcher.
//
// This is the "GSU-internal opcode/data ROM-bus banking" surface; it is
// distinct from the CPU-visible ROM-window arbitration (cartridge.go
// arbitrateGSUROM / gsuCPUROMVector) covered by cartridge_test.go.
func TestROMAtBankingWindows(t *testing.T) {
	// 4 MiB ROM lets us exercise both the LoROM-fold low half and the
	// $40-5F linear half. Use distinct sentinel bytes at addresses that
	// each window maps onto.
	rom := make([]byte, 0x400000)
	for i := range rom {
		rom[i] = 0xEE // background pattern, distinct from sentinels
	}
	// $00:8000 -> LoROM fold target = (0<<15)|0x0000 = 0x000000
	rom[0x000000] = 0xA0
	// $00:FFFF -> LoROM fold target = (0<<15)|0x7FFF = 0x007FFF
	rom[0x007FFF] = 0xA1
	// $3F:8000 -> LoROM fold target = (0x3F<<15)|0x0000 = 0x1F8000
	rom[0x1F8000] = 0xA2
	// $40:0000 -> linear target = 0x400000 & 0x3FFFFF = 0x000000.
	// Already 0xA0 from LoROM aliasing, which is part of the spec
	// (bsnes mask folds them together when ROM is small). Use a
	// different cell that is unique to the linear path:
	// $40:1234 -> linear target = 0x001234.
	rom[0x001234] = 0xB0
	// $5F:FFFF -> linear target = 0x1FFFFF.
	rom[0x1FFFFF] = 0xB1

	ram := make([]byte, 0x20000) // 128 KiB shared RAM
	for i := range ram {
		ram[i] = 0xCC
	}
	// $70:0000 -> RAM[0] (per bsnes addr & ramMask). Use offsets that
	// are < len(ram) to dodge the modulo aliasing question.
	ram[0x00000] = 0xD0
	ram[0x00FFF] = 0xD1
	ram[0x10000] = 0xD2

	d := New(rom, ram)

	// --- $00-3F:0000-FFFF: LoROM-fold ROM ---
	if got := d.romAt(0x008000); got != 0xA0 {
		t.Errorf("LoROM-fold $00:8000 = %02X, want A0 (folds to ROM[0x000000])", got)
	}
	if got := d.romAt(0x00FFFF); got != 0xA1 {
		t.Errorf("LoROM-fold $00:FFFF = %02X, want A1 (folds to ROM[0x007FFF])", got)
	}
	if got := d.romAt(0x3F8000); got != 0xA2 {
		t.Errorf("LoROM-fold $3F:8000 = %02X, want A2 (folds to ROM[0x1F8000])", got)
	}
	// Aliasing within the LoROM-fold window: $00:0000 and $00:8000
	// both fold to ROM[0x000000] (offset & 0x7FFF strips bit 15).
	if got := d.romAt(0x000000); got != 0xA0 {
		t.Errorf("LoROM-fold $00:0000 = %02X, want A0 (alias of $00:8000)", got)
	}

	// --- $40-5F:0000-FFFF: linear ROM ---
	if got := d.romAt(0x401234); got != 0xB0 {
		t.Errorf("linear $40:1234 = %02X, want B0", got)
	}
	if got := d.romAt(0x5FFFFF); got != 0xB1 {
		t.Errorf("linear $5F:FFFF = %02X, want B1", got)
	}

	// --- $60-7F:0000-FFFF: shared RAM (the gap before this fix) ---
	if got := d.romAt(0x600000); got != 0xD0 {
		t.Errorf("RAM-window $60:0000 = %02X, want D0 (bsnes memory.cpp:20-26)", got)
	}
	if got := d.romAt(0x600FFF); got != 0xD1 {
		t.Errorf("RAM-window $60:0FFF = %02X, want D1", got)
	}
	if got := d.romAt(0x710000); got != 0xD2 {
		t.Errorf("RAM-window $71:0000 = %02X, want D2", got)
	}
}

// TestROMAtRAMWindowEmptyRAM confirms graceful handling when the GSU
// has no shared RAM allocated (defensive: New always allocates 64 KiB
// when ram=nil, but romAt should not panic if RAM is later cleared).
func TestROMAtRAMWindowEmptyRAM(t *testing.T) {
	rom := make([]byte, 0x10000)
	d := New(rom, nil)
	d.RAM = nil
	if got := d.romAt(0x600000); got != 0 {
		t.Errorf("romAt($60:0000) with empty RAM = %02X, want 00", got)
	}
}

// TestReadOpcodeUsesROMBusForPBRBelow60 pins the readOpcode →
// romAt path for the ROM half (PBR ≤ $5F). The cache miss codepath
// fills 16 bytes from (PBR<<16 | (CBR + dp)&0xFFF0); verifying via
// the cache buffer ensures both LoROM-fold and linear ROM windows
// reach romAt's first two cases.
func TestReadOpcodeUsesROMBusForPBRBelow60(t *testing.T) {
	rom := make([]byte, 0x400000)
	rom[0x000000] = 0x42 // PBR=$00, R15=$8000 → LoROM-fold target 0x000000
	rom[0x001234] = 0x69 // PBR=$40, R15=$1234 → linear target 0x001234
	d := New(rom, nil)

	// PBR=$00, R15=$8000 → opcode fetch through LoROM-fold.
	d.PBR = 0x00
	d.R[15] = 0x8000
	d.PrimePipeline()
	if d.Pipeline != 0x42 {
		t.Errorf("opcode fetch PBR=$00 R15=$8000 pipeline=%02X, want 42 (LoROM-fold)", d.Pipeline)
	}

	// PBR=$40, R15=$1234 → linear ROM.
	d.PBR = 0x40
	d.R[15] = 0x1234
	d.PrimePipeline()
	if d.Pipeline != 0x69 {
		t.Errorf("opcode fetch PBR=$40 R15=$1234 pipeline=%02X, want 69 (linear)", d.Pipeline)
	}
}

// TestReadOpcodeUsesRAMBusForPBR60 pins the bsnes memory.cpp:60-70
// readOpcode dispatch: when PBR ≥ $60, the program byte comes from
// shared RAM (via the same SuperFX::read dispatcher, which routes
// $60-7F to the RAM bus per memory.cpp:20-26).
func TestReadOpcodeUsesRAMBusForPBR60(t *testing.T) {
	rom := make([]byte, 0x10000)
	ram := make([]byte, 0x20000)
	ram[0x05678] = 0x55 // PBR=$60, R15=$5678 → RAM[0x05678]
	d := New(rom, ram)

	d.PBR = 0x60
	d.R[15] = 0x5678
	d.PrimePipeline()
	if d.Pipeline != 0x55 {
		t.Errorf("opcode fetch PBR=$60 R15=$5678 pipeline=%02X, want 55 (RAM bus per bsnes memory.cpp:20-26)", d.Pipeline)
	}
}
