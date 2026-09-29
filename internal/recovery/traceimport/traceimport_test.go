package traceimport

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/asmexport"
	"github.com/tmc/snes/internal/recovery/verify"
)

func createSyntheticTestROM() ([]byte, string) {
	rom := make([]byte, 32*1024)
	// Reset vector at $7FC0 + $3C = $7FFC
	rom[0x7FFC] = 0x00
	rom[0x7FFD] = 0x80

	// Code at offset 0:
	// 0x00: SEI (78)
	// 0x01: CLC (18)
	// 0x02: XCE (FB)
	// 0x03: JSR $800A (20 0A 80)
	// 0x06: STP (DB)
	// Offset 0x0A:
	// 0x0A: LDA #$42 (A9 42)
	// 0x0C: RTS (60)
	code := []byte{
		0x78,             // 00: SEI
		0x18,             // 01: CLC
		0xFB,             // 02: XCE
		0x20, 0x0A, 0x80, // 03: JSR $800A
		0xDB,             // 06: STP
		0xEA, 0xEA, 0xEA, // 07-09: NOP
		0xA9, 0x42, // 0A: LDA #$42
		0x60, // 0C: RTS
	}
	copy(rom, code)

	sum := sha256.Sum256(rom)
	return rom, hex.EncodeToString(sum[:])
}

func ptrUint32(v uint32) *uint32 {
	return &v
}

