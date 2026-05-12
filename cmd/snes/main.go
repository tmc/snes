package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/snesagent"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
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
	stateSlotDir string

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
	showHelp    bool
	fastForward bool

	agentInputMu        sync.Mutex
	agentInput          uint16
	agentInputFrames    int
	agentFramesPerInput int
	agentInputCh        <-chan uint16
	agentCommandCh      <-chan snesagent.Command
	agentSocket         *agentSocketHub
	agentObserveEvery   int
	agentObserveRAMOff  int64
	agentObserveRAMLen  int

	humanHoldFrames    int
	humanHoldRemaining int
	lastExecutedInput  uint16
	lastExecutedActor  string
	lastHumanInput     uint16

	playbackInputs []uint16
	playbackFrames int
	playbackName   string
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

const keyHelpText = `Keys
P pause/resume
O step one frame
Tab fast-forward
Backspace rewind
G run-ahead
R reset
F1 debug overlay
F5 save memory state
F8 load memory state
F6 save state file
F9 load state file
Ctrl+1..9 save slot
1..9 load slot
(number row or keypad)
?/K show/hide keys`

const helpTextLineSpacing = 10

var helpTextFace = mustHelpTextFace()

func mustHelpTextFace() font.Face {
	tt, err := opentype.Parse(goregular.TTF)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    9,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic(err)
	}
	return face
}

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

	if g.fastForward || ebiten.IsKeyPressed(ebiten.KeyTab) {
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

	if err := g.system.SetInputState(0, g.pollInput()); err != nil {
		return err
	}
	if err := g.system.RunFrame(); err != nil {
		return err
	}
	g.updateAudio()
	g.publishAgentObservation()

	if g.stepFrame {
		g.stepFrame = false
	}
	if len(g.playbackInputs) > 0 {
		done := g.playbackFrames
		if done <= 0 || done > len(g.playbackInputs) {
			done = len(g.playbackInputs)
		}
		if g.frameCount >= done {
			g.paused = true
			g.status = fmt.Sprintf("%s complete %d/%d", g.playbackName, done, done)
		} else {
			g.status = fmt.Sprintf("%s %d/%d %s", g.playbackName, g.frameCount, done, actionNameFor(g.lastExecutedInput))
		}
	} else {
		g.status = ""
	}
	return nil
}

func (g *Game) publishAgentObservation() {
	if g.agentSocket == nil || g.agentObserveEvery <= 0 || g.frameCount%g.agentObserveEvery != 0 {
		return
	}
	obs := snesagent.Observation{
		Type:        "observation",
		Frame:       g.frameCount,
		Width:       256,
		Height:      len(g.system.FrameBuffer()) / 256,
		FrameBuffer: encodeU16LEBytes(g.system.FrameBuffer()),
	}
	if g.agentObserveRAMLen > 0 {
		ram := make([]byte, g.agentObserveRAMLen)
		n, err := g.system.ReadWRAMAt(ram, g.agentObserveRAMOff)
		if err == nil {
			obs.RAMOffset = g.agentObserveRAMOff
			obs.RAM = ram[:n]
		}
	}
	obs.Actor = g.lastExecutedActor
	obs.HumanActive = g.humanHoldRemaining > 0
	obs.HumanButtons = buttonNames(g.lastHumanInput)
	obs.ExecutedButtons = buttonNames(g.lastExecutedInput)
	obs.ExecutedAction = actionNameFor(g.lastExecutedInput)
	g.agentSocket.Broadcast(obs)
}

