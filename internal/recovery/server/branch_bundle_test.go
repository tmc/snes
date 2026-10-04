package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestBranchBundle_ValidLoad(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	card := srv.LoadBranchComparisonBundle()
	if card.Status != "available" {
		t.Fatalf("expected valid branch comparison bundle status available, got %q (reason: %s)", card.Status, card.Reason)
	}
	if card.BaselineInput != 3 {
		t.Errorf("expected baseline input 3, got %d", card.BaselineInput)
	}
	if card.BaselinePC != "$0CC133" {
		t.Errorf("expected baseline PC $0CC133, got %s", card.BaselinePC)
	}
	if !card.BaselineTaken {
		t.Errorf("expected baseline branch taken = true")
	}
	if len(card.Cases) != 4 {
		t.Errorf("expected 4 cases, got %d", len(card.Cases))
	}
	if len(card.Timeline) != 3 {
		t.Errorf("expected 3 timeline steps, got %d", len(card.Timeline))
	}

	// Verify timeline steps
	if card.Timeline[0].ROMOffset != "$064120" || card.Timeline[0].Mnemonic != "LDA $11" {
		t.Errorf("unexpected step 0: %+v", card.Timeline[0])
	}
	if card.Timeline[1].ROMOffset != "$064122" || card.Timeline[1].Mnemonic != "CMP #$08" {
		t.Errorf("unexpected step 1: %+v", card.Timeline[1])
	}
	if card.Timeline[2].ROMOffset != "$064124" || card.Timeline[2].Mnemonic != "BCC $C133" {
		t.Errorf("unexpected step 2: %+v", card.Timeline[2])
	}

	// Check receipt verification
	if card.ReceiptSummary == nil || !card.ReceiptSummary.DualBackendVerified || !card.ReceiptSummary.ZeroWritesVerified {
		t.Errorf("receipt summary not verified: %+v", card.ReceiptSummary)
	}

	// Check prediction writes derived from actual receipt
	for _, st := range card.Timeline {
		if st.Prediction7.Writes != 0 || st.Prediction8.Writes != 0 || st.Prediction9.Writes != 0 {
			t.Errorf("expected 0 writes for all predictions in step %d, got 7=%d 8=%d 9=%d",
				st.StepIndex, st.Prediction7.Writes, st.Prediction8.Writes, st.Prediction9.Writes)
		}
		if st.StepIndex == 3 {
			if !strings.Contains(st.Prediction7.Annotation, "0 writes") ||
				!strings.Contains(st.Prediction8.Annotation, "0 writes") ||
				!strings.Contains(st.Prediction9.Annotation, "0 writes") {
				t.Errorf("step 3 prediction annotations missing actual writes: 7=%q 8=%q 9=%q",
					st.Prediction7.Annotation, st.Prediction8.Annotation, st.Prediction9.Annotation)
			}
		}
	}

	// Check cases carry and branch taken
	for _, c := range card.Cases {
		switch c.InputVal {
		case 3:
			if c.CarrySet || !c.BranchTaken || c.ActualSuccessorPC != "$0CC133" || c.WritesCount != 0 {
				t.Errorf("case 3 mismatch: %+v", c)
			}
		case 7:
			if c.CarrySet || !c.BranchTaken || c.ActualSuccessorPC != "$0CC133" || c.WritesCount != 0 {
				t.Errorf("case 7 mismatch: %+v", c)
			}
		case 8:
			if !c.CarrySet || c.BranchTaken || c.ActualSuccessorPC != "$0CC126" || c.WritesCount != 0 {
				t.Errorf("case 8 mismatch: %+v", c)
			}
		case 9:
			if !c.CarrySet || c.BranchTaken || c.ActualSuccessorPC != "$0CC126" || c.WritesCount != 0 {
				t.Errorf("case 9 mismatch: %+v", c)
			}
		}
	}
}