func TestTraceImport_ValidObservations(t *testing.T) {
	rom, romHash := createSyntheticTestROM()

	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom","engine_revision":"rev1"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":2,"entry":{"pb":0,"pc":32769,"p":56,"e":true},"exit":{"pb":0,"pc":32770,"p":56,"e":true},"fetches":[{"addr":32769,"value":24,"role":"opcode","rom_offset":1}],"length":1,"sequential_pc":{"bank":0,"addr":32770},"successor_pc":{"bank":0,"addr":32770},"status":"retired"}}`,
	}, "\n")

	receiptJSON := `{"schema":2,"outcome":"complete","last_seq":2,"event_count":2,"stream_sha256":""}`

	res, err := Parse(strings.NewReader(streamJSON), strings.NewReader(receiptJSON), rom, romHash)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !res.IsComplete {
		t.Errorf("expected complete trace, got incomplete")
	}
	if len(res.Instructions) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(res.Instructions))
	}
	if res.Instructions[0].Mnemonic != "sei" || res.Instructions[1].Mnemonic != "clc" {
		t.Errorf("unexpected mnemonics: %s, %s", res.Instructions[0].Mnemonic, res.Instructions[1].Mnemonic)
	}
	if len(res.Edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(res.Edges))
	}
	if len(res.Evidence) != 2 {
		t.Errorf("expected 2 evidence records, got %d", len(res.Evidence))
	}
}

func TestTraceImport_WrongROM(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := `{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"0000000000000000000000000000000000000000000000000000000000000000","mapper":"lorom"}}`

	_, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err == nil {
		t.Fatal("expected error on mismatched ROM hash, got nil")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected mismatch error message, got %v", err)
	}
}

func TestTraceImport_UnsupportedSchema(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := `{"id":0,"schema":1,"kind":"run","run":{"rom_sha256":"` + romHash + `"}}`

	_, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err == nil {
		t.Fatal("expected error on unsupported schema 1, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported schema version") {
		t.Errorf("expected unsupported schema error, got %v", err)
	}
}

func TestTraceImport_MismatchingFetchBytes(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	// Offset 0 in rom is 0x78 (SEI), but trace reports value 0xEA (NOP) at rom_offset 0
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":52,"e":true},"fetches":[{"addr":32768,"value":234,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Should not have emitted the mismatched instruction
	if len(res.Instructions) != 0 {
		t.Errorf("expected 0 instructions due to fetch mismatch, got %d", len(res.Instructions))
	}
	// Should have recorded an issue
	foundIssue := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Reason, "does not match ROM byte") {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Errorf("expected fetch mismatch issue to be recorded, got %+v", res.Issues)
	}
}

func TestTraceImport_NonROMFetches(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	// Fetch from RAM address $7E:0000 (no rom_offset)
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":126,"pc":0,"p":52,"e":true},"exit":{"pb":126,"pc":1,"p":52,"e":true},"fetches":[{"addr":8257536,"value":120,"role":"opcode"}],"length":1,"sequential_pc":{"bank":126,"addr":1},"successor_pc":{"bank":126,"addr":1},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(res.Instructions) != 0 {
		t.Errorf("expected 0 instructions for non-ROM execution, got %d", len(res.Instructions))
	}
	foundIssue := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Reason, "non-ROM address") {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Errorf("expected non-ROM execution issue, got %+v", res.Issues)
	}
}

func TestTraceImport_MultipleContexts(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	// Offset 0x0A has LDA #$42.
	// Execution 1: E=0, M=1 (8-bit immediate): bytes "a942"
	// Execution 2: E=0, M=0 (16-bit immediate): bytes "a94260" (reads into next byte)
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32778,"p":32,"e":false},"exit":{"pb":0,"pc":32780,"p":32,"e":false},"fetches":[{"addr":32778,"value":169,"role":"opcode","rom_offset":10},{"addr":32779,"value":66,"role":"operand","rom_offset":11}],"length":2,"sequential_pc":{"bank":0,"addr":32780},"successor_pc":{"bank":0,"addr":32780},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":2,"insn":{"seq":2,"entry":{"pb":0,"pc":32778,"p":0,"e":false},"exit":{"pb":0,"pc":32781,"p":0,"e":false},"fetches":[{"addr":32778,"value":169,"role":"opcode","rom_offset":10},{"addr":32779,"value":66,"role":"operand","rom_offset":11},{"addr":32780,"value":96,"role":"operand","rom_offset":12}],"length":3,"sequential_pc":{"bank":0,"addr":32781},"successor_pc":{"bank":0,"addr":32781},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: romHash, Mapper: "lorom"})
	mr, err := Merge(doc, res)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}

	if mr.InstructionsAdded != 2 {
		t.Fatalf("expected 2 distinct instruction interpretations added, got %d", mr.InstructionsAdded)
	}
	if len(doc.Instructions) != 2 {
		t.Fatalf("expected document to retain both contexts, got %d", len(doc.Instructions))
	}
	// Both have offset 10, but different IDs, lengths, and contexts
	if doc.Instructions[0].ID == doc.Instructions[1].ID {
		t.Errorf("expected distinct instruction IDs for different contexts")
	}
}

func TestTraceImport_ConflictingInstructions(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	// Two conflicting instructions at offset 10 (one 2 bytes, one 3 bytes).
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32778,"p":32,"e":false},"exit":{"pb":0,"pc":32780,"p":32,"e":false},"fetches":[{"addr":32778,"value":169,"role":"opcode","rom_offset":10},{"addr":32779,"value":66,"role":"operand","rom_offset":11}],"length":2,"sequential_pc":{"bank":0,"addr":32780},"successor_pc":{"bank":0,"addr":32780},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":2,"insn":{"seq":2,"entry":{"pb":0,"pc":32778,"p":0,"e":false},"exit":{"pb":0,"pc":32781,"p":0,"e":false},"fetches":[{"addr":32778,"value":169,"role":"opcode","rom_offset":10},{"addr":32779,"value":66,"role":"operand","rom_offset":11},{"addr":32780,"value":96,"role":"operand","rom_offset":12}],"length":3,"sequential_pc":{"bank":0,"addr":32781},"successor_pc":{"bank":0,"addr":32781},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: romHash, Mapper: "lorom"})
	_, err = Merge(doc, res)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}

	// Export assembly: conflicting span must fall back to raw db bytes
	tmpDir := t.TempDir()
	exportDir := filepath.Join(tmpDir, "export")
	_, err = asmexport.Export(exportDir, doc, rom, asmexport.Config{
		ProjectName: "test_conflict",
		EntryAsm:    "main.asm",
	})
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}

	bankData, err := os.ReadFile(filepath.Join(exportDir, "bank_00.asm"))
	if err != nil {
		t.Fatalf("Read bank_00.asm: %v", err)
	}
	// Bank should NOT contain lda instruction at conflicted offset; it should be raw db
	if strings.Contains(string(bankData), "lda") {
		t.Errorf("expected conflicted instruction to fall back to db, but found 'lda' in output:\n%s", string(bankData))
	}
}

func TestTraceImport_IdempotentMerge(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: romHash, Mapper: "lorom"})
	mr1, err := Merge(doc, res)
	if err != nil {
		t.Fatalf("Merge 1 error: %v", err)
	}
	if mr1.InstructionsAdded != 1 {
		t.Errorf("expected 1 instruction added in run 1, got %d", mr1.InstructionsAdded)
	}

	docJSON1, _ := json.MarshalIndent(doc, "", "  ")

	// Merge again with same result
	mr2, err := Merge(doc, res)
	if err != nil {
		t.Fatalf("Merge 2 error: %v", err)
	}
	if mr2.InstructionsAdded != 0 || mr2.InstructionsExisting != 1 {
		t.Errorf("expected 0 added, 1 existing in run 2, got added=%d existing=%d", mr2.InstructionsAdded, mr2.InstructionsExisting)
	}

	docJSON2, _ := json.MarshalIndent(doc, "", "  ")

	if string(docJSON1) != string(docJSON2) {
		t.Errorf("merge is not idempotent: documents differ:\nRun1:\n%s\nRun2:\n%s", docJSON1, docJSON2)
	}
}

func TestTraceImport_IncompleteRun(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
	}, "\n")

	// Truncated receipt
	receiptJSON := `{"schema":2,"outcome":"limit","last_seq":1,"event_count":1,"stream_sha256":"","truncation_reason":"event_limit"}`

	res, err := Parse(strings.NewReader(streamJSON), strings.NewReader(receiptJSON), rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if res.IsComplete {
		t.Errorf("expected IsComplete to be false for outcome 'limit'")
	}
	foundIssue := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Reason, "outcome \"limit\"") {
			foundIssue = true
			break
		}
	}
	if !foundIssue {
		t.Errorf("expected truncation issue, got %+v", res.Issues)
	}
}

func TestTraceImport_ObservedCallReturnPaths(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	// Sequence:
	// 1. JSR $800A (at $8003, offset 3) -> target $800A
	// 2. LDA #$42 (at $800A, offset 10)
	// 3. RTS (at $800C, offset 12) -> returns to $8006
	// 4. STP (at $8006, offset 6)
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32771,"p":52,"e":true},"exit":{"pb":0,"pc":32778,"p":52,"e":true},"fetches":[{"addr":32771,"value":32,"role":"opcode","rom_offset":3},{"addr":32772,"value":10,"role":"operand","rom_offset":4},{"addr":32773,"value":128,"role":"operand","rom_offset":5}],"length":3,"sequential_pc":{"bank":0,"addr":32774},"successor_pc":{"bank":0,"addr":32778},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":2,"entry":{"pb":0,"pc":32778,"p":52,"e":true},"exit":{"pb":0,"pc":32780,"p":52,"e":true},"fetches":[{"addr":32778,"value":169,"role":"opcode","rom_offset":10},{"addr":32779,"value":66,"role":"operand","rom_offset":11}],"length":2,"sequential_pc":{"bank":0,"addr":32780},"successor_pc":{"bank":0,"addr":32780},"status":"retired"}}`,
		`{"id":3,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":3,"entry":{"pb":0,"pc":32780,"p":52,"e":true},"exit":{"pb":0,"pc":32774,"p":52,"e":true},"fetches":[{"addr":32780,"value":96,"role":"opcode","rom_offset":12}],"length":1,"sequential_pc":{"bank":0,"addr":32781},"successor_pc":{"bank":0,"addr":32774},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(res.Instructions) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(res.Instructions))
	}
	if len(res.Edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(res.Edges))
	}

	// JSR edge should have kind "call"
	if res.Edges[0].Kind != "call" || res.Edges[0].Destination != 0x800A {
		t.Errorf("unexpected JSR edge: %+v", res.Edges[0])
	}
	// RTS edge should have kind "return" and destination 0x8006
	if res.Edges[2].Kind != "return" || res.Edges[2].Destination != 0x8006 {
		t.Errorf("unexpected RTS edge: %+v", res.Edges[2])
	}
}

func TestTraceImport_ExactRebuildAfterImport(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
		`{"id":2,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":2,"entry":{"pb":0,"pc":32769,"p":56,"e":true},"exit":{"pb":0,"pc":32770,"p":56,"e":true},"fetches":[{"addr":32769,"value":24,"role":"opcode","rom_offset":1}],"length":1,"sequential_pc":{"bank":0,"addr":32770},"successor_pc":{"bank":0,"addr":32770},"status":"retired"}}`,
		`{"id":3,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":3,"entry":{"pb":0,"pc":32770,"p":56,"e":true},"exit":{"pb":0,"pc":32771,"p":56,"e":false},"fetches":[{"addr":32770,"value":251,"role":"opcode","rom_offset":2}],"length":1,"sequential_pc":{"bank":0,"addr":32771},"successor_pc":{"bank":0,"addr":32771},"status":"retired"}}`,
	}, "\n")

	res, err := Parse(strings.NewReader(streamJSON), nil, rom, romHash)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: romHash, Mapper: "lorom"})
	_, err = Merge(doc, res)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}

	tmpDir := t.TempDir()
	exportDir := filepath.Join(tmpDir, "export")
	expRes, err := asmexport.Export(exportDir, doc, rom, asmexport.Config{
		ProjectName: "rebuild_test",
		EntryAsm:    "main.asm",
	})
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}

	// Verify bank_00.asm contains the instructions
	bankBytes, err := os.ReadFile(filepath.Join(exportDir, "bank_00.asm"))
	if err != nil {
		t.Fatalf("ReadFile bank_00.asm: %v", err)
	}
	content := string(bankBytes)
	if !strings.Contains(content, "sei") || !strings.Contains(content, "clc") || !strings.Contains(content, "xce") {
		t.Fatalf("expected bank_00.asm to contain sei, clc, xce, got:\n%s", content)
	}

	// Assemble with snesasm and verify byte-identical output
	rebuiltPath := filepath.Join(tmpDir, "rebuilt.sfc")
	receipt, err := verify.Verify(t.Context(), rebuiltPath, rom, verify.Config{
		AssemblerPath: "snesasm",
		AssemblerArgs: []string{"-o", rebuiltPath, "main.asm"},
		WorkingDir:    exportDir,
		SourceHashes:  expRes.SourceHashes,
	})
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}

	if receipt.Outcome != verify.OutcomeMatched {
		t.Fatalf("verification failed with outcome %s: %s", receipt.Outcome, receipt.Error)
	}
	if receipt.MismatchCount != 0 {
		t.Fatalf("expected 0 mismatches, got %d", receipt.MismatchCount)
	}
}

