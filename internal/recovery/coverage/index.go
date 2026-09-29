package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
)

// Format and SchemaVersion identify the on-disk coverage index format.
// Decode rejects files with any other format or schema.
const (
	Format        = "snes-coverage"
	SchemaVersion = 2
)

// Index holds aggregated execution sites across runs.
type Index struct {
	Format      string             `json:"format"`
	Schema      int                `json:"schema"`
	ROMHash     string             `json:"rom_hash"`
	DefaultRuns []string           `json:"default_runs"`
	Runs        map[string]RunInfo `json:"runs"`
	Sites       []Site             `json:"sites"`
}

// NewIndex returns a new empty Index for the ROM with the given hash.
func NewIndex(romHash string) *Index {
	return &Index{
		Format:      Format,
		Schema:      SchemaVersion,
		ROMHash:     romHash,
		DefaultRuns: []string{},
		Runs:        make(map[string]RunInfo),
		Sites:       []Site{},
	}
}

// AddRun records a run and its aggregated sites.
// If info.ID is already present and matches the exact same verified capture
// (sequence range, event count, frames, and outcome), it replaces that run's
// sites idempotently. Shard merging and conflicting re-imports are unsupported
// and return an error without modifying the index.
// AddRun sets the RunID of each site to info.ID and derives info.EventCount,
// info.MinFrame, and info.MaxFrame from sites.
func (idx *Index) AddRun(info RunInfo, sites []Site) error {
	if info.ID == "" {
		return errors.New("coverage: run ID is required")
	}
	if info.StreamSHA == "" {
		return errors.New("coverage: run stream SHA is required")
	}

	// 1. Calculate incoming bounds and event totals with overflow checking first (zero mutation on error).
	var inMinSeq, inMaxSeq uint64
	var minFrame, maxFrame uint64
	var eventCount uint64
	for i, s := range sites {
		if i == 0 || s.FirstSeq < inMinSeq {
			inMinSeq = s.FirstSeq
		}
		if s.LastSeq > inMaxSeq {
			inMaxSeq = s.LastSeq
		}
		if i == 0 || s.FirstFrame < minFrame {
			minFrame = s.FirstFrame
		}
		maxFrame = max(maxFrame, s.LastFrame)
		sum, ok := checkedAdd(eventCount, s.Hits)
		if !ok {
			return errors.New("coverage: run event count overflow")
		}
		eventCount = sum
	}
	info.EventCount = eventCount
	info.MinFrame = minFrame
	info.MaxFrame = maxFrame

	// 2. Validate against existing run if present.
	if idx.Runs != nil {
		if existing, ok := idx.Runs[info.ID]; ok {
			if existing.StreamSHA == "" || existing.StreamSHA != info.StreamSHA {
				return fmt.Errorf("coverage: shard merging is unsupported: run %q already exists (stream SHA mismatch or missing)", info.ID)
			}

			var existMinSeq, existMaxSeq uint64
			var existSiteCount int
			for _, s := range idx.Sites {
				if s.RunID == info.ID {
					if existSiteCount == 0 || s.FirstSeq < existMinSeq {
						existMinSeq = s.FirstSeq
					}
					if s.LastSeq > existMaxSeq {
						existMaxSeq = s.LastSeq
					}
					existSiteCount++
				}
			}

			if existSiteCount == 0 && len(sites) > 0 {
				return fmt.Errorf("coverage: shard merging is unsupported: run %q already exists (empty existing sites, nonempty incoming sites)", info.ID)
			}
			if existSiteCount > 0 && len(sites) == 0 {
				return fmt.Errorf("coverage: shard merging is unsupported: run %q already exists (nonempty existing sites, empty incoming sites)", info.ID)
			}

			var isIdentical bool
			if existSiteCount == 0 && len(sites) == 0 {
				isIdentical = (info.EventCount == 0 && existing.EventCount == 0 &&
					info.MinFrame == existing.MinFrame &&
					info.MaxFrame == existing.MaxFrame &&
					info.IsComplete == existing.IsComplete &&
					info.Outcome == existing.Outcome &&
					info.StreamSHA == existing.StreamSHA)
			} else {
				isIdentical = (inMinSeq == existMinSeq &&
					inMaxSeq == existMaxSeq &&
					info.EventCount == existing.EventCount &&
					info.MinFrame == existing.MinFrame &&
					info.MaxFrame == existing.MaxFrame &&
					info.IsComplete == existing.IsComplete &&
					info.Outcome == existing.Outcome &&
					info.StreamSHA == existing.StreamSHA)
			}

			if isIdentical {
				// Verified identical canonical capture: replace sites safely into a new slice.
				newSites := make([]Site, 0, len(idx.Sites))
				for _, s := range idx.Sites {
					if s.RunID != info.ID {
						newSites = append(newSites, s)
					}
				}
				for _, s := range sites {
					s.RunID = info.ID
					newSites = append(newSites, s)
				}
				newRuns := make(map[string]RunInfo, len(idx.Runs))
				for k, v := range idx.Runs {
					newRuns[k] = v
				}
				newRuns[info.ID] = info

				idx.Sites = newSites
				idx.Runs = newRuns
				return nil
			}

			// Partial shards, disjoint intervals, or conflicting captures are unsupported.
			return fmt.Errorf("coverage: shard merging is unsupported: run %q already exists (existing seq [%d, %d], incoming seq [%d, %d])",
				info.ID, existMinSeq, existMaxSeq, inMinSeq, inMaxSeq)
		}
	}

	// 3. New run: build new sites slice and publish only after success.
	newSites := make([]Site, len(idx.Sites), len(idx.Sites)+len(sites))
	copy(newSites, idx.Sites)
	for _, s := range sites {
		s.RunID = info.ID
		newSites = append(newSites, s)
	}

	var newRuns map[string]RunInfo
	if idx.Runs == nil {
		newRuns = make(map[string]RunInfo, 1)
	} else {
		newRuns = make(map[string]RunInfo, len(idx.Runs)+1)
		for k, v := range idx.Runs {
			newRuns[k] = v
		}
	}
	newRuns[info.ID] = info

	var newDefaultRuns []string
	if !slices.Contains(idx.DefaultRuns, info.ID) {
		newDefaultRuns = make([]string, len(idx.DefaultRuns), len(idx.DefaultRuns)+1)
		copy(newDefaultRuns, idx.DefaultRuns)
		newDefaultRuns = append(newDefaultRuns, info.ID)
		sort.Strings(newDefaultRuns)
	} else {
		newDefaultRuns = idx.DefaultRuns
	}

	idx.Sites = newSites
	idx.Runs = newRuns
	idx.DefaultRuns = newDefaultRuns
	return nil
}