func (g *Game) pollInput() uint16 {
	human := pollInput()
	g.lastHumanInput = human
	if len(g.playbackInputs) > 0 {
		frame := g.frameCount - 1
		if frame >= 0 && frame < len(g.playbackInputs) {
			input := g.playbackInputs[frame]
			g.lastExecutedInput = input
			g.lastExecutedActor = "request"
			return input
		}
		g.lastExecutedInput = 0
		g.lastExecutedActor = "request"
		return 0
	}
	if human != 0 && g.humanHoldFrames > 0 {
		g.humanHoldRemaining = g.humanHoldFrames
	}
	humanActive := g.humanHoldRemaining > 0
	if humanActive && human == 0 {
		g.humanHoldRemaining--
	}
	if g.agentInputCh != nil {
		g.drainAgentCommands()
		g.drainAgentInput()
	}
	if humanActive {
		// Auto-takeover: human input wins; ignore queued agent input but let it tick down
		// so a fresh action message after the human releases is honored.
		g.agentInputMu.Lock()
		if g.agentInputFrames > 0 {
			g.agentInputFrames--
		}
		g.agentInputMu.Unlock()
		g.lastExecutedInput = human
		g.lastExecutedActor = "human"
		return human
	}
	if g.agentInputCh == nil {
		g.lastExecutedInput = human
		if human != 0 {
			g.lastExecutedActor = "human"
		} else {
			g.lastExecutedActor = "noop"
		}
		return human
	}
	g.agentInputMu.Lock()
	defer g.agentInputMu.Unlock()
	if g.agentInputFrames <= 0 {
		g.lastExecutedInput = human
		if human != 0 {
			g.lastExecutedActor = "human"
		} else {
			g.lastExecutedActor = "noop"
		}
		return human
	}
	g.agentInputFrames--
	combined := human | g.agentInput
	g.lastExecutedInput = combined
	g.lastExecutedActor = "policy"
	return combined
}

func (g *Game) drainAgentInput() {
	for {
		select {
		case input, ok := <-g.agentInputCh:
			if !ok {
				return
			}
			g.agentInputMu.Lock()
			g.agentInput = input
			g.agentInputFrames = g.agentFramesPerInput
			g.agentInputMu.Unlock()
		default:
			return
		}
	}
}

func (g *Game) drainAgentCommands() {
	for {
		select {
		case cmd, ok := <-g.agentCommandCh:
			if !ok {
				return
			}
			if err := g.handleAgentCommand(cmd); err != nil {
				log.Printf("agent command: %v", err)
			}
		default:
			return
		}
	}
}

func (g *Game) handleAgentCommand(cmd snesagent.Command) error {
	switch cmd.Type {
	case "load_state":
		if cmd.Path == "" {
			return fmt.Errorf("load_state: missing path")
		}
		if err := loadSystemState(g.system, cmd.Path); err != nil {
			return err
		}
		g.agentInputMu.Lock()
		g.agentInput = 0
		g.agentInputFrames = 0
		g.agentInputMu.Unlock()
		g.status = "agent loaded state"
		return nil
	case "save_state":
		if cmd.Path == "" {
			return fmt.Errorf("save_state: missing path")
		}
		if err := saveSystemState(g.system, cmd.Path); err != nil {
			return err
		}
		g.status = "agent saved state"
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd.Type)
	}
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
	if g.fastForward || ebiten.IsKeyPressed(ebiten.KeyTab) {
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
	} else if g.status != "" {
		ebitenutil.DebugPrintAt(screen, g.status, 4, int(g.system.Display().Height)-12)
	}
	if g.showHelp {
		g.drawHelp(screen)
	}
}

func (g *Game) drawHelp(screen *ebiten.Image) {
	x, baseline := 4, 26
	w, h := helpTextBounds(keyHelpText)
	vector.DrawFilledRect(screen, float32(x-3), float32(baseline-10), float32(w+8), float32(h+12), color.RGBA{0, 0, 0, 176}, false)
	for i, line := range strings.Split(keyHelpText, "\n") {
		text.Draw(screen, line, helpTextFace, x, baseline+i*helpTextLineSpacing, color.RGBA{235, 235, 235, 240})
	}
}

