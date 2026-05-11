// snestrace records and queries structured SNES execution traces.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	snes "github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/cpu"
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
	fmt.Fprintln(w, "usage: snestrace run [flags] | snestrace replay [flags] | snestrace index [flags] | snestrace query <writers|readers|explain-writer|last-writer-at-frame|dma-for-dest|bus-for-pc|trace-window|frame-summary|first-difference> [flags]")
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
			_ = tw.Emit(trace.Event{Kind: "frame", Frame: frame, Name: "state", Hash: hash})
		}
	}
	if err := out.Close(); err != nil {
		fmt.Fprintf(stderr, "snestrace run: close trace: %v\n", err)
		return 1
	}

	if *summaryPath != "" {
		var stateHashText string
		if *statePath != "" {
			stateBytes, _ := os.ReadFile(*statePath)
			stateHashText = hexHash(stateBytes)
		}
		if err := writeSummary(*summaryPath, summary{
			ROMPath:         *romPath,
			ROMHash:         hexHash(rom),
			StatePath:       *statePath,
			StateHash:       stateHashText,
			InputPath:       *inputPath,
			InputHash:       hashOptional(inputBytes),
			TracePath:       *outPath,
			TraceHash:       hashFileOptional(*outPath),
			Emulator:        buildRevision(),
			Frames:          *frames,
			FrameSummary:    framesOut,
			EventKinds:      keys(eventSet),
			EventCount:      tw.Count(),
			EventKindCounts: tw.Kinds(),
			AddressRange:    ranges,
		}); err != nil {
			fmt.Fprintf(stderr, "snestrace run: write summary: %v\n", err)
			return 1
		}
	}
	return 0
}

