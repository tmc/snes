package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
)

type runSeqKey struct {
	runID string
	seq   uint64
}

// Index tracks and indexes execution events across runs.
type Index struct {
	ROMHash     string             `json:"rom_hash"`
	DefaultRuns []string           `json:"default_runs"`
	Runs        map[string]RunInfo `json:"runs"`
	Events      []Event            `json:"events"`

	seen map[runSeqKey]struct{}
}

// NewIndex returns a new initialized Index.
func NewIndex(romHash string) *Index {
	return &Index{
		ROMHash:     romHash,
		DefaultRuns: []string{},
		Runs:        make(map[string]RunInfo),
		Events:      make([]Event, 0),
		seen:        make(map[runSeqKey]struct{}),
	}
}

// AddRun registers metadata for an execution run.
func (idx *Index) AddRun(info RunInfo) {
	if idx.Runs == nil {
		idx.Runs = make(map[string]RunInfo)
	}
	idx.Runs[info.ID] = info
	for _, r := range idx.DefaultRuns {
		if r == info.ID {
			return
		}
	}
	idx.DefaultRuns = append(idx.DefaultRuns, info.ID)
	sort.Strings(idx.DefaultRuns)
}

// AddEvent records a single execution event, deduplicating by run ID and sequence number.
// Returns true if the event was newly added, or false if already present.
func (idx *Index) AddEvent(e Event) bool {
	if idx.seen == nil {
		idx.seen = make(map[runSeqKey]struct{}, len(idx.Events))
		for _, ev := range idx.Events {
			idx.seen[runSeqKey{ev.RunID, ev.Seq}] = struct{}{}
		}
	}

	key := runSeqKey{e.RunID, e.Seq}
	if _, ok := idx.seen[key]; ok {
		return false
	}
	idx.seen[key] = struct{}{}
	idx.Events = append(idx.Events, e)
	return true
}

// AddEvents records multiple events idempotently. Returns number of newly added events.
func (idx *Index) AddEvents(events []Event) int {
	added := 0
	for _, e := range events {
		if idx.AddEvent(e) {
			added++
		}
	}
	return added
}

// Encode writes the index to w as JSON.
func (idx *Index) Encode(w io.Writer) error {
	if w == nil {
		return errors.New("coverage: writer is nil")
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(idx)
}

// Decode reads a coverage Index from r.
func Decode(r io.Reader) (*Index, error) {
	if r == nil {
		return nil, errors.New("coverage: reader is nil")
	}
	var idx Index
	dec := json.NewDecoder(r)
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("coverage: decode index: %w", err)
	}
	idx.seen = make(map[runSeqKey]struct{}, len(idx.Events))
	for _, ev := range idx.Events {
		idx.seen[runSeqKey{ev.RunID, ev.Seq}] = struct{}{}
	}
	if idx.Runs == nil {
		idx.Runs = make(map[string]RunInfo)
	}
	return &idx, nil
}