func helpTextBounds(msg string) (width, height int) {
	for _, line := range strings.Split(msg, "\n") {
		bounds, _ := font.BoundString(helpTextFace, line)
		w := (bounds.Max.X - bounds.Min.X).Ceil()
		if width < w {
			width = w
		}
		height += helpTextLineSpacing
	}
	return width, height
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
	g.handleStateSlotHotkeys()

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
	if g.keyPressedAnyOnce(ebiten.KeySlash, ebiten.KeyK) {
		g.showHelp = !g.showHelp
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
			slog.Info("saved memory state")
			g.status = "state saved"
		}
	}
	if g.keyPressedOnce(ebiten.KeyF8) {
		if len(g.savedState) == 0 {
			g.status = "no state"
			return
		}
		if err := g.loadStateForDisplay(g.savedState); err != nil {
			g.status = "load-state error"
			return
		}
		slog.Info("loaded memory state")
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
		slog.Info("saved state", "path", g.statePath)
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
		if err := g.loadStateForDisplay(state); err != nil {
			g.status = "state load error"
			return
		}
		slog.Info("loaded state", "path", g.statePath)
		g.status = "state file loaded"
	}
}

var stateSlotKeys = [...][2]ebiten.Key{
	{ebiten.KeyDigit1, ebiten.KeyNumpad1},
	{ebiten.KeyDigit2, ebiten.KeyNumpad2},
	{ebiten.KeyDigit3, ebiten.KeyNumpad3},
	{ebiten.KeyDigit4, ebiten.KeyNumpad4},
	{ebiten.KeyDigit5, ebiten.KeyNumpad5},
	{ebiten.KeyDigit6, ebiten.KeyNumpad6},
	{ebiten.KeyDigit7, ebiten.KeyNumpad7},
	{ebiten.KeyDigit8, ebiten.KeyNumpad8},
	{ebiten.KeyDigit9, ebiten.KeyNumpad9},
}

func (g *Game) handleStateSlotHotkeys() {
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	for i, keys := range stateSlotKeys {
		slot := i + 1
		if !g.keyPressedAnyOnce(keys[0], keys[1]) {
			continue
		}
		if ctrl {
			g.saveStateSlot(slot)
		} else {
			g.loadStateSlot(slot)
		}
		return
	}
}

func (g *Game) keyPressedAnyOnce(keys ...ebiten.Key) bool {
	pressed := false
	for _, key := range keys {
		pressed = g.keyPressedOnce(key) || pressed
	}
	return pressed
}

func (g *Game) saveStateSlot(slot int) {
	if g.stateSlotDir == "" {
		g.status = "no state slot dir"
		slog.Info("state slot save skipped", "slot", slot, "reason", "no state slot dir")
		return
	}
	state, err := g.system.Serialize()
	if err != nil {
		g.status = fmt.Sprintf("slot %d save serialize error", slot)
		return
	}
	if err := os.MkdirAll(g.stateSlotDir, 0o755); err != nil {
		g.status = fmt.Sprintf("slot %d save mkdir error", slot)
		return
	}
	path := stateSlotPath(g.stateSlotDir, g.statePath, slot)
	if err := os.WriteFile(path, state, 0o644); err != nil {
		g.status = fmt.Sprintf("slot %d save error", slot)
		return
	}
	slog.Info("saved state slot", "slot", slot, "path", path)
	g.status = fmt.Sprintf("slot %d saved", slot)
}

func (g *Game) loadStateSlot(slot int) {
	if g.stateSlotDir == "" {
		g.status = "no state slot dir"
		slog.Info("state slot load skipped", "slot", slot, "reason", "no state slot dir")
		return
	}
	path := stateSlotPath(g.stateSlotDir, g.statePath, slot)
	state, err := os.ReadFile(path)
	if err != nil {
		g.status = fmt.Sprintf("slot %d empty", slot)
		slog.Info("state slot load skipped", "slot", slot, "path", path, "reason", "empty")
		return
	}
	if err := g.loadStateForDisplay(state); err != nil {
		g.status = fmt.Sprintf("slot %d load error", slot)
		return
	}
	slog.Info("loaded state slot", "slot", slot, "path", path)
	g.status = fmt.Sprintf("slot %d loaded", slot)
}

