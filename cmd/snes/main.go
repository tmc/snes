package main

import (
	"bufio"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
)

type Game struct {
	system       *snes.System
	audioContext *audio.Context
	audioPlayer  *audio.Player
	audioStream  *AudioStream
	audioStarted bool
	romPath      string
	sramPath     string
	statePath    string

	pixels      []byte
	paused      bool
	stepFrame   bool
	savedState  []byte
	status      string
	keyLatch    map[ebiten.Key]bool
	rewind      [][]byte
	rewindHead  int
	rewindCount int
	frameCount  int
	showDebug   bool
}

type AudioStream struct {
	system     *snes.System
	drainAudio func([]int16) int
	samples    []int16
	queue      []int16
	drainBuf   []int16
	lastFrame  [2]int16
	mu         sync.Mutex
}

const (
	rewindCapacity        = 300
	rewindCaptureInterval = 6
	audioSampleRate       = 32000
	audioReadPollInterval = time.Millisecond
	audioReadTimeout      = 5 * time.Millisecond
	audioPlayerBuffer     = 100 * time.Millisecond
	audioStartBuffer      = 200 * time.Millisecond
	audioQueueBuffer      = time.Second
)

// Read implements io.Reader for AudioStream
func (s *AudioStream) Read(buf []byte) (int, error) {
	sampleCount := len(buf) / 2
	if cap(s.samples) < sampleCount {
		s.samples = make([]int16, sampleCount)
	}
	samples := s.samples[:sampleCount]
	n := s.readSamples(samples)
	for i := 0; i < n; i++ {
		s.lastFrame[i&1] = samples[i]
	}
	for i := n; i < sampleCount; i++ {
		samples[i] = s.lastFrame[i&1]
	}
	for i, sample := range samples {
		buf[i*2] = byte(sample)
		buf[i*2+1] = byte(sample >> 8)
	}
	if len(buf)%2 != 0 {
		buf[len(buf)-1] = 0
	}
	return len(buf), nil
}

func (s *AudioStream) readSamples(samples []int16) int {
	deadline := time.Now().Add(audioReadTimeout)
	n := 0
	for n < len(samples) {
		got := s.readQueued(samples[n:])
		n += got
		if n == len(samples) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(audioReadPollInterval)
	}
	return n
}

func (s *AudioStream) readQueued(dst []int16) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := copy(dst, s.queue)
	copy(s.queue, s.queue[n:])
	s.queue = s.queue[:len(s.queue)-n]
	return n
}

func (s *AudioStream) drain(dst []int16) int {
	if s.drainAudio != nil {
		return s.drainAudio(dst)
	}
	return s.system.DrainAudio(dst)
}

func (s *AudioStream) enqueue(src []int16) {
	if len(src) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	max := audioSamplesFor(audioQueueBuffer)
	if len(src) >= max {
		s.queue = append(s.queue[:0], src[len(src)-max:]...)
		return
	}
	if over := len(s.queue) + len(src) - max; over > 0 {
		copy(s.queue, s.queue[over:])
		s.queue = s.queue[:len(s.queue)-over]
	}
	s.queue = append(s.queue, src...)
}

func (s *AudioStream) drainSystemAudio() int {
	if cap(s.drainBuf) < audioSamplesFor(audioPlayerBuffer) {
		s.drainBuf = make([]int16, audioSamplesFor(audioPlayerBuffer))
	}

	buf := s.drainBuf[:cap(s.drainBuf)]
	total := 0
	for {
		n := s.drain(buf)
		if n == 0 {
			return total
		}
		s.enqueue(buf[:n])
		total += n
	}
}

func (s *AudioStream) bufferedSamples() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func audioSamplesFor(d time.Duration) int {
	return int(d) * audioSampleRate * 2 / int(time.Second)
}

