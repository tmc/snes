package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/recovery/coverage"
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

func TestSubcommands(t *testing.T) {
	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "test.sfc")
	rom := make([]byte, 32*1024)
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80

	// Code at offset 0:
	// SEI (78), STZ $2100 (9C 00 21), JSR $8010 (20 10 80), STP (DB)
	// Offset 0x10:
	// LDA #$42 (A9 42), RTS (60)
	copy(rom[0:], []byte{0x78, 0x9C, 0x00, 0x21, 0x20, 0x10, 0x80, 0xDB})
	copy(rom[0x10:], []byte{0xA9, 0x42, 0x60})
	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatalf("write test rom: %v", err)
	}

	h := sha256.Sum256(rom)
	romHash := hex.EncodeToString(h[:])

	// Create trace stream
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":5,"insn":{"seq":2,"entry":{"pb":0,"pc":32769,"p":56,"e":true},"exit":{"pb":0,"pc":32772,"p":56,"e":true},"fetches":[{"addr":32769,"value":156,"role":"opcode","rom_offset":1},{"addr":32770,"value":0,"role":"operand","rom_offset":2},{"addr":32771,"value":33,"role":"operand","rom_offset":3}],"length":3,"sequential_pc":{"bank":0,"addr":32772},"successor_pc":{"bank":0,"addr":32772},"status":"retired"}}`,
	}, "\n")
	tracePath := filepath.Join(tempDir, "trace.jsonl")
	if err := os.WriteFile(tracePath, []byte(streamJSON), 0644); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	projectDir := filepath.Join(tempDir, "project")
	var stdout, stderr bytes.Buffer

	// 1. Initial recovery run with trace
	recArgs := []string{"-rom", romPath, "-out", projectDir, "-trace", tracePath}
	if err := run(recArgs, &stdout, &stderr); err != nil {
		t.Fatalf("recovery run failed: %v", err)
	}

	// Verify coverage.json was created
	covData, err := os.ReadFile(filepath.Join(projectDir, "coverage.json"))
	if err != nil {
		t.Fatalf("coverage.json was not created: %v", err)
	}
	if len(covData) == 0 {
		t.Fatalf("coverage.json is empty")
	}

	// 2. Test coverage subcommand (json format)
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"coverage", "-project", projectDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm coverage failed: %v", err)
	}
	var covRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &covRes); err != nil {
		t.Fatalf("unmarshal coverage output: %v", err)
	}
	if covRes["total_hits"] != "2" {
		t.Errorf("expected 2 hits, got %v", covRes["total_hits"])
	}

	// 3. Test coverage with frame filter [0, 3)
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"coverage", "-project", projectDir, "-frames", "0:3", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm coverage with frames failed: %v", err)
	}
	var filteredCov map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &filteredCov); err != nil {
		t.Fatalf("unmarshal filtered coverage: %v", err)
	}
	if filteredCov["total_hits"] != "1" {
		t.Errorf("expected 1 hit for frames [0,3), got %v", filteredCov["total_hits"])
	}

	// Re-importing the same trace replaces its run instead of double-counting.
	if err := run(append(recArgs, "-overwrite"), io.Discard, io.Discard); err != nil {
		t.Fatalf("re-import failed: %v", err)
	}
	covIdx := readCoverage(t, projectDir)
	if len(covIdx.Runs) != 1 {
		t.Fatalf("after re-import: %d runs, want 1", len(covIdx.Runs))
	}
	for _, ri := range covIdx.Runs {
		if ri.EventCount != 2 || ri.MinFrame != 1 || ri.MaxFrame != 5 {
			t.Errorf("run info = %+v, want EventCount 2, MinFrame 1, MaxFrame 5", ri)
		}
	}
	stdout.Reset()
	if err := run([]string{"coverage", "-project", projectDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm coverage after re-import failed: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &covRes); err != nil {
		t.Fatalf("unmarshal coverage output: %v", err)
	}
	if covRes["total_hits"] != "2" {
		t.Errorf("after re-import: expected 2 hits, got %v", covRes["total_hits"])
	}

	// 4. Test routines subcommand
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"routines", "-project", projectDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm routines failed: %v", err)
	}
	var routines []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &routines); err != nil {
		t.Fatalf("unmarshal routines: %v", err)
	}
	if len(routines) == 0 {
		t.Errorf("expected at least 1 routine")
	}

	// 5. Test disasm subcommand
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"disasm", "-project", projectDir, "-addr", "008000", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm disasm failed: %v", err)
	}
	var insns []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &insns); err != nil {
		t.Fatalf("unmarshal disasm: %v", err)
	}
	if len(insns) == 0 {
		t.Errorf("expected disasm instructions")
	}

	// 6. Test refs subcommand
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"refs", "-project", projectDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm refs failed: %v", err)
	}
	var refs []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &refs); err != nil {
		t.Fatalf("unmarshal refs: %v", err)
	}
	foundINIDISP := false
	for _, r := range refs {
		if r["hardware_name"] == "INIDISP" {
			foundINIDISP = true
			break
		}
	}
	if !foundINIDISP {
		t.Errorf("expected INIDISP ref in refs output")
	}

	// 7. Test graph subcommand (format dot)
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"graph", "-project", projectDir, "-format", "dot"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm graph failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "digraph CFG") {
		t.Errorf("expected DOT graph output, got:\n%s", stdout.String())
	}
}

