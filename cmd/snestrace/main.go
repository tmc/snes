// snestrace records and queries structured SNES execution traces.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	snes "github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/dma"
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
	case "query":
		return runQuery(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "snestrace: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: snestrace run [flags] | snestrace query <writers|readers|dma-for-dest|trace-window|frame-summary> [flags]")
}

func runTrace(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snestrace run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	romPath := fs.String("rom", "", "ROM path")
	statePath := fs.String("state", "", "save-state path")
	allowStateROMMismatch := fs.Bool("allow-state-rom-mismatch", false, "restore state even if its embedded ROM hash differs")
	inputPath := fs.String("inputs", "", "input trace JSON path")
	watchPath := fs.String("watch", "", "watch profile path")
	eventsFlag := fs.String("events", "frame,input,bus,mmio,dma,watch", "comma-separated event kinds")
	addrFlag := fs.String("addr", "", "comma-separated address filters such as wram:0x20-0x2f,vram:0x4000-0x47ff")
	frames := fs.Int("frames", 0, "frames to run")
	outPath := fs.String("out", "", "trace JSONL output path")
	summaryPath := fs.String("summary", "", "summary JSON output path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *romPath == "" || *outPath == "" || *frames < 0 {
		fmt.Fprintln(stderr, "snestrace run: --rom, --out, and --frames >= 0 are required")
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
	sys.Power()
	if *statePath != "" {
		state, err := os.ReadFile(*statePath)
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
	}

	eventSet := parseSet(*eventsFlag)
	ranges, err := parseRanges(*addrFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	watches, err := loadWatches(*watchPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	inputs, err := loadInputs(*inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: %v\n", err)
		return 2
	}
	var inputBytes []byte
	if *inputPath != "" {
		inputBytes, _ = os.ReadFile(*inputPath)
	}

	out, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace run: create trace: %v\n", err)
		return 1
	}
	defer out.Close()
	tw := trace.NewWriter(out)
	ctx := &runContext{sys: sys, tw: tw, events: eventSet, filters: ranges}
	ctx.installHooks()

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
				_ = tw.Emit(trace.Event{Kind: "input", Frame: frame, Value: uint64(state), Width: 2})
			}
		}
		if err := sys.Run(); err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame %d: %v\n", frame, err)
			return 1
		}
		if eventSet["watch"] {
			ctx.emitWatches(watches)
		}
		hash, err := stateHash(sys)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace run: frame %d state hash: %v\n", frame, err)
			return 1
		}
		frameOut := frameSummary{Frame: frame, StateHash: hash, Watches: ctx.watchValues(watches)}
		framesOut = append(framesOut, frameOut)
		if eventSet["frame"] {
			_ = tw.Emit(trace.Event{Kind: "frame", Frame: frame, Name: "state", Hash: hash})
		}
	}

	if *summaryPath != "" {
		var stateHashText string
		if *statePath != "" {
			stateBytes, _ := os.ReadFile(*statePath)
			stateHashText = hexHash(stateBytes)
		}
		if err := writeSummary(*summaryPath, summary{
			ROMPath:      *romPath,
			ROMHash:      hexHash(rom),
			StatePath:    *statePath,
			StateHash:    stateHashText,
			InputPath:    *inputPath,
			InputHash:    hashOptional(inputBytes),
			TracePath:    *outPath,
			Emulator:     buildRevision(),
			Frames:       *frames,
			FrameSummary: framesOut,
			EventKinds:   keys(eventSet),
			AddressRange: ranges,
		}); err != nil {
			fmt.Fprintf(stderr, "snestrace run: write summary: %v\n", err)
			return 1
		}
	}
	return 0
}

type runContext struct {
	sys     *snes.System
	tw      *trace.Writer
	events  map[string]bool
	filters []trace.Range
	frame   int
}

