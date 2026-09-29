package coverage

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestCoverage_ExactCountsAndDeduplication(t *testing.T) {
	idx := NewIndex("synthetic-hash")
	idx.AddRun(RunInfo{
		ID:         "run1",
		ROM_SHA256: "synthetic-hash",
		Outcome:    "complete",
		IsComplete: true,
	})

	// Add events:
	// Event 1 at offset 0 (ROM offset 0)
	ev1 := Event{
		RunID:         "run1",
		Seq:           1,
		Frame:         0,
		Address:       0x008000,
		Offset:        0,
		HasROMOffset:  true,
		InstructionID: "inst-0",
		Context:       recovery.Context{E: "set", M: "set", X: "set", C: "clear"},
	}
	// Event 2 (loop body, executed twice at seq 2 and seq 3)
	ev2 := Event{
		RunID:         "run1",
		Seq:           2,
		Frame:         1,
		Address:       0x008005,
		Offset:        5,
		HasROMOffset:  true,
		InstructionID: "inst-loop",
		Context:       recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
	}
	ev3 := Event{
		RunID:         "run1",
		Seq:           3,
		Frame:         1,
		Address:       0x008005,
		Offset:        5,
		HasROMOffset:  true,
		InstructionID: "inst-loop",
		Context:       recovery.Context{E: "clear", M: "clear", X: "clear", C: "clear"},
	}
	// Event 4: execution from WRAM (non-ROM)
	ev4 := Event{
		RunID:         "run1",
		Seq:           4,
		Frame:         2,
		Address:       0x7E2000,
		Offset:        0,
		HasROMOffset:  false,
		InstructionID: "inst-wram",
	}

	if !idx.AddEvent(ev1) {
		t.Fatalf("expected ev1 to be added")
	}
	if !idx.AddEvent(ev2) {
		t.Fatalf("expected ev2 to be added")
	}
	if !idx.AddEvent(ev3) {
		t.Fatalf("expected ev3 to be added")
	}
	if !idx.AddEvent(ev4) {
		t.Fatalf("expected ev4 to be added")
	}

	// Re-adding duplicate event must return false and NOT inflate totals
	if idx.AddEvent(ev2) {
		t.Fatalf("duplicate event ev2 should have been rejected")
	}

	res, err := idx.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if res.TotalHits != "4" {
		t.Errorf("expected total hits 4, got %s", res.TotalHits)
	}
	if res.Quality != QualityComplete {
		t.Errorf("expected quality complete, got %s", res.Quality)
	}

	// Offset 0 must have exactly 1 hit
	if res.ByOffset[0].Hits != "1" {
		t.Errorf("expected offset 0 hits = 1, got %s", res.ByOffset[0].Hits)
	}
	// Offset 5 must have exactly 2 hits
	if res.ByOffset[5].Hits != "2" {
		t.Errorf("expected offset 5 hits = 2, got %s", res.ByOffset[5].Hits)
	}
	// Instruction inst-loop must have exactly 2 hits
	if res.ByInstruction["inst-loop"].Hits != "2" {
		t.Errorf("expected inst-loop hits = 2, got %s", res.ByInstruction["inst-loop"].Hits)
	}
	// Non-ROM address 0x7E2000 must have 1 hit in ByAddress
	if res.ByAddress[0x7E2000].Hits != "1" {
		t.Errorf("expected address 0x7E2000 hits = 1, got %s", res.ByAddress[0x7E2000].Hits)
	}
}

