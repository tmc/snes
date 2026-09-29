package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/recovery/watches"
)

func runWatches(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm watches", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectDir = fs.String("project", "", "path to project directory")
		format     = fs.String("format", "text", "output format (text|json)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required")
	}

	watchesPath := filepath.Join(*projectDir, "watches.json")
	wf, err := watches.LoadWatches(watchesPath)
	if err != nil {
		return fmt.Errorf("load watches: %w", err)
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(wf)
	}

	fmt.Fprintf(stdout, "Watches for ROM %s (schema %d, %d definitions):\n",
		wf.ROMSHA256, wf.SchemaVersion, len(wf.Watches))
	for _, w := range wf.Watches {
		cpuStr := "-"
		if w.CPUAddress != nil {
			cpuStr = fmt.Sprintf("$%06X", *w.CPUAddress)
		}
		unitStr := ""
		if w.Unit != "" {
			unitStr = " [" + w.Unit + "]"
		}
		fmt.Fprintf(stdout, "  %-16s %-20s %s:0x%05x (CPU %s, %dB%s)\n",
			w.ID, w.Name, w.MemorySpace, w.Offset, cpuStr, w.Width, unitStr)
		if len(w.Conditions) > 0 {
			var condStrs []string
			for _, c := range w.Conditions {
				if c.Value != nil {
					condStrs = append(condStrs, fmt.Sprintf("%s %s %d", c.WatchID, c.Op, *c.Value))
				} else if c.OtherWatchID != nil {
					condStrs = append(condStrs, fmt.Sprintf("%s %s %s", c.WatchID, c.Op, *c.OtherWatchID))
				}
			}
			fmt.Fprintf(stdout, "    Conditions: %s\n", strings.Join(condStrs, ", "))
		}
	}
	return nil
}

func runWatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectDir = fs.String("project", "", "path to project directory")
		watchID    = fs.String("id", "", "watch definition ID")
		runID      = fs.String("run", "", "filter by run ID")
		frames     = fs.String("frames", "", "filter frame interval A:B (half-open [A,B))")
		changes    = fs.Bool("changes", false, "display only observed change intervals")
		format     = fs.String("format", "text", "output format (text|json)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required")
	}
	if *watchID == "" {
		return fmt.Errorf("-id flag is required")
	}

	watchesPath := filepath.Join(*projectDir, "watches.json")
	wf, err := watches.LoadWatches(watchesPath)
	if err != nil {
		return fmt.Errorf("load watches: %w", err)
	}

	var targetWatch *watches.WatchDefinition
	for i := range wf.Watches {
		if wf.Watches[i].ID == *watchID {
			targetWatch = &wf.Watches[i]
			break
		}
	}
	if targetWatch == nil {
		return fmt.Errorf("watch %q not found in %s", *watchID, watchesPath)
	}

	// Try loading snapshots
	snaps, err := findSnapshots(*projectDir)
	if err != nil || len(snaps) == 0 {
		// No snapshots found
		if *format == "json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"watch":     targetWatch,
				"snapshots": 0,
				"history":   []any{},
			})
		}
		fmt.Fprintf(stdout, "Watch: %s (%s)\n", targetWatch.Name, targetWatch.ID)
		fmt.Fprintf(stdout, "Offset: %s:0x%05x (%d bytes)\n", targetWatch.MemorySpace, targetWatch.Offset, targetWatch.Width)
		fmt.Fprintf(stdout, "No snapshot data available in project %s\n", *projectDir)
		return nil
	}

	// Filter snapshots by runID and frames
	var filtered []*watches.Snapshot
	var frameStart, frameEnd *uint64
	if *frames != "" {
		parts := strings.Split(*frames, ":")
		if len(parts) == 2 {
			if a, err := strconv.ParseUint(parts[0], 10, 64); err == nil {
				frameStart = &a
			}
			if b, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
				frameEnd = &b
			}
		}
	}

	for _, s := range snaps {
		if *runID != "" && s.RunID != *runID {
			continue
		}
		if frameStart != nil && s.Frame < *frameStart {
			continue
		}
		if frameEnd != nil && s.Frame >= *frameEnd {
			continue
		}
		filtered = append(filtered, s)
	}

	history, err := watches.BuildHistory(targetWatch, filtered, wf)
	if err != nil {
		return fmt.Errorf("build history: %w", err)
	}

	if *changes {
		changeList := watches.ExtractChanges(history)
		if *format == "json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(changeList)
		}
		fmt.Fprintf(stdout, "Observed changes for %s (%s) over %d snapshots:\n",
			targetWatch.Name, targetWatch.ID, len(filtered))
		for _, ch := range changeList {
			fmt.Fprintf(stdout, "  Interval %-12s: %s -> %s (Run: %s)\n",
				ch.IntervalLabel, ch.PrevValue.DecodedString, ch.CurrValue.DecodedString, ch.RunID)
		}
		return nil
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(history)
	}

	fmt.Fprintf(stdout, "Sampled history for %s (%s) [%d samples]:\n",
		targetWatch.Name, targetWatch.ID, len(history))
	for _, h := range history {
		changeMark := " "
		if h.Changed {
			changeMark = "*"
		}
		gapMark := ""
		if h.Gap {
			gapMark = " [GAP]"
		}
		rawHex := fmt.Sprintf("0x%X", h.Evaluation.RawValue)
		fmt.Fprintf(stdout, "  [%5d, %5d)%s %-16s (raw: %-8s validity: %-18s)%s\n",
			h.FrameStart, h.FrameEnd, changeMark, h.Evaluation.DecodedString, rawHex, h.Evaluation.Validity, gapMark)
	}

	return nil
}

func findSnapshots(projectDir string) ([]*watches.Snapshot, error) {
	// 1. snapshots directory
	snapDir := filepath.Join(projectDir, "snapshots")
	if fi, err := os.Stat(snapDir); err == nil && fi.IsDir() {
		return watches.LoadSnapshots(snapDir)
	}
	// 2. snapshots.json
	snapFile := filepath.Join(projectDir, "snapshots.json")
	if fi, err := os.Stat(snapFile); err == nil && !fi.IsDir() {
		return watches.LoadSnapshots(snapFile)
	}
	// 3. snapshots.jsonl
	snapLineFile := filepath.Join(projectDir, "snapshots.jsonl")
	if fi, err := os.Stat(snapLineFile); err == nil && !fi.IsDir() {
		return watches.LoadSnapshots(snapLineFile)
	}
	return nil, nil
}
