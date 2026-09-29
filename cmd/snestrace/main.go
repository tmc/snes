// snestrace records and queries structured SNES execution traces.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	snes "github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/disasm"
	"github.com/tmc/snes/internal/dma"
	"github.com/tmc/snes/internal/framecap"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/trace"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return runTrace(args[1:], stdout, stderr)
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "snestrace: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: snestrace run [flags] | snestrace replay [flags] | snestrace index [flags] | snestrace query <writers|readers|explain-writer|last-writer-at-frame|dma-for-dest|bus-for-pc|pc-context|trace-window|frame-summary|first-difference> [flags]")
}

func runTrace(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snestrace run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	romPath := fs.String("rom", "", "ROM path")
	statePath := fs.String("state", "", "save-state path")
	allowStateROMMismatch := fs.Bool("allow-state-rom-mismatch", false, "restore state even if its embedded ROM hash differs")
	inputPath := fs.String("inputs", "", "input trace JSON path")
	watchPath := fs.String("watch", "", "watch profile path")
	watchNameFlag := fs.String("watch-name", "", "comma-separated watch names to include")
	eventsFlag := fs.String("events", "frame,input,bus,mmio,dma,hdma,watch", "comma-separated event kinds")
	addrFlag := fs.String("addr", "", "comma-separated address filters such as wram:0x20-0x2f,vram:0x4000-0x47ff")
	pcFlag := fs.String("pc", "", "comma-separated CPU PC filters such as cpu:80:8000-cpu:80:80ff")
	opFlag := fs.String("op", "", "bus operation filter: read or write")
	dmaChannelFlag := fs.String("dma-channel", "", "comma-separated DMA channels 0-7 to include")
	maxEvents := fs.Int("max-events", 0, "maximum trace events to emit; 0 means unlimited")
	maxBytes := fs.Int("max-bytes", 0, "maximum trace bytes to emit; 0 means unlimited")
	compressFlag := fs.String("compress", "", "trace compression: gzip")
	framePNGDir := fs.String("frame-png-dir", "", "directory for frame PNG images")
	framePNGEvery := fs.Int("frame-png-every", 1, "write one frame PNG every N frames")
	frameDir := fs.String("frame-dir", "", "directory for synchronized frame capture (manifest, blobs, receipt)")
	frameFrom := fs.Int("frame-from", 0, "first PPU frame number whose pixels are stored")
	frameEvery := fs.Int("frame-every", 1, "store pixels of every Nth PPU frame")
	frameMax := fs.Int("frame-max", 0, "maximum frames with stored pixels; 0 means unlimited")
	frameMaxBytes := fs.Int64("frame-max-bytes", 0, "maximum bytes of stored frame content; 0 means unlimited")
	framePNG := fs.String("frame-png", "", "comma-separated PPU frame numbers to export as PNG in --frame-dir, or all")
	frames := fs.Int("frames", 0, "frames to run")
	outPath := fs.String("out", "", "trace JSONL output path")
	summaryPath := fs.String("summary", "", "summary JSON output path")
	receiptPath := fs.String("receipt", "", "receipt JSON output path (default receipt.json next to --out)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *romPath == "" || *outPath == "" || *frames < 0 {
		fmt.Fprintln(stderr, "snestrace run: --rom, --out, and --frames >= 0 are required")
		return 2
	}
	if *maxEvents < 0 {
		fmt.Fprintln(stderr, "snestrace run: --max-events must be >= 0")
		return 2
	}
	if *maxBytes < 0 {
		fmt.Fprintln(stderr, "snestrace run: --max-bytes must be >= 0")
		return 2
	}
	if *framePNGEvery <= 0 {
		fmt.Fprintln(stderr, "snestrace run: --frame-png-every must be > 0")
		return 2
	}
	if *frameEvery <= 0 || *frameFrom < 0 || *frameMax < 0 || *frameMaxBytes < 0 {
		fmt.Fprintln(stderr, "snestrace run: --frame-every must be > 0 and --frame-from, --frame-max, --frame-max-bytes >= 0")
		return 2
	}
	pngFrames, err := parseFrameSet(*framePNG)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: --frame-png: %v\n", err)
		return 2
	}
	compression, err := parseCompression(*compressFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}

	rom, err := os.ReadFile(*romPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: read rom: %v\n", err)
		return 1
	}
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		fmt.Fprintf(stderr, "snestrace run: load rom: %v\n", err)
		return 1
	}
	if *receiptPath == "" {
		*receiptPath = filepath.Join(filepath.Dir(*outPath), "receipt.json")
	}
	if err := os.Remove(*receiptPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "snestrace run: remove stale receipt: %v\n", err)
		return 1
	}
	var stateBytes []byte
	if *statePath != "" {
		stateBytes, err = os.ReadFile(*statePath)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: read state: %v\n", err)
			return 1
		}
	}

	eventSet := parseSet(*eventsFlag)
	ranges, err := parseRanges(*addrFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	pcRanges, err := parseRanges(*pcFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	opFilter, err := parseOpFilter(*opFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	dmaChannels, err := parseDMAChannels(*dmaChannelFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	watches, err := loadWatches(*watchPath, *watchNameFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	inputs, err := loadInputs(*inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	if *framePNGDir != "" {
		if err := os.MkdirAll(*framePNGDir, 0777); err != nil {
			fmt.Fprintf(stderr, "snestrace run: create frame png dir: %v\n", err)
			return 1
		}
	}
	var inputBytes []byte
	if *inputPath != "" {
		inputBytes, _ = os.ReadFile(*inputPath)
	}

	out, traceHash, closeTrace, err := createTraceOutput(*outPath, compression)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: create trace: %v\n", err)
		return 1
	}
	tw := trace.NewWriter(out)
	tw.SetLimit(*maxEvents)
	tw.SetByteLimit(*maxBytes)
	ctx := &runContext{sys: sys, tw: tw, events: eventSet, filters: ranges, pcFilters: pcRanges, opFilter: opFilter, dmaChannels: dmaChannels}
	run := runInfo(sys, eventSet, pcRanges, stateBytes, inputBytes, *maxEvents, *maxBytes, *frames)
	if eventSet["cpu_insn"] || eventSet["cpu_transition"] {
		tw.Emit(trace.Event{Kind: "run", Run: run})
		rec := trace.NewRecorder(tw)
		rec.Frame = func() int { return ctx.frame }
		rec.Instructions = eventSet["cpu_insn"]
		rec.Transitions = eventSet["cpu_transition"]
		if _, ok := sys.ROMProvenance(); ok {
			rec.ROMOffset = sys.ROMAddress
		}
		if len(pcRanges) > 0 {
			rec.Keep = ctx.matchesPC
		}
		ctx.rec = rec
	}
	// Attach before power-on so that reset is observed.
	// A restored state replaces the power-on state, so attach after it.
	if *statePath == "" {
		if err := ctx.observe(); err != nil {
			fmt.Fprintf(stderr, "snestrace run: %v\n", err)
			return 1
		}
	}
	sys.Power()
	if *statePath != "" {
		state := stateBytes
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: read state: %v\n", err)
			return 1
		}
		if *allowStateROMMismatch {
			err = sys.UnserializeWithOptions(state, snes.UnserializeOptions{IgnoreROMHash: true})
		} else {
			err = sys.Unserialize(state)
		}
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: restore state: %v\n", err)
			return 1
		}
		if err := ctx.observe(); err != nil {
			fmt.Fprintf(stderr, "snestrace run: %v\n", err)
			return 1
		}
	}
	uninstall := ctx.installHooks()
	defer uninstall()

	var fw *framecap.Writer
	if *frameDir != "" {
		if ctx.rec != nil {
			ctx.seq = framecap.NewSeqClock()
		}
		fw, err = framecap.Create(framecap.Options{
			Dir:        *frameDir,
			Run:        run,
			Trace:      *outPath,
			TraceFrame: func() int { return ctx.frame },
			Selection:  framecap.Selection{From: *frameFrom, Every: *frameEvery},
			Limits:     framecap.Limits{Frames: *frameMax, Bytes: *frameMaxBytes},
			PNG:        pngFrames,
			Seq:        ctx.seq,
		})
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: %v\n", err)
			return 1
		}
		stop, err := sys.CaptureFrames(fw.Keep, fw.Frame)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: %v\n", err)
			return 1
		}
		defer stop()
	}

	if eventSet["watch"] {
		ctx.emitWatches(watches)
	}
	var framesOut []frameSummary
	for frame := 0; frame < *frames; frame++ {
		ctx.frame = frame
		if state, ok := inputs[frame]; ok {
			if err := sys.SetInputState(0, state); err != nil {
				fmt.Fprintf(stderr, "snestrace run: frame %d input: %v\n", frame, err)
				return 1
			}
			if eventSet["input"] {
				tw.Emit(trace.Event{Kind: "input", Frame: frame, Value: uint64(state), Width: 2})
			}
		}
		if err := sys.Run(); err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame %d: %v\n", frame, err)
			return 1
		}
		if *framePNGDir != "" && frame%*framePNGEvery == 0 {
			pngPath := filepath.Join(*framePNGDir, fmt.Sprintf("frame_%06d.png", frame))
			if err := writeFramePNG(pngPath, sys.FrameBuffer()); err != nil {
				fmt.Fprintf(stderr, "snestrace run: write frame png: %v\n", err)
				return 1
			}
		}
		if eventSet["watch"] {
			ctx.emitWatches(watches)
		}
		hash, err := stateHash(sys)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame %d state hash: %v\n", frame, err)
			return 1
		}
		componentHashes, err := sys.StateHashes()
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame %d component hashes: %v\n", frame, err)
			return 1
		}
		frameOut := frameSummary{
			Frame:           frame,
			StateHash:       hash,
			FrameBufferHash: hashBGR555Frame(sys.FrameBuffer()),
			ComponentHashes: componentHashes,
			Watches:         ctx.watchValues(watches),
		}
		framesOut = append(framesOut, frameOut)
		if eventSet["frame"] {
			tw.Emit(trace.Event{Kind: "frame", Frame: frame, Name: "state", Hash: hash})
		}
		if tw.Err() != nil || (fw != nil && fw.Err() != nil) {
			break
		}
	}
	receipt := trace.Receipt{Outcome: trace.OutcomeComplete}
	if ctx.rec != nil {
		ctx.rec.Flush()
		receipt.LastSeq = ctx.rec.LastSeq()
	}
	ctx.detach()
	switch {
	case tw.Err() != nil:
		receipt.Outcome = trace.OutcomeSinkError
		receipt.Error = tw.Err().Error()
	case tw.Truncated():
		receipt.Outcome = trace.OutcomeLimit
		receipt.TruncationReason = tw.TruncationReason()
	case sys.CPU.Fault != nil:
		receipt.Outcome = trace.OutcomeCPUFault
		receipt.Error = sys.CPU.Fault.Error()
	}
	if err := closeTrace(); err != nil {
		fmt.Fprintf(stderr, "snestrace run: close trace: %v\n", err)
		return 1
	}
	receipt.EventCount = tw.Count()
	receipt.StreamSHA256 = hashFileOptional(*outPath)
	if err := trace.WriteReceipt(*receiptPath, receipt); err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 1
	}
	if fw != nil {
		outcome := receipt.Outcome
		if outcome == trace.OutcomeLimit {
			// The trace was truncated, not the frames.
			outcome = trace.OutcomeComplete
		}
		if _, err := fw.Close(outcome); err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame capture: %v\n", err)
			return 1
		}
	}
	if err := tw.Err(); err != nil {
		fmt.Fprintf(stderr, "snestrace run: write trace: %v\n", err)
		return 1
	}

	if *summaryPath != "" {
		var stateHashText string
		if *statePath != "" {
			stateHashText = hexHash(stateBytes)
		}
		if err := writeSummary(*summaryPath, summary{
			ROMPath:              *romPath,
			ROMHash:              hexHash(rom),
			StatePath:            *statePath,
			StateHash:            stateHashText,
			StartBoundary:        startBoundary(*statePath),
			InputPath:            *inputPath,
			InputHash:            hashOptional(inputBytes),
			WatchNames:           keysString(parseSet(*watchNameFlag)),
			TracePath:            *outPath,
			TraceHash:            traceHash.Sum(),
			TraceCompression:     compression,
			TraceCompressedHash:  hashCompressedTrace(*outPath, compression),
			TraceCompressedBytes: compressedTraceBytes(*outPath, compression),
			FramePNGDir:          *framePNGDir,
			FramePNGEvery:        omitDefaultOne(*framePNGEvery),
			Emulator:             buildRevision(),
			Frames:               *frames,
			FrameSummary:         framesOut,
			EventKinds:           keys(eventSet),
			EventCount:           tw.Count(),
			EventKindCounts:      tw.Kinds(),
			MaxEvents:            *maxEvents,
			MaxBytes:             *maxBytes,
			TraceBytes:           tw.Bytes(),
			Truncated:            tw.Truncated(),
			AddressRange:         ranges,
			PCRange:              pcRanges,
			Op:                   opFilter,
			DMAChannel:           keysInt(dmaChannels),
		}); err != nil {
			fmt.Fprintf(stderr, "snestrace run: write summary: %v\n", err)
			return 1
		}
	}
	return 0
}