func TestCoverage_FrameFiltering(t *testing.T) {
	idx := NewIndex("test-rom")
	idx.AddRun(RunInfo{
		ID:         "run1",
		ROM_SHA256: "test-rom",
		Outcome:    "complete",
		IsComplete: true,
	})

	// Add events at frames 5, 10, 20, 30, 31
	frames := []uint64{5, 10, 20, 30, 31}
	for i, f := range frames {
		idx.AddEvent(Event{
			RunID:         "run1",
			Seq:           uint64(i + 1),
			Frame:         f,
			Address:       0x008000,
			Offset:        0,
			HasROMOffset:  true,
			InstructionID: "inst-0",
		})
	}

	// Query half-open [10, 31): should match frames 10, 20, 30 (total 3 hits)
	fStart := uint64(10)
	fEnd := uint64(31)
	res, err := idx.Query(Filter{
		FrameStart: &fStart,
		FrameEnd:   &fEnd,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if res.TotalHits != "3" {
		t.Errorf("expected 3 hits for [10, 31), got %s", res.TotalHits)
	}
	if res.FrameInterval != "[10,31)" {
		t.Errorf("expected frame interval [10,31), got %s", res.FrameInterval)
	}
}

func TestCoverage_BinAggregation(t *testing.T) {
	idx := NewIndex("test-rom")
	idx.AddRun(RunInfo{
		ID:         "run1",
		ROM_SHA256: "test-rom",
		Outcome:    "complete",
		IsComplete: true,
	})

	// Add hits:
	// Bin 0: offset 10 (3 hits), offset 20 (2 hits) -> bin total = 5 hits, hottest start = 10 (3 hits)
	// Bin 1: offset 150 (4 hits) -> bin total = 4 hits, hottest start = 150 (4 hits)
	for seq := uint64(1); seq <= 3; seq++ {
		idx.AddEvent(Event{RunID: "run1", Seq: seq, Frame: 0, Offset: 10, HasROMOffset: true})
	}
	for seq := uint64(4); seq <= 5; seq++ {
		idx.AddEvent(Event{RunID: "run1", Seq: seq, Frame: 0, Offset: 20, HasROMOffset: true})
	}
	for seq := uint64(6); seq <= 9; seq++ {
		idx.AddEvent(Event{RunID: "run1", Seq: seq, Frame: 0, Offset: 150, HasROMOffset: true})
	}

	// Bin size = 100 bytes, total ROM size = 300 bytes -> 3 bins
	bins, err := idx.QueryBins(Filter{}, 100, 300)
	if err != nil {
		t.Fatalf("QueryBins failed: %v", err)
	}

	if len(bins) != 3 {
		t.Fatalf("expected 3 bins, got %d", len(bins))
	}

	// Bin 0: [0, 100)
	if bins[0].TotalHits != "5" {
		t.Errorf("bin 0 total hits expected 5, got %s", bins[0].TotalHits)
	}
	if bins[0].HottestStart != 10 {
		t.Errorf("bin 0 hottest start expected 10, got %d", bins[0].HottestStart)
	}
	if bins[0].MaxStartHits != "3" {
		t.Errorf("bin 0 max start hits expected 3, got %s", bins[0].MaxStartHits)
	}

	// Bin 1: [100, 200)
	if bins[1].TotalHits != "4" {
		t.Errorf("bin 1 total hits expected 4, got %s", bins[1].TotalHits)
	}
	if bins[1].HottestStart != 150 {
		t.Errorf("bin 1 hottest start expected 150, got %d", bins[1].HottestStart)
	}

	// Bin 2: [200, 300) -> 0 hits
	if bins[2].TotalHits != "0" {
		t.Errorf("bin 2 total hits expected 0, got %s", bins[2].TotalHits)
	}
}

func TestCoverage_EncodeDecodeJSON(t *testing.T) {
	idx := NewIndex("test-rom")
	idx.AddRun(RunInfo{
		ID:         "run1",
		ROM_SHA256: "test-rom",
		Outcome:    "complete",
		IsComplete: true,
	})
	idx.AddEvent(Event{
		RunID:         "run1",
		Seq:           1,
		Frame:         1,
		Address:       0x008000,
		Offset:        0,
		HasROMOffset:  true,
		InstructionID: "inst-1",
	})

	var buf bytes.Buffer
	if err := idx.Encode(&buf); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := Decode(&buf)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded.ROMHash != idx.ROMHash {
		t.Errorf("ROMHash mismatch: got %q, want %q", decoded.ROMHash, idx.ROMHash)
	}
	if len(decoded.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(decoded.Events))
	}
}

func TestCoverage_CheckedAddOverflow(t *testing.T) {
	_, ok := checkedAdd(math.MaxUint64, 1)
	if ok {
		t.Errorf("expected overflow detection for MaxUint64 + 1")
	}
	sum, ok := checkedAdd(100, 200)
	if !ok || sum != 300 {
		t.Errorf("expected sum 300, got %d", sum)
	}
}

func TestCoverage_JSONStringPrecision(t *testing.T) {
	summary := CountSummary{
		Hits:    "9007199254740993", // 2^53 + 1, cannot be represented precisely as a JS float64
		Quality: QualityComplete,
	}
	b, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if !bytes.Contains(b, []byte(`"hits":"9007199254740993"`)) {
		t.Errorf("expected hits serialized as string, got %s", string(b))
	}
}
