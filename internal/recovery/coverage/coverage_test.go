package coverage

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// newTestIndex returns an index holding one complete run built from events.
func newTestIndex(t *testing.T, events ...Event) *Index {
	t.Helper()
	b := NewBuilder()
	for _, e := range events {
		if err := b.Add(e); err != nil {
			t.Fatalf("Builder.Add: %v", err)
		}
	}
	idx := NewIndex("test-rom")
	if err := idx.AddRun(RunInfo{ID: "run1", ROM_SHA256: "test-rom", StreamSHA: "test-stream-sha", Outcome: "complete", IsComplete: true}, b.Sites("run1")); err != nil {
		t.Fatalf("AddRun: %v", err)
	}
	return idx
}

func romEvent(seq, frame uint64, off uint32, id string) Event {
	return Event{Seq: seq, Frame: frame, Address: 0x008000 | off, Offset: off, HasROMOffset: true, InstructionID: id}
}

func TestBuilderAggregates(t *testing.T) {
	events := []Event{
		romEvent(1, 0, 0, "a"),
		romEvent(2, 1, 5, "loop"),
		romEvent(3, 1, 5, "loop"),
		romEvent(4, 3, 5, "loop"),
		romEvent(5, 2, 5, "loop"), // out of frame order
		{Seq: 6, Frame: 2, Address: 0x7E2000, InstructionID: "wram"},
	}
	b := NewBuilder()
	for _, e := range events {
		if err := b.Add(e); err != nil {
			t.Fatalf("Builder.Add: %v", err)
		}
	}
	sites := b.Sites("run1")

	var total uint64
	byID := make(map[string]Site)
	for _, s := range sites {
		total += s.Hits
		byID[s.InstructionID] = s
		if s.RunID != "run1" {
			t.Errorf("site %s RunID = %q, want run1", s.InstructionID, s.RunID)
		}
		var sum uint64
		for _, h := range s.FrameHits {
			sum += h
		}
		if sum != s.Hits {
			t.Errorf("site %s frame hits sum to %d, want %d", s.InstructionID, sum, s.Hits)
		}
	}
	if total != uint64(len(events)) {
		t.Errorf("total hits = %d, want %d", total, len(events))
	}

	got := byID["loop"]
	want := Site{
		RunID: "run1", InstructionID: "loop", Address: 0x008005, Offset: 5, HasROMOffset: true,
		Hits: 4, FirstSeq: 2, LastSeq: 5, FirstFrame: 1, LastFrame: 3,
		Frames: []uint64{1, 2, 3}, FrameHits: []uint64{2, 1, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loop site = %+v, want %+v", got, want)
	}
}

func TestQueryCounts(t *testing.T) {
	idx := newTestIndex(t,
		romEvent(1, 0, 0, "a"),
		romEvent(2, 1, 5, "loop"),
		romEvent(3, 1, 5, "loop"),
		Event{Seq: 4, Frame: 2, Address: 0x7E2000, InstructionID: "wram"},
	)
	res, err := idx.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalHits != "4" || res.Quality != QualityComplete {
		t.Errorf("TotalHits, Quality = %s, %s; want 4, complete", res.TotalHits, res.Quality)
	}
	if got := res.ByOffset[0].Hits; got != "1" {
		t.Errorf("offset 0 hits = %s, want 1", got)
	}
	if got := res.ByOffset[5].Hits; got != "2" {
		t.Errorf("offset 5 hits = %s, want 2", got)
	}
	if got := res.ByInstruction["loop"].Hits; got != "2" {
		t.Errorf("loop hits = %s, want 2", got)
	}
	if got := res.ByAddress[0x7E2000].Hits; got != "1" {
		t.Errorf("address $7E2000 hits = %s, want 1", got)
	}
	if len(res.ByOffset) != 2 {
		t.Errorf("ByOffset has %d entries, want 2 (non-ROM sites excluded)", len(res.ByOffset))
	}
}

func TestQueryFrameInterval(t *testing.T) {
	// Instruction "a" runs twice in each of frames 5, 10, 20, 30, 31.
	var events []Event
	seq := uint64(1)
	for _, f := range []uint64{5, 10, 20, 30, 31} {
		for range 2 {
			events = append(events, romEvent(seq, f, 0, "a"))
			seq++
		}
	}
	// Instruction "b" runs once in frame 40.
	events = append(events, romEvent(seq, 40, 3, "b"))
	idx := newTestIndex(t, events...)

	u := func(v uint64) *uint64 { return &v }
	tests := []struct {
		name       string
		start, end *uint64
		interval   string
		total      string
		hits       string // hits for "a"; "" means absent
		firstFrame uint64
		lastFrame  uint64
		firstSeq   uint64 // 0 means omitted
		lastSeq    uint64
		hasB       bool
	}{
		{"all", nil, nil, "", "11", "10", 5, 31, 1, 10, true},
		{"middle", u(10), u(31), "[10,31)", "6", "6", 10, 30, 0, 0, false},
		{"covers a", u(0), u(32), "[0,32)", "10", "10", 5, 31, 1, 10, false},
		{"head", u(0), u(11), "[0,11)", "4", "4", 5, 10, 1, 0, false},
		{"tail", u(30), nil, "[30,inf)", "5", "4", 30, 31, 0, 10, true},
		{"prefix", nil, u(6), "[0,6)", "2", "2", 5, 5, 1, 0, false},
		{"empty", u(11), u(20), "[11,20)", "0", "", 0, 0, 0, 0, false},
		{"single b", u(40), u(41), "[40,41)", "1", "", 0, 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := idx.Query(Filter{FrameStart: tt.start, FrameEnd: tt.end})
			if err != nil {
				t.Fatal(err)
			}
			if res.FrameInterval != tt.interval {
				t.Errorf("FrameInterval = %q, want %q", res.FrameInterval, tt.interval)
			}
			if res.TotalHits != tt.total {
				t.Errorf("TotalHits = %s, want %s", res.TotalHits, tt.total)
			}
			if _, ok := res.ByInstruction["b"]; ok != tt.hasB {
				t.Errorf("ByInstruction has b = %v, want %v", ok, tt.hasB)
			}
			a, ok := res.ByInstruction["a"]
			if tt.hits == "" {
				if ok {
					t.Errorf("ByInstruction[a] = %+v, want absent", a)
				}
				if _, ok := res.ByOffset[0]; ok {
					t.Errorf("ByOffset[0] present, want absent")
				}
				return
			}
			if !ok {
				t.Fatalf("ByInstruction[a] absent")
			}
			if a.Hits != tt.hits {
				t.Errorf("a hits = %s, want %s", a.Hits, tt.hits)
			}
			if a.FirstFrame == nil || *a.FirstFrame != tt.firstFrame || a.LastFrame == nil || *a.LastFrame != tt.lastFrame {
				t.Errorf("a frames = %v..%v, want %d..%d", a.FirstFrame, a.LastFrame, tt.firstFrame, tt.lastFrame)
			}
			if a.FirstSeq != tt.firstSeq || a.LastSeq != tt.lastSeq {
				t.Errorf("a seqs = %d..%d, want %d..%d", a.FirstSeq, a.LastSeq, tt.firstSeq, tt.lastSeq)
			}
			if !reflect.DeepEqual(res.ByOffset[0], a) {
				t.Errorf("ByOffset[0] = %+v, want %+v", res.ByOffset[0], a)
			}
		})
	}
}