func runReplay(args []string, stdout, stderr io.Writer) int {
	start := time.Now()
	phase := start
	fs := flag.NewFlagSet("snestrace replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	requestPath := fs.String("request", "", "replay request JSON path")
	romPath := fs.String("rom", "", "ROM path")
	statePath := fs.String("state", "", "save-state path")
	allowStateROMMismatch := fs.Bool("allow-state-rom-mismatch", false, "restore state even if its embedded ROM hash differs")
	inputPath := fs.String("inputs", "", "input trace JSON path")
	watchPath := fs.String("watch", "", "watch profile path")
	watchNameFlag := fs.String("watch-name", "", "comma-separated watch names passed to run")
	eventsFlag := fs.String("events", "cpu_block,frame,input,bus,mmio,dma,hdma,watch", "comma-separated event kinds")
	addrFlag := fs.String("addr", "", "comma-separated writer query ranges")
	pcFlag := fs.String("pc", "", "comma-separated CPU PC filters passed to run")
	opFlag := fs.String("op", "", "bus operation filter passed to run: read or write")
	dmaChannelFlag := fs.String("dma-channel", "", "comma-separated DMA channels passed to run")
	maxEvents := fs.Int("max-events", 0, "maximum trace events to emit; 0 means unlimited")
	maxBytes := fs.Int("max-bytes", 0, "maximum trace bytes to emit; 0 means unlimited")
	compressFlag := fs.String("compress", "", "trace compression: gzip")
	framePNGDir := fs.String("frame-png-dir", "", "directory for frame PNG images")
	framePNGEvery := fs.Int("frame-png-every", 1, "write one frame PNG every N frames")
	viewer := fs.Bool("viewer", false, "write a static HTML frame viewer")
	frameStart := fs.Int("frame-start", -1, "first frame for generated writer reports")
	frameEnd := fs.Int("frame-end", -1, "last frame for generated writer reports")
	comparePath := fs.String("compare", "", "optional trace JSONL to compare with first-difference")
	stopOnDivergence := fs.Bool("stop-on-divergence", false, "exit non-zero when --compare finds a first difference")
	frames := fs.Int("frames", 0, "frames to run")
	outDir := fs.String("out-dir", "", "artifact output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var req replayRequest
	if *requestPath != "" {
		var err error
		req, err = loadReplayRequest(*requestPath)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace replay: %v\n", err)
			return 2
		}
		applyReplayRequest(req, romPath, statePath, allowStateROMMismatch, inputPath, watchPath, eventsFlag, compressFlag, frames, outDir)
	}
	if *romPath == "" || *outDir == "" || *frames < 0 {
		fmt.Fprintln(stderr, "snestrace replay: --rom, --out-dir, and --frames >= 0 are required")
		return 2
	}
	if *maxEvents < 0 {
		fmt.Fprintln(stderr, "snestrace replay: --max-events must be >= 0")
		return 2
	}
	if *maxBytes < 0 {
		fmt.Fprintln(stderr, "snestrace replay: --max-bytes must be >= 0")
		return 2
	}
	if *framePNGEvery <= 0 {
		fmt.Fprintln(stderr, "snestrace replay: --frame-png-every must be > 0")
		return 2
	}
	compression, err := parseCompression(*compressFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace replay: %v\n", err)
		return 2
	}
	if *stopOnDivergence && *comparePath == "" {
		fmt.Fprintln(stderr, "snestrace replay: --stop-on-divergence requires --compare")
		return 2
	}
	if err := os.MkdirAll(*outDir, 0777); err != nil {
		fmt.Fprintf(stderr, "snestrace replay: create output dir: %v\n", err)
		return 1
	}

	tracePath := replayTracePath(*outDir, compression)
	summaryPath := filepath.Join(*outDir, "summary.json")
	indexPath := filepath.Join(*outDir, "index.json")
	progressPath := filepath.Join(*outDir, "progress.jsonl")
	progress, err := newReplayProgress(progressPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace replay: create progress: %v\n", err)
		return 1
	}
	defer progress.Close()
	replayFramePNGDir := *framePNGDir
	if *viewer && replayFramePNGDir == "" {
		replayFramePNGDir = filepath.Join(*outDir, "frames")
	}
	runArgs := []string{
		"run",
		"--rom", *romPath,
		"--frames", strconv.Itoa(*frames),
		"--events", *eventsFlag,
		"--out", tracePath,
		"--summary", summaryPath,
	}
	if *statePath != "" {
		runArgs = append(runArgs, "--state", *statePath)
	}
	if *allowStateROMMismatch {
		runArgs = append(runArgs, "--allow-state-rom-mismatch")
	}
	if *inputPath != "" {
		runArgs = append(runArgs, "--inputs", *inputPath)
	}
	if *watchPath != "" {
		runArgs = append(runArgs, "--watch", *watchPath)
	}
	if *watchNameFlag != "" {
		runArgs = append(runArgs, "--watch-name", *watchNameFlag)
	}
	if *pcFlag != "" {
		runArgs = append(runArgs, "--pc", *pcFlag)
	}
	if *opFlag != "" {
		runArgs = append(runArgs, "--op", *opFlag)
	}
	if *dmaChannelFlag != "" {
		runArgs = append(runArgs, "--dma-channel", *dmaChannelFlag)
	}
	if *maxEvents > 0 {
		runArgs = append(runArgs, "--max-events", strconv.Itoa(*maxEvents))
	}
	if *maxBytes > 0 {
		runArgs = append(runArgs, "--max-bytes", strconv.Itoa(*maxBytes))
	}
	if compression != "" {
		runArgs = append(runArgs, "--compress", compression)
	}
	if replayFramePNGDir != "" {
		runArgs = append(runArgs, "--frame-png-dir", replayFramePNGDir, "--frame-png-every", strconv.Itoa(*framePNGEvery))
	}
	var childOut bytes.Buffer
	var childErr bytes.Buffer
	progress.Write(replayProgressEvent{Phase: "run", Status: "start", Frames: *frames, OutputPath: tracePath})
	if code := run(runArgs, &childOut, &childErr); code != 0 {
		progress.Write(replayProgressEvent{Phase: "run", Status: "error", Frames: *frames, OutputPath: tracePath})
		fmt.Fprintf(stderr, "snestrace replay: run failed: %s", childErr.String())
		return code
	}
	runSummary := readSummaryOptional(summaryPath)
	progress.Write(replayProgressEvent{
		Phase:           "run",
		Status:          "done",
		CurrentFrame:    summaryCurrentFrame(runSummary),
		Frames:          *frames,
		EventCount:      runSummary.EventCount,
		TraceBytes:      runSummary.TraceBytes,
		CompressedBytes: runSummary.TraceCompressedBytes,
		OutputPath:      tracePath,
		Elapsed:         time.Since(start),
		PhaseElapsed:    time.Since(phase),
	})
	fmt.Fprintf(stderr, "snestrace replay: wrote trace and summary in %s\n", time.Since(phase).Round(time.Millisecond))
	writerRanges, err := replayWriterRanges(*addrFlag, *watchPath, *watchNameFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace replay: %v\n", err)
		return 2
	}
	phase = time.Now()
	progress.Write(replayProgressEvent{Phase: "build-index", Status: "start", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: indexPath, Elapsed: time.Since(start)})
	_, writerResults, err := buildReplayIndexAndCollectWriters(tracePath, indexPath, writerRanges, *frameStart, *frameEnd, lastReplayFrame(*frames))
	if err != nil {
		progress.Write(replayProgressEvent{Phase: "build-index", Status: "error", OutputPath: indexPath, Elapsed: time.Since(start), PhaseElapsed: time.Since(phase)})
		fmt.Fprintf(stderr, "snestrace replay: build index: %v\n", err)
		return 1
	}
	progress.Write(replayProgressEvent{Phase: "build-index", Status: "done", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: indexPath, Elapsed: time.Since(start), PhaseElapsed: time.Since(phase)})
	fmt.Fprintf(stderr, "snestrace replay: wrote index and collected writer events in %s\n", time.Since(phase).Round(time.Millisecond))
	phase = time.Now()

	var artifacts []replayArtifact
	traceHash := replayTraceHash(summaryPath, tracePath)
	artifacts = append(artifacts,
		replayArtifact{Name: "trace", Path: tracePath, Hash: traceHash, CompressedHash: hashCompressedTrace(tracePath, compression), CompressedBytes: compressedTraceBytes(tracePath, compression)},
		replayArtifact{Name: "summary", Path: summaryPath, Hash: hashFileOptional(summaryPath)},
		replayArtifact{Name: "index", Path: indexPath, Hash: hashFileOptional(indexPath)},
	)
	if replayFramePNGDir != "" {
		artifacts = append(artifacts, replayArtifact{Name: "frame-png-dir", Path: replayFramePNGDir})
	}
	progress.Write(replayProgressEvent{Phase: "query-writers", Status: "start", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: *outDir, Elapsed: time.Since(start)})
	writerArtifacts, err := writeReplayWriterArtifactResults(*outDir, writerResults, *frameStart, *frameEnd, lastReplayFrame(*frames))
	if err != nil {
		progress.Write(replayProgressEvent{Phase: "query-writers", Status: "error", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: *outDir, Elapsed: time.Since(start), PhaseElapsed: time.Since(phase)})
		fmt.Fprintf(stderr, "snestrace replay: writer artifacts: %v\n", err)
		return 1
	}
	artifacts = append(artifacts, writerArtifacts...)
	progress.Write(replayProgressEvent{Phase: "query-writers", Status: "done", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: *outDir, Elapsed: time.Since(start), PhaseElapsed: time.Since(phase)})
	if len(writerArtifacts) > 0 {
		fmt.Fprintf(stderr, "snestrace replay: wrote %d writer artifacts in %s\n", len(writerArtifacts), time.Since(phase).Round(time.Millisecond))
		phase = time.Now()
	}
	var diff *firstDifferenceResult
	if *comparePath != "" {
		diffPath := filepath.Join(*outDir, "first-difference.json")
		progress.Write(replayProgressEvent{Phase: "first-difference", Status: "start", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: diffPath, Elapsed: time.Since(start)})
		d, err := writeFirstDifference(diffPath, tracePath, *comparePath)
		if err != nil {
			progress.Write(replayProgressEvent{Phase: "first-difference", Status: "error", OutputPath: diffPath, Elapsed: time.Since(start)})
			fmt.Fprintf(stderr, "snestrace replay: first-difference: %v\n", err)
			return 1
		}
		progress.Write(replayProgressEvent{Phase: "first-difference", Status: "done", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: diffPath, Elapsed: time.Since(start)})
		diff = &d
		artifacts = append(artifacts, replayArtifact{Name: "first-difference", Path: diffPath, Hash: hashFileOptional(diffPath)})
	}
	if *viewer {
		viewerPath := filepath.Join(*outDir, "viewer.html")
		if err := writeReplayViewer(viewerPath, summaryPath, replayFramePNGDir, *framePNGEvery); err != nil {
			progress.Write(replayProgressEvent{Phase: "write-summary", Status: "error", OutputPath: viewerPath, Elapsed: time.Since(start)})
			fmt.Fprintf(stderr, "snestrace replay: write viewer: %v\n", err)
			return 1
		}
		artifacts = append(artifacts, replayArtifact{Name: "viewer", Path: viewerPath, Hash: hashFileOptional(viewerPath)})
	}

	manifestPath := filepath.Join(*outDir, "manifest.json")
	progress.Write(replayProgressEvent{Phase: "write-summary", Status: "start", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: manifestPath, Elapsed: time.Since(start)})
	if err := writeReplayManifest(manifestPath, replayManifest{
		ROMPath:              *romPath,
		ROMHash:              hashFileOptional(*romPath),
		RequestPath:          *requestPath,
		RequestHash:          hashFileOptional(*requestPath),
		RequestTarget:        req.Target,
		StatePath:            *statePath,
		StateHash:            hashFileOptional(*statePath),
		InputPath:            *inputPath,
		InputHash:            hashFileOptional(*inputPath),
		WatchPath:            *watchPath,
		WatchHash:            hashFileOptional(*watchPath),
		WatchNames:           keysString(parseSet(*watchNameFlag)),
		Events:               *eventsFlag,
		PC:                   *pcFlag,
		Op:                   *opFlag,
		DMAChannel:           *dmaChannelFlag,
		MaxEvents:            *maxEvents,
		MaxBytes:             *maxBytes,
		TraceHash:            traceHash,
		TraceCompression:     compression,
		TraceCompressedHash:  hashCompressedTrace(tracePath, compression),
		TraceCompressedBytes: compressedTraceBytes(tracePath, compression),
		FramePNGDir:          replayFramePNGDir,
		FramePNGEvery:        omitDefaultOne(*framePNGEvery),
		Viewer:               *viewer,
		FrameStart:           *frameStart,
		FrameEnd:             *frameEnd,
		StopOnDiff:           *stopOnDivergence,
		Frames:               *frames,
		Artifacts:            artifacts,
	}); err != nil {
		fmt.Fprintf(stderr, "snestrace replay: write manifest: %v\n", err)
		return 1
	}
	progress.Write(replayProgressEvent{Phase: "write-summary", Status: "done", CurrentFrame: summaryCurrentFrame(runSummary), Frames: *frames, EventCount: runSummary.EventCount, OutputPath: manifestPath, Elapsed: time.Since(start), PhaseElapsed: time.Since(phase)})
	fmt.Fprintf(stdout, "%s\n", manifestPath)
	fmt.Fprintf(stderr, "snestrace replay: completed in %s\n", time.Since(start).Round(time.Millisecond))
	if *stopOnDivergence && diff != nil && diff.EventIndex >= 0 {
		fmt.Fprintf(stderr, "snestrace replay: divergence at comparable event %d: %s\n", diff.EventIndex, diff.Reason)
		return 1
	}
	return 0
}

func frameArgs(args []string, startFrame, endFrame int) []string {
	if startFrame >= 0 {
		args = append(args, "--frame-start", strconv.Itoa(startFrame))
	}
	if endFrame >= 0 {
		args = append(args, "--frame-end", strconv.Itoa(endFrame))
	}
	return args
}

func lastReplayFrame(frames int) int {
	if frames <= 0 {
		return 0
	}
	return frames - 1
}

func runToFile(path string, args []string, stderr io.Writer) int {
	var stdout bytes.Buffer
	var childErr bytes.Buffer
	code := run(args, &stdout, &childErr)
	if code != 0 {
		fmt.Fprintf(stderr, "snestrace replay: %s failed: %s", strings.Join(args, " "), childErr.String())
		return code
	}
	if err := os.WriteFile(path, stdout.Bytes(), 0666); err != nil {
		fmt.Fprintf(stderr, "snestrace replay: write %s: %v\n", path, err)
		return 1
	}
	return 0
}

type replayProgress struct {
	path   string
	stderr io.Writer
	file   *os.File
	enc    *json.Encoder
}

type replayProgressEvent struct {
	Phase           string
	Status          string
	CurrentFrame    int
	Frames          int
	EventCount      int
	TraceBytes      int
	CompressedBytes int64
	OutputPath      string
	Elapsed         time.Duration
	PhaseElapsed    time.Duration
}

type replayProgressRecord struct {
	Schema          int    `json:"schema"`
	Phase           string `json:"phase"`
	Status          string `json:"status"`
	CurrentFrame    int    `json:"current_frame,omitempty"`
	Frames          int    `json:"frames,omitempty"`
	EventCount      int    `json:"event_count,omitempty"`
	TraceBytes      int    `json:"trace_bytes,omitempty"`
	CompressedBytes int64  `json:"compressed_bytes,omitempty"`
	OutputPath      string `json:"output_path,omitempty"`
	ElapsedMS       int64  `json:"elapsed_ms,omitempty"`
	PhaseElapsedMS  int64  `json:"phase_elapsed_ms,omitempty"`
}

func newReplayProgress(path string, stderr io.Writer) (*replayProgress, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &replayProgress{path: path, stderr: stderr, file: f, enc: json.NewEncoder(f)}, nil
}

func (p *replayProgress) Write(e replayProgressEvent) {
	if p == nil || p.enc == nil {
		return
	}
	rec := replayProgressRecord{
		Schema:          1,
		Phase:           e.Phase,
		Status:          e.Status,
		CurrentFrame:    e.CurrentFrame,
		Frames:          e.Frames,
		EventCount:      e.EventCount,
		TraceBytes:      e.TraceBytes,
		CompressedBytes: e.CompressedBytes,
		OutputPath:      e.OutputPath,
		ElapsedMS:       e.Elapsed.Round(time.Millisecond).Milliseconds(),
		PhaseElapsedMS:  e.PhaseElapsed.Round(time.Millisecond).Milliseconds(),
	}
	if err := p.enc.Encode(rec); err != nil {
		fmt.Fprintf(p.stderr, "snestrace replay: write progress: %v\n", err)
		return
	}
	fmt.Fprintf(p.stderr, "snestrace replay: progress phase=%s status=%s", e.Phase, e.Status)
	if e.Frames > 0 {
		fmt.Fprintf(p.stderr, " frame=%d/%d", e.CurrentFrame, e.Frames)
	}
	if e.EventCount > 0 {
		fmt.Fprintf(p.stderr, " events=%d", e.EventCount)
	}
	if e.CompressedBytes > 0 {
		fmt.Fprintf(p.stderr, " compressed_bytes=%d", e.CompressedBytes)
	}
	if e.OutputPath != "" {
		fmt.Fprintf(p.stderr, " path=%s", e.OutputPath)
	}
	fmt.Fprintln(p.stderr)
}

func (p *replayProgress) Close() error {
	if p == nil || p.file == nil {
		return nil
	}
	return p.file.Close()
}

func readSummaryOptional(path string) summary {
	data, err := os.ReadFile(path)
	if err != nil {
		return summary{}
	}
	var s summary
	if err := json.Unmarshal(data, &s); err != nil {
		return summary{}
	}
	return s
}

func summaryCurrentFrame(s summary) int {
	if len(s.FrameSummary) == 0 {
		return 0
	}
	return s.FrameSummary[len(s.FrameSummary)-1].Frame
}

func writeReplayWriterArtifacts(tracePath, outDir string, ranges []trace.Range, frameStart, frameEnd, lastFrame int) ([]replayArtifact, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	results, err := collectReplayWriterResults(tracePath, ranges, frameStart, frameEnd, lastFrame)
	if err != nil {
		return nil, err
	}
	return writeReplayWriterArtifactResults(outDir, results, frameStart, frameEnd, lastFrame)
}

func writeReplayWriterArtifactResults(outDir string, results []replayWriterResult, frameStart, frameEnd, lastFrame int) ([]replayArtifact, error) {
	if len(results) == 0 {
		return nil, nil
	}
	var artifacts []replayArtifact
	for i, result := range results {
		explainPath := filepath.Join(outDir, fmt.Sprintf("writer-%02d.json", i+1))
		if err := writeJSONFile(explainPath, newExplainWriterResult(result.Range, frameStart, frameEnd, result.Explain)); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, replayArtifact{Name: "explain-writer", Path: explainPath, Addr: formatRange(result.Range), Hash: hashFileOptional(explainPath)})

		lastPath := filepath.Join(outDir, fmt.Sprintf("last-writer-%02d.json", i+1))
		if err := writeJSONFile(lastPath, newExplainWriterResult(result.Range, -1, lastFrame, result.Last)); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, replayArtifact{Name: "last-writer-at-frame", Path: lastPath, Addr: formatRange(result.Range), Hash: hashFileOptional(lastPath)})
	}
	return artifacts, nil
}