func stateSlotPath(dir, base string, slot int) string {
	ext := filepath.Ext(base)
	if ext == "" {
		ext = ".state"
	}
	return filepath.Join(dir, strconv.Itoa(slot)+ext)
}

func (g *Game) loadStateForDisplay(state []byte) error {
	if err := g.system.Unserialize(state); err != nil {
		return err
	}

	// Save states can be captured mid-frame, leaving FrontBuffer with
	// partly stale scanlines. Refresh the displayed frame without moving
	// the restored machine state forward.
	restored, err := g.system.Serialize()
	if err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if err := g.system.RunFrame(); err != nil {
			return err
		}
	}
	frame := append([]uint16(nil), g.system.PPU.FrontBuffer...)
	if err := g.system.Unserialize(restored); err != nil {
		return err
	}
	copy(g.system.PPU.FrontBuffer, frame)
	return nil
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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println("BSNES STARTING...")
	cheatPath := flag.String("cheats", "", "path to cheats file")
	frameCount := flag.Int("frames", 0, "run headless for N frames and emit frame log")
	frameLogPath := flag.String("frame-log", "", "frame log output path (default stdout)")
	frameLogVerbose := flag.Bool("frame-log-verbose", false, "include CPU/APU/PPU debug fields in frame log output")
	framePNGDir := flag.String("frame-png-dir", "", "directory to write rendered frame PNGs in headless mode")
	framePNGEvery := flag.Int("frame-png-every", 1, "write every Nth frame PNG in headless mode")
	inputScriptPath := flag.String("input-script", "", "path to headless input script")
	statePathFlag := flag.String("state", "", "save-state file for F6/F9 (default ROM base with .state)")
	loadStatePath := flag.String("load-state", "", "save-state file to load after power-on")
	saveStatePath := flag.String("save-state", "", "save-state file to write after headless run or on exit")
	agentSocketPath := flag.String("agent-socket", "", "Unix socket path for agent control")
	agentSocketFormat := flag.String("agent-socket-format", "jsonl", "agent socket wire format: jsonl or proto")
	agentFramesPerInput := flag.Int("agent-frames-per-input", 8, "rendered frames to hold each agent socket action")
	agentObserveEvery := flag.Int("agent-observe-every", 8, "rendered frames between agent socket observations")
	agentObserveRAM := flag.String("agent-observe-ram", "0:8192", "WRAM observation range as offset:length, or empty to disable")
	humanHoldFrames := flag.Int("human-hold-frames", 48, "frames to keep human takeover active after the last keypress (0 disables auto-takeover)")
	fastForward := flag.Bool("fast-forward", true, "run with fast-forward frame skip enabled by default")
	playRequestPath := flag.String("play-request", "", "snestrace replay request JSON path to play in the GUI at gameplay rate")
	flag.Parse()
	romPath := flag.Arg(0)
	romHash := ""
	var playReq snesReplayRequest
	var playbackInputs []uint16
	if *playRequestPath != "" {
		var err error
		playReq, err = loadSNESReplayRequest(*playRequestPath)
		if err != nil {
			log.Fatalf("failed to load play request %s: %v", *playRequestPath, err)
		}
		playbackInputs, err = loadRequestInputs(playReq.InputsPath)
		if err != nil {
			log.Fatalf("failed to load request inputs %s: %v", playReq.InputsPath, err)
		}
		if romPath == "" {
			romPath = playReq.ROMPath
		}
		if *loadStatePath == "" {
			*loadStatePath = playReq.StatePath
		}
		if len(playbackInputs) > 0 && playReq.Frames == 0 {
			playReq.Frames = len(playbackInputs)
		}
		if *fastForward {
			*fastForward = false
		}
	}

	// Initialize the system
	sys := snes.NewSystem(nil)

	if romPath != "" {
		fmt.Printf("Loading ROM: %s\n", romPath)
		data, err := os.ReadFile(romPath)
		if err != nil {
			log.Fatalf("Failed to read ROM: %v", err)
		}
		sum := sha256.Sum256(data)
		romHash = fmt.Sprintf("%x", sum)
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
	if *loadStatePath != "" {
		var err error
		if *playRequestPath != "" && playReq.AllowStateROMMismatch {
			err = loadSystemStateWithOptions(sys, *loadStatePath, snes.UnserializeOptions{IgnoreROMHash: true})
		} else {
			err = loadSystemState(sys, *loadStatePath)
		}
		if err != nil {
			log.Fatalf("failed to load state %s: %v", *loadStatePath, err)
		}
		log.Printf("loaded state: %s", *loadStatePath)
	}
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
		if *saveStatePath != "" {
			if err := saveSystemState(sys, *saveStatePath); err != nil {
				log.Fatalf("failed to save state %s: %v", *saveStatePath, err)
			}
			log.Printf("saved state: %s", *saveStatePath)
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
		system:              sys,
		romPath:             romPath,
		sramPath:            defaultSRAMPath(romPath),
		statePath:           statePath(romPath, *statePathFlag),
		stateSlotDir:        defaultStateSlotDir(romHash),
		keyLatch:            make(map[ebiten.Key]bool),
		fastForward:         *fastForward,
		agentFramesPerInput: *agentFramesPerInput,
		agentObserveEvery:   *agentObserveEvery,
		humanHoldFrames:     *humanHoldFrames,
		playbackInputs:      playbackInputs,
		playbackFrames:      playReq.Frames,
		playbackName:        "request",
	}
	if *agentSocketPath != "" {
		if *agentFramesPerInput <= 0 {
			log.Fatalf("agent-frames-per-input must be > 0")
		}
		if *agentObserveEvery <= 0 {
			log.Fatalf("agent-observe-every must be > 0")
		}
		off, length, err := parseAgentRAMRange(*agentObserveRAM)
		if err != nil {
			log.Fatalf("bad agent-observe-ram: %v", err)
		}
		format, err := snesagent.ParseFormat(*agentSocketFormat)
		if err != nil {
			log.Fatal(err)
		}
		agentSocket, err := listenAgentSocket(*agentSocketPath, format)
		if err != nil {
			log.Fatalf("failed to listen on agent socket: %v", err)
		}
		defer agentSocket.Close()
		game.agentSocket = agentSocket
		game.agentInputCh = agentSocket.Actions()
		game.agentCommandCh = agentSocket.Commands()
		game.agentObserveRAMOff = off
		game.agentObserveRAMLen = length
		log.Printf("listening for agent actions on %s (%s)", *agentSocketPath, format)
	}
	if *saveStatePath != "" {
		defer func() {
			if err := saveSystemState(sys, *saveStatePath); err != nil {
				log.Printf("failed to save state %s: %v", *saveStatePath, err)
				return
			}
			log.Printf("saved state: %s", *saveStatePath)
		}()
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

func defaultStateSlotDir(romHash string) string {
	if romHash == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".snes", "saves", romHash)
}

func statePath(romPath, path string) string {
	if path != "" {
		return path
	}
	return defaultStatePath(romPath)
}

func saveSystemState(sys *snes.System, path string) error {
	state, err := sys.Serialize()
	if err != nil {
		return fmt.Errorf("serialize state: %w", err)
	}
	if err := os.WriteFile(path, state, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func loadSystemState(sys *snes.System, path string) error {
	return loadSystemStateWithOptions(sys, path, snes.UnserializeOptions{})
}

func loadSystemStateWithOptions(sys *snes.System, path string, opts snes.UnserializeOptions) error {
	state, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if err := sys.UnserializeWithOptions(state, opts); err != nil {
		return fmt.Errorf("unserialize state: %w", err)
	}
	return nil
}

type snesReplayRequest struct {
	ROMPath               string          `json:"rom_path"`
	StatePath             string          `json:"state_path"`
	Frames                int             `json:"frames"`
	InputsPath            string          `json:"inputs_path"`
	AllowStateROMMismatch bool            `json:"allow_state_rom_mismatch"`
	Target                json.RawMessage `json:"target"`
}

func loadSNESReplayRequest(path string) (snesReplayRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snesReplayRequest{}, fmt.Errorf("read request: %w", err)
	}
	var req snesReplayRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return snesReplayRequest{}, fmt.Errorf("parse request: %w", err)
	}
	if req.ROMPath == "" {
		return snesReplayRequest{}, fmt.Errorf("request missing rom_path")
	}
	if req.InputsPath == "" {
		return snesReplayRequest{}, fmt.Errorf("request missing inputs_path")
	}
	if req.Frames < 0 {
		return snesReplayRequest{}, fmt.Errorf("request frames must be >= 0")
	}
	return req, nil
}

func loadRequestInputs(path string) ([]uint16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inputs: %w", err)
	}
	var values []uint16
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("parse inputs: %w", err)
	}
	return values, nil
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