// A Builder aggregates the executions of a single run into sites.
// Its memory use is proportional to the number of distinct instructions and
// the frames in which each executed, not to the number of executions.
type Builder struct {
	sites     map[string]*Site
	hasSeq    bool
	lastSeq   uint64
	lastEvent Event
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{sites: make(map[string]*Site)}
}

// Add records one execution. Executions are grouped into sites by
// InstructionID; the first execution of a site supplies its address, offset,
// and context. Sequence numbers are strictly monotonic; sequence 0 is a valid
// sequence number and is validated like any other.
// Duplicate sequence numbers with identical full event payload are deduplicated idempotently.
// Decreasing sequence numbers or conflicting duplicate sequence numbers return an error
// with zero mutation to builder state.
func (b *Builder) Add(e Event) error {
	if b.hasSeq {
		if e.Seq < b.lastSeq {
			return fmt.Errorf("coverage: non-monotonic sequence: seq %d < previous %d", e.Seq, b.lastSeq)
		}
		if e.Seq == b.lastSeq {
			// Compare complete event payload for identical duplicate vs conflicting event.
			if e.Frame != b.lastEvent.Frame ||
				e.Address != b.lastEvent.Address ||
				e.Offset != b.lastEvent.Offset ||
				e.HasROMOffset != b.lastEvent.HasROMOffset ||
				e.InstructionID != b.lastEvent.InstructionID ||
				e.Context != b.lastEvent.Context {
				return fmt.Errorf("coverage: conflicting duplicate sequence %d: event payload changed", e.Seq)
			}
			return nil // Truly identical duplicate: deduplicate idempotently without mutation.
		}
	}

	// Validate calculations and overflow BEFORE mutating site or builder state.
	s := b.sites[e.InstructionID]
	var newHits uint64
	var frameIdx int = -1
	var needInsert bool
	var newFrameHit uint64

	if s != nil {
		h, ok := checkedAdd(s.Hits, 1)
		if !ok {
			return errors.New("coverage: hit counter overflow")
		}
		newHits = h

		n := len(s.Frames)
		switch {
		case n > 0 && s.Frames[n-1] == e.Frame:
			frameIdx = n - 1
			fh, ok := checkedAdd(s.FrameHits[frameIdx], 1)
			if !ok {
				return errors.New("coverage: frame hit counter overflow")
			}
			newFrameHit = fh
		case n == 0 || s.Frames[n-1] < e.Frame:
			// Append 1, cannot overflow
		default:
			i, found := slices.BinarySearch(s.Frames, e.Frame)
			if found {
				frameIdx = i
				fh, ok := checkedAdd(s.FrameHits[frameIdx], 1)
				if !ok {
					return errors.New("coverage: frame hit counter overflow")
				}
				newFrameHit = fh
			} else {
				needInsert = true
			}
		}
	}

	// All validations passed. Apply mutation now.
	if s == nil {
		s = &Site{
			InstructionID: e.InstructionID,
			Address:       e.Address,
			Offset:        e.Offset,
			HasROMOffset:  e.HasROMOffset,
			Context:       e.Context,
			Hits:          1,
			FirstSeq:      e.Seq,
			LastSeq:       e.Seq,
			FirstFrame:    e.Frame,
			LastFrame:     e.Frame,
			Frames:        []uint64{e.Frame},
			FrameHits:     []uint64{1},
		}
		b.sites[e.InstructionID] = s
	} else {
		s.Hits = newHits
		s.FirstSeq = min(s.FirstSeq, e.Seq)
		s.LastSeq = max(s.LastSeq, e.Seq)
		s.FirstFrame = min(s.FirstFrame, e.Frame)
		s.LastFrame = max(s.LastFrame, e.Frame)
		if frameIdx >= 0 {
			s.FrameHits[frameIdx] = newFrameHit
		} else if needInsert {
			i, _ := slices.BinarySearch(s.Frames, e.Frame)
			s.Frames = slices.Insert(s.Frames, i, e.Frame)
			s.FrameHits = slices.Insert(s.FrameHits, i, 1)
		} else {
			s.Frames = append(s.Frames, e.Frame)
			s.FrameHits = append(s.FrameHits, 1)
		}
	}

	b.hasSeq = true
	b.lastSeq = e.Seq
	b.lastEvent = e
	return nil
}