func buildReplayIndexAndCollectWriters(tracePath, indexPath string, ranges []trace.Range, frameStart, frameEnd, lastFrame int) (indexFile, []replayWriterResult, error) {
	results := make([]replayWriterResult, len(ranges))
	for i, r := range ranges {
		results[i].Range = r
	}
	rangeIndex := newReplayWriterRangeIndex(ranges)
	idx := newIndexFile(tracePath)
	r, closeFn, err := openTrace(tracePath)
	if err != nil {
		return indexFile{}, nil, fmt.Errorf("open trace: %w", err)
	}
	defer closeFn()
	dec := json.NewDecoder(r)
	for i := 0; ; i++ {
		var e trace.Event
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			return indexFile{}, nil, fmt.Errorf("decode trace: %w", err)
		}
		addIndexEvent(&idx, i, e)
		header := replayWriterEventHeader{
			Kind:  e.Kind,
			Frame: e.Frame,
			Space: e.Space,
			Addr:  e.Addr,
			Op:    e.Op,
			Dest:  e.Dest,
		}
		for _, resultIndex := range rangeIndex.matches(header) {
			if replayFrameInRange(e.Frame, frameStart, frameEnd) {
				results[resultIndex].Explain = append(results[resultIndex].Explain, e)
			}
			if e.Frame <= lastFrame {
				results[resultIndex].Last = []trace.Event{e}
			}
		}
	}
	finishIndex(&idx)
	if err := writeJSONFile(indexPath, idx); err != nil {
		return indexFile{}, nil, err
	}
	return idx, results, nil
}

type replayWriterResult struct {
	Range   trace.Range
	Explain []trace.Event
	Last    []trace.Event
}

func collectReplayWriterResults(tracePath string, ranges []trace.Range, frameStart, frameEnd, lastFrame int) ([]replayWriterResult, error) {
	results := make([]replayWriterResult, len(ranges))
	for i, r := range ranges {
		results[i].Range = r
	}
	index := newReplayWriterRangeIndex(ranges)
	r, closeFn, err := openTrace(tracePath)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer closeFn()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(line) == 0 {
					break
				}
			} else {
				return nil, fmt.Errorf("read trace: %w", err)
			}
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}
		var header replayWriterEventHeader
		if err := json.Unmarshal(line, &header); err != nil {
			return nil, fmt.Errorf("decode trace header: %w", err)
		}
		matches := index.matches(header)
		if len(matches) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}
		var e trace.Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("decode trace event: %w", err)
		}
		for _, i := range matches {
			if replayFrameInRange(e.Frame, frameStart, frameEnd) {
				results[i].Explain = append(results[i].Explain, e)
			}
			if e.Frame <= lastFrame {
				results[i].Last = []trace.Event{e}
			}
		}
		if err == io.EOF {
			break
		}
	}
	return results, nil
}

type replayWriterEventHeader struct {
	Kind  string      `json:"kind"`
	Frame int         `json:"frame"`
	Space string      `json:"space,omitempty"`
	Addr  uint32      `json:"addr,omitempty"`
	Op    string      `json:"op,omitempty"`
	Dest  trace.Range `json:"dest,omitempty"`
}

type replayWriterRangeIndex struct {
	exact map[string]map[uint32][]int
	wide  []int
	all   []trace.Range
}

func newReplayWriterRangeIndex(ranges []trace.Range) replayWriterRangeIndex {
	index := replayWriterRangeIndex{
		exact: make(map[string]map[uint32][]int),
		all:   ranges,
	}
	for i, r := range ranges {
		if r.Start != r.End {
			index.wide = append(index.wide, i)
			continue
		}
		byAddr := index.exact[r.Space]
		if byAddr == nil {
			byAddr = make(map[uint32][]int)
			index.exact[r.Space] = byAddr
		}
		byAddr[r.Start] = append(byAddr[r.Start], i)
	}
	return index
}

func (index replayWriterRangeIndex) matches(e replayWriterEventHeader) []int {
	if replayHeaderIsWriteEvent(e) {
		var out []int
		if byAddr := index.exact[e.Space]; byAddr != nil {
			out = append(out, byAddr[e.Addr]...)
		}
		for _, i := range index.wide {
			if index.all[i].Contains(e.Space, e.Addr) {
				out = append(out, i)
			}
		}
		return out
	}
	if replayHeaderIsDMAEvent(e) {
		var out []int
		for i, r := range index.all {
			if e.Dest.Intersects(r) {
				out = append(out, i)
			}
		}
		return out
	}
	return nil
}

func replayHeaderIsWriteEvent(e replayWriterEventHeader) bool {
	switch e.Kind {
	case "bus", "mmio", "apu", "input", "ppu":
		return e.Op == "write"
	}
	return false
}

func replayHeaderIsDMAEvent(e replayWriterEventHeader) bool {
	return e.Kind == "dma" || e.Kind == "hdma"
}