// buttonNames returns the button-name slice for a pad bitmask in a stable order.
func buttonNames(state uint16) []string {
	if state == 0 {
		return nil
	}
	order := []struct {
		name string
		mask uint16
	}{
		{"up", emulator.StandardButtonUp},
		{"down", emulator.StandardButtonDown},
		{"left", emulator.StandardButtonLeft},
		{"right", emulator.StandardButtonRight},
		{"a", emulator.StandardButtonA},
		{"b", emulator.StandardButtonB},
		{"x", emulator.StandardButtonX},
		{"y", emulator.StandardButtonY},
		{"l", emulator.StandardButtonL},
		{"r", emulator.StandardButtonR},
		{"start", emulator.StandardButtonStart},
		{"select", emulator.StandardButtonSelect},
	}
	var out []string
	for _, b := range order {
		if state&b.mask == b.mask {
			out = append(out, b.name)
		}
	}
	return out
}

// actionNameFor maps a pad bitmask to the closest discrete action_name.
// Diagonals win over single directions; A is reported only when no D-pad bit is held.
func actionNameFor(state uint16) string {
	if state == 0 {
		return "noop"
	}
	preferred := []string{
		"up_left", "up_right", "down_left", "down_right",
		"up_a", "down_a", "left_a", "right_a",
		"up_b", "down_b", "left_b", "right_b",
		"up", "down", "left", "right",
		"a", "b", "x", "y", "l", "r", "start", "select",
	}
	for _, name := range preferred {
		mask, ok := actionInputByName[name]
		if !ok || mask == 0 {
			continue
		}
		if state&mask == mask {
			return name
		}
	}
	return "noop"
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

var actionInputByName = map[string]uint16{
	"noop":       0,
	"up":         emulator.StandardButtonUp,
	"down":       emulator.StandardButtonDown,
	"left":       emulator.StandardButtonLeft,
	"right":      emulator.StandardButtonRight,
	"a":          emulator.StandardButtonA,
	"b":          emulator.StandardButtonB,
	"x":          emulator.StandardButtonX,
	"y":          emulator.StandardButtonY,
	"l":          emulator.StandardButtonL,
	"r":          emulator.StandardButtonR,
	"start":      emulator.StandardButtonStart,
	"select":     emulator.StandardButtonSelect,
	"up_left":    emulator.StandardButtonUp | emulator.StandardButtonLeft,
	"up_right":   emulator.StandardButtonUp | emulator.StandardButtonRight,
	"down_left":  emulator.StandardButtonDown | emulator.StandardButtonLeft,
	"down_right": emulator.StandardButtonDown | emulator.StandardButtonRight,
	"up_a":       emulator.StandardButtonUp | emulator.StandardButtonA,
	"down_a":     emulator.StandardButtonDown | emulator.StandardButtonA,
	"left_a":     emulator.StandardButtonLeft | emulator.StandardButtonA,
	"right_a":    emulator.StandardButtonRight | emulator.StandardButtonA,
	"up_b":       emulator.StandardButtonUp | emulator.StandardButtonB,
	"down_b":     emulator.StandardButtonDown | emulator.StandardButtonB,
	"left_b":     emulator.StandardButtonLeft | emulator.StandardButtonB,
	"right_b":    emulator.StandardButtonRight | emulator.StandardButtonB,
}

var actionInputByIndex = []uint16{
	actionInputByName["noop"],
	actionInputByName["up"],
	actionInputByName["down"],
	actionInputByName["left"],
	actionInputByName["right"],
	actionInputByName["a"],
	actionInputByName["b"],
	actionInputByName["x"],
	actionInputByName["y"],
	actionInputByName["l"],
	actionInputByName["r"],
	actionInputByName["start"],
	actionInputByName["select"],
	actionInputByName["up_left"],
	actionInputByName["up_right"],
	actionInputByName["down_left"],
	actionInputByName["down_right"],
	actionInputByName["up_a"],
	actionInputByName["down_a"],
	actionInputByName["left_a"],
	actionInputByName["right_a"],
	actionInputByName["up_b"],
	actionInputByName["down_b"],
	actionInputByName["left_b"],
	actionInputByName["right_b"],
}

type agentSocketHub struct {
	path     string
	format   snesagent.Format
	l        net.Listener
	actions  chan uint16
	commands chan snesagent.Command
	done     chan struct{}
	mu       sync.Mutex
	clients  map[net.Conn]*agentSocketClient
}

type agentSocketClient struct {
	conn net.Conn
	send chan snesagent.Observation
	once sync.Once
}

func (c *agentSocketClient) close() {
	c.once.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}

func listenAgentSocket(path string, format snesagent.Format) (*agentSocketHub, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	h := &agentSocketHub{
		path:     path,
		format:   format,
		l:        l,
		actions:  make(chan uint16, 32),
		commands: make(chan snesagent.Command, 16),
		done:     make(chan struct{}),
		clients:  make(map[net.Conn]*agentSocketClient),
	}
	go h.accept()
	return h, nil
}

func (h *agentSocketHub) Actions() <-chan uint16 {
	return h.actions
}

func (h *agentSocketHub) Commands() <-chan snesagent.Command {
	return h.commands
}

func (h *agentSocketHub) Close() {
	close(h.done)
	_ = h.l.Close()
	h.mu.Lock()
	for conn, client := range h.clients {
		client.close()
		delete(h.clients, conn)
	}
	h.mu.Unlock()
	_ = os.Remove(h.path)
}

func (h *agentSocketHub) accept() {
	for {
		conn, err := h.l.Accept()
		if err != nil {
			select {
			case <-h.done:
				return
			default:
				log.Printf("agent socket accept: %v", err)
				continue
			}
		}
		client := &agentSocketClient{
			conn: conn,
			send: make(chan snesagent.Observation, 1),
		}
		h.mu.Lock()
		h.clients[conn] = client
		h.mu.Unlock()
		go h.write(client)
		go h.read(conn)
	}
}

func (h *agentSocketHub) read(conn net.Conn) {
	defer func() {
		h.mu.Lock()
		client := h.clients[conn]
		if client != nil {
			client.close()
		}
		delete(h.clients, conn)
		h.mu.Unlock()
	}()
	reader := newAgentMessageReader(conn, h.format)
	for {
		msg, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("agent action: %v", err)
			continue
		}
		input, cmd, err := parseAgentMessage(msg)
		if err != nil {
			log.Printf("agent action: %v", err)
			continue
		}
		if cmd != nil {
			select {
			case h.commands <- *cmd:
			default:
				<-h.commands
				h.commands <- *cmd
			}
			continue
		}
		select {
		case h.actions <- *input:
		default:
			<-h.actions
			h.actions <- *input
		}
	}
}