func TestQueryCombinedSeqs(t *testing.T) {
	// Two instructions at one address (different contexts). Within [2,4) the
	// first execution of "x" is inside the interval but "y" began earlier, so
	// the combined first seq is unknown.
	idx := newTestIndex(t,
		Event{Seq: 1, Frame: 1, Address: 0x8000, InstructionID: "y"},
		Event{Seq: 2, Frame: 2, Address: 0x8000, InstructionID: "x"},
		Event{Seq: 3, Frame: 3, Address: 0x8000, InstructionID: "y"},
	)
	start, end := uint64(2), uint64(4)
	res, err := idx.Query(Filter{FrameStart: &start, FrameEnd: &end})
	if err != nil {
		t.Fatal(err)
	}
	got := res.ByAddress[0x8000]
	if got.Hits != "2" || got.FirstSeq != 0 || got.LastSeq != 3 || *got.FirstFrame != 2 || *got.LastFrame != 3 {
		t.Errorf("ByAddress[$8000] = %+v (frames %d..%d), want hits 2, seqs 0..3, frames 2..3",
			got, *got.FirstFrame, *got.LastFrame)
	}
}

func TestAddRun(t *testing.T) {
	b := NewBuilder()
	for _, e := range []Event{romEvent(1, 7, 0, "a"), romEvent(2, 9, 0, "a"), romEvent(3, 12, 4, "b")} {
		if err := b.Add(e); err != nil {
			t.Fatalf("Builder.Add: %v", err)
		}
	}
	idx := NewIndex("test-rom")
	info := RunInfo{ID: "run1", StreamSHA: "stream1", Outcome: "complete", IsComplete: true}
	if err := idx.AddRun(info, b.Sites("run1")); err != nil {
		t.Fatalf("AddRun: %v", err)
	}

	run := idx.Runs["run1"]
	if run.EventCount != 3 || run.MinFrame != 7 || run.MaxFrame != 12 {
		t.Errorf("run = %+v, want EventCount 3, MinFrame 7, MaxFrame 12", run)
	}

	// Re-importing the same run replaces it rather than double-counting.
	if err := idx.AddRun(info, b.Sites("run1")); err != nil {
		t.Fatalf("AddRun re-import: %v", err)
	}
	res, err := idx.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalHits != "3" || len(idx.Sites) != 2 || !reflect.DeepEqual(idx.DefaultRuns, []string{"run1"}) {
		t.Errorf("after re-import: TotalHits %s, %d sites, DefaultRuns %v; want 3, 2, [run1]",
			res.TotalHits, len(idx.Sites), idx.DefaultRuns)
	}

	// A different run adds to the totals.
	if err := idx.AddRun(RunInfo{ID: "run2", StreamSHA: "stream2", IsComplete: true}, b.Sites("run2")); err != nil {
		t.Fatalf("AddRun run2: %v", err)
	}
	if res, _ := idx.Query(Filter{}); res.TotalHits != "6" {
		t.Errorf("with two runs: TotalHits %s, want 6", res.TotalHits)
	}
	if res, _ := idx.Query(Filter{RunIDs: []string{"run2"}}); res.TotalHits != "3" {
		t.Errorf("run2 only: TotalHits %s, want 3", res.TotalHits)
	}
}