func replayFrameInRange(frame, startFrame, endFrame int) bool {
	if startFrame >= 0 && frame < startFrame {
		return false
	}
	if endFrame >= 0 && frame > endFrame {
		return false
	}
	return true
}

func replayIsDMAEvent(e trace.Event) bool {
	return e.Kind == "dma" || e.Kind == "hdma"
}

func replayIsWriteEvent(e trace.Event) bool {
	switch e.Kind {
	case "bus", "mmio", "apu", "input", "ppu":
		return e.Op == "write"
	}
	return false
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0666); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func parseCompression(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return "", nil
	case "gzip", "gz":
		return "gzip", nil
	default:
		return "", fmt.Errorf("unknown compression %q", s)
	}
}

func replayTracePath(outDir, compression string) string {
	if compression == "gzip" {
		return filepath.Join(outDir, "trace.jsonl.gz")
	}
	return filepath.Join(outDir, "trace.jsonl")
}

type traceHashOutput struct {
	w      io.Writer
	file   *os.File
	gzipw  *gzip.Writer
	hash   hash.Hash
	closed bool
}

func createTraceOutput(path, compression string) (io.Writer, *traceHashOutput, func() error, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, nil, err
	}
	out := &traceHashOutput{file: f, hash: sha256.New()}
	var w io.Writer = f
	if compression == "gzip" {
		out.gzipw = gzip.NewWriter(f)
		w = out.gzipw
	}
	out.w = io.MultiWriter(w, out.hash)
	return out.w, out, out.Close, nil
}

func (o *traceHashOutput) Close() error {
	if o == nil || o.closed {
		return nil
	}
	o.closed = true
	if o.gzipw != nil {
		if err := o.gzipw.Close(); err != nil {
			_ = o.file.Close()
			return err
		}
	}
	return o.file.Close()
}

func (o *traceHashOutput) Sum() string {
	if o == nil || o.hash == nil {
		return ""
	}
	return hex.EncodeToString(o.hash.Sum(nil))
}

func hashCompressedTrace(path, compression string) string {
	if compression == "" {
		return ""
	}
	return hashFileOptional(path)
}

func compressedTraceBytes(path, compression string) int64 {
	if compression == "" {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func replayTraceHash(summaryPath, tracePath string) string {
	data, err := os.ReadFile(summaryPath)
	if err == nil {
		var s summary
		if json.Unmarshal(data, &s) == nil && s.TraceHash != "" {
			return s.TraceHash
		}
	}
	return hashTraceSemanticOptional(tracePath)
}

func hashTraceSemanticOptional(path string) string {
	r, closeFn, err := openTrace(path)
	if err != nil {
		return ""
	}
	defer closeFn()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeFirstDifference(path, leftPath, rightPath string) (firstDifferenceResult, error) {
	left, err := readTraceFile(leftPath)
	if err != nil {
		return firstDifferenceResult{}, fmt.Errorf("read left: %w", err)
	}
	right, err := readTraceFile(rightPath)
	if err != nil {
		return firstDifferenceResult{}, fmt.Errorf("read right: %w", err)
	}
	diff := firstDifference(left, right)
	data, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return firstDifferenceResult{}, fmt.Errorf("encode: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0666); err != nil {
		return firstDifferenceResult{}, fmt.Errorf("write %s: %w", path, err)
	}
	return diff, nil
}

func replayWriterRanges(addrText, watchPath, watchNames string) ([]trace.Range, error) {
	var ranges []trace.Range
	for _, part := range strings.Split(addrText, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		r, err := trace.ParseRange(part)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}
	watches, err := loadWatches(watchPath, watchNames)
	if err != nil {
		return nil, err
	}
	for _, w := range watches {
		ranges = append(ranges, w.Range)
	}
	return ranges, nil
}

type replayManifest struct {
	ROMPath              string           `json:"rom_path"`
	ROMHash              string           `json:"rom_hash"`
	RequestPath          string           `json:"request_path,omitempty"`
	RequestHash          string           `json:"request_hash,omitempty"`
	RequestTarget        json.RawMessage  `json:"request_target,omitempty"`
	StatePath            string           `json:"state_path,omitempty"`
	StateHash            string           `json:"state_hash,omitempty"`
	InputPath            string           `json:"input_path,omitempty"`
	InputHash            string           `json:"input_hash,omitempty"`
	WatchPath            string           `json:"watch_path,omitempty"`
	WatchHash            string           `json:"watch_hash,omitempty"`
	WatchNames           []string         `json:"watch_names,omitempty"`
	Events               string           `json:"events,omitempty"`
	PC                   string           `json:"pc,omitempty"`
	Op                   string           `json:"op,omitempty"`
	DMAChannel           string           `json:"dma_channel,omitempty"`
	MaxEvents            int              `json:"max_events,omitempty"`
	MaxBytes             int              `json:"max_bytes,omitempty"`
	TraceHash            string           `json:"trace_hash,omitempty"`
	TraceCompression     string           `json:"trace_compression,omitempty"`
	TraceCompressedHash  string           `json:"trace_compressed_hash,omitempty"`
	TraceCompressedBytes int64            `json:"trace_compressed_bytes,omitempty"`
	FramePNGDir          string           `json:"frame_png_dir,omitempty"`
	FramePNGEvery        int              `json:"frame_png_every,omitempty"`
	Viewer               bool             `json:"viewer,omitempty"`
	FrameStart           int              `json:"frame_start,omitempty"`
	FrameEnd             int              `json:"frame_end,omitempty"`
	StopOnDiff           bool             `json:"stop_on_divergence,omitempty"`
	Frames               int              `json:"frames"`
	Artifacts            []replayArtifact `json:"artifacts"`
}

type replayRequest struct {
	SchemaVersion         string          `json:"schema_version,omitempty"`
	Target                json.RawMessage `json:"target,omitempty"`
	ROMPath               string          `json:"rom_path,omitempty"`
	StatePath             string          `json:"state_path,omitempty"`
	Frames                int             `json:"frames,omitempty"`
	Events                string          `json:"events,omitempty"`
	Compress              string          `json:"compress,omitempty"`
	AllowStateROMMismatch bool            `json:"allow_state_rom_mismatch,omitempty"`
	InputsPath            string          `json:"inputs_path,omitempty"`
	WatchPath             string          `json:"watch_path,omitempty"`
	OutDir                string          `json:"out_dir,omitempty"`
}

func loadReplayRequest(path string) (replayRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return replayRequest{}, fmt.Errorf("read request: %w", err)
	}
	var req replayRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return replayRequest{}, fmt.Errorf("parse request: %w", err)
	}
	return req, nil
}

func applyReplayRequest(req replayRequest, romPath, statePath *string, allowMismatch *bool, inputPath, watchPath, eventsFlag, compressFlag *string, frames *int, outDir *string) {
	if *romPath == "" {
		*romPath = req.ROMPath
	}
	if *statePath == "" {
		*statePath = req.StatePath
	}
	if req.AllowStateROMMismatch {
		*allowMismatch = true
	}
	if *inputPath == "" {
		*inputPath = req.InputsPath
	}
	if *watchPath == "" {
		*watchPath = req.WatchPath
	}
	if req.Events != "" {
		*eventsFlag = req.Events
	}
	if req.Compress != "" {
		*compressFlag = req.Compress
	}
	if *frames == 0 && req.Frames > 0 {
		*frames = req.Frames
	}
	if *outDir == "" {
		*outDir = req.OutDir
	}
}

type replayArtifact struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	Addr            string `json:"addr,omitempty"`
	Hash            string `json:"hash,omitempty"`
	CompressedHash  string `json:"compressed_hash,omitempty"`
	CompressedBytes int64  `json:"compressed_bytes,omitempty"`
}

func writeReplayManifest(path string, m replayManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0666)
}

type runContext struct {
	sys         *snes.System
	tw          *trace.Writer
	events      map[string]bool
	filters     []trace.Range
	pcFilters   []trace.Range
	opFilter    string
	dmaChannels map[int]bool
	frame       int
	block       *trace.Event
	step        *trace.Event

	// rec serializes cpu_insn and cpu_transition records, if enabled.
	rec *trace.Recorder
	// seq, if non-nil, maps cycles to rec's sequence numbers for frame capture.
	seq *framecap.SeqClock
	// last is the most recently completed instruction observation.
	last           cpu.Observation
	hasLast        bool
	detachObserver func()
}

// observe attaches c as the CPU observer if any enabled event needs
// instruction observations.
func (c *runContext) observe() error {
	if c.rec == nil && !c.needsCPUContext() {
		return nil
	}
	detach, err := c.sys.CPU.Observe(c)
	if err != nil {
		return fmt.Errorf("observe cpu: %w", err)
	}
	c.detachObserver = detach
	return nil
}

// detach detaches the CPU observer, if attached.
func (c *runContext) detach() {
	if c.detachObserver != nil {
		c.detachObserver()
		c.detachObserver = nil
	}
}

func (c *runContext) needsCPUContext() bool {
	for _, k := range []string{"cpu_block", "cpu_step", "bus", "mmio", "apu", "dma", "hdma", "ppu", "input"} {
		if c.events[k] {
			return true
		}
	}
	return false
}

// ObserveInstruction implements cpu.Observer.
func (c *runContext) ObserveInstruction(o cpu.Observation) {
	c.last = o
	c.hasLast = true
	if c.rec != nil {
		c.rec.ObserveInstruction(o)
		if c.seq != nil {
			c.seq.Observe(c.rec.LastSeq(), o.Entry.Cycles)
		}
	}
}

// ObserveTransition implements cpu.Observer.
func (c *runContext) ObserveTransition(t cpu.Transition) {
	if c.rec != nil {
		c.rec.ObserveTransition(t)
		if c.seq != nil {
			c.seq.Observe(c.rec.LastSeq(), t.Before.Cycles)
		}
	}
}