func (g *Game) Update() error {
	g.handleHotkeys()

	if ebiten.IsKeyPressed(ebiten.KeyTab) {
		g.system.SetFrameSkip(4)
	} else {
		g.system.SetFrameSkip(0)
	}

	if ebiten.IsKeyPressed(ebiten.KeyBackspace) {
		state := g.popRewind()
		if state != nil {
			if err := g.system.Unserialize(state); err != nil {
				return fmt.Errorf("rewind: %w", err)
			}
			g.status = "rewind"
		}
		return nil
	}

	if g.paused && !g.stepFrame {
		return nil
	}

	g.frameCount++
	if g.frameCount%rewindCaptureInterval == 0 {
		state, err := g.system.Serialize()
		if err == nil {
			g.pushRewind(state)
		}
	}

	if err := g.system.SetInputState(0, pollInput()); err != nil {
		return err
	}
	if err := g.system.RunFrame(); err != nil {
		return err
	}
	g.updateAudio()

	if g.stepFrame {
		g.stepFrame = false
	}
	g.status = ""
	return nil
}

func (g *Game) updateAudio() {
	if g.audioStream == nil {
		return
	}
	g.audioStream.drainSystemAudio()
	if g.audioStarted || g.audioPlayer == nil {
		return
	}
	if g.audioStream.bufferedSamples() < audioSamplesFor(audioStartBuffer) {
		return
	}
	g.audioPlayer.Play()
	g.audioStarted = true
}

func (g *Game) Draw(screen *ebiten.Image) {
	const screenW = 256
	screenH := int(g.system.Display().Height)
	if screenH <= 0 || screenH > 240 {
		screenH = 224
	}

	params := g.system.FrameBuffer()
	if len(params)%screenW != 0 || len(params) == 0 {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("SNES Emulator Running\nCycles: %d\nBuffer Mismatch", g.system.CPU.Cycles))
		return
	}
	srcH := len(params) / screenW
	pixelCount := screenW * screenH

	if cap(g.pixels) < pixelCount*4 {
		g.pixels = make([]byte, pixelCount*4)
	}
	pixels := g.pixels[:pixelCount*4]
	for i := range pixels {
		pixels[i] = 0
	}

	lines := srcH
	if lines > screenH {
		lines = screenH
	}
	for y := 0; y < lines; y++ {
		srcRow := y * screenW
		dstRow := y * screenW
		for x := 0; x < screenW; x++ {
			col16 := params[srcRow+x]
			r5 := (col16) & 0x1F
			g5 := (col16 >> 5) & 0x1F
			b5 := (col16 >> 10) & 0x1F

			r8 := uint8((r5 * 255) / 31)
			g8 := uint8((g5 * 255) / 31)
			b8 := uint8((b5 * 255) / 31)

			idx := (dstRow + x) * 4
			pixels[idx] = r8
			pixels[idx+1] = g8
			pixels[idx+2] = b8
			pixels[idx+3] = 0xFF
		}
	}

	screen.WritePixels(pixels)

	mode := "run"
	if g.paused {
		mode = "paused"
	}
	if ebiten.IsKeyPressed(ebiten.KeyTab) {
		mode += " ff"
	}
	if g.system.RunAhead() {
		mode += " ra"
	}
	if g.status != "" {
		mode += " " + g.status
	}
	if g.showDebug {
		msg := fmt.Sprintf("PC:%02X:%04X Cy:%d [%s]\nP:pause O:step Tab:ff Backspace:rewind F5/F8:state G:runahead R:reset F1:debug",
			g.system.CPU.PB, g.system.CPU.PC, g.system.CPU.Cycles, mode)
		ebitenutil.DebugPrintAt(screen, msg, 4, 4)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	h := int(g.system.Display().Height)
	if h <= 0 || h > 240 {
		h = 224
	}
	return 256, h
}

func (g *Game) keyPressedOnce(key ebiten.Key) bool {
	pressed := ebiten.IsKeyPressed(key)
	prev := g.keyLatch[key]
	g.keyLatch[key] = pressed
	return pressed && !prev
}