func runReplay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snestrace replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	romPath := fs.String("rom", "", "ROM path")
	statePath := fs.String("state", "", "save-state path")
	allowStateROMMismatch := fs.Bool("allow-state-rom-mismatch", false, "restore state even if its embedded ROM hash differs")
	inputPath := fs.String("inputs", "", "input trace JSON path")
	watchPath := fs.String("watch", "", "watch profile path")
	addrFlag := fs.String("addr", "", "comma-separated writer query ranges")
	comparePath := fs.String("compare", "", "optional trace JSONL to compare with first-difference")
	frames := fs.Int("frames", 0, "frames to run")
	outDir := fs.String("out-dir", "", "artifact output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *romPath == "" || *outDir == "" || *frames < 0 {
		fmt.Fprintln(stderr, "snestrace replay: --rom, --out-dir, and --frames >= 0 are required")
		return 2
	}
	if err := os.MkdirAll(*outDir, 0777); err != nil {
		fmt.Fprintf(stderr, "snestrace replay: create output dir: %v\n", err)
		return 1
	}

	tracePath := filepath.Join(*outDir, "trace.jsonl")
	summaryPath := filepath.Join(*outDir, "summary.json")
	indexPath := filepath.Join(*outDir, "index.json")
	runArgs := []string{
		"run",
		"--rom", *romPath,
		"--frames", strconv.Itoa(*frames),
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
	var childOut bytes.Buffer
	var childErr bytes.Buffer
	if code := run(runArgs, &childOut, &childErr); code != 0 {
		fmt.Fprintf(stderr, "snestrace replay: run failed: %s", childErr.String())
		return code
	}
	if code := run([]string{"index", "--trace", tracePath, "--out", indexPath}, &childOut, &childErr); code != 0 {
		fmt.Fprintf(stderr, "snestrace replay: index failed: %s", childErr.String())
		return code
	}

	writerRanges, err := replayWriterRanges(*addrFlag, *watchPath)
	if err != nil {
		fmt.Fprintf(stderr, "snestrace replay: %v\n", err)
		return 2
	}
	var artifacts []replayArtifact
	artifacts = append(artifacts,
		replayArtifact{Name: "trace", Path: tracePath, Hash: hashFileOptional(tracePath)},
		replayArtifact{Name: "summary", Path: summaryPath, Hash: hashFileOptional(summaryPath)},
		replayArtifact{Name: "index", Path: indexPath, Hash: hashFileOptional(indexPath)},
	)
	for i, r := range writerRanges {
		explainPath := filepath.Join(*outDir, fmt.Sprintf("writer-%02d.json", i+1))
		if code := runToFile(explainPath, []string{"query", "explain-writer", "--trace", tracePath, "--addr", formatRange(r)}, stderr); code != 0 {
			return code
		}
		artifacts = append(artifacts, replayArtifact{Name: "explain-writer", Path: explainPath, Addr: formatRange(r), Hash: hashFileOptional(explainPath)})

		lastPath := filepath.Join(*outDir, fmt.Sprintf("last-writer-%02d.json", i+1))
		if code := runToFile(lastPath, []string{"query", "last-writer-at-frame", "--trace", tracePath, "--addr", formatRange(r), "--frame", strconv.Itoa(lastReplayFrame(*frames))}, stderr); code != 0 {
			return code
		}
		artifacts = append(artifacts, replayArtifact{Name: "last-writer-at-frame", Path: lastPath, Addr: formatRange(r), Hash: hashFileOptional(lastPath)})
	}
	if *comparePath != "" {
		diffPath := filepath.Join(*outDir, "first-difference.json")
		if code := runToFile(diffPath, []string{"query", "first-difference", "--left", tracePath, "--right", *comparePath}, stderr); code != 0 {
			return code
		}
		artifacts = append(artifacts, replayArtifact{Name: "first-difference", Path: diffPath, Hash: hashFileOptional(diffPath)})
	}

	manifestPath := filepath.Join(*outDir, "manifest.json")
	if err := writeReplayManifest(manifestPath, replayManifest{
		ROMPath:   *romPath,
		ROMHash:   hashFileOptional(*romPath),
		StatePath: *statePath,
		StateHash: hashFileOptional(*statePath),
		InputPath: *inputPath,
		InputHash: hashFileOptional(*inputPath),
		WatchPath: *watchPath,
		WatchHash: hashFileOptional(*watchPath),
		Frames:    *frames,
		Artifacts: artifacts,
	}); err != nil {
		fmt.Fprintf(stderr, "snestrace replay: write manifest: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", manifestPath)
	return 0
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

func replayWriterRanges(addrText, watchPath string) ([]trace.Range, error) {
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
	watches, err := loadWatches(watchPath)
	if err != nil {
		return nil, err
	}
	for _, w := range watches {
		ranges = append(ranges, w.Range)
	}
	return ranges, nil
}

type replayManifest struct {
	ROMPath   string           `json:"rom_path"`
	ROMHash   string           `json:"rom_hash"`
	StatePath string           `json:"state_path,omitempty"`
	StateHash string           `json:"state_hash,omitempty"`
	InputPath string           `json:"input_path,omitempty"`
	InputHash string           `json:"input_hash,omitempty"`
	WatchPath string           `json:"watch_path,omitempty"`
	WatchHash string           `json:"watch_hash,omitempty"`
	Frames    int              `json:"frames"`
	Artifacts []replayArtifact `json:"artifacts"`
}

type replayArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Addr string `json:"addr,omitempty"`
	Hash string `json:"hash,omitempty"`
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
	sys     *snes.System
	tw      *trace.Writer
	events  map[string]bool
	filters []trace.Range
	frame   int
	cpu     trace.CPUContext
}

func (c *runContext) installHooks() {
	if c.events["cpu_block"] || c.events["bus"] || c.events["mmio"] || c.events["dma"] {
		prev := c.sys.CPU.BeforeExecute
		c.sys.CPU.BeforeExecute = func() {
			c.captureCPU()
			if c.events["cpu_block"] {
				_ = c.tw.Emit(trace.Event{
					Kind:  "cpu_block",
					Frame: c.frame,
					Cycle: c.sys.CPU.Cycles,
					PC:    &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
					CPU:   c.cpuContext(),
					Value: uint64(c.sys.CPU.P),
				})
			}
			if prev != nil {
				prev()
			}
		}
	}
	if c.events["bus"] || c.events["mmio"] {
		c.sys.Bus.ReadHook = func(addr uint32, value uint8) {
			c.emitBus("read", addr, value)
		}
		c.sys.Bus.WriteHook = func(addr uint32, value uint8) {
			c.emitBus("write", addr, value)
		}
	}
	if c.events["dma"] {
		c.sys.DMA.Trace = func(dt dma.TransferTrace) {
			count := uint32(dt.Count)
			src := uint32(dt.SrcBank)<<16 | uint32(dt.SrcAddr)
			dst := c.dmaDest(dt, count)
			_ = c.tw.Emit(trace.Event{
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
}

func dmaDirection(control uint8) string {
	if control&0x80 != 0 {
		return "b_to_a"
	}
	return "a_to_b"
}

func (c *runContext) captureCPU() {
	op := cpu.Opcodes[c.sys.CPU.LastOpcode]
	effAddr, effExpr := c.effectiveAddress(op.Mode)
	c.cpu = trace.CPUContext{
		PBR:           c.sys.CPU.LastOpcodePB,
		PC:            c.sys.CPU.LastOpcodePC,
		DBR:           c.sys.CPU.DB,
		DP:            c.sys.CPU.D,
		X:             c.sys.CPU.X,
		Y:             c.sys.CPU.Y,
		S:             c.sys.CPU.S,
		P:             c.sys.CPU.P,
		Opcode:        c.sys.CPU.LastOpcode,
		Bytes:         c.instructionBytes(op.Size),
		Disasm:        op.Name,
		Addressing:    addressingName(op.Mode),
		EffectiveAddr: effAddr,
		EffectiveExpr: effExpr,
	}
}

func (c *runContext) effectiveAddress(mode cpu.AddressingMode) (*uint32, string) {
	bytes := c.instructionBytes(cpu.Opcodes[c.sys.CPU.LastOpcode].Size)
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
		addr = directPageAddress(c.sys.CPU.E, c.sys.CPU.D, b1)
		expr = "dp"
	case cpu.AddrDirX:
		addr = directPageAddress(c.sys.CPU.E, c.sys.CPU.D, b1+c.sys.CPU.X)
		expr = "dp,x"
	case cpu.AddrDirY:
		addr = directPageAddress(c.sys.CPU.E, c.sys.CPU.D, b1+c.sys.CPU.Y)
		expr = "dp,y"
	case cpu.AddrIndX:
		ptr := c.peekDirectPageWord(b1 + c.sys.CPU.X)
		addr = uint32(c.sys.CPU.DB)<<16 | uint32(ptr)
		expr = "(dp,x)"
	case cpu.AddrIndY:
		ptr := c.peekDirectPageWord(b1)
		addr = (uint32(c.sys.CPU.DB)<<16 | uint32(ptr)) + uint32(c.sys.CPU.Y)
		addr &= 0xffffff
		expr = "(dp),y"
	case cpu.AddrDirInd:
		ptr := c.peekDirectPageWord(b1)
		addr = uint32(c.sys.CPU.DB)<<16 | uint32(ptr)
		expr = "(dp)"
	case cpu.AddrDirIndL:
		addr = c.peekDirectPageLong(b1)
		expr = "[dp]"
	case cpu.AddrDirIndLIdxY:
		addr = (c.peekDirectPageLong(b1) + uint32(c.sys.CPU.Y)) & 0xffffff
		expr = "[dp],y"
	case cpu.AddrSr:
		addr = uint32(c.sys.CPU.S+b1) & 0xffff
		expr = "sr,s"
	case cpu.AddrSrIndY:
		ptrAddr := uint32(c.sys.CPU.S+b1) & 0xffff
		ptr := uint16(c.peekCPU(ptrAddr)) | uint16(c.peekCPU((ptrAddr+1)&0xffff))<<8
		addr = (uint32(c.sys.CPU.DB)<<16 | uint32(ptr)) + uint32(c.sys.CPU.Y)
		addr &= 0xffffff
		expr = "(sr,s),y"
	case cpu.AddrAbs:
		addr = uint32(c.sys.CPU.DB)<<16 | uint32(word)
		expr = "abs"
	case cpu.AddrAbsX:
		addr = (uint32(c.sys.CPU.DB)<<16 | uint32(word)) + uint32(c.sys.CPU.X)
		addr &= 0xffffff
		expr = "abs,x"
	case cpu.AddrAbsY:
		addr = (uint32(c.sys.CPU.DB)<<16 | uint32(word)) + uint32(c.sys.CPU.Y)
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
		addr = ((uint32(bytes[3])<<16 | uint32(word)) + uint32(c.sys.CPU.X)) & 0xffffff
		expr = "long,x"
	default:
		return nil, ""
	}
	return uint32Ptr(addr), expr
}

func (c *runContext) peekDirectPageWord(offset uint16) uint16 {
	low := c.peekCPU(directPageAddress(c.sys.CPU.E, c.sys.CPU.D, offset))
	highAddr := directPageAddress(c.sys.CPU.E, c.sys.CPU.D, offset+1)
	if c.sys.CPU.E && c.sys.CPU.D&0xff == 0 {
		highAddr = uint32(c.sys.CPU.D&0xff00) | uint32((offset+1)&0x00ff)
	}
	return uint16(low) | uint16(c.peekCPU(highAddr))<<8
}

func (c *runContext) peekDirectPageLong(offset uint16) uint32 {
	low := uint32(c.peekCPU(directPageAddress(c.sys.CPU.E, c.sys.CPU.D, offset)))
	midAddr := directPageAddress(c.sys.CPU.E, c.sys.CPU.D, offset+1)
	highAddr := directPageAddress(c.sys.CPU.E, c.sys.CPU.D, offset+2)
	if c.sys.CPU.E && c.sys.CPU.D&0xff == 0 {
		page := uint32(c.sys.CPU.D & 0xff00)
		midAddr = page | uint32((offset+1)&0x00ff)
		highAddr = page | uint32((offset+2)&0x00ff)
	}
	mid := uint32(c.peekCPU(midAddr))
	high := uint32(c.peekCPU(highAddr))
	return high<<16 | mid<<8 | low
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

func (c *runContext) instructionBytes(size uint8) []uint16 {
	if size == 0 {
		size = 1
	}
	out := make([]uint16, size)
	base := uint32(c.sys.CPU.LastOpcodePB)<<16 | uint32(c.sys.CPU.LastOpcodePC)
	for i := range out {
		out[i] = uint16(c.peekCPU(base + uint32(i)))
	}
	return out
}

func (c *runContext) peekCPU(addr uint32) uint8 {
	dev := c.sys.Bus.GetPage((addr>>16)&0xff, (addr>>8)&0xff)
	if dev == nil {
		return 0
	}
	return dev.Read(addr)
}

func (c *runContext) cpuContext() *trace.CPUContext {
	ctx := c.cpu
	return &ctx
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
	var before, after *uint64
	if op == "write" && space == "wram" {
		if v, ok := c.peekWRAM(addr); ok {
			before = uint64Ptr(uint64(v))
			after = uint64Ptr(uint64(value))
		}
	}
	_ = c.tw.Emit(trace.Event{
		Kind:   kind,
		Frame:  c.frame,
		Cycle:  c.sys.CPU.Cycles,
		PC:     &trace.PC{Bank: c.sys.CPU.LastOpcodePB, Addr: c.sys.CPU.LastOpcodePC},
		CPU:    c.cpuContext(),
		Space:  space,
		Addr:   mapped,
		Width:  1,
		Value:  uint64(value),
		Before: before,
		After:  after,
		Op:     op,
	})
}

func (c *runContext) peekWRAM(addr uint32) (uint8, bool) {
	dev := c.sys.Bus.GetPage((addr>>16)&0xff, (addr>>8)&0xff)
	if dev == nil {
		return 0, false
	}
	return dev.Read(addr), true
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
	leftPath := fs.String("left", "", "left trace JSONL path")
	rightPath := fs.String("right", "", "right trace JSONL path")
	addrFlag := fs.String("addr", "", "address or range")
	destFlag := fs.String("dest", "", "destination address or range")
	eventID := fs.Uint64("event", 0, "event id")
	before := fs.Int("before", 20, "events before")
	after := fs.Int("after", 20, "events after")
	frame := fs.Int("frame", 0, "frame number")
	frameStart := fs.Int("frame-start", -1, "first frame to include")
	frameEnd := fs.Int("frame-end", -1, "last frame to include")
	if err := fs.Parse(args[1:]); err != nil {
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
	var explain *explainWriterResult
	switch name {
	case "writers":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query writers: %v\n", err)
			return 2
		}
		out = q.Writers(r)
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
		out = q.Readers(r)
	case "dma-for-dest":
		r, err := trace.ParseRange(*destFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query dma-for-dest: %v\n", err)
			return 2
		}
		out = q.DMAForDest(r)
	case "bus-for-pc":
		r, err := trace.ParseRange(*addrFlag)
		if err != nil {
			fmt.Fprintf(stderr, "snestrace query bus-for-pc: %v\n", err)
			return 2
		}
		out = q.BusForPC(r)
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
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "snestrace query: encode: %v\n", err)
		return 1
	}
	return 0
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
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()
	events, err := trace.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode trace: %w", err)
	}
	return events, nil
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
}

type summaryRange struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

func buildIndex(path string, events []trace.Event) indexFile {
	idx := indexFile{
		Schema:       trace.SchemaVersion,
		TracePath:    path,
		EventCount:   len(events),
		Kinds:        map[string]int{},
		Spaces:       map[string]int{},
		AddressRange: map[string][]summaryRange{},
	}
	for i, e := range events {
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
	}
	for space := range idx.AddressRange {
		sortSummaryRanges(idx.AddressRange[space])
	}
	sortSummaryRanges(idx.DMADest)
	return idx
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
	ROMPath         string         `json:"rom_path"`
	ROMHash         string         `json:"rom_hash"`
	StatePath       string         `json:"state_path,omitempty"`
	StateHash       string         `json:"state_hash,omitempty"`
	InputPath       string         `json:"input_path,omitempty"`
	InputHash       string         `json:"input_hash,omitempty"`
	TracePath       string         `json:"trace_path"`
	TraceHash       string         `json:"trace_hash,omitempty"`
	SummaryHash     string         `json:"summary_hash,omitempty"`
	Emulator        string         `json:"emulator"`
	Frames          int            `json:"frames"`
	FrameSummary    []frameSummary `json:"frame_summary,omitempty"`
	EventKinds      []string       `json:"event_kinds"`
	EventCount      int            `json:"event_count"`
	EventKindCounts map[string]int `json:"event_kind_counts,omitempty"`
	AddressRange    []trace.Range  `json:"address_ranges,omitempty"`
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

func summaryHash(s summary) string {
	s.SummaryHash = ""
	s.ROMPath = ""
	s.StatePath = ""
	s.InputPath = ""
	s.TracePath = ""
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return hexHash(data)
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
	sort.Strings(out)
	return out
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(s, 0, 64)
}

var _ = parseUint