func TestSnesDasmCLI_Watches(t *testing.T) {
	tempDir := t.TempDir()

	// Write synthetic watches.json
	watchesContent := `{
  "format": "snes-game-state-watches",
  "schema_version": 1,
  "rom_sha256": "test-sha",
  "watches": [
    {
      "id": "player_hp",
      "name": "Player HP",
      "memory_space": "wram",
      "offset": 16,
      "width": 1,
      "unit": "hearts",
      "scale_denominator": 4
    },
    {
      "id": "game_mode",
      "name": "Game Mode",
      "memory_space": "wram",
      "offset": 32,
      "width": 1,
      "enum_labels": {
        "0": "Title",
        "1": "Overworld",
        "2": "Dungeon"
      }
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "watches.json"), []byte(watchesContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write synthetic snapshots.json
	data1 := make([]byte, 64)
	data1[16] = 12 // 3 hearts
	data1[32] = 0  // Title
	data2 := make([]byte, 64)
	data2[16] = 8 // 2 hearts
	data2[32] = 1 // Overworld

	snapshotsContent := `[
  {
    "format": "snes-wram-snapshot",
    "schema_version": 1,
    "run_id": "run-test",
    "rom_sha256": "test-sha",
    "memory_space": "wram",
    "base_offset": 0,
    "length": 64,
    "sequence": 1,
    "frame": 10,
    "data": "` + hex.EncodeToString(data1) + `"
  },
  {
    "format": "snes-wram-snapshot",
    "schema_version": 1,
    "run_id": "run-test",
    "rom_sha256": "test-sha",
    "memory_space": "wram",
    "base_offset": 0,
    "length": 64,
    "sequence": 2,
    "frame": 20,
    "data": "` + hex.EncodeToString(data2) + `"
  }
]`
	if err := os.WriteFile(filepath.Join(tempDir, "snapshots.json"), []byte(snapshotsContent), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	// 1. snesdasm watches -format text
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"watches", "-project", tempDir}, &stdout, &stderr); err != nil {
		t.Fatalf("watches text failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "player_hp") || !strings.Contains(stdout.String(), "game_mode") {
		t.Errorf("expected watches list to contain player_hp and game_mode, got:\n%s", stdout.String())
	}

	// 2. snesdasm watches -format json
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"watches", "-project", tempDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("watches json failed: %v", err)
	}
	var wf map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &wf); err != nil {
		t.Fatalf("unmarshal watches json: %v", err)
	}
	if wf["format"] != "snes-game-state-watches" {
		t.Errorf("unexpected format in watches json: %v", wf["format"])
	}

	// 3. snesdasm watch -id player_hp -format text
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"watch", "-project", tempDir, "-id", "player_hp"}, &stdout, &stderr); err != nil {
		t.Fatalf("watch player_hp failed: %v", err)
	}
	outText := stdout.String()
	if !strings.Contains(outText, "Player HP") || !strings.Contains(outText, "hearts") {
		t.Errorf("expected player_hp history output, got:\n%s", outText)
	}

	// 4. snesdasm watch -id player_hp -changes
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"watch", "-project", tempDir, "-id", "player_hp", "-changes"}, &stdout, &stderr); err != nil {
		t.Fatalf("watch changes failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "[10, 20)") {
		t.Errorf("expected change interval [10, 20), got:\n%s", stdout.String())
	}

	// 5. snesdasm watch -id game_mode -format json
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"watch", "-project", tempDir, "-id", "game_mode", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("watch game_mode json failed: %v", err)
	}
	var modeHistory []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &modeHistory); err != nil {
		t.Fatalf("unmarshal game_mode history: %v", err)
	}
	if len(modeHistory) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(modeHistory))
	}
	eval1, ok := modeHistory[0]["evaluation"].(map[string]any)
	if !ok || eval1["enum_label"] != "Title" {
		t.Errorf("expected first snapshot enum label Title, got %v", eval1)
	}
	eval2, ok := modeHistory[1]["evaluation"].(map[string]any)
	if !ok || eval2["enum_label"] != "Overworld" {
		t.Errorf("expected second snapshot enum label Overworld, got %v", eval2)
	}
}

func readCoverage(t *testing.T, projectDir string) *coverage.Index {
	t.Helper()
	f, err := os.Open(filepath.Join(projectDir, "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	idx, err := coverage.Decode(f)
	if err != nil {
		t.Fatalf("decode coverage.json: %v", err)
	}
	return idx
}

func TestHelp(t *testing.T) {
	tests := []struct {
		args  []string
		wants []string
	}{
		{[]string{"-h"}, []string{"usage: snesdasm", "Commands:", "coverage", "serve", "Examples:", "snesdasm -rom"}},
		{[]string{"-help"}, []string{"Recovery flags:", "Examples:"}},
		{[]string{"help"}, []string{"usage: snesdasm", "Commands:", "coverage", "serve"}},
		{[]string{"help", "coverage"}, []string{"usage: snesdasm coverage -project dir [flags]", "Report execution coverage", "-frames", "Examples:"}},
		{[]string{"coverage", "-h"}, []string{"usage: snesdasm coverage -project dir [flags]", "-frames", "(required)"}},
		{[]string{"help", "routines"}, []string{"usage: snesdasm routines -project dir [flags]", "List routine candidates", "-format", "Examples:"}},
		{[]string{"routines", "-h"}, []string{"usage: snesdasm routines -project dir [flags]", "-format", "(required)"}},
		{[]string{"help", "disasm"}, []string{"usage: snesdasm disasm -project dir [flags]", "Print recovered disassembly", "-limit", "Examples:"}},
		{[]string{"disasm", "-h"}, []string{"usage: snesdasm disasm -project dir [flags]", "-addr", "(required)"}},
		{[]string{"help", "refs"}, []string{"usage: snesdasm refs -project dir [flags]", "List memory references", "-format", "Examples:"}},
		{[]string{"refs", "-h"}, []string{"usage: snesdasm refs -project dir [flags]", "-addr", "(required)"}},
		{[]string{"help", "graph"}, []string{"usage: snesdasm graph -project dir [flags]", "Print the control-flow graph", "-format", "Examples:"}},
		{[]string{"graph", "-h"}, []string{"usage: snesdasm graph -project dir [flags]", "-addr", "(required)"}},
		{[]string{"help", "watches"}, []string{"usage: snesdasm watches -project dir [flags]", "List game-state watch definitions", "-format", "Examples:"}},
		{[]string{"watches", "-h"}, []string{"usage: snesdasm watches -project dir [flags]", "-format", "(required)"}},
		{[]string{"help", "watch"}, []string{"usage: snesdasm watch -project dir -id name [flags]", "Show the value history", "-changes", "Examples:"}},
		{[]string{"watch", "-h"}, []string{"usage: snesdasm watch -project dir -id name [flags]", "-id", "(required)"}},
		{[]string{"help", "pseudoc"}, []string{"usage: snesdasm pseudoc -project dir [flags]", "Generate machine-semantic pseudo-C", "-compilable", "Examples:"}},
		{[]string{"pseudoc", "-h"}, []string{"usage: snesdasm pseudoc -project dir [flags]", "-compilable", "(required)"}},
		{[]string{"help", "serve"}, []string{"usage: snesdasm serve -project dir [flags]", "Start an HTTP server", "-http", "Examples:"}},
		{[]string{"serve", "-h"}, []string{"usage: snesdasm serve -project dir [flags]", "-http", "(required)"}},
		{[]string{"help", "correlate"}, []string{"Usage: correlate -case cases.jsonl -trace trace.jsonl"}},
		{[]string{"correlate", "-h"}, []string{"Usage: correlate -case cases.jsonl -trace trace.jsonl"}},
		{[]string{"help", "readers"}, []string{"usage: snesdasm readers -window file -window-sha256 sha -writer id", "Explain observed readers"}},
		{[]string{"readers", "-h"}, []string{"usage: snesdasm readers -window file -window-sha256 sha -writer id"}},
		{[]string{"help", "semantic"}, []string{"usage: snesdasm semantic -config pinned.json -out dir", "Emit opt-in executable local-value C"}},
		{[]string{"semantic", "-h"}, []string{"usage: snesdasm semantic -config pinned.json -out dir"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run(tt.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%q) = %v, want nil", tt.args, err)
			}
			out := stdout.String() + stderr.String()
			for _, want := range tt.wants {
				if !strings.Contains(out, want) {
					t.Errorf("run(%q) output missing %q:\n%s", tt.args, want, out)
				}
			}
		})
	}

	// Unknown help topic
	t.Run("help unknown", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"help", "foobar"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), `unknown help topic "foobar"`) {
			t.Errorf("expected unknown help topic error, got %v", err)
		}
	})

	// Unknown command
	t.Run("unknown command", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"foobar"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), `unknown command "foobar"`) {
			t.Errorf("expected unknown command error, got %v", err)
		}
	})
}

func TestCoverageErrors(t *testing.T) {
	dir := t.TempDir()
	oldFormat := `{"rom_hash":"x","default_runs":["r"],"runs":{},"events":[{"run_id":"r","seq":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "coverage.json"), []byte(oldFormat), 0644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-frames", "5:3"}, "invalid -frames"},
		{[]string{"-frames", "5"}, "invalid -frames"},
		{[]string{"-frames", "a:b"}, "invalid -frames"},
		{[]string{"-frames", "-1:3"}, "invalid -frames"},
		{[]string{"-frames", "1:2:3"}, "invalid -frames"},
		{nil, "re-run import"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			args := append([]string{"coverage", "-project", dir}, tt.args...)
			err := run(args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run(%q) = %v, want error containing %q", args, err, tt.want)
			}
		})
	}
}