func (g *Game) handleHotkeys() {
	if g.keyPressedOnce(ebiten.KeyP) {
		g.paused = !g.paused
		if g.paused {
			g.status = "paused"
		} else {
			g.status = "running"
		}
	}
	if g.keyPressedOnce(ebiten.KeyF1) {
		g.showDebug = !g.showDebug
	}
	if g.keyPressedOnce(ebiten.KeyO) {
		g.stepFrame = true
	}
	if g.keyPressedOnce(ebiten.KeyG) {
		g.system.SetRunAhead(!g.system.RunAhead())
	}
	if g.keyPressedOnce(ebiten.KeyR) {
		g.system.Reset()
		g.clearRewind()
		g.status = "reset"
	}
	if g.keyPressedOnce(ebiten.KeyF5) {
		state, err := g.system.Serialize()
		if err != nil {
			g.status = "save-state error"
		} else {
			g.savedState = state
			g.status = "state saved"
		}
	}
	if g.keyPressedOnce(ebiten.KeyF8) {
		if len(g.savedState) == 0 {
			g.status = "no state"
			return
		}
		if err := g.system.Unserialize(g.savedState); err != nil {
			g.status = "load-state error"
			return
		}
		g.status = "state loaded"
	}
	if g.keyPressedOnce(ebiten.KeyF6) {
		if g.statePath == "" {
			g.status = "no state path"
			return
		}
		state, err := g.system.Serialize()
		if err != nil {
			g.status = "state serialize error"
			return
		}
		if err := os.WriteFile(g.statePath, state, 0o644); err != nil {
			g.status = "state write error"
			return
		}
		g.status = "state file saved"
	}
	if g.keyPressedOnce(ebiten.KeyF9) {
		if g.statePath == "" {
			g.status = "no state path"
			return
		}
		state, err := os.ReadFile(g.statePath)
		if err != nil {
			g.status = "state read error"
			return
		}
		if err := g.system.Unserialize(state); err != nil {
			g.status = "state load error"
			return
		}
		g.status = "state file loaded"
	}
}

func (g *Game) pushRewind(state []byte) {
	if len(g.rewind) == 0 {
		g.rewind = make([][]byte, rewindCapacity)
	}
	g.rewind[g.rewindHead] = state
	g.rewindHead = (g.rewindHead + 1) % len(g.rewind)
	if g.rewindCount < len(g.rewind) {
		g.rewindCount++
	}
}

func (g *Game) popRewind() []byte {
	if g.rewindCount == 0 || len(g.rewind) == 0 {
		return nil
	}
	g.rewindHead = (g.rewindHead - 1 + len(g.rewind)) % len(g.rewind)
	state := g.rewind[g.rewindHead]
	g.rewind[g.rewindHead] = nil
	g.rewindCount--
	return state
}

func (g *Game) clearRewind() {
	for i := range g.rewind {
		g.rewind[i] = nil
	}
	g.rewindHead = 0
	g.rewindCount = 0
	g.frameCount = 0
}