// Sites returns the aggregated sites with RunID set to runID,
// ordered by ROM offset, address, and instruction ID.
func (b *Builder) Sites(runID string) []Site {
	sites := make([]Site, 0, len(b.sites))
	for _, s := range b.sites {
		site := *s
		site.RunID = runID
		sites = append(sites, site)
	}
	sort.Slice(sites, func(i, j int) bool {
		a, b := sites[i], sites[j]
		if a.Offset != b.Offset {
			return a.Offset < b.Offset
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.InstructionID < b.InstructionID
	})
	return sites
}

// Encode writes the index to w as compact JSON.
func (idx *Index) Encode(w io.Writer) error {
	if w == nil {
		return errors.New("coverage: writer is nil")
	}
	return json.NewEncoder(w).Encode(idx)
}

// Decode reads a coverage Index from r.
// It rejects indexes written in another format or schema, including the
// older per-execution format, without reading their execution records.
func Decode(r io.Reader) (*Index, error) {
	if r == nil {
		return nil, errors.New("coverage: reader is nil")
	}
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("coverage: decode index: %w", err)
	} else if t != json.Delim('{') {
		return nil, errors.New("coverage: decode index: not a JSON object")
	}
	var idx Index
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("coverage: decode index: %w", err)
		}
		var v any
		switch key, _ := t.(string); key {
		case "format":
			v = &idx.Format
		case "schema":
			v = &idx.Schema
		case "rom_hash":
			v = &idx.ROMHash
		case "default_runs":
			v = &idx.DefaultRuns
		case "runs":
			v = &idx.Runs
		case "sites":
			v = &idx.Sites
		case "events":
			return nil, errors.New("coverage: index uses the old per-execution format; re-run import")
		default:
			v = new(json.RawMessage)
		}
		if err := dec.Decode(v); err != nil {
			return nil, fmt.Errorf("coverage: decode index: %w", err)
		}
	}
	if idx.Format != Format || idx.Schema != SchemaVersion {
		return nil, fmt.Errorf("coverage: unsupported index format %q schema %d (want %q schema %d); re-run import",
			idx.Format, idx.Schema, Format, SchemaVersion)
	}
	if idx.Runs == nil {
		idx.Runs = make(map[string]RunInfo)
	}
	return &idx, nil
}

// siteCount is a site's executions within a frame interval.
type siteCount struct {
	hits                  uint64
	firstFrame, lastFrame uint64
	firstSeq, lastSeq     uint64
	firstSeqOK, lastSeqOK bool
}