func TestPseudocCLI(t *testing.T) {
	tempDir := t.TempDir()
	romPath := filepath.Join(tempDir, "game.sfc")
	rom := makeSyntheticLoROM(64)
	if err := os.WriteFile(romPath, rom, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tempDir, "recovered")
	args := []string{"-rom", romPath, "-out", outDir}
	if err := run(args, io.Discard, io.Discard); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	// 1. Text pseudo-C output
	var stdout, stderr bytes.Buffer
	if err := run([]string{"pseudoc", "-project", outDir}, &stdout, &stderr); err != nil {
		t.Fatalf("pseudoc failed: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "void block_") {
		t.Errorf("expected pseudo-C function in output, got:\n%s", stdout.String())
	}

	// 2. Compilable C output
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"pseudoc", "-project", outDir, "-compilable"}, &stdout, &stderr); err != nil {
		t.Fatalf("pseudoc -compilable failed: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "execute_block_") {
		t.Errorf("expected execute_block_ in compilable output, got:\n%s", stdout.String())
	}

	// 3. JSON output
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"pseudoc", "-project", outDir, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("pseudoc -format json failed: %v (stderr: %s)", err, stderr.String())
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json output failed: %v", err)
	}
	if res["block_id"] == nil || res["pseudoc"] == nil {
		t.Errorf("expected block_id and pseudoc in JSON response, got: %v", res)
	}

	// 4. Validate and save receipt
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"pseudoc", "-project", outDir, "-validate", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("pseudoc -validate failed: %v (stderr: %s)", err, stderr.String())
	}
	var valRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &valRes); err != nil {
		t.Fatalf("unmarshal json output failed: %v", err)
	}
	valObj, ok := valRes["validation"].(map[string]any)
	if !ok || valObj["matched"] != true {
		t.Fatalf("expected validation matched=true, got: %v", valRes["validation"])
	}

	// 5. Read saved receipt with -receipt
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"pseudoc", "-project", outDir, "-receipt", "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("pseudoc -receipt failed: %v (stderr: %s)", err, stderr.String())
	}
	var recRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &recRes); err != nil {
		t.Fatalf("unmarshal json output failed: %v", err)
	}
	recObj, ok := recRes["validation"].(map[string]any)
	if !ok || recObj["matched"] != true {
		t.Fatalf("expected saved receipt matched=true, got: %v", recRes["validation"])
	}
}