// Query evaluates coverage filters and aggregates hit counts.
func (idx *Index) Query(f Filter) (*CoverageResult, error) {
	targetRuns := f.RunIDs
	if len(targetRuns) == 0 {
		targetRuns = idx.DefaultRuns
		if len(targetRuns) == 0 {
			for r := range idx.Runs {
				targetRuns = append(targetRuns, r)
			}
			sort.Strings(targetRuns)
		}
	}

	activeRuns := make(map[string]bool)
	for _, r := range targetRuns {
		activeRuns[r] = true
	}

	var limitations []string
	quality := QualityComplete
	if len(targetRuns) == 0 {
		quality = QualityUnavailable
		limitations = append(limitations, "no execution runs selected or available")
	}

	for _, rID := range targetRuns {
		run, exists := idx.Runs[rID]
		if !exists {
			limitations = append(limitations, fmt.Sprintf("run %q not found in index", rID))
			quality = QualityUnavailable
			continue
		}
		if !run.IsComplete {
			quality = QualityTruncated
			limitations = append(limitations, fmt.Sprintf("run %q is incomplete (outcome: %s)", rID, run.Outcome))
		}
		if len(run.Gaps) > 0 {
			if quality != QualityTruncated {
				quality = QualityFiltered
			}
			limitations = append(limitations, fmt.Sprintf("run %q has %d filtered gaps", rID, len(run.Gaps)))
		}
	}

	frameIntervalStr := ""
	if f.FrameStart != nil && f.FrameEnd != nil {
		frameIntervalStr = fmt.Sprintf("[%d,%d)", *f.FrameStart, *f.FrameEnd)
	} else if f.FrameStart != nil {
		frameIntervalStr = fmt.Sprintf("[%d,inf)", *f.FrameStart)
	} else if f.FrameEnd != nil {
		frameIntervalStr = fmt.Sprintf("[0,%d)", *f.FrameEnd)
	}

	res := &CoverageResult{
		ProjectROMHash: idx.ROMHash,
		SelectedRuns:   targetRuns,
		FrameInterval:  frameIntervalStr,
		Quality:        quality,
		ByOffset:       make(map[uint32]CountSummary),
		ByAddress:      make(map[uint32]CountSummary),
		ByInstruction:  make(map[string]CountSummary),
		Limitations:    limitations,
	}

	var totalHits uint64
	offsetCounts := make(map[uint32]uint64)
	addrCounts := make(map[uint32]uint64)
	insnCounts := make(map[string]uint64)

	for _, ev := range idx.Events {
		if !activeRuns[ev.RunID] {
			continue
		}
		if f.FrameStart != nil && ev.Frame < *f.FrameStart {
			continue
		}
		if f.FrameEnd != nil && ev.Frame >= *f.FrameEnd {
			continue
		}
		if f.Address != nil && ev.Address != *f.Address {
			continue
		}
		if f.Offset != nil && (!ev.HasROMOffset || ev.Offset != *f.Offset) {
			continue
		}
		if f.InstructionID != "" && ev.InstructionID != f.InstructionID {
			continue
		}

		var ok bool
		totalHits, ok = checkedAdd(totalHits, 1)
		if !ok {
			return nil, errors.New("coverage: counter overflow")
		}

		if ev.HasROMOffset {
			offsetCounts[ev.Offset], _ = checkedAdd(offsetCounts[ev.Offset], 1)
		}
		addrCounts[ev.Address], _ = checkedAdd(addrCounts[ev.Address], 1)
		insnCounts[ev.InstructionID], _ = checkedAdd(insnCounts[ev.InstructionID], 1)
	}

	res.TotalHits = strconv.FormatUint(totalHits, 10)

	for off, count := range offsetCounts {
		res.ByOffset[off] = CountSummary{
			Hits:      strconv.FormatUint(count, 10),
			Quality:   quality,
			ExactZero: count == 0,
		}
	}
	for addr, count := range addrCounts {
		res.ByAddress[addr] = CountSummary{
			Hits:      strconv.FormatUint(count, 10),
			Quality:   quality,
			ExactZero: count == 0,
		}
	}
	for id, count := range insnCounts {
		res.ByInstruction[id] = CountSummary{
			Hits:      strconv.FormatUint(count, 10),
			Quality:   quality,
			ExactZero: count == 0,
		}
	}

	return res, nil
}

// QueryBins computes coarse zoom frequency bins over physical ROM offsets.
// A bin's frequency is the sum of hits at physical start offsets inside the bin.
func (idx *Index) QueryBins(f Filter, binSize uint32, totalROMSize uint32) ([]BinCoverage, error) {
	if binSize == 0 {
		return nil, errors.New("coverage: bin size must be greater than zero")
	}
	if totalROMSize == 0 {
		totalROMSize = 1024 * 1024 // default 1MB if unspecified
	}

	numBins := (totalROMSize + binSize - 1) / binSize
	bins := make([]BinCoverage, numBins)

	res, err := idx.Query(f)
	if err != nil {
		return nil, err
	}

	binHits := make([]uint64, numBins)
	hottestStart := make([]uint32, numBins)
	maxStartHits := make([]uint64, numBins)

	for off, summary := range res.ByOffset {
		hits, _ := strconv.ParseUint(summary.Hits, 10, 64)
		bIdx := off / binSize
		if bIdx < numBins {
			binHits[bIdx], _ = checkedAdd(binHits[bIdx], hits)
			if hits > maxStartHits[bIdx] {
				maxStartHits[bIdx] = hits
				hottestStart[bIdx] = off
			}
		}
	}

	for i := uint32(0); i < numBins; i++ {
		start := i * binSize
		sz := binSize
		if start+sz > totalROMSize {
			sz = totalROMSize - start
		}
		bins[i] = BinCoverage{
			BinIndex:     i,
			StartOffset:  start,
			Size:         sz,
			TotalHits:    strconv.FormatUint(binHits[i], 10),
			HottestStart: hottestStart[i],
			MaxStartHits: strconv.FormatUint(maxStartHits[i], 10),
			Quality:      res.Quality,
		}
	}

	return bins, nil
}

func checkedAdd(a, b uint64) (uint64, bool) {
	if math.MaxUint64-a < b {
		return 0, false
	}
	return a + b, true
}