func TestBranchBundle_TamperAndMismatch(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Corrupted byte in case.json
	tamperDir := t.TempDir()
	bundleSrc := filepath.Join(projectDir, "evidence", "bundles", "branch_0cc124")
	bundleDst := filepath.Join(tamperDir, "evidence", "bundles", "branch_0cc124")
	if err := os.MkdirAll(bundleDst, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	for _, name := range []string{"manifest.json", "case.json", "timeline.json", "receipt.json"} {
		content, err := os.ReadFile(filepath.Join(bundleSrc, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if name == "case.json" {
			content = append(content, ' ')
		}
		if err := os.WriteFile(filepath.Join(bundleDst, name), content, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	tamperSrv := *srv
	tamperSrv.ProjectDir = tamperDir
	tamperCard := tamperSrv.LoadBranchComparisonBundle()
	if tamperCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for tampered case.json, got %q", tamperCard.Status)
	}

	// 2. Tampered content with recomputed manifest -> rejected by accepted content pins
	recomputedManifestDir := t.TempDir()
	recomputedBundleDst := filepath.Join(recomputedManifestDir, "evidence", "bundles", "branch_0cc124")
	if err := os.MkdirAll(recomputedBundleDst, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	for _, name := range []string{"case.json", "timeline.json", "receipt.json"} {
		content, err := os.ReadFile(filepath.Join(bundleSrc, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if name == "timeline.json" {
			content = append(content, ' ')
		}
		if err := os.WriteFile(filepath.Join(recomputedBundleDst, name), content, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Recompute manifest digests to simulate attacker updating manifest
	manifestContent, err := os.ReadFile(filepath.Join(bundleSrc, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest BranchBundleManifest
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	for _, name := range []string{"case.json", "timeline.json", "receipt.json"} {
		b, _ := os.ReadFile(filepath.Join(recomputedBundleDst, name))
		d := fmt.Sprintf("%x", sha256.Sum256(b))
		switch name {
		case "case.json":
			manifest.ArtifactDigests["case_sha256"] = d
		case "timeline.json":
			manifest.ArtifactDigests["timeline_sha256"] = d
		case "receipt.json":
			manifest.ArtifactDigests["receipt_sha256"] = d
		}
	}
	updatedManifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(recomputedBundleDst, "manifest.json"), updatedManifestBytes, 0644); err != nil {
		t.Fatalf("write updated manifest: %v", err)
	}

	recomputedSrv := *srv
	recomputedSrv.ProjectDir = recomputedManifestDir
	recomputedCard := recomputedSrv.LoadBranchComparisonBundle()
	if recomputedCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for recomputed manifest with altered timeline, got %q", recomputedCard.Status)
	}

	// 3. Mismatched ROM SHA
	mismatchedROMSrv := *srv
	mismatchedDoc := *srv.Document
	mismatchedDoc.ROM.NormalizedSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	mismatchedROMSrv.Document = &mismatchedDoc

	romMismatchCard := mismatchedROMSrv.LoadBranchComparisonBundle()
	if romMismatchCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for mismatched ROM SHA, got %q", romMismatchCard.Status)
	}

	// 4. Mismatched Stream SHA
	mismatchedStreamSrv := *srv
	mismatchedOcc := *srv.Occurrences
	mismatchedOcc.StreamSHA256 = "2222222222222222222222222222222222222222222222222222222222222222"
	mismatchedStreamSrv.Occurrences = &mismatchedOcc

	streamMismatchCard := mismatchedStreamSrv.LoadBranchComparisonBundle()
	if streamMismatchCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for mismatched Stream SHA, got %q", streamMismatchCard.Status)
	}

	// 5. Mismatched Document SHA
	mismatchedDocSrv := *srv
	mismatchedDocSrv.DocumentSHA256 = "3333333333333333333333333333333333333333333333333333333333333333"

	docMismatchCard := mismatchedDocSrv.LoadBranchComparisonBundle()
	if docMismatchCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for mismatched Document SHA, got %q", docMismatchCard.Status)
	}

	// 6. Manifest retirement seq mismatch
	seqMismatchDir := t.TempDir()
	seqBundleDst := filepath.Join(seqMismatchDir, "evidence", "bundles", "branch_0cc124")
	if err := os.MkdirAll(seqBundleDst, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	for _, name := range []string{"case.json", "timeline.json", "receipt.json"} {
		content, _ := os.ReadFile(filepath.Join(bundleSrc, name))
		_ = os.WriteFile(filepath.Join(seqBundleDst, name), content, 0644)
	}
	seqManifest := manifest
	seqManifest.RetirementSeqs = []uint64{8484, 8485, 8486}
	seqManifestBytes, _ := json.MarshalIndent(seqManifest, "", "  ")
	_ = os.WriteFile(filepath.Join(seqBundleDst, "manifest.json"), seqManifestBytes, 0644)

	seqMismatchSrv := *srv
	seqMismatchSrv.ProjectDir = seqMismatchDir
	seqCard := seqMismatchSrv.LoadBranchComparisonBundle()
	if seqCard.Status != "unavailable" {
		t.Errorf("expected unavailable status for mismatched manifest retirement seqs, got %q", seqCard.Status)
	}
}

func TestBranchBundle_ProjectLocalAbsentMeansUnavailable(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Empty project directory with no evidence/bundles
	emptyProjectDir := t.TempDir()
	emptySrv := *srv
	emptySrv.ProjectDir = emptyProjectDir

	card := emptySrv.LoadBranchComparisonBundle()
	if card.Status != "unavailable" {
		t.Fatalf("expected unavailable status when bundle absent from project dir, got %q", card.Status)
	}
}

func TestBranchBundle_OccurrenceAttachment(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Canonical BCC $0CC124 at trace frame 0 / PPU 332 has BranchComparison attached
	tf0 := 0
	pf332 := 332
	var bccInst *recovery.Instruction
	for _, inst := range srv.Document.Instructions {
		if inst.Address == 0x0CC124 {
			instCopy := inst
			bccInst = &instCopy
			break
		}
	}
	if bccInst == nil {
		t.Fatalf("BCC $0CC124 instruction not found in recovery document")
	}

	rawRep := srv.lookupOccurrence(*bccInst, &tf0, &pf332)
	if rawRep.Status != "available" {
		t.Fatalf("expected raw occurrence available for BCC $0CC124 at trace frame 0, got %q (reason: %s)", rawRep.Status, rawRep.Reason)
	}

	presented := srv.presentOccurrence(rawRep)
	if presented.BranchComparison == nil {
		t.Fatalf("expected BranchComparison attached to presented occurrence for BCC $0CC124 at trace frame 0")
	}
	if presented.BranchComparison.Status != "available" {
		t.Errorf("expected BranchComparison status available, got %q", presented.BranchComparison.Status)
	}

	// 2. Different trace frame (e.g. trace frame 1) has no BranchComparison
	tf1 := 1
	pf333 := 333
	rawRepTF1 := srv.lookupOccurrence(*bccInst, &tf1, &pf333)
	presentedTF1 := srv.presentOccurrence(rawRepTF1)
	if presentedTF1 != nil && presentedTF1.BranchComparison != nil {
		t.Errorf("expected no BranchComparison for BCC $0CC124 at trace frame 1")
	}

	// 3. Different instruction (e.g. LDA $0CC120 or CMP $0CC122) has no BranchComparison
	var ldaInst *recovery.Instruction
	for _, inst := range srv.Document.Instructions {
		if inst.Address == 0x0CC120 {
			instCopy := inst
			ldaInst = &instCopy
			break
		}
	}
	if ldaInst != nil {
		rawRepLDA := srv.lookupOccurrence(*ldaInst, &tf0, &pf332)
		presentedLDA := srv.presentOccurrence(rawRepLDA)
		if presentedLDA != nil && presentedLDA.BranchComparison != nil {
			t.Errorf("expected no BranchComparison attached to LDA $0CC120 (must be canonical BCC $0CC124 only)")
		}
	}
}

func TestBranchBundle_FullStateContinuityProbe(t *testing.T) {
	projectDir := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project"
	if _, err := os.Stat(projectDir); err != nil {
		t.Skipf("natural producer project not found at %s: %v", projectDir, err)
		return
	}

	srv, err := NewServer(projectDir)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Baseline must be available initially
	card := srv.LoadBranchComparisonBundle()
	if card.Status != "available" {
		t.Fatalf("expected initial bundle available, got %q (reason: %s)", card.Status, card.Reason)
	}

	rep1 := srv.Occurrences.Lookup(0, BranchNode1CanonicalID, 0x0CC122)
	if rep1 == nil || rep1.Status != "available" {
		t.Fatalf("rep1 not available")
	}

	// 1. Changing CMP Entry.X by 1 causes refusal
	origX := rep1.Entry.X
	rep1.Entry.X = origX + 1
	cardX := srv.LoadBranchComparisonBundle()
	rep1.Entry.X = origX
	if cardX.Status != "unavailable" || !strings.Contains(cardX.Reason, "X mismatch") {
		t.Errorf("expected unavailable with X mismatch when CMP Entry.X mutated, got status=%q reason=%q", cardX.Status, cardX.Reason)
	}

	// 2. Changing Node 0 Exit.Y by 1 causes refusal
	rep0 := srv.Occurrences.Lookup(0, BranchNode0CanonicalID, 0x0CC120)
	if rep0 == nil || rep0.Status != "available" {
		t.Fatalf("rep0 not available")
	}
	origY := rep0.Exit.Y
	rep0.Exit.Y = origY + 1
	cardY := srv.LoadBranchComparisonBundle()
	rep0.Exit.Y = origY
	if cardY.Status != "unavailable" || !strings.Contains(cardY.Reason, "Y mismatch") {
		t.Errorf("expected unavailable with Y mismatch when Node 0 Exit.Y mutated, got status=%q reason=%q", cardY.Status, cardY.Reason)
	}

	// 3. Changing Node 2 Exit.S by 1 causes refusal (against baseline receipt)
	rep2 := srv.Occurrences.Lookup(0, BranchNode2CanonicalID, 0x0CC124)
	if rep2 == nil || rep2.Status != "available" {
		t.Fatalf("rep2 not available")
	}
	origS := rep2.Exit.S
	rep2.Exit.S = origS + 1
	cardS := srv.LoadBranchComparisonBundle()
	rep2.Exit.S = origS
	if cardS.Status != "unavailable" || !strings.Contains(cardS.Reason, "S mismatch") {
		t.Errorf("expected unavailable with S mismatch when Node 2 Exit.S mutated, got status=%q reason=%q", cardS.Status, cardS.Reason)
	}

	// 4. Changing Node 0 Entry.X by 1 causes refusal (against case.json initial_cpu_state)
	origEntryX := rep0.Entry.X
	rep0.Entry.X = origEntryX + 1
	cardEntryX := srv.LoadBranchComparisonBundle()
	rep0.Entry.X = origEntryX
	if cardEntryX.Status != "unavailable" || !strings.Contains(cardEntryX.Reason, "X mismatch") {
		t.Errorf("expected unavailable with X mismatch when Node 0 Entry.X mutated, got status=%q reason=%q", cardEntryX.Status, cardEntryX.Reason)
	}
}