func TestCoverage_BinAggregation(t *testing.T) {
	// Bin 0: offset 10 (3 hits), offset 20 (2 hits) -> bin total = 5 hits, hottest start = 10 (3 hits)
	// Bin 1: offset 150 (4 hits) -> bin total = 4 hits, hottest start = 150 (4 hits)
	var events []Event
	for seq := uint64(1); seq <= 3; seq++ {
		events = append(events, romEvent(seq, 0, 10, "i10"))
	}
	for seq := uint64(4); seq <= 5; seq++ {
		events = append(events, romEvent(seq, 0, 20, "i20"))
	}
	for seq := uint64(6); seq <= 9; seq++ {
		events = append(events, romEvent(seq, 0, 150, "i150"))
	}
	idx := newTestIndex(t, events...)

	// Bin size = 100 bytes, total ROM size = 300 bytes -> 3 bins
	bins, err := idx.QueryBins(Filter{}, 100, 300)
	if err != nil {
		t.Fatalf("QueryBins failed: %v", err)
	}
	if len(bins) != 3 {
		t.Fatalf("expected 3 bins, got %d", len(bins))
	}
	if bins[0].TotalHits != "5" || bins[0].HottestStart != 10 || bins[0].MaxStartHits != "3" {
		t.Errorf("bin 0 = %+v, want total 5, hottest 10 with 3", bins[0])
	}
	if bins[1].TotalHits != "4" || bins[1].HottestStart != 150 {
		t.Errorf("bin 1 = %+v, want total 4, hottest 150", bins[1])
	}
	if bins[2].TotalHits != "0" {
		t.Errorf("bin 2 total hits = %s, want 0", bins[2].TotalHits)
	}
}

func TestEncodeDecode(t *testing.T) {
	idx := newTestIndex(t, romEvent(1, 1, 0, "a"), romEvent(2, 3, 0, "a"))

	var buf bytes.Buffer
	if err := idx.Encode(&buf); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	decoded, err := Decode(&buf)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if !reflect.DeepEqual(decoded, idx) {
		t.Errorf("decoded = %+v, want %+v", decoded, idx)
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"per-execution format", `{"rom_hash":"x","default_runs":[],"runs":{},"events":[{"run_id":"r","seq":1}]}`, "re-run import"},
		{"missing format", `{"rom_hash":"x","sites":[]}`, "re-run import"},
		{"old schema", `{"format":"snes-coverage","schema":1,"sites":[]}`, "re-run import"},
		{"not an object", `[]`, "not a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Decode error = %v, want containing %q", err, tt.want)
			}
		})
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

