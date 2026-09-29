package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func makeSyntheticLoROM(sizeKiB int) []byte {
	rom := make([]byte, sizeKiB*1024)
	for i := range rom {
		rom[i] = 0xEA
	}
	headerOffset := 0x7FC0
	copy(rom[headerOffset:headerOffset+21], []byte("SYNTHETIC LOROM TEST "))
	rom[headerOffset+0x15] = 0x20 // LoROM
	var sum uint16
	for _, b := range rom {
		sum += uint16(b)
	}
	compOffset := headerOffset + 0x1C
	binary.LittleEndian.PutUint16(rom[compOffset:], ^sum)
	binary.LittleEndian.PutUint16(rom[compOffset+2:], sum)
	return rom
}

func TestSnesRecoverCLI(t *testing.T) {
	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "game.sfc")
	rom := makeSyntheticLoROM(64)
	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tempDir, "recovered")

	// Test basic admission and export
	args := []string{
		"-rom", romPath,
		"-out", outDir,
		"-project-name", "game",
	}

	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run failed: %v (stderr: %s)", err, stderr.String())
	}

	// Verify recovery.json exists
	if _, err := os.Stat(filepath.Join(outDir, "recovery.json")); err != nil {
		t.Errorf("recovery.json not found: %v", err)
	}

	// Verify export directory and files exist
	if _, err := os.Stat(filepath.Join(outDir, "export", "game.futaba")); err != nil {
		t.Errorf("game.futaba not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "export", "bank_00.asm")); err != nil {
		t.Errorf("bank_00.asm not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "export", "export.json")); err != nil {
		t.Errorf("export.json not found: %v", err)
	}

	// Test with verification
	argsWithVerify := []string{
		"-rom", romPath,
		"-out", outDir,
		"-overwrite",
		"-verify-rebuilt", romPath, // verifying against itself should match
	}
	stdout.Reset()
	stderr.Reset()
	if err := run(argsWithVerify, &stdout, &stderr); err != nil {
		t.Fatalf("run with verify failed: %v (stderr: %s)", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "verification", "receipt.json")); err != nil {
		t.Errorf("receipt.json not found: %v", err)
	}
}

func TestSnesRecoverCLIMissingROM(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{}, &stdout, &stderr); err == nil {
		t.Error("expected error when -rom is missing, got nil")
	}
}
