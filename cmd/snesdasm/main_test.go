package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery/verify"
)

func makeSyntheticLoROM(sizeKiB int) []byte {
	rom := make([]byte, sizeKiB*1024)
	for i := range rom {
		rom[i] = 0xEA
	}
	headerOffset := 0x7FC0
	copy(rom[headerOffset:headerOffset+21], []byte("SYNTHETIC LOROM TEST "))
	rom[headerOffset+0x15] = 0x20 // LoROM
	return rom
}

func TestSnesDasmCLI(t *testing.T) {
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
}

func TestOutputOwnership(t *testing.T) {
	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "game.sfc")
	rom := makeSyntheticLoROM(64)
	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tempDir, "existing_output")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Place a pre-existing file in outDir
	dummyFile := filepath.Join(outDir, "dummy.txt")
	if err := os.WriteFile(dummyFile, []byte("prior content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Place pre-existing annotations.json
	annotationsPath := filepath.Join(outDir, "annotations.json")
	annotationData := []byte(`{"format":"snes-recovery-annotations","annotations":[]}`)
	if err := os.WriteFile(annotationsPath, annotationData, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Without -overwrite: MUST fail and MUST NOT create/overwrite recovery.json
	var stdout, stderr bytes.Buffer
	args := []string{"-rom", romPath, "-out", outDir}
	if err := run(args, &stdout, &stderr); err == nil {
		t.Fatal("expected error on non-empty output directory without -overwrite, got nil")
	}
	if _, err := os.Stat(filepath.Join(outDir, "recovery.json")); err == nil {
		t.Error("recovery.json was created despite output ownership failure!")
	}

	// 2. With -overwrite: MUST succeed and MUST preserve annotations.json
	stdout.Reset()
	stderr.Reset()
	args = []string{"-rom", romPath, "-out", outDir, "-overwrite"}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run with -overwrite failed: %v", err)
	}

	// Check annotations.json was preserved
	readAnn, err := os.ReadFile(annotationsPath)
	if err != nil {
		t.Fatalf("annotations.json was lost during overwrite: %v", err)
	}
	if !bytes.Equal(readAnn, annotationData) {
		t.Errorf("annotations.json was modified: got %s, want %s", string(readAnn), string(annotationData))
	}
}

func TestIntegrationGate_SyntheticLoROM(t *testing.T) {
	if _, err := exec.LookPath("snesasm"); err != nil {
		t.Skip("snesasm binary not found in PATH; skipping integration assembly gate")
	}

	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "synthetic_lorom.sfc")
	rom := makeSyntheticLoROM(64) // 2 banks, 64 KiB
	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tempDir, "dasm_out")
	var stdout, stderr bytes.Buffer
	args := []string{
		"-rom", romPath,
		"-out", outDir,
		"-assemble",
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run -assemble failed: %v (stderr: %s)", err, stderr.String())
	}

	// Check receipt
	receiptPath := filepath.Join(outDir, "verification", "receipt.json")
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("failed to read receipt.json: %v", err)
	}

	var rc verify.Receipt
	if err := json.Unmarshal(data, &rc); err != nil {
		t.Fatalf("invalid receipt json: %v", err)
	}

	if rc.Outcome != verify.OutcomeMatched {
		t.Errorf("got outcome %v, want matched", rc.Outcome)
	}
	if rc.MismatchCount != 0 {
		t.Errorf("got %d mismatches, want 0", rc.MismatchCount)
	}
	if rc.TotalExpectedBytes != int64(len(rom)) || rc.TotalActualBytes != int64(len(rom)) {
		t.Errorf("byte count mismatch: expected %d, actual %d", rc.TotalExpectedBytes, rc.TotalActualBytes)
	}
	if rc.AssemblerPath == "" {
		t.Error("receipt missing AssemblerPath")
	}
	if rc.AssemblerSHA256 == "" {
		t.Error("receipt missing AssemblerSHA256")
	}
	if len(rc.SourceHashes) == 0 {
		t.Error("receipt missing SourceHashes")
	}

	// Check log and rebuilt binary
	if _, err := os.Stat(filepath.Join(outDir, "verification", "assembler.log")); err != nil {
		t.Error("assembler.log not found")
	}
	if _, err := os.Stat(filepath.Join(outDir, "verification", "build", "rebuilt.sfc")); err != nil {
		t.Error("rebuilt.sfc not found")
	}
}