// count returns the executions of s in the frame interval [start, end).
// A nil end means no upper bound.
func (s *Site) count(start uint64, end *uint64) siteCount {
	below := func(f uint64) bool { return end == nil || f < *end }
	c := siteCount{
		firstSeq:   s.FirstSeq,
		lastSeq:    s.LastSeq,
		firstSeqOK: s.FirstFrame >= start && below(s.FirstFrame),
		lastSeqOK:  s.LastFrame >= start && below(s.LastFrame),
	}
	if c.firstSeqOK && c.lastSeqOK {
		c.hits, c.firstFrame, c.lastFrame = s.Hits, s.FirstFrame, s.LastFrame
		return c
	}
	i, _ := slices.BinarySearch(s.Frames, start)
	for ; i < len(s.Frames) && below(s.Frames[i]); i++ {
		if c.hits == 0 {
			c.firstFrame = s.Frames[i]
		}
		c.lastFrame = s.Frames[i]
		c.hits += s.FrameHits[i]
	}
	return c
}

// summary accumulates site counts for one offset, address, or instruction.
type summary struct {
	siteCount
	firstSeqUnknown, lastSeqUnknown bool
}

func (a *summary) add(c siteCount) bool {
	if a.hits == 0 {
		a.firstFrame, a.lastFrame = c.firstFrame, c.lastFrame
		a.firstSeq, a.lastSeq = c.firstSeq, c.lastSeq
	} else {
		a.firstFrame = min(a.firstFrame, c.firstFrame)
		a.lastFrame = max(a.lastFrame, c.lastFrame)
		a.firstSeq = min(a.firstSeq, c.firstSeq)
		a.lastSeq = max(a.lastSeq, c.lastSeq)
	}
	a.firstSeqUnknown = a.firstSeqUnknown || !c.firstSeqOK
	a.lastSeqUnknown = a.lastSeqUnknown || !c.lastSeqOK
	var ok bool
	a.hits, ok = checkedAdd(a.hits, c.hits)
	return ok
}

func (a *summary) countSummary(q Quality) CountSummary {
	cs := CountSummary{
		Hits:       strconv.FormatUint(a.hits, 10),
		Quality:    q,
		FirstFrame: &a.firstFrame,
		LastFrame:  &a.lastFrame,
	}
	if !a.firstSeqUnknown {
		cs.FirstSeq = a.firstSeq
	}
	if !a.lastSeqUnknown {
		cs.LastSeq = a.lastSeq
	}
	return cs
}

// lookup returns m[k], allocating it if needed.
func lookup[K comparable](m map[K]*summary, k K) *summary {
	s := m[k]
	if s == nil {
		s = new(summary)
		m[k] = s
	}
	return s
}

// Query evaluates coverage filters and aggregates hit counts.
// Sites with no executions in the filter's frame interval are omitted from
// the result's ByOffset, ByAddress, and ByInstruction maps.
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

	var start uint64
	if f.FrameStart != nil {
		start = *f.FrameStart
	}

	var total uint64
	byOffset := make(map[uint32]*summary)
	byAddr := make(map[uint32]*summary)
	byInsn := make(map[string]*summary)
	for i := range idx.Sites {
		s := &idx.Sites[i]
		if !activeRuns[s.RunID] {
			continue
		}
		if f.Address != nil && s.Address != *f.Address {
			continue
		}
		if f.Offset != nil && (!s.HasROMOffset || s.Offset != *f.Offset) {
			continue
		}
		if f.InstructionID != "" && s.InstructionID != f.InstructionID {
			continue
		}
		c := s.count(start, f.FrameEnd)
		if c.hits == 0 {
			continue
		}
		var ok bool
		if total, ok = checkedAdd(total, c.hits); !ok {
			return nil, errors.New("coverage: counter overflow")
		}
		sums := []*summary{lookup(byAddr, s.Address), lookup(byInsn, s.InstructionID)}
		if s.HasROMOffset {
			sums = append(sums, lookup(byOffset, s.Offset))
		}
		for _, sum := range sums {
			if !sum.add(c) {
				return nil, errors.New("coverage: counter overflow")
			}
		}
	}

	res := &CoverageResult{
		ProjectROMHash: idx.ROMHash,
		SelectedRuns:   targetRuns,
		FrameInterval:  frameIntervalStr,
		TotalHits:      strconv.FormatUint(total, 10),
		Quality:        quality,
		ByOffset:       make(map[uint32]CountSummary, len(byOffset)),
		ByAddress:      make(map[uint32]CountSummary, len(byAddr)),
		ByInstruction:  make(map[string]CountSummary, len(byInsn)),
		Limitations:    limitations,
	}
	for k, s := range byOffset {
		res.ByOffset[k] = s.countSummary(quality)
	}
	for k, s := range byAddr {
		res.ByAddress[k] = s.countSummary(quality)
	}
	for k, s := range byInsn {
		res.ByInstruction[k] = s.countSummary(quality)
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