func TestSubcommands_CorrelateAndReaders(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Test correlate subcommand
	caseFile := filepath.Join(tmpDir, "cases.jsonl")
	traceFile := filepath.Join(tmpDir, "trace.jsonl")

	caseData := `{
		"case_id": "test_dasm_correlate",
		"frame": 82,
		"entry_pc": 34300,
		"entry_seq": 100,
		"return_seq": 200,
		"instruction_count": 10,
		"initial_state": {"cycles": 1000},
		"observed_exit_state": {"cycles": 2000},
		"observed_writes": [
			{"address": 8260096, "value": 170}
		]
	}`
	traceData := strings.Join([]string{
		`{"kind": "cpu_transition", "frame": 83, "cycle": 2500, "transition": {"kind": "nmi", "seq": 250}}`,
		`{"kind": "cpu_insn", "frame": 83, "cycle": 2600, "insn": {"seq": 260, "entry": {"pc": 32768, "a": 4, "p": 32, "cycles": 2600}, "fetches": [{"addr": 32768, "value": 141}, {"addr": 32769, "value": 1}, {"addr": 32770, "value": 67}]}}`,
	}, "\n")

	if err := os.WriteFile(caseFile, []byte(caseData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traceFile, []byte(traceData), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"correlate", "-case", caseFile, "-trace", traceFile, "-format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm correlate failed: %v, stderr: %s", err, stderr.String())
	}
	var corrRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &corrRes); err != nil {
		t.Fatalf("unmarshal correlate json output failed: %v", err)
	}
	if corrRes["case_id"] != "test_dasm_correlate" {
		t.Errorf("got case_id %v, want test_dasm_correlate", corrRes["case_id"])
	}

	// 2. Test readers subcommand
	hStr := strings.Repeat("a", 64)
	w := provenance.Window{
		Schema:   "snes-observation-window-v1",
		Complete: true,
		Coverage: provenance.WriterCoverage,
		To:       1,
		Identity: provenance.Identity{
			ROMSHA256:    hStr,
			StateSHA256:  hStr,
			InputsSHA256: hStr,
			RunSHA256:    hStr,
			Mode:         "original_interpreter",
		},
		Frames: []provenance.FrameIdentity{
			{PPUFrame: 1, VBlankCycle: 2, EndCycle: 3, StateSHA256: hStr, BusSHA256: hStr, PixelSHA256: hStr},
		},
		Events: []provenance.Event{
			{Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x1f05, PPUFrame: 1},
		},
	}
	windowData, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	windowFile := filepath.Join(tmpDir, "window.json")
	if err := os.WriteFile(windowFile, windowData, 0644); err != nil {
		t.Fatal(err)
	}
	windowPin := fmt.Sprintf("%x", sha256.Sum256(windowData))

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"readers", "-window", windowFile, "-window-sha256", windowPin, "-writer", "0"}, &stdout, &stderr); err != nil {
		t.Fatalf("snesdasm readers failed: %v, stderr: %s", err, stderr.String())
	}
	var readRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &readRes); err != nil {
		t.Fatalf("unmarshal readers json output failed: %v", err)
	}
	if readRes["termination"] != "window_end" {
		t.Errorf("got termination %v, want window_end", readRes["termination"])
	}
}

func TestSemanticCommand(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"missing", `{}`, "missing ROM sha256"},
		{"wrong ROM", `{"rom":"ROM","rom_sha256":"bad"}`, "ROM sha256 differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rom := filepath.Join(dir, "rom")
			if err := os.WriteFile(rom, []byte{1}, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := strings.Replace(tc.config, "ROM", rom, 1)
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			err := run([]string{"semantic", "-config", p, "-out", filepath.Join(dir, "out")}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if _, err = os.Stat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
				t.Fatal("refused generation published output")
			}
		})
	}
}