func TestBuilder_SequenceValidationAndDeduplication(t *testing.T) {
	b := NewBuilder()

	// 1. Initial execution at seq 0 (seq=0 is explicitly valid and tracked)
	e0 := romEvent(0, 1, 0, "instA")
	if err := b.Add(e0); err != nil {
		t.Fatalf("first Add at seq 0: %v", err)
	}

	// 2. Monotonic next execution at seq 1
	e1 := romEvent(1, 1, 0, "instA")
	if err := b.Add(e1); err != nil {
		t.Fatalf("second Add: %v", err)
	}

	// 3. Truly identical duplicate at seq 1 is deduplicated idempotently
	if err := b.Add(e1); err != nil {
		t.Fatalf("duplicate Add: %v", err)
	}
	sites := b.Sites("test")
	if len(sites) != 1 || sites[0].Hits != 2 {
		t.Fatalf("expected 2 hits after identical duplicate, got %d", sites[0].Hits)
	}

	// 4. Conflicting duplicate at same sequence with changed frame is rejected
	e1DiffFrame := romEvent(1, 2, 0, "instA")
	if err := b.Add(e1DiffFrame); err == nil {
		t.Errorf("expected error for same seq with changed frame, got nil")
	}

	// 5. Conflicting duplicate at same sequence with changed instruction is rejected
	e1DiffInsn := romEvent(1, 1, 2, "instB")
	if err := b.Add(e1DiffInsn); err == nil {
		t.Errorf("expected error for conflicting duplicate sequence, got nil")
	}

	// 6. Conflicting duplicate at same sequence with changed context is rejected
	e1DiffCtx := romEvent(1, 1, 0, "instA")
	e1DiffCtx.Context.M = "set"
	if err := b.Add(e1DiffCtx); err == nil {
		t.Errorf("expected error for changed context at same seq, got nil")
	}

	// 7. Decreasing sequence is rejected
	if err := b.Add(romEvent(0, 1, 0, "instA")); err == nil {
		t.Errorf("expected error for decreasing sequence, got nil")
	}
}