func main() {
	fmt.Println("BSNES STARTING...")
	cheatPath := flag.String("cheats", "", "path to cheats file")
	frameCount := flag.Int("frames", 0, "run headless for N frames and emit frame log")
	frameLogPath := flag.String("frame-log", "", "frame log output path (default stdout)")
	frameLogVerbose := flag.Bool("frame-log-verbose", false, "include CPU/APU/PPU debug fields in frame log output")
	framePNGDir := flag.String("frame-png-dir", "", "directory to write rendered frame PNGs in headless mode")
	framePNGEvery := flag.Int("frame-png-every", 1, "write every Nth frame PNG in headless mode")
	inputScriptPath := flag.String("input-script", "", "path to headless input script")
	flag.Parse()
	romPath := flag.Arg(0)

	// Initialize the system
	sys := snes.NewSystem(nil)

	if romPath != "" {
		fmt.Printf("Loading ROM: %s\n", romPath)
		data, err := os.ReadFile(romPath)
		if err != nil {
			log.Fatalf("Failed to read ROM: %v", err)
		}
		if err := sys.LoadROM(data); err != nil {
			log.Fatalf("Failed to load ROM: %v", err)
		}
		if *cheatPath != "" {
			cheats, err := loadCheats(*cheatPath)
			if err != nil {
				log.Fatalf("Failed to load cheats: %v", err)
			}
			if err := sys.SetCheats(cheats); err != nil {
				log.Fatalf("Failed to apply cheats: %v", err)
			}
			log.Printf("loaded %d cheats from %s", len(cheats), *cheatPath)
		}
		sramPath := defaultSRAMPath(romPath)
		if sram, err := os.ReadFile(sramPath); err == nil {
			if err := sys.LoadSaveRAM(sram); err != nil {
				log.Printf("failed to load SRAM %s: %v", sramPath, err)
			} else {
				log.Printf("loaded SRAM: %s", sramPath)
			}
		}

		defer func() {
			sram := sys.SaveRAM()
			if len(sram) == 0 {
				return
			}
			if err := os.WriteFile(sramPath, sram, 0o644); err != nil {
				log.Printf("failed to save SRAM %s: %v", sramPath, err)
				return
			}
			log.Printf("saved SRAM: %s", sramPath)
		}()
	} else {
		log.Fatal("no ROM provided. usage: snes [rom.sfc]")
	}

	// Power On
	sys.Power()
	if *frameCount > 0 {
		inputScript, err := loadInputScript(*inputScriptPath)
		if err != nil {
			log.Fatalf("failed to load input script %s: %v", *inputScriptPath, err)
		}
		out := io.Writer(os.Stdout)
		var f *os.File
		if *frameLogPath != "" {
			file, err := os.Create(*frameLogPath)
			if err != nil {
				log.Fatalf("failed to create frame log %s: %v", *frameLogPath, err)
			}
			defer file.Close()
			out = file
			f = file
		}
		if err := runHeadlessFrames(sys, *frameCount, out, *framePNGDir, *framePNGEvery, *frameLogVerbose, inputScript); err != nil {
			log.Fatalf("headless frame run failed: %v", err)
		}
		if f != nil {
			log.Printf("wrote frame log: %s", *frameLogPath)
		}
		if *framePNGDir != "" {
			log.Printf("wrote frame pngs: %s", *framePNGDir)
		}
		return
	}

	ebiten.SetWindowSize(768, 720)
	ebiten.SetWindowTitle("bsnes-go")

	game := &Game{
		system:    sys,
		romPath:   romPath,
		sramPath:  defaultSRAMPath(romPath),
		statePath: defaultStatePath(romPath),
		keyLatch:  make(map[ebiten.Key]bool),
	}

	// Audio Init
	game.audioContext = audio.NewContext(audioSampleRate)
	stream := &AudioStream{system: sys}
	player, err := game.audioContext.NewPlayer(stream)
	if err != nil {
		log.Fatal(err)
	}
	player.SetBufferSize(audioPlayerBuffer)
	game.audioStream = stream
	game.audioPlayer = player

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}

}

func defaultSRAMPath(romPath string) string {
	if romPath == "" {
		return ""
	}
	return strings.TrimSuffix(romPath, filepath.Ext(romPath)) + ".srm"
}

func defaultStatePath(romPath string) string {
	if romPath == "" {
		return ""
	}
	return strings.TrimSuffix(romPath, filepath.Ext(romPath)) + ".state"
}

func loadCheats(path string) ([]snes.Cheat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cheats file: %w", err)
	}
	defer f.Close()

	var cheats []snes.Cheat
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		cheat, err := parseCheatLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse cheats line %d: %w", lineNo, err)
		}
		cheats = append(cheats, cheat)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan cheats file: %w", err)
	}
	return cheats, nil
}

