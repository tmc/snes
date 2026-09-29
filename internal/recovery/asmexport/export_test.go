package asmexport_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/asmexport"
)

func makeSyntheticLoROM(sizeKiB int) []byte {
	rom := make([]byte, sizeKiB*1024)
	for i := range rom {
		rom[i] = byte(i % 256)
	}
	headerOffset := 0x7FC0
	rom[headerOffset+0x15] = 0x20 // LoROM
	return rom
}

func TestExportRawLoROM(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "export")

	rom := makeSyntheticLoROM(64) // 2 banks
	ident := recovery.ComputeROMIdentity(rom, rom, "none", "lorom")
	doc := recovery.NewDocument(ident)

	cfg := asmexport.Config{
		ProjectName: "test_rom",
		EntryAsm:    "main.asm",
	}

	res, err := asmexport.Export(outDir, doc, rom, cfg)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if res.BytesExported != int64(len(rom)) {
		t.Errorf("got %d bytes exported, want %d", res.BytesExported, len(rom))
	}

	// 1. Verify Manifest
	manifestContent, err := os.ReadFile(res.ManifestPath)
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}
	lines := strings.Split(string(manifestContent), "\n")
	if len(lines) == 0 || lines[0] != "[FUTABA:assemble]" {
		t.Errorf("line 1 of manifest is %q, want '[FUTABA:assemble]'", lines[0])
	}

	// 2. Verify Bank Files
	bank0Content, err := os.ReadFile(filepath.Join(outDir, "bank_00.asm"))
	if err != nil {
		t.Fatalf("failed to read bank_00.asm: %v", err)
	}
	if !strings.Contains(string(bank0Content), "org $008000") {
		t.Errorf("bank_00.asm missing 'org $008000'")
	}
	if !strings.Contains(string(bank0Content), "db $00, $01") {
		t.Errorf("bank_00.asm missing expected db bytes")
	}

	// 3. Verify Main
	mainContent, err := os.ReadFile(filepath.Join(outDir, "main.asm"))
	if err != nil {
		t.Fatalf("failed to read main.asm: %v", err)
	}
	if !strings.Contains(string(mainContent), "incsrc bank_00.asm") ||
		!strings.Contains(string(mainContent), "incsrc bank_01.asm") {
		t.Errorf("main.asm missing bank includes")
	}

	// 4. Verify Overwrite protection
	if _, err := asmexport.Export(outDir, doc, rom, cfg); err == nil {
		t.Error("expected error when exporting into non-empty directory without overwrite, got nil")
	}

	cfg.Overwrite = true
	if _, err := asmexport.Export(outDir, doc, rom, cfg); err != nil {
		t.Errorf("failed to export with overwrite=true: %v", err)
	}
}

func TestExportValidation(t *testing.T) {
	rom := makeSyntheticLoROM(64)
	doc := recovery.NewDocument(recovery.ComputeROMIdentity(rom, rom, "none", "lorom"))

	if _, err := asmexport.Export("", doc, rom, asmexport.Config{}); err == nil {
		t.Error("expected error for empty target dir")
	}
	if _, err := asmexport.Export("dir", nil, rom, asmexport.Config{}); err == nil {
		t.Error("expected error for nil doc")
	}
	if _, err := asmexport.Export("dir", doc, nil, asmexport.Config{}); err == nil {
		t.Error("expected error for empty rom")
	}
	if _, err := asmexport.Export("dir", doc, bytes.Repeat([]byte{0}, 100), asmexport.Config{}); err == nil {
		t.Error("expected error for unaligned rom size")
	}
}
