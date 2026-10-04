package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestStackBundle_ValidLoad(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	card := srv.LoadStackComparisonBundle()
	if card.Status != "available" {
		t.Fatalf("expected valid stack comparison bundle status available, got %q (reason: %s)", card.Status, card.Reason)
	}
	if card.BaselineInput != 54 {
		t.Errorf("expected baseline input 54, got %d", card.BaselineInput)
	}
	if card.BaselineOutput != 55 {
		t.Errorf("expected baseline output 55, got %d", card.BaselineOutput)
	}
	if card.BaselineSuccessorPC != "$0CC40A" {
		t.Errorf("expected baseline successor PC $0CC40A, got %s", card.BaselineSuccessorPC)
	}
	if card.BaselineS != "$01FB" {
		t.Errorf("expected baseline S $01FB, got %s", card.BaselineS)
	}
	if card.BaselineDB != "$0C" {
		t.Errorf("expected baseline DB $0C, got %s", card.BaselineDB)
	}
	if card.BaselineP != "$30" {
		t.Errorf("expected baseline P $30, got %s", card.BaselineP)
	}
	if card.BaselineWrites != 3 {
		t.Errorf("expected baseline writes 3, got %d", card.BaselineWrites)
	}
	if card.ScopeStop != "$0CC40A before JSR" {
		t.Errorf("expected scope stop $0CC40A before JSR, got %s", card.ScopeStop)
	}
	if len(card.Cases) != 3 {
		t.Errorf("expected 3 cases, got %d", len(card.Cases))
	}
	if len(card.Timeline) != 4 {
		t.Errorf("expected 4 timeline steps, got %d", len(card.Timeline))
	}

	// Verify timeline steps
	if card.Timeline[0].ROMOffset != "$064404" || card.Timeline[0].Mnemonic != "PHB" {
		t.Errorf("unexpected step 0: %+v", card.Timeline[0])
	}
	if card.Timeline[1].ROMOffset != "$064405" || card.Timeline[1].Mnemonic != "PHK" {
		t.Errorf("unexpected step 1: %+v", card.Timeline[1])
	}
	if card.Timeline[2].ROMOffset != "$064406" || card.Timeline[2].Mnemonic != "PLB" {
		t.Errorf("unexpected step 2: %+v", card.Timeline[2])
	}
	if card.Timeline[3].ROMOffset != "$064407" || !strings.HasPrefix(card.Timeline[3].Mnemonic, "INC") {
		t.Errorf("unexpected step 3: %+v", card.Timeline[3])
	}

	// Verify 3 ordered writes in all cases
	for _, c := range card.Cases {
		if c.WritesCount != 3 {
			t.Errorf("case %s expected 3 writes, got %d", c.CaseID, c.WritesCount)
		}
		if c.ActualResult == nil {
			t.Errorf("case %s missing actual result", c.CaseID)
			continue
		}
		writes := c.ActualResult.Writes
		if len(writes) != 3 {
			t.Errorf("case %s writes slice length %d != 3", c.CaseID, len(writes))
			continue
		}
		if writes[0].Address != 0x7E01FC || writes[0].Value != 0x00 {
			t.Errorf("case %s write 0 mismatch: addr=%06X val=%02X", c.CaseID, writes[0].Address, writes[0].Value)
		}
		if writes[1].Address != 0x7E01FB || writes[1].Value != 0x0C {
			t.Errorf("case %s write 1 mismatch: addr=%06X val=%02X", c.CaseID, writes[1].Address, writes[1].Value)
		}
		if writes[2].Address != 0x7E1E0A || writes[2].Value != c.ExpectedOutput {
			t.Errorf("case %s write 2 mismatch: addr=%06X val=%02X expected=%02X", c.CaseID, writes[2].Address, writes[2].Value, c.ExpectedOutput)
		}
	}

	// Verify receipt verification flags
	if card.ReceiptSummary == nil || !card.ReceiptSummary.DualBackendVerified || !card.ReceiptSummary.ThreeWritesVerified || !card.ReceiptSummary.BaselineRawVerified {
		t.Errorf("receipt summary verification flags not all true: %+v", card.ReceiptSummary)
	}
}