func parseCheatLine(line string) (snes.Cheat, error) {
	var cheat snes.Cheat
	cheat.Enabled = true

	name := ""
	if i := strings.IndexByte(line, ':'); i >= 0 {
		prefix := line[:i]
		if !strings.Contains(prefix, "=") && !strings.Contains(prefix, "?") {
			name = strings.TrimSpace(prefix)
			line = strings.TrimSpace(line[i+1:])
		}
	}
	cheat.Name = name

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return snes.Cheat{}, fmt.Errorf("expected ADDRESS=VALUE")
	}

	addrPart := strings.TrimSpace(parts[0])
	valPart := strings.TrimSpace(parts[1])

	if q := strings.IndexByte(addrPart, '?'); q >= 0 {
		cmpPart := strings.TrimSpace(addrPart[q+1:])
		addrPart = strings.TrimSpace(addrPart[:q])
		cmp, err := parseHexByte(cmpPart)
		if err != nil {
			return snes.Cheat{}, fmt.Errorf("compare byte: %w", err)
		}
		cheat.HasCompare = true
		cheat.Compare = cmp
	}

	addr, err := strconv.ParseUint(addrPart, 16, 24)
	if err != nil {
		return snes.Cheat{}, fmt.Errorf("address: %w", err)
	}
	val, err := parseHexByte(valPart)
	if err != nil {
		return snes.Cheat{}, fmt.Errorf("value: %w", err)
	}
	cheat.Address = uint32(addr)
	cheat.Value = val
	return cheat, nil
}

func parseHexByte(s string) (uint8, error) {
	u, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x"), 16, 8)
	if err != nil {
		return 0, err
	}
	return uint8(u), nil
}

type inputSpan struct {
	start int
	end   int
	state uint16
}

func loadInputScript(path string) ([]inputSpan, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open input script: %w", err)
	}
	defer f.Close()

	var spans []inputSpan
	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		span, err := parseInputScriptLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse input script line %d: %w", lineNo, err)
		}
		spans = append(spans, span)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan input script: %w", err)
	}
	return spans, nil
}

func parseInputScriptLine(line string) (inputSpan, error) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return inputSpan{}, fmt.Errorf("expected START-END: BUTTONS")
	}
	start, end, err := parseFrameRange(strings.TrimSpace(parts[0]))
	if err != nil {
		return inputSpan{}, err
	}
	state, err := parseInputButtons(parts[1])
	if err != nil {
		return inputSpan{}, err
	}
	return inputSpan{start: start, end: end, state: state}, nil
}

func parseFrameRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("start frame: %w", err)
	}
	end := start
	if len(parts) == 2 {
		end, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("end frame: %w", err)
		}
	}
	if start < 0 || end < start {
		return 0, 0, fmt.Errorf("invalid frame range %q", s)
	}
	return start, end, nil
}

func parseInputButtons(s string) (uint16, error) {
	var state uint16
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == '+' || r == ',' || r == ' ' || r == '\t' }) {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" || name == "none" {
			continue
		}
		button, ok := inputButtonByName[name]
		if !ok {
			return 0, fmt.Errorf("unknown button %q", raw)
		}
		state |= button
	}
	return state, nil
}

var inputButtonByName = map[string]uint16{
	"a":      emulator.StandardButtonA,
	"b":      emulator.StandardButtonB,
	"x":      emulator.StandardButtonX,
	"y":      emulator.StandardButtonY,
	"l":      emulator.StandardButtonL,
	"r":      emulator.StandardButtonR,
	"start":  emulator.StandardButtonStart,
	"select": emulator.StandardButtonSelect,
	"up":     emulator.StandardButtonUp,
	"down":   emulator.StandardButtonDown,
	"left":   emulator.StandardButtonLeft,
	"right":  emulator.StandardButtonRight,
}