// installHooks installs the diagnostic hooks for the enabled events and
// returns a function that restores the previous hooks.
func (c *runContext) installHooks() (uninstall func()) {
	cpuc, b, d, pp := c.sys.CPU, c.sys.Bus, c.sys.DMA, c.sys.PPU
	before, after, intr := cpuc.BeforeExecute, cpuc.AfterExecute, cpuc.InterruptHook
	rh, wh := b.ReadHook, b.WriteHook
	dt, ht := d.Trace, d.HDMATrace
	pw := pp.WriteHook
	uninstall = func() {
		cpuc.BeforeExecute, cpuc.AfterExecute, cpuc.InterruptHook = before, after, intr
		b.ReadHook, b.WriteHook = rh, wh
		d.Trace, d.HDMATrace = dt, ht
		pp.WriteHook = pw
	}
	if c.events["cpu_block"] || c.events["cpu_step"] {
		prev := c.sys.CPU.BeforeExecute
		c.sys.CPU.BeforeExecute = func() {
			if c.events["cpu_block"] {
				if c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
					c.block = &trace.Event{
						Kind:  "cpu_block",
						Frame: c.frame,
						Cycle: c.sys.CPU.Cycles,
						PC:    &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
						Value: uint64(c.sys.CPU.P),
					}
				} else {
					c.block = nil
				}
			}
			if c.events["cpu_step"] {
				if c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
					c.step = &trace.Event{
						Kind:  "cpu_step",
						Frame: c.frame,
						Cycle: c.sys.CPU.Cycles,
						PC:    &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
					}
				} else {
					c.step = nil
				}
			}
			if prev != nil {
				prev()
			}
		}
	}
	if c.events["cpu_block"] || c.events["cpu_step"] || c.events["interrupt"] {
		prev := c.sys.CPU.AfterExecute
		c.sys.CPU.AfterExecute = func() {
			// The observer has seen this instruction: c.last is complete.
			if c.block != nil {
				c.finishStep(c.block)
				c.tw.Emit(*c.block)
				c.block = nil
			}
			if c.step != nil {
				c.finishStep(c.step)
				c.step.CPUAfter = c.currentCPUContext()
				c.tw.Emit(*c.step)
				c.step = nil
			}
			if c.events["interrupt"] && c.sys.CPU.LastOpcode == 0x40 {
				c.tw.Emit(trace.Event{
					Kind:     "interrupt",
					Frame:    c.frame,
					Cycle:    c.sys.CPU.Cycles,
					PC:       &trace.PC{Bank: c.sys.CPU.PB, Addr: c.sys.CPU.PC},
					CPU:      c.currentCPUContext(),
					Category: "interrupt",
					Op:       "rti_exit",
					Value:    uint64(c.sys.CPU.P),
				})
			}
			if prev != nil {
				prev()
			}
		}
	}
	if c.events["interrupt"] {
		c.sys.CPU.InterruptHook = func(kind string) {
			c.tw.Emit(trace.Event{
				Kind:     "interrupt",
				Frame:    c.frame,
				Cycle:    c.sys.CPU.Cycles,
				PC:       &trace.PC{Bank: c.sys.CPU.PB, Addr: c.sys.CPU.PC},
				CPU:      c.currentCPUContext(),
				Category: "interrupt",
				Op:       kind,
				Value:    uint64(c.sys.CPU.P),
			})
		}
	}
	if c.events["bus"] || c.events["mmio"] || c.events["apu"] || c.events["input"] {
		c.sys.Bus.ReadHook = func(addr uint32, value uint8) {
			c.emitBus("read", addr, value)
		}
		c.sys.Bus.WriteHook = func(addr uint32, value uint8) {
			c.emitBus("write", addr, value)
		}
	}
	if c.events["dma"] {
		c.sys.DMA.Trace = func(dt dma.TransferTrace) {
			if !c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
				return
			}
			if !c.matchesDMAChannel(dt.Channel) {
				return
			}
			count := uint32(dt.Count)
			src := uint32(dt.SrcBank)<<16 | uint32(dt.SrcAddr)
			dst := c.dmaDest(dt, count)
			c.tw.Emit(trace.Event{
				Kind:         "dma",
				Frame:        c.frame,
				Cycle:        c.sys.CPU.Cycles,
				PC:           &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
				CPU:          c.cpuContext(),
				Channel:      dt.Channel,
				Mode:         dt.Control,
				Count:        dt.Count,
				Direction:    dmaDirection(dt.Control),
				Target:       dt.Target,
				DestRegister: 0x2100 | uint16(dt.Target),
				DMA: &trace.DMAContext{
					Channel:      dt.Channel,
					Mode:         dt.Control,
					Count:        dt.Count,
					Direction:    dmaDirection(dt.Control),
					Target:       dt.Target,
					DestRegister: 0x2100 | uint16(dt.Target),
				},
				Source: trace.Range{Space: "cpu", Start: src, End: src + count - 1},
				Dest:   dst,
			})
		}
	}
	if c.events["hdma"] {
		c.sys.DMA.HDMATrace = func(dt dma.TransferTrace) {
			if !c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
				return
			}
			if !c.matchesDMAChannel(dt.Channel) {
				return
			}
			count := uint32(dt.Count)
			src := uint32(dt.SrcBank)<<16 | uint32(dt.SrcAddr)
			dst := c.dmaDest(dt, count)
			c.tw.Emit(trace.Event{
				Kind:         "hdma",
				Frame:        c.frame,
				Cycle:        c.sys.CPU.Cycles,
				PC:           &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
				CPU:          c.cpuContext(),
				Channel:      dt.Channel,
				Mode:         dt.Control,
				Count:        dt.Count,
				Direction:    dmaDirection(dt.Control),
				Target:       dt.Target,
				DestRegister: 0x2100 | uint16(dt.Target),
				DMA: &trace.DMAContext{
					Channel:      dt.Channel,
					Mode:         dt.Control,
					Count:        dt.Count,
					Direction:    dmaDirection(dt.Control),
					Target:       dt.Target,
					DestRegister: 0x2100 | uint16(dt.Target),
				},
				Source: trace.Range{Space: "cpu", Start: src, End: src + count - 1},
				Dest:   dst,
			})
		}
	}
	if c.events["ppu"] {
		c.sys.PPU.WriteHook = func(pe ppu.WriteEvent) {
			if !c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
				return
			}
			if c.opFilter != "" && c.opFilter != "write" {
				return
			}
			if len(c.filters) > 0 && !matches(c.filters, pe.Space, pe.Addr) {
				return
			}
			register, category := trace.MMIORegister(uint32(pe.Register))
			before := uint64(pe.Before)
			after := uint64(pe.After)
			c.tw.Emit(trace.Event{
				Kind:     "ppu",
				Frame:    c.frame,
				Cycle:    c.sys.CPU.Cycles,
				PC:       &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
				CPU:      c.cpuContext(),
				Register: register,
				Category: category,
				Space:    pe.Space,
				Addr:     pe.Addr,
				Width:    1,
				Value:    uint64(pe.After),
				Before:   &before,
				After:    &after,
				Op:       "write",
			})
		}
	}
	return uninstall
}

func dmaDirection(control uint8) string {
	if control&0x80 != 0 {
		return "b_to_a"
	}
	return "a_to_b"
}

// finishStep completes a cpu_step or cpu_block event from the
// observation of the instruction it describes.
func (c *runContext) finishStep(e *trace.Event) {
	o := c.last
	e.CPU = c.insnContext(o)
	// Instruction bytes wrap within the program bank.
	e.EndPC = &trace.PC{Bank: o.Entry.PB, Addr: o.Entry.PC + uint16(insnLength(o))}
	e.SuccessorPC = &trace.PC{Bank: o.Exit.PB, Addr: o.Exit.PC}
	e.BranchKind = branchKind(e.CPU.Opcode, e.EndPC, e.SuccessorPC)
}

// insnLength returns the length of the observed instruction, decoded
// from its opcode and entry register widths, or 0 if nothing was fetched.
func insnLength(o cpu.Observation) int {
	if o.NumFetches == 0 {
		return 0
	}
	return disasm.InstructionLength65816(o.Fetches[0].Value, o.Entry.MemoryWidth() == 8, o.Entry.IndexWidth() == 8)
}

func branchKind(opcode uint8, endPC, successorPC *trace.PC) string {
	if endPC == nil || successorPC == nil {
		return ""
	}
	name := cpu.Opcodes[opcode].Name
	switch name {
	case "JMP", "JML":
		return "jump"
	case "JSR", "JSL":
		return "call"
	case "RTS", "RTL", "RTI":
		return "return"
	case "BRA", "BRL":
		return "branch_taken"
	case "BCC", "BCS", "BEQ", "BMI", "BNE", "BPL", "BVC", "BVS":
		if endPC.Bank != successorPC.Bank || endPC.Addr != successorPC.Addr {
			return "branch_taken"
		}
		return "branch_not_taken"
	default:
		if endPC.Bank != successorPC.Bank || endPC.Addr != successorPC.Addr {
			return "pc_changed"
		}
		return "fallthrough"
	}
}

// insnContext returns the CPU context of observation o, which may be
// in progress. Registers are the entry state and Bytes are the bytes
// fetched so far. The effective address is reported only once all of
// the instruction's bytes have been fetched, and only when any pointer
// it depends on is in work RAM.
func (c *runContext) insnContext(o cpu.Observation) *trace.CPUContext {
	e := o.Entry
	ctx := &trace.CPUContext{
		A:      e.A,
		E:      e.E,
		PBR:    e.PB,
		PC:     e.PC,
		DBR:    e.DB,
		DP:     e.D,
		X:      e.X,
		Y:      e.Y,
		S:      e.S,
		P:      e.P,
		MWidth: e.MemoryWidth(),
		XWidth: e.IndexWidth(),
	}
	if o.NumFetches == 0 {
		return ctx
	}
	op := cpu.Opcodes[o.Fetches[0].Value]
	ctx.Opcode = o.Fetches[0].Value
	ctx.Disasm = op.Name
	ctx.Addressing = addressingName(op.Mode)
	ctx.Bytes = make([]uint16, o.NumFetches)
	for i := range ctx.Bytes {
		ctx.Bytes[i] = uint16(o.Fetches[i].Value)
	}
	if o.NumFetches >= insnLength(o) {
		ctx.EffectiveAddr, ctx.EffectiveExpr = c.effectiveAddress(e, op.Mode, ctx.Bytes)
	}
	return ctx
}

// effectiveAddress returns the effective address of an instruction
// with the given mode and bytes executed from register state r.
func (c *runContext) effectiveAddress(r cpu.Snapshot, mode cpu.AddressingMode, bytes []uint16) (*uint32, string) {
	if len(bytes) < 2 {
		return nil, ""
	}
	b1 := uint16(bytes[1])
	word := b1
	if len(bytes) > 2 {
		word |= uint16(bytes[2]) << 8
	}
	var addr uint32
	var expr string
	switch mode {
	case cpu.AddrDir:
		addr = directPageAddress(r.E, r.D, b1)
		expr = "dp"
	case cpu.AddrDirX:
		addr = directPageAddress(r.E, r.D, b1+r.X)
		expr = "dp,x"
	case cpu.AddrDirY:
		addr = directPageAddress(r.E, r.D, b1+r.Y)
		expr = "dp,y"
	case cpu.AddrIndX:
		ptr, ok := c.peekDirectPageWord(r, b1+r.X)
		if !ok {
			return nil, ""
		}
		addr = uint32(r.DB)<<16 | uint32(ptr)
		expr = "(dp,x)"
	case cpu.AddrIndY:
		ptr, ok := c.peekDirectPageWord(r, b1)
		if !ok {
			return nil, ""
		}
		addr = (uint32(r.DB)<<16 | uint32(ptr)) + uint32(r.Y)
		addr &= 0xffffff
		expr = "(dp),y"
	case cpu.AddrDirInd:
		ptr, ok := c.peekDirectPageWord(r, b1)
		if !ok {
			return nil, ""
		}
		addr = uint32(r.DB)<<16 | uint32(ptr)
		expr = "(dp)"
	case cpu.AddrDirIndL:
		ptr, ok := c.peekDirectPageLong(r, b1)
		if !ok {
			return nil, ""
		}
		addr = ptr
		expr = "[dp]"
	case cpu.AddrDirIndLIdxY:
		ptr, ok := c.peekDirectPageLong(r, b1)
		if !ok {
			return nil, ""
		}
		addr = (ptr + uint32(r.Y)) & 0xffffff
		expr = "[dp],y"
	case cpu.AddrSr:
		addr = uint32(r.S+b1) & 0xffff
		expr = "sr,s"
	case cpu.AddrSrIndY:
		ptrAddr := uint32(r.S+b1) & 0xffff
		lo, ok1 := c.peekWRAM(ptrAddr)
		hi, ok2 := c.peekWRAM((ptrAddr + 1) & 0xffff)
		if !ok1 || !ok2 {
			return nil, ""
		}
		ptr := uint16(lo) | uint16(hi)<<8
		addr = (uint32(r.DB)<<16 | uint32(ptr)) + uint32(r.Y)
		addr &= 0xffffff
		expr = "(sr,s),y"
	case cpu.AddrAbs:
		addr = uint32(r.DB)<<16 | uint32(word)
		expr = "abs"
	case cpu.AddrAbsX:
		addr = (uint32(r.DB)<<16 | uint32(word)) + uint32(r.X)
		addr &= 0xffffff
		expr = "abs,x"
	case cpu.AddrAbsY:
		addr = (uint32(r.DB)<<16 | uint32(word)) + uint32(r.Y)
		addr &= 0xffffff
		expr = "abs,y"
	case cpu.AddrLong:
		if len(bytes) < 4 {
			return nil, ""
		}
		addr = uint32(bytes[3])<<16 | uint32(word)
		expr = "long"
	case cpu.AddrLongX:
		if len(bytes) < 4 {
			return nil, ""
		}
		addr = ((uint32(bytes[3])<<16 | uint32(word)) + uint32(r.X)) & 0xffffff
		expr = "long,x"
	default:
		return nil, ""
	}
	return uint32Ptr(addr), expr
}

// peekDirectPageWord reads a pointer word from the direct page without
// side effects. It reports false unless both bytes are in work RAM.
func (c *runContext) peekDirectPageWord(r cpu.Snapshot, offset uint16) (uint16, bool) {
	lowAddr := directPageAddress(r.E, r.D, offset)
	highAddr := directPageAddress(r.E, r.D, offset+1)
	if r.E && r.D&0xff == 0 {
		highAddr = uint32(r.D&0xff00) | uint32((offset+1)&0x00ff)
	}
	low, ok1 := c.peekWRAM(lowAddr)
	high, ok2 := c.peekWRAM(highAddr)
	return uint16(low) | uint16(high)<<8, ok1 && ok2
}