func TestIndex_SupportedIdentityAndShardPolicy(t *testing.T) {
	idx := NewIndex("test-rom")

	// Capture 1: seq 1..10, frames 0..2
	b1 := NewBuilder()
	if err := b1.Add(romEvent(1, 0, 0, "instA")); err != nil {
		t.Fatal(err)
	}
	if err := b1.Add(romEvent(10, 2, 0, "instA")); err != nil {
		t.Fatal(err)
	}
	info1 := RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-sha-1",
		Outcome:    "complete",
		IsComplete: true,
	}

	// 0. Empty StreamSHA or ID must be rejected on new run
	if err := idx.AddRun(RunInfo{ID: "run-no-sha"}, b1.Sites("run-no-sha")); err == nil {
		t.Errorf("expected error for empty StreamSHA, got nil")
	}
	if err := idx.AddRun(RunInfo{StreamSHA: "sha"}, b1.Sites("run-no-id")); err == nil {
		t.Errorf("expected error for empty ID, got nil")
	}

	if err := idx.AddRun(info1, b1.Sites("run-1")); err != nil {
		t.Fatalf("AddRun: %v", err)
	}

	// 1. Identical canonical capture replaces idempotently without double-counting
	if err := idx.AddRun(info1, b1.Sites("run-1")); err != nil {
		t.Fatalf("AddRun identical re-import: %v", err)
	}
	run := idx.Runs["run-1"]
	if run.EventCount != 2 {
		t.Errorf("EventCount after re-import = %d, want 2", run.EventCount)
	}

	// 2. Shard with separated extents (seq 20..30) for same run ID must be rejected
	b2 := NewBuilder()
	if err := b2.Add(romEvent(20, 3, 0, "instA")); err != nil {
		t.Fatal(err)
	}
	infoSeparated := RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-sha-1",
		Outcome:    "complete",
		IsComplete: true,
	}
	if err := idx.AddRun(infoSeparated, b2.Sites("run-1")); err == nil {
		t.Errorf("expected error for separated shard extents, got nil")
	}

	// 3. Shard with overlapping extents (seq 5..15) for same run ID must be rejected
	b3 := NewBuilder()
	if err := b3.Add(romEvent(5, 1, 0, "instA")); err != nil {
		t.Fatal(err)
	}
	infoOverlap := RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-sha-1",
		Outcome:    "complete",
		IsComplete: true,
	}
	if err := idx.AddRun(infoOverlap, b3.Sites("run-1")); err == nil {
		t.Errorf("expected error for overlapping shard, got nil")
	}

	// 4. Incomplete incoming capture for complete existing run must be rejected
	infoIncomplete := info1
	infoIncomplete.IsComplete = false
	infoIncomplete.Outcome = "truncated"
	if err := idx.AddRun(infoIncomplete, b1.Sites("run-1")); err == nil {
		t.Errorf("expected error for conflicting incomplete re-import, got nil")
	}

	// 5. Mismatched StreamSHA on re-import must be rejected
	infoDiffSHA := info1
	infoDiffSHA.StreamSHA = "different-stream-sha"
	if err := idx.AddRun(infoDiffSHA, b1.Sites("run-1")); err == nil {
		t.Errorf("expected error for re-import with mismatched StreamSHA, got nil")
	}

	// 6. Existing nonempty sites to incoming empty sites must be rejected
	if err := idx.AddRun(info1, nil); err == nil {
		t.Errorf("expected error for existing nonempty sites with incoming empty sites, got nil")
	}

	// 7. Existing empty sites to incoming nonempty sites must be rejected
	idxEmpty := NewIndex("test-rom")
	infoEmpty := RunInfo{
		ID:         "run-empty",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-empty",
		Outcome:    "complete",
		IsComplete: true,
	}
	if err := idxEmpty.AddRun(infoEmpty, nil); err != nil {
		t.Fatalf("AddRun empty sites: %v", err)
	}
	// Re-importing identical empty run is accepted
	if err := idxEmpty.AddRun(infoEmpty, nil); err != nil {
		t.Fatalf("AddRun identical empty re-import: %v", err)
	}
	// Re-importing with nonempty sites must be rejected
	if err := idxEmpty.AddRun(infoEmpty, b1.Sites("run-empty")); err == nil {
		t.Errorf("expected error for existing empty sites with incoming nonempty sites, got nil")
	}
}

func TestBuilder_NoMutationOnRejectedInput(t *testing.T) {
	b := NewBuilder()
	e1 := romEvent(10, 1, 0, "instA")
	if err := b.Add(e1); err != nil {
		t.Fatal(err)
	}

	s := b.sites["instA"]
	if s.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", s.Hits)
	}

	// Reject conflicting duplicate (changed frame): verify no mutation
	eConf := romEvent(10, 2, 0, "instA")
	if err := b.Add(eConf); err == nil {
		t.Fatal("expected error on conflicting duplicate")
	}
	if s.Hits != 1 || len(s.Frames) != 1 || b.lastSeq != 10 {
		t.Errorf("Builder was mutated on conflicting duplicate: hits=%d, frames=%v, lastSeq=%d",
			s.Hits, s.Frames, b.lastSeq)
	}

	// Reject decreasing sequence: verify no mutation
	eDec := romEvent(5, 1, 0, "instA")
	if err := b.Add(eDec); err == nil {
		t.Fatal("expected error on decreasing sequence")
	}
	if s.Hits != 1 || b.lastSeq != 10 {
		t.Errorf("Builder was mutated on decreasing sequence: hits=%d, lastSeq=%d", s.Hits, b.lastSeq)
	}

	// Frame hit overflow: simulate existing frame at MaxUint64
	s.Hits = 100
	s.FrameHits[0] = math.MaxUint64
	eOverflow := romEvent(11, 1, 0, "instA")
	if err := b.Add(eOverflow); err == nil {
		t.Fatal("expected error on frameHits overflow")
	}
	if s.Hits != 100 || b.lastSeq != 10 {
		t.Errorf("Builder was mutated on frameHits overflow: hits=%d, lastSeq=%d", s.Hits, b.lastSeq)
	}
}