func (c *runContext) installHooks() {
	if c.events["bus"] || c.events["mmio"] {
		c.sys.Bus.ReadHook = func(addr uint32, value uint8) {
			c.emitBus("read", addr, value)
		}
		c.sys.Bus.WriteHook = func(addr uint32, value uint8) {
			c.emitBus("write", addr, value)
		}
	}
	if c.events["cpu_block"] {
		prev := c.sys.CPU.BeforeExecute
		c.sys.CPU.BeforeExecute = func() {
			_ = c.tw.Emit(trace.Event{
				Kind:  "cpu_block",
				Frame: c.frame,
				Cycle: c.sys.CPU.Cycles,
				PC:    &trace.PC{Bank: c.sys.CPU.PB, Addr: c.sys.CPU.PC - 1},
				Value: uint64(c.sys.CPU.P),
			})
			if prev != nil {
				prev()
			}
		}
	}
	if c.events["dma"] {
		c.sys.DMA.Trace = func(dt dma.TransferTrace) {
			count := uint32(dt.Count)
			src := uint32(dt.SrcBank)<<16 | uint32(dt.SrcAddr)
			dst := c.dmaDest(dt, count)
			_ = c.tw.Emit(trace.Event{
				Kind:    "dma",
				Frame:   c.frame,
				Cycle:   c.sys.CPU.Cycles,
				PC:      &trace.PC{Bank: c.sys.CPU.PB, Addr: c.sys.CPU.PC},
				Channel: dt.Channel,
				Mode:    dt.Control,
				Source:  trace.Range{Space: "cpu", Start: src, End: src + count - 1},
				Dest:    dst,
			})
		}
	}
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
	space, mapped := trace.CPUSpace(addr)
	if len(c.filters) > 0 && !matches(c.filters, space, mapped) {
		return
	}
	kind := "bus"
	if space == "ppu" || space == "apu" || space == "dma" {
		kind = "mmio"
	}
	if !c.events[kind] {
		return
	}
	_ = c.tw.Emit(trace.Event{
		Kind:  kind,
		Frame: c.frame,
		Cycle: c.sys.CPU.Cycles,
		PC:    &trace.PC{Bank: c.sys.CPU.PB, Addr: c.sys.CPU.PC},
		Space: space,
		Addr:  mapped,
		Width: 1,
		Value: uint64(value),
		Op:    op,
	})
}

func (c *runContext) emitWatches(watches []trace.Watch) {
	for _, w := range watches {
		value := c.readWatch(w)
		_ = c.tw.Emit(trace.Event{
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
	addrFlag := fs.String("addr", "", "address or range")
	destFlag := fs.String("dest", "", "destination address or range")
	eventID := fs.Uint64("event", 0, "event id")
	before := fs.Int("before", 20, "events before")
	after := fs.Int("after", 20, "events after")
	frame := fs.Int("frame", 0, "frame number")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *tracePath == "" {
		fmt.Fprintln(stderr, "snestrace query: --trace is required")
		return 2
	}
	f, err := os.Open(*tracePath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace query: open trace: %v\n", err)
		return 1
	}
	defer f.Close()
	events, err := trace.Decode(f)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace query: decode trace: %v\n", err)
		return 1
	}
	q := trace.Query{Events: events}
	var out []trace.Event
	switch name {
	case "writers":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query writers: %v\n", err)
			return 2
		}
		out = q.Writers(r)
	case "readers":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query readers: %v\n", err)
			return 2
		}
		out = q.Readers(r)
	case "dma-for-dest":
		r, err := trace.ParseRange(*destFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query dma-for-dest: %v\n", err)
			return 2
		}
		out = q.DMAForDest(r)
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
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
		return 1
	}
	return 0
}

type summary struct {
	ROMPath      string         `json:"rom_path"`
	ROMHash      string         `json:"rom_hash"`
	StatePath    string         `json:"state_path,omitempty"`
	StateHash    string         `json:"state_hash,omitempty"`
	InputPath    string         `json:"input_path,omitempty"`
	InputHash    string         `json:"input_hash,omitempty"`
	TracePath    string         `json:"trace_path"`
	Emulator     string         `json:"emulator"`
	Frames       int            `json:"frames"`
	FrameSummary []frameSummary `json:"frame_summary,omitempty"`
	EventKinds   []string       `json:"event_kinds"`
	AddressRange []trace.Range  `json:"address_ranges,omitempty"`
}

type frameSummary struct {
	Frame     int               `json:"frame"`
	StateHash string            `json:"state_hash"`
	Watches   map[string]uint64 `json:"watches,omitempty"`
}

func writeSummary(path string, s summary) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func loadWatches(path string) ([]trace.Watch, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open watch profile: %w", err)
	}
	defer f.Close()
	return trace.ParseWatches(f)
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

func matches(ranges []trace.Range, space string, addr uint32) bool {
	for _, r := range ranges {
		if r.Contains(space, addr) {
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

func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return setting.Value
		}
	}
	return "unknown"
}

func keys(set map[string]bool) []string {
	var out []string
	for key := range set {
		out = append(out, key)
	}
	return out
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 0, 64)
}

var _ = parseUint