func TestIntegrationGate_FailureCases(t *testing.T) {
	rom := makeSyntheticLoROM(32)

	t.Run("SingleByteMismatch", func(t *testing.T) {
		corrupted := make([]byte, len(rom))
		copy(corrupted, rom)
		corrupted[100] ^= 0xFF

		rc := verify.CompareBytes(rom, corrupted, 50)
		if rc.Outcome != verify.OutcomeMismatch {
			t.Errorf("got outcome %v, want %v", rc.Outcome, verify.OutcomeMismatch)
		}
		if rc.MismatchCount != 1 {
			t.Errorf("got %d mismatches, want 1", rc.MismatchCount)
		}
	})

	t.Run("TruncatedROM", func(t *testing.T) {
		truncated := rom[:len(rom)-10]
		rc := verify.CompareBytes(rom, truncated, 50)
		if rc.Outcome != verify.OutcomeMismatch {
			t.Errorf("got outcome %v, want %v", rc.Outcome, verify.OutcomeMismatch)
		}
		if rc.MismatchCount != 10 {
			t.Errorf("got %d mismatches, want 10", rc.MismatchCount)
		}
	})

	t.Run("ExtraBytes", func(t *testing.T) {
		extra := append(rom, 0x00, 0x00, 0x00)
		rc := verify.CompareBytes(rom, extra, 50)
		if rc.Outcome != verify.OutcomeMismatch {
			t.Errorf("got outcome %v, want %v", rc.Outcome, verify.OutcomeMismatch)
		}
		if rc.MismatchCount != 3 {
			t.Errorf("got %d mismatches, want 3", rc.MismatchCount)
		}
	})

	t.Run("AssemblerFailure", func(t *testing.T) {
		tempDir := t.TempDir()
		rebuilt := filepath.Join(tempDir, "rebuilt.sfc")
		// Non-existent assembler
		cfg := verify.Config{
			AssemblerPath: filepath.Join(tempDir, "non_existent_assembler"),
		}
		rc, err := verify.Verify(context.Background(), rebuilt, rom, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rc.Outcome != verify.OutcomeBuildFailed {
			t.Errorf("got outcome %v, want %v", rc.Outcome, verify.OutcomeBuildFailed)
		}
	})

	t.Run("AssemblerTimeout", func(t *testing.T) {
		tempDir := t.TempDir()
		rebuilt := filepath.Join(tempDir, "rebuilt.sfc")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		cfg := verify.Config{
			ToolchainCmd: []string{"sleep", "1"},
		}
		rc, err := verify.Verify(ctx, rebuilt, rom, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rc.Outcome != verify.OutcomeTimedOut && rc.Outcome != verify.OutcomeBuildFailed {
			t.Errorf("got outcome %v, want timed_out or build_failed", rc.Outcome)
		}
	})
}

func TestIntegrationGate_TraceImport(t *testing.T) {
	if _, err := exec.LookPath("snesasm"); err != nil {
		t.Skip("snesasm binary not found in PATH")
	}

	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "synthetic.sfc")
	rom := make([]byte, 32*1024)
	// Reset vector at $7FC0 + $3C = $7FFC
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80
	// Reset code: SEI (78), CLC (18), XCE (FB), STP (DB)
	rom[0] = 0x78
	rom[1] = 0x18
	rom[2] = 0xFB
	rom[3] = 0xDB
	// Subroutine at $8020 (offset 32): LDA #$55 (A9 55), RTS (60)
	rom[32] = 0xA9
	rom[33] = 0x55
	rom[34] = 0x60

	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	h := sha256.Sum256(rom)
	romHash := hex.EncodeToString(h[:])

	// Create trace file
	tracePath := filepath.Join(tempDir, "boot.jsonl")
	streamLines := []string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom","engine_revision":"rev1"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":2,"entry":{"pb":0,"pc":32800,"p":32,"e":false},"exit":{"pb":0,"pc":32802,"p":32,"e":false},"fetches":[{"addr":32800,"value":169,"role":"opcode","rom_offset":32},{"addr":32801,"value":85,"role":"operand","rom_offset":33}],"length":2,"sequential_pc":{"bank":0,"addr":32802},"successor_pc":{"bank":0,"addr":32802},"status":"retired"}}`,
		`{"id":3,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":3,"entry":{"pb":0,"pc":32802,"p":32,"e":false},"exit":{"pb":0,"pc":32770,"p":32,"e":false},"fetches":[{"addr":32802,"value":96,"role":"opcode","rom_offset":34}],"length":1,"sequential_pc":{"bank":0,"addr":32803},"successor_pc":{"bank":0,"addr":32770},"status":"retired"}}`,
	}
	if err := os.WriteFile(tracePath, []byte(strings.Join(streamLines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	receiptPath := filepath.Join(tempDir, "receipt.json")
	receiptJSON := `{"schema":2,"outcome":"complete","last_seq":3,"event_count":3,"stream_sha256":""}`
	if err := os.WriteFile(receiptPath, []byte(receiptJSON), 0644); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	outDir := filepath.Join(tempDir, "recovered")
	var stdout, stderr bytes.Buffer
	args := []string{
		"-rom", romPath,
		"-out", outDir,
		"-trace", tracePath,
		"-assemble",
	}

	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run failed: %v (stderr: %s)", err, stderr.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "Imported trace (complete)") {
		t.Errorf("expected complete trace import output, got:\n%s", outStr)
	}

	// Verify receipt
	rcData, err := os.ReadFile(filepath.Join(outDir, "verification", "receipt.json"))
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var rc verify.Receipt
	if err := json.Unmarshal(rcData, &rc); err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if rc.Outcome != verify.OutcomeMatched {
		t.Errorf("outcome: got %s, want matched", rc.Outcome)
	}
	if rc.MismatchCount != 0 {
		t.Errorf("mismatches: got %d, want 0", rc.MismatchCount)
	}

	// Verify bank_00.asm contains the imported subroutine instructions
	bankData, err := os.ReadFile(filepath.Join(outDir, "export", "bank_00.asm"))
	if err != nil {
		t.Fatalf("read bank_00.asm: %v", err)
	}
	bankStr := string(bankData)
	if !strings.Contains(bankStr, "lda.b #$55") || !strings.Contains(bankStr, "rts") {
		prefix := bankStr
		if len(prefix) > 500 {
			prefix = prefix[:500]
		}
		t.Errorf("expected bank_00.asm to contain subroutine instructions, got:\n%s", prefix)
	}
}