// peekDirectPageLong reads a long pointer from the direct page without
// side effects. It reports false unless all bytes are in work RAM.
func (c *runContext) peekDirectPageLong(r cpu.Snapshot, offset uint16) (uint32, bool) {
	lowAddr := directPageAddress(r.E, r.D, offset)
	midAddr := directPageAddress(r.E, r.D, offset+1)
	highAddr := directPageAddress(r.E, r.D, offset+2)
	if r.E && r.D&0xff == 0 {
		page := uint32(r.D & 0xff00)
		midAddr = page | uint32((offset+1)&0x00ff)
		highAddr = page | uint32((offset+2)&0x00ff)
	}
	low, ok1 := c.peekWRAM(lowAddr)
	mid, ok2 := c.peekWRAM(midAddr)
	high, ok3 := c.peekWRAM(highAddr)
	return uint32(high)<<16 | uint32(mid)<<8 | uint32(low), ok1 && ok2 && ok3
}

func directPageAddress(emulation bool, dp, offset uint16) uint32 {
	if emulation && dp&0xff == 0 {
		return uint32((dp & 0xff00) | (offset & 0x00ff))
	}
	return uint32((dp + offset) & 0xffff)
}

func addressingName(mode cpu.AddressingMode) string {
	switch mode {
	case cpu.AddrDir:
		return "direct"
	case cpu.AddrDirX:
		return "direct_x"
	case cpu.AddrDirY:
		return "direct_y"
	case cpu.AddrIndX:
		return "direct_indexed_indirect"
	case cpu.AddrIndY:
		return "direct_indirect_indexed_y"
	case cpu.AddrDirInd:
		return "direct_indirect"
	case cpu.AddrDirIndL:
		return "direct_indirect_long"
	case cpu.AddrDirIndLIdxY:
		return "direct_indirect_long_y"
	case cpu.AddrSr:
		return "stack_relative"
	case cpu.AddrSrIndY:
		return "stack_relative_indirect_y"
	case cpu.AddrAbs:
		return "absolute"
	case cpu.AddrAbsX:
		return "absolute_x"
	case cpu.AddrAbsY:
		return "absolute_y"
	case cpu.AddrLong:
		return "long"
	case cpu.AddrLongX:
		return "long_x"
	default:
		return ""
	}
}

// cpuContext returns the CPU context for an event raised now: the
// instruction in progress, or else the last completed one.
func (c *runContext) cpuContext() *trace.CPUContext {
	if o, ok := c.sys.CPU.Current(); ok {
		return c.insnContext(o)
	}
	if c.hasLast {
		return c.insnContext(c.last)
	}
	return c.currentCPUContext()
}

func (c *runContext) currentCPUContext() *trace.CPUContext {
	return &trace.CPUContext{
		A:      c.sys.CPU.A,
		E:      c.sys.CPU.E,
		PBR:    c.sys.CPU.PB,
		PC:     c.sys.CPU.PC,
		DBR:    c.sys.CPU.DB,
		DP:     c.sys.CPU.D,
		X:      c.sys.CPU.X,
		Y:      c.sys.CPU.Y,
		S:      c.sys.CPU.S,
		P:      c.sys.CPU.P,
		MWidth: c.mWidth(),
		XWidth: c.xWidth(),
	}
}

func (c *runContext) mWidth() int {
	if c.sys.CPU.E || c.sys.CPU.P&0x20 != 0 {
		return 8
	}
	return 16
}

func (c *runContext) xWidth() int {
	if c.sys.CPU.E || c.sys.CPU.P&0x10 != 0 {
		return 8
	}
	return 16
}

func (c *runContext) dmaDest(dt dma.TransferTrace, count uint32) trace.Range {
	if count == 0 {
		count = 1
	}
	switch dt.Target {
	case 0x04:
		start := uint32(c.sys.PPU.OAMAddr & 0x03ff)
		end := start + count - 1
		if end >= 0x220 {
			end = 0x21f
		}
		return trace.Range{Space: "oam", Start: start, End: end}
	case 0x18, 0x19:
		start := uint32(c.sys.PPU.VRAMAddr) * 2
		end := start + count - 1
		if end >= 0x10000 {
			end = 0xffff
		}
		return trace.Range{Space: "vram", Start: start, End: end}
	case 0x22:
		start := uint32(c.sys.PPU.CGRAMAddr) * 2
		end := start + count - 1
		if end >= 0x200 {
			end = 0x1ff
		}
		return trace.Range{Space: "cgram", Start: start, End: end}
	default:
		dstSpace, dstStart := trace.CPUSpace(0x2100 | uint32(dt.Target))
		return trace.Range{Space: dstSpace, Start: dstStart, End: dstStart + count - 1}
	}
}

func (c *runContext) emitBus(op string, addr uint32, value uint8) {
	if !c.matchesPC(c.sys.CPU.LastOpcodePB, c.sys.CPU.LastOpcodePC) {
		return
	}
	if c.opFilter != "" && c.opFilter != op {
		return
	}
	if register, category := trace.InputRegister(addr); register != "" && c.events["input"] {
		c.tw.Emit(trace.Event{
			Kind:     "input",
			Frame:    c.frame,
			Cycle:    c.sys.CPU.Cycles,
			PC:       &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
			CPU:      c.cpuContext(),
			Register: register,
			Category: category,
			Space:    "cpu",
			Addr:     addr & 0xffffff,
			Width:    1,
			Value:    uint64(value),
			Op:       op,
		})
	}
	space, mapped := trace.CPUSpace(addr)
	var source trace.Range
	if op == "read" {
		if romAddr, ok := c.sys.ROMAddress(addr); ok {
			source = trace.Range{Space: "rom", Start: romAddr, End: romAddr}
		}
	}
	if len(c.filters) > 0 && !matchesBusFilters(c.filters, space, mapped, source) {
		return
	}
	kind := "bus"
	if space == "ppu" || space == "apu" || space == "dma" {
		kind = "mmio"
	}
	var before, after *uint64
	if op == "write" && space == "wram" {
		if v, ok := c.peekWRAM(addr); ok {
			before = uint64Ptr(uint64(v))
			after = uint64Ptr(uint64(value))
		}
	}
	register, category := trace.MMIORegister(mapped)
	if space == "apu" && c.events["apu"] {
		c.tw.Emit(trace.Event{
			Kind:     "apu",
			Frame:    c.frame,
			Cycle:    c.sys.CPU.Cycles,
			PC:       &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
			CPU:      c.cpuContext(),
			Register: register,
			Category: category,
			Space:    space,
			Addr:     mapped,
			Width:    1,
			Value:    uint64(value),
			Op:       op,
		})
	}
	if !c.events[kind] {
		return
	}
	c.tw.Emit(trace.Event{
		Kind:     kind,
		Frame:    c.frame,
		Cycle:    c.sys.CPU.Cycles,
		PC:       &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
		CPU:      c.cpuContext(),
		Register: register,
		Category: category,
		Space:    space,
		Addr:     mapped,
		Width:    1,
		Value:    uint64(value),
		Before:   before,
		After:    after,
		Op:       op,
		Source:   source,
	})
}

func (c *runContext) matchesPC(bank uint8, pc uint16) bool {
	if len(c.pcFilters) == 0 {
		return true
	}
	addr := uint32(bank)<<16 | uint32(pc)
	return matches(c.pcFilters, "cpu", addr)
}

func (c *runContext) matchesDMAChannel(channel int) bool {
	return len(c.dmaChannels) == 0 || c.dmaChannels[channel]
}

// peekWRAM reads the work RAM byte at CPU bus address addr without
// side effects. It reports false if addr does not map to work RAM.
func (c *runContext) peekWRAM(addr uint32) (uint8, bool) {
	bank, off := (addr>>16)&0xff, addr&0xffff
	var wram int64
	switch {
	case bank == 0x7e || bank == 0x7f:
		wram = int64(bank-0x7e)<<16 | int64(off)
	case bank&0x40 == 0 && off < 0x2000:
		wram = int64(off)
	default:
		return 0, false
	}
	var b [1]byte
	if _, err := c.sys.ReadWRAMAt(b[:], wram); err != nil {
		return 0, false
	}
	return b[0], true
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}

func (c *runContext) emitWatches(watches []trace.Watch) {
	for _, w := range watches {
		value := c.readWatch(w)
		c.tw.Emit(trace.Event{
			Kind:  "watch",
			Frame: c.frame,
			Name:  w.Name,
			Space: w.Range.Space,
			Addr:  w.Range.Start,
			Width: w.Width,
			Value: value,
		})
	}
}

func (c *runContext) watchValues(watches []trace.Watch) map[string]uint64 {
	if len(watches) == 0 {
		return nil
	}
	values := make(map[string]uint64, len(watches))
	for _, w := range watches {
		values[w.Name] = c.readWatch(w)
	}
	return values
}

func (c *runContext) readWatch(w trace.Watch) uint64 {
	if w.Range.Space != "wram" {
		return 0
	}
	readHook := c.sys.Bus.ReadHook
	c.sys.Bus.ReadHook = nil
	defer func() { c.sys.Bus.ReadHook = readHook }()
	addr := 0x7e0000 | w.Range.Start
	lo := uint64(c.sys.Bus.Read(addr))
	if w.Width == 1 {
		return lo
	}
	hi := uint64(c.sys.Bus.Read(addr + 1))
	return lo | hi<<8
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "snestrace query: missing query")
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("snestrace query "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	tracePath := fs.String("trace", "", "trace JSONL path")
	leftPath := fs.String("left", "", "left trace JSONL path")
	rightPath := fs.String("right", "", "right trace JSONL path")
	addrFlag := fs.String("addr", "", "address or range")
	pcFlag := fs.String("pc", "", "CPU PC address or range")
	destFlag := fs.String("dest", "", "destination address or range")
	eventID := fs.Uint64("event", 0, "event id")
	before := fs.Int("before", 20, "events before")
	after := fs.Int("after", 20, "events after")
	maxMatches := fs.Int("max-matches", 0, "maximum pc-context matches to return; 0 means unlimited")
	frame := fs.Int("frame", 0, "frame number")
	frameStart := fs.Int("frame-start", -1, "first frame to include")
	frameEnd := fs.Int("frame-end", -1, "last frame to include")
	format := fs.String("format", "json", "output format: json")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *before < 0 || *after < 0 {
		fmt.Fprintln(stderr, "snestrace query: before and after must be nonnegative")
		return 2
	}
	if *format != "json" {
		fmt.Fprintf(stderr, "snestrace query: unsupported format %q\n", *format)
		return 2
	}
	if name == "first-difference" {
		if *leftPath == "" || *rightPath == "" {
			fmt.Fprintln(stderr, "snestrace query first-difference: --left and --right are required")
			return 2
		}
		left, err := readTraceFile(*leftPath)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query first-difference: read left: %v\n", err)
			return 1
		}
		right, err := readTraceFile(*rightPath)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query first-difference: read right: %v\n", err)
			return 1
		}
		result := firstDifference(left, right)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
			return 1
		}
		return 0
	}
	if *tracePath == "" {
		fmt.Fprintln(stderr, "snestrace query: --trace is required")
		return 2
	}
	events, err := readTraceOrIndexFile(*tracePath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace query: read trace: %v\n", err)
		return 1
	}
	q := trace.Query{Events: events}
	var out []trace.Event
	var explain *explainWriterResult
	var pcContext *pcContextResult
	switch name {
	case "writers":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query writers: %v\n", err)
			return 2
		}
		out = q.WritersInFrameRange(r, *frameStart, *frameEnd)
	case "explain-writer":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query explain-writer: %v\n", err)
			return 2
		}
		out = q.ExplainWriters(r, *frameStart, *frameEnd)
		explain = newExplainWriterResult(r, *frameStart, *frameEnd, out)
	case "last-writer-at-frame":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query last-writer-at-frame: %v\n", err)
			return 2
		}
		out = q.LastWriterAtFrame(r, *frame)
		explain = newExplainWriterResult(r, -1, *frame, out)
	case "readers":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query readers: %v\n", err)
			return 2
		}
		out = q.ReadersInFrameRange(r, *frameStart, *frameEnd)
	case "dma-for-dest":
		r, err := trace.ParseRange(*destFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query dma-for-dest: %v\n", err)
			return 2
		}
		out = q.DMAForDestInFrameRange(r, *frameStart, *frameEnd)
	case "bus-for-pc":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query bus-for-pc: %v\n", err)
			return 2
		}
		out = q.BusForPCInFrameRange(r, *frameStart, *frameEnd)
	case "pc-context":
		if *maxMatches < 0 {
			fmt.Fprintln(stderr, "snestrace query pc-context: --max-matches must be >= 0")
			return 2
		}
		r, err := parsePCRange(*pcFlag, *addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query pc-context: %v\n", err)
			return 2
		}
		pcContext = newPCContextResult(q, r, *frameStart, *frameEnd, *before, *after, *maxMatches)
	case "trace-window":
		out = q.TraceWindow(*eventID, *before, *after)
	case "frame-summary":
		out = q.FrameSummary(*frame)
	default:
		fmt.Fprintf(stderr, "snestrace query: unknown query %q\n", name)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if explain != nil {
		if err := enc.Encode(explain); err != nil {
			fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
			return 1
		}
		return 0
	}
	if pcContext != nil {
		if err := enc.Encode(pcContext); err != nil {
			fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
			return 1
		}
		return 0
	}
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
		return 1
	}
	return 0
}