func (h *agentSocketHub) write(client *agentSocketClient) {
	writer := newAgentObservationWriter(client.conn, h.format)
	for obs := range client.send {
		if err := writer.Write(obs); err != nil {
			log.Printf("agent observation write: %v", err)
			client.close()
			return
		}
	}
}

func (h *agentSocketHub) Broadcast(obs snesagent.Observation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, client := range h.clients {
		select {
		case client.send <- obs:
		default:
			select {
			case <-client.send:
			default:
			}
			select {
			case client.send <- obs:
			default:
			}
		}
	}
}

type agentMessageReader interface {
	Read() (snesagent.ClientMessage, error)
}

type agentObservationWriter interface {
	Write(snesagent.Observation) error
}

func newAgentMessageReader(r io.Reader, format snesagent.Format) agentMessageReader {
	if format == snesagent.FormatProto {
		return snesagent.NewProtoReader(r)
	}
	return snesagent.NewJSONReader(r)
}

func newAgentObservationWriter(w io.Writer, format snesagent.Format) agentObservationWriter {
	if format == snesagent.FormatProto {
		return snesagent.NewProtoWriter(w)
	}
	return snesagent.NewJSONWriter(w)
}

func parseAgentLine(line []byte) (*uint16, *snesagent.Command, error) {
	msg, err := snesagent.NewJSONReader(bytes.NewReader(append(line, '\n'))).Read()
	if err != nil {
		return nil, nil, err
	}
	return parseAgentMessage(msg)
}