func TestTraceImport_GzipStream(t *testing.T) {
	rom, romHash := createSyntheticTestROM()
	streamJSON := strings.Join([]string{
		`{"id":0,"schema":2,"kind":"run","run":{"rom_sha256":"` + romHash + `","mapper":"lorom"}}`,
		`{"id":1,"schema":2,"kind":"cpu_insn","frame":1,"insn":{"seq":1,"entry":{"pb":0,"pc":32768,"p":52,"e":true},"exit":{"pb":0,"pc":32769,"p":56,"e":true},"fetches":[{"addr":32768,"value":120,"role":"opcode","rom_offset":0}],"length":1,"sequential_pc":{"bank":0,"addr":32769},"successor_pc":{"bank":0,"addr":32769},"status":"retired"}}`,
	}, "\n")

	// Compress stream JSON using gzip
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write([]byte(streamJSON)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	gzBytes := gzBuf.Bytes()
	gzHashBytes := sha256.Sum256(gzBytes)
	gzHash := hex.EncodeToString(gzHashBytes[:])

	receiptJSON := `{"schema":2,"outcome":"complete","last_seq":1,"event_count":1,"stream_sha256":"` + gzHash + `"}`

	// 1. Valid gzip stream with matching receipt hash
	res, err := Parse(bytes.NewReader(gzBytes), strings.NewReader(receiptJSON), rom, romHash)
	if err != nil {
		t.Fatalf("Parse gzipped stream failed: %v", err)
	}
	if !res.IsComplete {
		t.Errorf("expected complete trace")
	}
	if res.StreamSHA256 != gzHash {
		t.Errorf("expected stream SHA %q, got %q", gzHash, res.StreamSHA256)
	}
	if len(res.Instructions) != 1 {
		t.Errorf("expected 1 instruction, got %d", len(res.Instructions))
	}

	// 2. Gzip stream with mismatched receipt hash must fail
	badReceiptJSON := `{"schema":2,"outcome":"complete","last_seq":1,"event_count":1,"stream_sha256":"badhash"}`
	_, err = Parse(bytes.NewReader(gzBytes), strings.NewReader(badReceiptJSON), rom, romHash)
	if err == nil {
		t.Fatalf("expected error on stream hash mismatch, got nil")
	}
}