func inputStateAt(spans []inputSpan, frame int) uint16 {
	var state uint16
	for _, span := range spans {
		if frame >= span.start && frame <= span.end {
			state |= span.state
		}
	}
	return state
}

func runHeadlessFrames(sys *snes.System, frames int, out io.Writer, pngDir string, pngEvery int, verbose bool, inputScript []inputSpan) error {
	if frames < 0 {
		return fmt.Errorf("frames must be >= 0")
	}
	if pngEvery <= 0 {
		return fmt.Errorf("frame-png-every must be > 0")
	}
	if pngDir != "" {
		if err := os.MkdirAll(pngDir, 0o755); err != nil {
			return fmt.Errorf("create frame png dir: %w", err)
		}
	}
	if verbose {
		if _, err := fmt.Fprintln(out, "frame,pc,op0,op1,op2,cycles,db,a,p,inidisp,hvbjoy,apu_pc,apu_op,apu_in0,apu_in1,apu_in2,apu_in3,apu_out0,apu_out1,apu_out2,apu_out3,apu_3c00,apu_3c01,apu_3c02,apu_3c03,fb_nonzero,fb_hash,fb_diff,audio_samples,audio_hash,audio_diff"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "frame,pc,cycles,fb_nonzero,fb_hash,fb_diff,audio_samples,audio_hash,audio_diff"); err != nil {
			return err
		}
	}

	audioBuf := make([]int16, 8192)
	var prevFB []uint16
	var prevAudio []int16

	for i := 0; i < frames; i++ {
		if err := sys.SetInputState(0, inputStateAt(inputScript, i)); err != nil {
			return fmt.Errorf("set input state: %w", err)
		}
		if err := sys.RunFrame(); err != nil {
			return fmt.Errorf("run frame %d: %w", i, err)
		}

		fb := sys.FrameBuffer()
		if pngDir != "" && i%pngEvery == 0 {
			pngPath := filepath.Join(pngDir, fmt.Sprintf("frame_%06d.png", i))
			if err := saveFramePNG(pngPath, fb); err != nil {
				return fmt.Errorf("write frame png %d: %w", i, err)
			}
		}
		fbNonZero := countNonZero16(fb)
		fbHash := hashU16(fb)
		fbDiff := countU16Diff(prevFB, fb)
		prevFB = append(prevFB[:0], fb...)

		n := sys.DrainAudio(audioBuf)
		audioNow := append([]int16(nil), audioBuf[:n]...)
		audioHash := hashI16(audioNow)
		audioDiff := countI16Diff(prevAudio, audioNow)
		prevAudio = append(prevAudio[:0], audioNow...)

		if verbose {
			pcAddr := uint32(sys.CPU.PB)<<16 | uint32(sys.CPU.PC)
			op0 := sys.Bus.Read(pcAddr)
			op1 := sys.Bus.Read((pcAddr + 1) & 0xFFFFFF)
			op2 := sys.Bus.Read((pcAddr + 2) & 0xFFFFFF)
			apuPC := sys.APU.Processor.PC
			apuOp := sys.APU.Read(apuPC)
			if _, err := fmt.Fprintf(out, "%d,%02X:%04X,%02X,%02X,%02X,%d,%02X,%04X,%02X,%02X,%02X,%04X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%02X,%d,%08X,%d,%d,%08X,%d\n",
				i,
				sys.CPU.PB, sys.CPU.PC,
				op0, op1, op2,
				sys.CPU.Cycles,
				sys.CPU.DB,
				sys.CPU.A,
				sys.CPU.P,
				sys.PPU.INIDISP,
				sys.PPU.ReadHVBJOY(),
				apuPC,
				apuOp,
				sys.APU.InPorts[0], sys.APU.InPorts[1], sys.APU.InPorts[2], sys.APU.InPorts[3],
				sys.APU.OutPorts[0], sys.APU.OutPorts[1], sys.APU.OutPorts[2], sys.APU.OutPorts[3],
				sys.APU.RAM[0x3C00], sys.APU.RAM[0x3C01], sys.APU.RAM[0x3C02], sys.APU.RAM[0x3C03],
				fbNonZero, fbHash, fbDiff,
				n, audioHash, audioDiff,
			); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(out, "%d,%02X:%04X,%d,%d,%08X,%d,%d,%08X,%d\n",
			i,
			sys.CPU.PB, sys.CPU.PC,
			sys.CPU.Cycles,
			fbNonZero, fbHash, fbDiff,
			n, audioHash, audioDiff,
		); err != nil {
			return err
		}
	}
	return nil
}

func saveFramePNG(path string, fb []uint16) error {
	const width = 256
	if len(fb) == 0 || len(fb)%width != 0 {
		return fmt.Errorf("invalid framebuffer length %d", len(fb))
	}
	height := len(fb) / width
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i, col16 := range fb {
		r5 := col16 & 0x1F
		g5 := (col16 >> 5) & 0x1F
		b5 := (col16 >> 10) & 0x1F

		r8 := uint8((r5 * 255) / 31)
		g8 := uint8((g5 * 255) / 31)
		b8 := uint8((b5 * 255) / 31)

		idx := i * 4
		img.Pix[idx] = r8
		img.Pix[idx+1] = g8
		img.Pix[idx+2] = b8
		img.Pix[idx+3] = 0xFF
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create png file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}

func countNonZero16(v []uint16) int {
	n := 0
	for _, x := range v {
		if x != 0 {
			n++
		}
	}
	return n
}

func hashU16(v []uint16) uint32 {
	var h uint32 = 2166136261
	for _, x := range v {
		h ^= uint32(x)
		h *= 16777619
	}
	return h
}

func hashI16(v []int16) uint32 {
	var h uint32 = 2166136261
	for _, x := range v {
		h ^= uint32(uint16(x))
		h *= 16777619
	}
	return h
}

func countU16Diff(prev, cur []uint16) int {
	n := 0
	m := len(cur)
	if len(prev) < m {
		m = len(prev)
	}
	for i := 0; i < m; i++ {
		if prev[i] != cur[i] {
			n++
		}
	}
	if len(cur) > m {
		n += len(cur) - m
	}
	if len(prev) > m {
		n += len(prev) - m
	}
	return n
}

func countI16Diff(prev, cur []int16) int {
	n := 0
	m := len(cur)
	if len(prev) < m {
		m = len(prev)
	}
	for i := 0; i < m; i++ {
		if prev[i] != cur[i] {
			n++
		}
	}
	if len(cur) > m {
		n += len(cur) - m
	}
	if len(prev) > m {
		n += len(prev) - m
	}
	return n
}

func pollInput() uint16 {
	var state uint16
	if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
		state |= emulator.StandardButtonUp
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
		state |= emulator.StandardButtonDown
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
		state |= emulator.StandardButtonLeft
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
		state |= emulator.StandardButtonRight
	}
	if ebiten.IsKeyPressed(ebiten.KeyX) {
		state |= emulator.StandardButtonA
	}
	if ebiten.IsKeyPressed(ebiten.KeyZ) {
		state |= emulator.StandardButtonB
	}
	if ebiten.IsKeyPressed(ebiten.KeyS) {
		state |= emulator.StandardButtonX
	}
	if ebiten.IsKeyPressed(ebiten.KeyA) {
		state |= emulator.StandardButtonY
	}
	if ebiten.IsKeyPressed(ebiten.KeyEnter) {
		state |= emulator.StandardButtonStart
	}
	if ebiten.IsKeyPressed(ebiten.KeyShiftRight) {
		state |= emulator.StandardButtonSelect
	}
	if ebiten.IsKeyPressed(ebiten.KeyQ) {
		state |= emulator.StandardButtonL
	}
	if ebiten.IsKeyPressed(ebiten.KeyW) {
		state |= emulator.StandardButtonR
	}
	return state
}