func parseAgentMessage(msg snesagent.ClientMessage) (*uint16, *snesagent.Command, error) {
	cmd := snesagent.Command{Type: msg.Type, Path: msg.Path}
	switch cmd.Type {
	case "load_state", "save_state":
		return nil, &cmd, nil
	}
	if msg.ActionName != "" {
		input, err := parseAgentActionName(msg.ActionName)
		return &input, nil, err
	}
	if text, ok := msg.ActionText(); ok {
		input, err := parseAgentActionName(text)
		return &input, nil, err
	}
	if action, ok := msg.ActionIndex(); ok {
		input, err := parseAgentActionIndex(int(*action))
		return &input, nil, err
	}
	return nil, nil, fmt.Errorf("missing action_name or action")
}

func parseAgentActionLine(line []byte) (uint16, error) {
	input, cmd, err := parseAgentLine(line)
	if err != nil {
		return 0, err
	}
	if cmd != nil {
		return 0, fmt.Errorf("line is command %q, not action", cmd.Type)
	}
	if input == nil {
		return 0, fmt.Errorf("missing action")
	}
	return *input, nil
}

func parseAgentActionName(name string) (uint16, error) {
	input, ok := actionInputByName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return 0, fmt.Errorf("unknown action %q", name)
	}
	return input, nil
}

func parseAgentActionIndex(index int) (uint16, error) {
	if index < 0 || index >= len(actionInputByIndex) {
		return 0, fmt.Errorf("action index %d out of range", index)
	}
	return actionInputByIndex[index], nil
}

func parseAgentRAMRange(spec string) (int64, int, error) {
	if spec == "" {
		return 0, 0, nil
	}
	offText, lengthText, ok := strings.Cut(spec, ":")
	if !ok {
		return 0, 0, fmt.Errorf("expected offset:length")
	}
	off, err := strconv.ParseInt(offText, 0, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("offset: %w", err)
	}
	if off < 0 {
		return 0, 0, fmt.Errorf("offset must be >= 0")
	}
	length, err := strconv.Atoi(lengthText)
	if err != nil {
		return 0, 0, fmt.Errorf("length: %w", err)
	}
	if length < 0 {
		return 0, 0, fmt.Errorf("length must be >= 0")
	}
	return off, length, nil
}

func encodeU16LEBytes(values []uint16) []byte {
	buf := make([]byte, len(values)*2)
	for i, v := range values {
		buf[2*i] = byte(v)
		buf[2*i+1] = byte(v >> 8)
	}
	return buf
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