func TestStackBundle_TamperAndFalsifiers(t *testing.T) {
	origProjectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(origProjectDir); err != nil {
		t.Skipf("natural producer project not found: %v", err)
		return
	}

	tmpDir, err := os.MkdirTemp(os.Getenv("HOME")+"/tmp/snes-auto-jpdasm", "stack-tamper-test-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Copy project tree
	copyDir(origProjectDir, tmpDir)

	srv, err := NewServer(tmpDir)
	if err != nil {
		t.Fatalf("NewServer on tmp copy failed: %v", err)
	}

	// 1. Valid on clean copy
	cleanCard := srv.LoadStackComparisonBundle()
	if cleanCard.Status != "available" {
		t.Fatalf("expected available on clean copy, got %s: %s", cleanCard.Status, cleanCard.Reason)
	}

	// 2. Tampered case.json
	casePath := filepath.Join(tmpDir, "evidence", "bundles", "stack_0cc404", "case.json")
	caseBytes, err := os.ReadFile(casePath)
	if err != nil {
		t.Fatalf("read case.json: %v", err)
	}
	if err := os.WriteFile(casePath, append(caseBytes, []byte("\n// tampering")...), 0644); err != nil {
		t.Fatalf("write tampered case.json: %v", err)
	}
	tamperCard := srv.LoadStackComparisonBundle()
	if tamperCard.Status != "unavailable" {
		t.Fatalf("expected tampered case.json to make bundle unavailable, got %s", tamperCard.Status)
	}

	// Restore case.json
	if err := os.WriteFile(casePath, caseBytes, 0644); err != nil {
		t.Fatalf("restore case.json: %v", err)
	}

	// 3. Mismatched ROM SHA
	mismatchedROMSrv := *srv
	dummyDoc := *srv.Document
	dummyDoc.ROM.NormalizedSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	mismatchedROMSrv.Document = &dummyDoc
	romMismatchCard := mismatchedROMSrv.LoadStackComparisonBundle()
	if romMismatchCard.Status != "unavailable" || !strings.Contains(romMismatchCard.Reason, "ROM SHA-256 mismatch") {
		t.Fatalf("expected ROM mismatch rejection, got %s (reason: %s)", romMismatchCard.Status, romMismatchCard.Reason)
	}

	// 4. Absent bundle directory
	emptyTmpDir, err := os.MkdirTemp(os.Getenv("HOME")+"/tmp/snes-auto-jpdasm", "stack-empty-test-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(emptyTmpDir)
	copyDir(origProjectDir, emptyTmpDir)
	os.RemoveAll(filepath.Join(emptyTmpDir, "evidence", "bundles", "stack_0cc404"))

	emptySrv, err := NewServer(emptyTmpDir)
	if err != nil {
		t.Fatalf("NewServer on empty bundle dir failed: %v", err)
	}
	absentCard := emptySrv.LoadStackComparisonBundle()
	if absentCard.Status != "unavailable" || !strings.Contains(absentCard.Reason, "not found") {
		t.Fatalf("expected absent bundle rejection, got %s (reason: %s)", absentCard.Status, absentCard.Reason)
	}
}

func TestStackBundle_OccurrenceAttachment(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found: %v", err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Canonical PHB $0CC404 at trace frame 0 / PPU 332 has StackComparison attached
	tf0 := 0
	pf332 := 332
	var phbInst *recovery.Instruction
	for _, inst := range srv.Document.Instructions {
		if inst.Address == 0x0CC404 {
			instCopy := inst
			phbInst = &instCopy
			break
		}
	}
	if phbInst == nil {
		t.Fatalf("PHB $0CC404 instruction not found in recovery document")
	}

	rawRep := srv.lookupOccurrence(*phbInst, &tf0, &pf332)
	if rawRep.Status != "available" {
		t.Fatalf("expected raw occurrence available for PHB $0CC404 at trace frame 0, got %q (reason: %s)", rawRep.Status, rawRep.Reason)
	}

	presented := srv.presentOccurrence(rawRep)
	if presented.StackComparison == nil {
		t.Fatalf("expected StackComparison attached to presented occurrence for PHB $0CC404 at trace frame 0")
	}
	if presented.StackComparison.Status != "available" {
		t.Errorf("expected StackComparison status available, got %q (reason: %s)", presented.StackComparison.Status, presented.StackComparison.Reason)
	}

	// 2. Different trace frame (e.g. trace frame 1) has no StackComparison
	tf1 := 1
	pf333 := 333
	rawRepTF1 := srv.lookupOccurrence(*phbInst, &tf1, &pf333)
	presentedTF1 := srv.presentOccurrence(rawRepTF1)
	if presentedTF1 != nil && presentedTF1.StackComparison != nil {
		t.Errorf("expected no StackComparison for PHB $0CC404 at trace frame 1")
	}

	// 3. Different instruction (e.g. PHK $0CC405) has no StackComparison
	var phkInst *recovery.Instruction
	for _, inst := range srv.Document.Instructions {
		if inst.Address == 0x0CC405 {
			instCopy := inst
			phkInst = &instCopy
			break
		}
	}
	if phkInst != nil {
		rawRepPHK := srv.lookupOccurrence(*phkInst, &tf0, &pf332)
		presentedPHK := srv.presentOccurrence(rawRepPHK)
		if presentedPHK != nil && presentedPHK.StackComparison != nil {
			t.Errorf("expected no StackComparison attached to PHK $0CC405 (must be canonical PHB $0CC404 only)")
		}
	}
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