type pcContextResult struct {
	Schema       int              `json:"schema"`
	Query        trace.Range      `json:"query"`
	FrameStart   int              `json:"frame_start,omitempty"`
	FrameEnd     int              `json:"frame_end,omitempty"`
	Before       int              `json:"before"`
	After        int              `json:"after"`
	MaxMatches   int              `json:"max_matches,omitempty"`
	Truncated    bool             `json:"truncated,omitempty"`
	EventCount   int              `json:"event_count"`
	SemanticHash string           `json:"semantic_hash"`
	Matches      []pcContextMatch `json:"matches"`
}

type pcContextMatch struct {
	Event  trace.Event   `json:"event"`
	Window []trace.Event `json:"window"`
}

func newPCContextResult(q trace.Query, r trace.Range, startFrame, endFrame, before, after, maxMatches int) *pcContextResult {
	matches := q.BusForPCInFrameRange(r, startFrame, endFrame)
	truncated := false
	if maxMatches > 0 && len(matches) > maxMatches {
		matches = matches[:maxMatches]
		truncated = true
	}
	result := &pcContextResult{
		Schema:     trace.SchemaVersion,
		Query:      r,
		FrameStart: startFrame,
		FrameEnd:   endFrame,
		Before:     before,
		After:      after,
		MaxMatches: maxMatches,
		Truncated:  truncated,
		EventCount: len(matches),
		Matches:    make([]pcContextMatch, 0, len(matches)),
	}
	for _, e := range matches {
		result.Matches = append(result.Matches, pcContextMatch{
			Event:  e,
			Window: q.TraceWindow(e.ID, before, after),
		})
	}
	result.SemanticHash = pcContextHash(*result)
	return result
}

func pcContextHash(result pcContextResult) string {
	result.SemanticHash = ""
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return hexHash(data)
}

func parsePCRange(pcText, addrText string) (trace.Range, error) {
	if strings.TrimSpace(pcText) != "" {
		return trace.ParseRange(pcText)
	}
	return trace.ParseRange(addrText)
}

type firstDifferenceResult struct {
	Schema       int          `json:"schema"`
	EventIndex   int          `json:"event_index"`
	Reason       string       `json:"reason"`
	SemanticHash string       `json:"semantic_hash"`
	Left         *trace.Event `json:"left,omitempty"`
	Right        *trace.Event `json:"right,omitempty"`
}

func firstDifference(left, right []trace.Event) firstDifferenceResult {
	leftSig := comparableEvents(left)
	rightSig := comparableEvents(right)
	n := len(leftSig)
	if len(rightSig) < n {
		n = len(rightSig)
	}
	result := firstDifferenceResult{Schema: trace.SchemaVersion, EventIndex: -1}
	for i := 0; i < n; i++ {
		if !sameComparableEvent(leftSig[i], rightSig[i]) {
			result.EventIndex = i
			result.Reason = "event_mismatch"
			result.Left = &leftSig[i]
			result.Right = &rightSig[i]
			result.SemanticHash = firstDifferenceHash(result)
			return result
		}
	}
	if len(leftSig) != len(rightSig) {
		result.EventIndex = n
		result.Reason = "event_count_mismatch"
		if len(leftSig) > n {
			result.Left = &leftSig[n]
		}
		if len(rightSig) > n {
			result.Right = &rightSig[n]
		}
	} else {
		result.Reason = "match"
	}
	result.SemanticHash = firstDifferenceHash(result)
	return result
}

func comparableEvents(events []trace.Event) []trace.Event {
	out := make([]trace.Event, 0, len(events))
	for _, e := range events {
		switch e.Kind {
		case "frame", "watch", "input":
			out = append(out, e)
		}
	}
	return out
}

func sameComparableEvent(a, b trace.Event) bool {
	return a.Kind == b.Kind &&
		a.Frame == b.Frame &&
		a.Name == b.Name &&
		a.Space == b.Space &&
		a.Addr == b.Addr &&
		a.Width == b.Width &&
		a.Value == b.Value &&
		a.Hash == b.Hash
}

func firstDifferenceHash(result firstDifferenceResult) string {
	result.SemanticHash = ""
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return hexHash(data)
}

type explainWriterResult struct {
	Schema       int           `json:"schema"`
	Query        trace.Range   `json:"query"`
	FrameStart   int           `json:"frame_start,omitempty"`
	FrameEnd     int           `json:"frame_end,omitempty"`
	EventCount   int           `json:"event_count"`
	SemanticHash string        `json:"semantic_hash"`
	Events       []trace.Event `json:"events"`
}

func newExplainWriterResult(r trace.Range, startFrame, endFrame int, events []trace.Event) *explainWriterResult {
	result := &explainWriterResult{
		Schema:     trace.SchemaVersion,
		Query:      r,
		FrameStart: startFrame,
		FrameEnd:   endFrame,
		EventCount: len(events),
		Events:     events,
	}
	result.SemanticHash = explainWriterHash(*result)
	return result
}

func explainWriterHash(result explainWriterResult) string {
	result.SemanticHash = ""
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return hexHash(data)
}

func runIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snestrace index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tracePath := fs.String("trace", "", "trace JSONL path")
	outPath := fs.String("out", "", "index JSON output path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tracePath == "" || *outPath == "" {
		fmt.Fprintln(stderr, "snestrace index: --trace and --out are required")
		return 2
	}
	events, err := readTraceFile(*tracePath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace index: %v\n", err)
		return 1
	}
	idx := buildIndex(*tracePath, events)
	f, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace index: create index: %v\n", err)
		return 1
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		fmt.Fprintf(stderr, "snestrace index: encode index: %v\n", err)
		return 1
	}
	return 0
}

func readTraceFile(path string) ([]trace.Event, error) {
	r, closeFn, err := openTrace(path)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer closeFn()
	events, err := trace.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode trace: %w", err)
	}
	return events, nil
}

func openTrace(path string) (io.Reader, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	var header [2]byte
	n, readErr := f.Read(header[:])
	if readErr != nil && readErr != io.EOF {
		_ = f.Close()
		return nil, nil, readErr
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if n == 2 && header[0] == 0x1f && header[1] == 0x8b {
		gr, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		return gr, func() error {
			err1 := gr.Close()
			err2 := f.Close()
			if err1 != nil {
				return err1
			}
			return err2
		}, nil
	}
	return f, f.Close, nil
}

func readTraceOrIndexFile(path string) ([]trace.Event, error) {
	events, err := readTraceFile(path)
	if err == nil && validTraceEvents(events) {
		return events, nil
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, err
	}
	var idx indexFile
	if jsonErr := json.Unmarshal(data, &idx); jsonErr != nil || idx.TracePath == "" {
		return nil, err
	}
	tracePath := idx.TracePath
	events, traceErr := readTraceFile(tracePath)
	if traceErr == nil {
		return events, nil
	}
	if !filepath.IsAbs(tracePath) {
		events, relErr := readTraceFile(filepath.Join(filepath.Dir(path), tracePath))
		if relErr == nil {
			return events, nil
		}
	}
	return nil, traceErr
}

func validTraceEvents(events []trace.Event) bool {
	for _, e := range events {
		if e.Kind == "" {
			return false
		}
	}
	return true
}

type indexFile struct {
	Schema       int                       `json:"schema"`
	TracePath    string                    `json:"trace_path"`
	EventCount   int                       `json:"event_count"`
	FrameStart   int                       `json:"frame_start,omitempty"`
	FrameEnd     int                       `json:"frame_end,omitempty"`
	Kinds        map[string]int            `json:"kinds"`
	Spaces       map[string]int            `json:"spaces,omitempty"`
	AddressRange map[string][]summaryRange `json:"address_ranges,omitempty"`
	DMADest      []summaryRange            `json:"dma_dest,omitempty"`
	PCRanges     []summaryRange            `json:"pc_ranges,omitempty"`
}

type summaryRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

func buildIndex(path string, events []trace.Event) indexFile {
	idx := newIndexFile(path)
	for i, e := range events {
		addIndexEvent(&idx, i, e)
	}
	finishIndex(&idx)
	return idx
}

func newIndexFile(path string) indexFile {
	return indexFile{
		Schema:       trace.SchemaVersion,
		TracePath:    path,
		Kinds:        map[string]int{},
		Spaces:       map[string]int{},
		AddressRange: map[string][]summaryRange{},
	}
}

func addIndexEvent(idx *indexFile, i int, e trace.Event) {
	idx.EventCount++
	idx.Kinds[e.Kind]++
	if i == 0 || e.Frame < idx.FrameStart {
		idx.FrameStart = e.Frame
	}
	if i == 0 || e.Frame > idx.FrameEnd {
		idx.FrameEnd = e.Frame
	}
	if e.Space != "" {
		idx.Spaces[e.Space]++
		idx.AddressRange[e.Space] = mergeSummaryRange(idx.AddressRange[e.Space], summaryRange{Start: e.Addr, End: e.Addr + uint32(max(1, e.Width)) - 1})
	}
	if e.Kind == "dma" && e.Dest.Space != "" {
		idx.DMADest = mergeSummaryRange(idx.DMADest, summaryRange{Start: e.Dest.Start, End: e.Dest.End})
	}
	if e.PC != nil {
		pc := uint32(e.PC.Bank)<<16 | uint32(e.PC.Addr)
		idx.PCRanges = mergeSummaryRange(idx.PCRanges, summaryRange{Start: pc, End: pc})
	}
}

func finishIndex(idx *indexFile) {
	for space := range idx.AddressRange {
		sortSummaryRanges(idx.AddressRange[space])
	}
	sortSummaryRanges(idx.DMADest)
	sortSummaryRanges(idx.PCRanges)
}

func mergeSummaryRange(ranges []summaryRange, r summaryRange) []summaryRange {
	if r.End < r.Start {
		r.End = r.Start
	}
	for i := range ranges {
		if r.Start <= ranges[i].End+1 && ranges[i].Start <= r.End+1 {
			if r.Start < ranges[i].Start {
				ranges[i].Start = r.Start
			}
			if r.End > ranges[i].End {
				ranges[i].End = r.End
			}
			return ranges
		}
	}
	return append(ranges, r)
}

func sortSummaryRanges(ranges []summaryRange) {
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})
}

type summary struct {
	ROMPath              string         `json:"rom_path"`
	ROMHash              string         `json:"rom_hash"`
	StatePath            string         `json:"state_path,omitempty"`
	StateHash            string         `json:"state_hash,omitempty"`
	StartBoundary        string         `json:"start_boundary"`
	InputPath            string         `json:"input_path,omitempty"`
	InputHash            string         `json:"input_hash,omitempty"`
	WatchNames           []string       `json:"watch_names,omitempty"`
	TracePath            string         `json:"trace_path"`
	TraceHash            string         `json:"trace_hash,omitempty"`
	TraceCompression     string         `json:"trace_compression,omitempty"`
	TraceCompressedHash  string         `json:"trace_compressed_hash,omitempty"`
	TraceCompressedBytes int64          `json:"trace_compressed_bytes,omitempty"`
	FramePNGDir          string         `json:"frame_png_dir,omitempty"`
	FramePNGEvery        int            `json:"frame_png_every,omitempty"`
	SummaryHash          string         `json:"summary_hash,omitempty"`
	Emulator             string         `json:"emulator"`
	Frames               int            `json:"frames"`
	FrameSummary         []frameSummary `json:"frame_summary,omitempty"`
	EventKinds           []string       `json:"event_kinds"`
	EventCount           int            `json:"event_count"`
	EventKindCounts      map[string]int `json:"event_kind_counts,omitempty"`
	MaxEvents            int            `json:"max_events,omitempty"`
	MaxBytes             int            `json:"max_bytes,omitempty"`
	TraceBytes           int            `json:"trace_bytes,omitempty"`
	Truncated            bool           `json:"truncated,omitempty"`
	AddressRange         []trace.Range  `json:"address_ranges,omitempty"`
	PCRange              []trace.Range  `json:"pc_ranges,omitempty"`
	Op                   string         `json:"op,omitempty"`
	DMAChannel           []int          `json:"dma_channels,omitempty"`
}

