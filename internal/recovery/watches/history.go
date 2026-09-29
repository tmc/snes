package watches

import (
	"fmt"
	"sort"
)

// ChangeInterval represents an observed value change between two snapshot points.
type ChangeInterval struct {
	RunID         string        `json:"run_id"`
	PrevFrame     uint64        `json:"prev_frame"`
	CurrFrame     uint64        `json:"curr_frame"`
	PrevValue     Evaluation    `json:"prev_value"`
	CurrValue     Evaluation    `json:"curr_value"`
	IntervalLabel string        `json:"interval_label"` // e.g. "[10, 20)"
}

// BuildHistory evaluates a watch definition across a series of snapshots.
// Snapshots are deduplicated, grouped by RunID, and ordered chronologically.
func BuildHistory(def *WatchDefinition, snapshots []*Snapshot, allDefs *File) ([]HistoryEntry, error) {
	if def == nil {
		return nil, fmt.Errorf("watches: nil watch definition")
	}

	// 1. Deduplicate snapshots by (RunID, Sequence, Frame)
	seenSnap := make(map[string]bool)
	var deduped []*Snapshot

	for _, s := range snapshots {
		if s == nil {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", s.RunID, s.Sequence, s.Frame)
		if seenSnap[key] {
			continue
		}
		seenSnap[key] = true
		deduped = append(deduped, s)
	}

	// 2. Group by RunID
	runs := make(map[string][]*Snapshot)
	var runIDs []string
	for _, s := range deduped {
		if _, ok := runs[s.RunID]; !ok {
			runIDs = append(runIDs, s.RunID)
		}
		runs[s.RunID] = append(runs[s.RunID], s)
	}
	sort.Strings(runIDs)

	var allEntries []HistoryEntry

	for _, runID := range runIDs {
		runSnaps := runs[runID]
		sort.Slice(runSnaps, func(i, j int) bool {
			if runSnaps[i].Sequence != runSnaps[j].Sequence {
				return runSnaps[i].Sequence < runSnaps[j].Sequence
			}
			return runSnaps[i].Frame < runSnaps[j].Frame
		})

		var prevEval *Evaluation
		var prevSnap *Snapshot

		for i, snap := range runSnaps {
			var context map[string]*Evaluation
			if allDefs != nil {
				allEvals, err := EvaluateAll(allDefs, snap)
				if err == nil {
					context = make(map[string]*Evaluation)
					for k := range allEvals {
						v := allEvals[k]
						context[k] = &v
					}
				}
			}

			eval := Evaluate(def, snap, context)

			// Next frame boundary or self + 1 if last
			var nextFrame uint64
			var nextSeq uint64
			if i+1 < len(runSnaps) {
				nextFrame = runSnaps[i+1].Frame
				nextSeq = runSnaps[i+1].Sequence
			} else {
				nextFrame = snap.Frame + 1
				nextSeq = snap.Sequence + 1
			}

			entry := HistoryEntry{
				RunID:         runID,
				FrameStart:    snap.Frame,
				FrameEnd:      nextFrame,
				SequenceStart: snap.Sequence,
				SequenceEnd:   nextSeq,
				Evaluation:    eval,
			}

			if prevSnap != nil {
				// Detect gap in sequence
				if snap.Sequence > prevSnap.Sequence+1 {
					entry.Gap = true
				}
				// Detect value or validity change
				if eval.MaskedValue != prevEval.MaskedValue || eval.Validity != prevEval.Validity {
					entry.Changed = true
				}
			}

			allEntries = append(allEntries, entry)
			copyEval := eval
			prevEval = &copyEval
			prevSnap = snap
		}
	}

	return allEntries, nil
}

// CompressHistory merges consecutive history entries that have identical evaluation
// values and validities with no intervening gaps into a single entry with extended [FrameStart, FrameEnd).
func CompressHistory(entries []HistoryEntry) []HistoryEntry {
	if len(entries) == 0 {
		return nil
	}

	var compressed []HistoryEntry
	current := entries[0]

	for i := 1; i < len(entries); i++ {
		next := entries[i]

		canMerge := (next.RunID == current.RunID &&
			!next.Gap &&
			!next.Changed &&
			next.Evaluation.Validity == current.Evaluation.Validity &&
			next.Evaluation.MaskedValue == current.Evaluation.MaskedValue &&
			next.FrameStart == current.FrameEnd)

		if canMerge {
			current.FrameEnd = next.FrameEnd
			current.SequenceEnd = next.SequenceEnd
		} else {
			compressed = append(compressed, current)
			current = next
		}
	}
	compressed = append(compressed, current)
	return compressed
}

// ExtractChanges returns all intervals where a value change occurred.
func ExtractChanges(entries []HistoryEntry) []ChangeInterval {
	var changes []ChangeInterval
	for i := 1; i < len(entries); i++ {
		prev := entries[i-1]
		curr := entries[i]
		if curr.RunID == prev.RunID && curr.Changed {
			changes = append(changes, ChangeInterval{
				RunID:         curr.RunID,
				PrevFrame:     prev.FrameStart,
				CurrFrame:     curr.FrameStart,
				PrevValue:     prev.Evaluation,
				CurrValue:     curr.Evaluation,
				IntervalLabel: fmt.Sprintf("[%d, %d)", prev.FrameStart, curr.FrameStart),
			})
		}
	}
	return changes
}
