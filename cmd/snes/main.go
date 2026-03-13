package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
}

type AudioStream struct {
	system  *snes.System
	samples []int16
}

const rewindCapacity = 300

// Read implements io.Reader for AudioStream
func (s *AudioStream) Read(buf []byte) (int, error) {
	sampleCount := len(buf) / 2
	if cap(s.samples) < sampleCount {
		s.samples = make([]int16, sampleCount)
	}
	samples := s.samples[:sampleCount]
	n := s.system.DrainAudio(samples)
	for i := n; i < len(samples); i++ {
		samples[i] = 0
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

	state, err := g.system.Serialize()
	if err == nil {
		g.pushRewind(state)
	}

	if err := g.system.SetInputState(0, pollInput()); err != nil {
		return err
	}
	if err := g.system.RunFrame(); err != nil {
		return err
	}

	if g.stepFrame {
		g.stepFrame = false
	}
	g.status = ""
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	params := g.system.FrameBuffer()
	width := 256
	if len(params)%width != 0 || len(params) == 0 {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("SNES Emulator Running\nCycles: %d\nBuffer Mismatch", g.system.CPU.Cycles))
		return
	}
	height := len(params) / width
	pixelCount := width * height

	if cap(g.pixels) < pixelCount*4 {
		g.pixels = make([]byte, pixelCount*4)
	}
	pixels := g.pixels[:pixelCount*4]

	for i := 0; i < pixelCount; i++ {
		col16 := params[i]
		r5 := (col16) & 0x1F
		g5 := (col16 >> 5) & 0x1F
		b5 := (col16 >> 10) & 0x1F

		r8 := uint8((r5 * 255) / 31)
		g8 := uint8((g5 * 255) / 31)
		b8 := uint8((b5 * 255) / 31)

		idx := i * 4
		pixels[idx] = r8
		pixels[idx+1] = g8
		pixels[idx+2] = b8
		pixels[idx+3] = 0xFF // Alpha
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
	msg := fmt.Sprintf("PC:%02X:%04X Cy:%d [%s]\nP:pause O:step Tab:ff Backspace:rewind F5/F8:state G:runahead R:reset",
		g.system.CPU.PB, g.system.CPU.PC, g.system.CPU.Cycles, mode)
	ebitenutil.DebugPrintAt(screen, msg, 4, 4)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	return 256, 240
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
}

func main() {
	fmt.Println("BSNES STARTING...")
	cheatPath := flag.String("cheats", "", "path to cheats file")
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
		fmt.Println("No ROM provided. Usage: snes [rom.sfc]")
		// Proceed empty?
	}

	// Power On
	sys.Power()

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
	game.audioContext = audio.NewContext(32000)
	stream := &AudioStream{system: sys}
	player, err := game.audioContext.NewPlayer(stream)
	if err != nil {
		log.Fatal(err)
	}
	player.SetBufferSize(4096)
	player.Play()
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
