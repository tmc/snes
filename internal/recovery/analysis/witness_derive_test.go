package analysis

import (
	"os"
	"reflect"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestDeriveWitnessesAndCumulativeRecovery(t *testing.T) {
	const (
		romPath   = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/rom.sfc"
		tracePath = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/trace.jsonl"
	)

	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Skipf("skipping: ROM not found: %v", err)
	}
	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Skipf("skipping: trace not found: %v", err)
	}

	witnesses, err := DeriveWitnessesFromTrace(traceBytes, rom)
	if err != nil {
		t.Fatalf("DeriveWitnessesFromTrace failed: %v", err)
	}

	if len(witnesses) != 2 {
		t.Fatalf("expected 2 witnesses, got %d", len(witnesses))
	}

	// Verify Witness 1: $0080C6 -> $0CC120
	w1 := witnesses[0]
	if w1.DispatchEventID != 29893 || w1.TargetEventID != 29897 {
		t.Errorf("w1 event IDs unexpected: dispatch=%d, target=%d", w1.DispatchEventID, w1.TargetEventID)
	}
	if w1.SourceAddress != 0x0080C6 || w1.TargetAddress != 0x0CC120 {
		t.Errorf("w1 edge unexpected: $%06X -> $%06X", w1.SourceAddress, w1.TargetAddress)
	}
	wantCtx := recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}
	if w1.ObservedContext != wantCtx {
		t.Errorf("w1 observed context unexpected: %+v", w1.ObservedContext)
	}

	// Verify Witness 2: $0087BD -> $0CC404
	w2 := witnesses[1]
	if w2.DispatchEventID != 30000 || w2.TargetEventID != 30003 {
		t.Errorf("w2 event IDs unexpected: dispatch=%d, target=%d", w2.DispatchEventID, w2.TargetEventID)
	}
	if w2.SourceAddress != 0x0087BD || w2.TargetAddress != 0x0CC404 {
		t.Errorf("w2 edge unexpected: $%06X -> $%06X", w2.SourceAddress, w2.TargetAddress)
	}
	if w2.ObservedContext != wantCtx {
		t.Errorf("w2 observed context unexpected: %+v", w2.ObservedContext)
	}

	doc := recovery.NewDocument(recovery.ROMIdentity{
		NormalizedSHA256: w1.ROMSHA256,
	})

	cfg := Config{
		MaxInstructions: 5000,
		SeedAddress:     0x008056,
		SeedContext:     wantCtx,
	}

	// Cumulative recovery for edge 1 (baseline 9 vs 79)
	rep1, err := RunCumulativeWitnessRecovery(rom, doc, witnesses[:1], cfg)
	if err != nil {
		t.Fatalf("RunCumulativeWitnessRecovery edge 1 failed: %v", err)
	}
	if rep1.BaselinePhysicalStarts != 9 || rep1.WitnessPhysicalStarts != 79 || rep1.DeltaPhysicalStarts != 70 {
		t.Errorf("rep1 starts unexpected: baseline=%d, witness=%d, delta=%d",
			rep1.BaselinePhysicalStarts, rep1.WitnessPhysicalStarts, rep1.DeltaPhysicalStarts)
	}

	// Cumulative recovery for edge 2 (baseline 79 vs 107)
	rep2, err := RunCumulativeWitnessRecovery(rom, doc, witnesses[:2], cfg)
	if err != nil {
		t.Fatalf("RunCumulativeWitnessRecovery edge 2 failed: %v", err)
	}
	if rep2.BaselinePhysicalStarts != 79 || rep2.WitnessPhysicalStarts != 107 || rep2.DeltaPhysicalStarts != 28 {
		t.Errorf("rep2 starts unexpected: baseline=%d, witness=%d, delta=%d",
			rep2.BaselinePhysicalStarts, rep2.WitnessPhysicalStarts, rep2.DeltaPhysicalStarts)
	}

	expectedAddedOffsets := []uint32{
		1921, 1923, 1924, 1926, 1928, 1931, 1932, 1933, 1934, 1936, 1937, 1939, 1941, 1943, 1945,
		410628, 410629, 410630, 410631, 410634, 410677, 410679, 410682, 410685, 410687, 410695, 410696, 410699,
	}
	if !reflect.DeepEqual(rep2.AddedPhysicalOffsets, expectedAddedOffsets) {
		t.Errorf("rep2 added offsets mismatch: got %v, want %v", rep2.AddedPhysicalOffsets, expectedAddedOffsets)
	}

	// Check that iss-0087bd was resolved and replaced by new boundaries while earlier issues were preserved
	issueIDs := make(map[string]bool)
	for _, iss := range rep2.UnresolvedIssues {
		issueIDs[iss.ID] = true
	}
	if issueIDs["iss-0087bd"] {
		t.Errorf("expected iss-0087bd to be resolved by witness 2, but it remained unresolved")
	}
	for _, expectedID := range []string{"iss-008056", "iss-0cc135", "iss-0cc40a", "iss-0cc43f", "iss-0cc44b", "iss-008799"} {
		if !issueIDs[expectedID] {
			t.Errorf("expected preserved issue %s not found in report", expectedID)
		}
	}
}