type frameSummary struct {
	Frame           int               `json:"frame"`
	StateHash       string            `json:"state_hash"`
	FrameBufferHash string            `json:"framebuffer_hash,omitempty"`
	ComponentHashes map[string]string `json:"component_hashes,omitempty"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
}

func hashBGR555Frame(fb []uint16) string {
	buf := make([]byte, 0, len(fb)*2)
	for _, px := range fb {
		buf = append(buf, byte(px), byte(px>>8))
	}
	return hexHash(buf)
}

// parseFrameSet parses a comma-separated list of frame numbers, or
// "all". It returns nil for "".
func parseFrameSet(s string) (func(int) bool, error) {
	switch s {
	case "":
		return nil, nil
	case "all":
		return func(int) bool { return true }, nil
	}
	set := make(map[int]bool)
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid frame number %q", f)
		}
		set[n] = true
	}
	return func(n int) bool { return set[n] }, nil
}

func writeFramePNG(path string, fb []uint16) error {
	const width = 256
	if len(fb) == 0 || len(fb)%width != 0 {
		return fmt.Errorf("invalid framebuffer length %d", len(fb))
	}
	img := image.NewRGBA(image.Rect(0, 0, width, len(fb)/width))
	for i, px := range fb {
		r5 := px & 0x1f
		g5 := (px >> 5) & 0x1f
		b5 := (px >> 10) & 0x1f
		j := i * 4
		img.Pix[j] = uint8((r5 * 255) / 31)
		img.Pix[j+1] = uint8((g5 * 255) / 31)
		img.Pix[j+2] = uint8((b5 * 255) / 31)
		img.Pix[j+3] = 0xff
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create png: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}

func writeSummary(path string, s summary) error {
	s.SummaryHash = summaryHash(s)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func writeReplayViewer(path, summaryPath, framePNGDir string, framePNGEvery int) error {
	if framePNGDir == "" {
		return fmt.Errorf("--viewer requires --frame-png-dir or replay output frames")
	}
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		return fmt.Errorf("read summary: %w", err)
	}
	var s summary
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("parse summary: %w", err)
	}
	frames := make([]viewerFrame, 0, len(s.FrameSummary))
	for _, fs := range s.FrameSummary {
		if fs.Frame%framePNGEvery != 0 {
			continue
		}
		imagePath := filepath.Join(framePNGDir, fmt.Sprintf("frame_%06d.png", fs.Frame))
		rel, err := filepath.Rel(filepath.Dir(path), imagePath)
		if err == nil {
			imagePath = rel
		}
		frames = append(frames, viewerFrame{
			Frame:           fs.Frame,
			Image:           filepath.ToSlash(imagePath),
			StateHash:       fs.StateHash,
			FrameBufferHash: fs.FrameBufferHash,
			Watches:         fs.Watches,
		})
	}
	payload := struct {
		ROMHash     string        `json:"rom_hash"`
		StateHash   string        `json:"state_hash,omitempty"`
		InputHash   string        `json:"input_hash,omitempty"`
		TraceHash   string        `json:"trace_hash,omitempty"`
		SummaryHash string        `json:"summary_hash,omitempty"`
		Frames      []viewerFrame `json:"frames"`
	}{
		ROMHash:     s.ROMHash,
		StateHash:   s.StateHash,
		InputHash:   s.InputHash,
		TraceHash:   s.TraceHash,
		SummaryHash: s.SummaryHash,
		Frames:      frames,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal viewer data: %w", err)
	}
	html := strings.Replace(replayViewerHTML, "__DATA__", string(payloadJSON), 1)
	return os.WriteFile(path, []byte(html), 0666)
}

type viewerFrame struct {
	Frame           int               `json:"frame"`
	Image           string            `json:"image"`
	StateHash       string            `json:"state_hash,omitempty"`
	FrameBufferHash string            `json:"framebuffer_hash,omitempty"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
}

const replayViewerHTML = `<!doctype html>
<meta charset="utf-8">
<title>snestrace replay viewer</title>
<style>
body{font-family:system-ui,sans-serif;margin:20px;background:#111;color:#eee}
main{display:grid;grid-template-columns:minmax(280px,512px) minmax(260px,1fr);gap:20px;align-items:start}
img{width:100%;image-rendering:pixelated;background:#000;border:1px solid #444}
input[type=range]{width:100%}
pre{white-space:pre-wrap;background:#1b1b1b;padding:12px;border:1px solid #333;overflow:auto}
button{margin-right:8px}
</style>
<main>
  <section>
    <img id="screen" alt="frame">
    <p><input id="slider" type="range" min="0" max="0" value="0"></p>
    <button id="prev">Prev</button><button id="play">Play</button><button id="next">Next</button>
  </section>
  <section>
    <h1>snestrace replay</h1>
    <pre id="meta"></pre>
  </section>
</main>
<script>
const replay = __DATA__;
let pos = 0, timer = 0;
const screen = document.getElementById("screen");
const slider = document.getElementById("slider");
const meta = document.getElementById("meta");
const play = document.getElementById("play");
slider.max = Math.max(0, replay.frames.length - 1);
function show(i) {
  if (!replay.frames.length) return;
  pos = Math.max(0, Math.min(replay.frames.length - 1, i));
  const f = replay.frames[pos];
  screen.src = f.image;
  slider.value = pos;
  meta.textContent = JSON.stringify({
    frame: f.frame,
    watches: f.watches || {},
    framebuffer_hash: f.framebuffer_hash,
    state_hash: f.state_hash,
    trace_hash: replay.trace_hash,
    summary_hash: replay.summary_hash,
    rom_hash: replay.rom_hash
  }, null, 2);
}
document.getElementById("prev").onclick = () => show(pos - 1);
document.getElementById("next").onclick = () => show(pos + 1);
slider.oninput = () => show(Number(slider.value));
play.onclick = () => {
  if (timer) { clearInterval(timer); timer = 0; play.textContent = "Play"; return; }
  timer = setInterval(() => show((pos + 1) % replay.frames.length), 100);
  play.textContent = "Pause";
};
show(0);
</script>
`

func summaryHash(s summary) string {
	s.SummaryHash = ""
	s.ROMPath = ""
	s.StatePath = ""
	s.InputPath = ""
	s.TracePath = ""
	s.FramePNGDir = ""
	s.FramePNGEvery = 0
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return hexHash(data)
}

func omitDefaultOne(n int) int {
	if n == 1 {
		return 0
	}
	return n
}

func startBoundary(statePath string) string {
	if statePath != "" {
		return "restored_state"
	}
	return "fresh_frame"
}

func loadWatches(path, names string) ([]trace.Watch, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open watch profile: %w", err)
	}
	defer f.Close()
	watches, err := trace.ParseWatches(f)
	if err != nil {
		return nil, err
	}
	return filterWatches(watches, parseSet(names)), nil
}

func filterWatches(watches []trace.Watch, names map[string]bool) []trace.Watch {
	if len(names) == 0 {
		return watches
	}
	var out []trace.Watch
	for _, w := range watches {
		if names[w.Name] {
			out = append(out, w)
		}
	}
	return out
}

func loadInputs(path string) (map[int]uint16, error) {
	inputs := map[int]uint16{}
	if path == "" {
		return inputs, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inputs: %w", err)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse inputs: %w", err)
	}
	for i, msg := range raw {
		var n uint16
		if err := json.Unmarshal(msg, &n); err == nil {
			inputs[i] = n
			continue
		}
		var rec struct {
			Frame int    `json:"frame"`
			Port  uint   `json:"port"`
			State string `json:"state"`
		}
		if err := json.Unmarshal(msg, &rec); err != nil {
			return nil, fmt.Errorf("parse input %d: %w", i, err)
		}
		if rec.Port > 0 {
			continue
		}
		state, err := parseButtons(rec.State)
		if err != nil {
			return nil, fmt.Errorf("parse input %d: %w", i, err)
		}
		inputs[rec.Frame] = state
	}
	return inputs, nil
}

func parseButtons(s string) (uint16, error) {
	var state uint16
	for _, part := range strings.Split(s, "+") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "", "none":
		case "b":
			state |= emulator.StandardButtonB
		case "y":
			state |= emulator.StandardButtonY
		case "select":
			state |= emulator.StandardButtonSelect
		case "start":
			state |= emulator.StandardButtonStart
		case "up":
			state |= emulator.StandardButtonUp
		case "down":
			state |= emulator.StandardButtonDown
		case "left":
			state |= emulator.StandardButtonLeft
		case "right":
			state |= emulator.StandardButtonRight
		case "a":
			state |= emulator.StandardButtonA
		case "x":
			state |= emulator.StandardButtonX
		case "l":
			state |= emulator.StandardButtonL
		case "r":
			state |= emulator.StandardButtonR
		default:
			return 0, fmt.Errorf("unknown button %q", part)
		}
	}
	return state, nil
}

func parseSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			set[part] = true
		}
	}
	return set
}

func parseRanges(s string) ([]trace.Range, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var ranges []trace.Range
	for _, part := range strings.Split(s, ",") {
		r, err := trace.ParseRange(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}
	return ranges, nil
}

func parseOpFilter(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "read", "write":
		return s, nil
	default:
		return "", fmt.Errorf("invalid --op %q: want read or write", s)
	}
}

func parseDMAChannels(s string) (map[int]bool, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	channels := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		text := strings.TrimSpace(part)
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 7 {
			return nil, fmt.Errorf("invalid --dma-channel %q: want channels 0-7", text)
		}
		channels[n] = true
	}
	return channels, nil
}

func keysInt(set map[int]bool) []int {
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func keysString(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatRange(r trace.Range) string {
	if r.Start == r.End {
		return fmt.Sprintf("%s:0x%x", r.Space, r.Start)
	}
	return fmt.Sprintf("%s:0x%x-0x%x", r.Space, r.Start, r.End)
}

func matches(ranges []trace.Range, space string, addr uint32) bool {
	for _, r := range ranges {
		if r.Contains(space, addr) {
			return true
		}
	}
	return false
}

func matchesBusFilters(ranges []trace.Range, space string, addr uint32, source trace.Range) bool {
	for _, r := range ranges {
		if r.Contains(space, addr) || source.Intersects(r) {
			return true
		}
	}
	return false
}

func stateHash(sys *snes.System) (string, error) {
	data, err := sys.Serialize()
	if err != nil {
		return "", err
	}
	return hexHash(data), nil
}

func hexHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashOptional(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return hexHash(data)
}

func hashFileOptional(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return hexHash(data)
}

func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	if out, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output(); err == nil {
		if rev := strings.TrimSpace(string(out)); rev != "" {
			return rev
		}
	}
	return "unknown"
}

func keys(set map[string]bool) []string {
	var out []string
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 0, 64)
}

var _ = parseUint

// runInfo returns the header record of a cpu_insn/cpu_transition stream.
func runInfo(sys *snes.System, events map[string]bool, pcRanges []trace.Range, state, inputs []byte, maxEvents, maxBytes, frames int) *trace.RunInfo {
	romHash := sys.ROMSHA256()
	mapper, ok := sys.ROMProvenance()
	provenance := mapper
	if !ok {
		provenance = "unsupported"
	}
	rev, dirty, dirtyHash := engineRevision()
	info := &trace.RunInfo{
		ROMSHA256:         hex.EncodeToString(romHash[:]),
		Mapper:            mapper,
		ROMProvenance:     provenance,
		EngineRevision:    rev,
		EngineDirty:       dirty,
		EngineDirtySHA256: dirtyHash,
		Start:             "power_on",
		ReplayInputSHA256: hashOptional(inputs),
		Events:            keys(events),
		Limits:            trace.Limits{Events: maxEvents, Bytes: maxBytes, Frames: frames},
	}
	if state != nil {
		info.Start = "checkpoint"
		info.InitialStateSHA256 = hexHash(state)
	}
	if len(pcRanges) > 0 {
		info.Filters = &trace.Filters{PCRanges: pcRanges}
	}
	return info
}

// engineRevision reports the engine's VCS revision, whether its tree
// had local changes, and, if so and the working directory is that
// tree, the SHA-256 of "git diff HEAD".
func engineRevision() (rev string, dirty bool, dirtyHash string) {
	rev = buildRevision()
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.modified" {
				dirty = s.Value == "true"
			}
		}
	}
	head, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != rev {
		return rev, dirty, ""
	}
	diff, err := exec.Command("git", "diff", "HEAD").Output()
	if err != nil {
		return rev, dirty, ""
	}
	if len(diff) > 0 {
		dirty = true
		dirtyHash = hexHash(diff)
	}
	return rev, dirty, dirtyHash
}