func TestIndex_NoMutationOnRejectedInput(t *testing.T) {
	// Zero-value Index test: rejected AddRun must leave Index completely zero-value (Runs is nil, Sites is nil)
	var zeroIdx Index
	if err := zeroIdx.AddRun(RunInfo{ID: "no-sha"}, nil); err == nil {
		t.Fatal("expected error for missing stream SHA")
	}
	if zeroIdx.Runs != nil || zeroIdx.Sites != nil || zeroIdx.DefaultRuns != nil {
		t.Errorf("zero-value Index was mutated on rejected input: runs=%v, sites=%v, defaultRuns=%v",
			zeroIdx.Runs, zeroIdx.Sites, zeroIdx.DefaultRuns)
	}

	idx := NewIndex("test-rom")
	b := NewBuilder()
	if err := b.Add(romEvent(1, 0, 0, "instA")); err != nil {
		t.Fatal(err)
	}
	info := RunInfo{
		ID:         "run-1",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-1",
		Outcome:    "complete",
		IsComplete: true,
	}
	if err := idx.AddRun(info, b.Sites("run-1")); err != nil {
		t.Fatal(err)
	}

	origSitesCount := len(idx.Sites)
	origRunsCount := len(idx.Runs)

	// 1. Attempt AddRun with event count overflow on incoming sites
	overflowSites := []Site{
		{
			InstructionID: "instB",
			Hits:          math.MaxUint64,
			FirstSeq:      100,
			LastSeq:       100,
		},
		{
			InstructionID: "instC",
			Hits:          1,
			FirstSeq:      101,
			LastSeq:       101,
		},
	}
	if err := idx.AddRun(RunInfo{ID: "run-overflow", StreamSHA: "stream-ovf"}, overflowSites); err == nil {
		t.Fatal("expected error on event count overflow")
	}
	if len(idx.Sites) != origSitesCount || len(idx.Runs) != origRunsCount {
		t.Errorf("Index was mutated on event count overflow error: sites=%d, runs=%d",
			len(idx.Sites), len(idx.Runs))
	}

	// 2. Attempt unsupported shard merge
	separatedSites := []Site{
		{
			InstructionID: "instA",
			Hits:          1,
			FirstSeq:      20,
			LastSeq:       20,
		},
	}
	if err := idx.AddRun(RunInfo{ID: "run-1", StreamSHA: "stream-1"}, separatedSites); err == nil {
		t.Fatal("expected error on unsupported shard merge")
	}
	if len(idx.Sites) != origSitesCount || len(idx.Runs) != origRunsCount {
		t.Errorf("Index was mutated on unsupported shard merge error: sites=%d, runs=%d",
			len(idx.Sites), len(idx.Runs))
	}
}

func TestIndex_GapQualityFiltered(t *testing.T) {
	idx := NewIndex("test-rom")
	b := NewBuilder()
	if err := b.Add(romEvent(1, 0, 0, "a")); err != nil {
		t.Fatal(err)
	}
	info := RunInfo{
		ID:         "run-gap",
		ROM_SHA256: "test-rom",
		StreamSHA:  "stream-gap",
		Outcome:    "complete",
		IsComplete: true, // even if marked complete by producer, gaps must demote to filtered
		Gaps: []Gap{
			{FirstSeq: 10, LastSeq: 20, Reason: "filter"},
		},
	}
	if err := idx.AddRun(info, b.Sites("run-gap")); err != nil {
		t.Fatalf("AddRun: %v", err)
	}

	res, err := idx.Query(Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Quality != QualityFiltered {
		t.Errorf("Query quality with gaps = %s, want %s", res.Quality, QualityFiltered)
	}
	if len(res.Limitations) == 0 || !strings.Contains(res.Limitations[0], "filtered gaps") {
		t.Errorf("expected limitations mentioning filtered gaps, got %v", res.Limitations)
	}
}

func TestBuilder_HitsOverflow(t *testing.T) {
	b := NewBuilder()
	s := &Site{
		InstructionID: "instA",
		Hits:          math.MaxUint64,
		Frames:        []uint64{1},
		FrameHits:     []uint64{math.MaxUint64},
	}
	b.sites["instA"] = s
	if err := b.Add(romEvent(1, 1, 0, "instA")); err == nil {
		t.Errorf("expected overflow error on Builder.Add, got nil")
	}
}

