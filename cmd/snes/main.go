package main

import (
	"flag"
	"fmt"
	"log"
	"os"
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
}

type AudioStream struct {
	system  *snes.System
	samples []int16
}

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
	if err := g.system.SetInputState(0, pollInput()); err != nil {
		return err
	}
	return g.system.RunFrame()
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Render PPU FrontBuffer
	// PPU FrontBuffer is []uint16 (RGB555: 0BBBBBGGGGGRRRRR or 00BBBBBGGGGGRRRR?)
	// SNES native is 0BBBBBGGGGGRRRRR (15-bit).
	// We convert to RGBA8888.

	width, height := 256, 224
	params := g.system.FrameBuffer()
	if len(params) != width*height {
		// Buffer not ready or sized incorrectly
		ebitenutil.DebugPrint(screen, fmt.Sprintf("SNES Emulator Running\nCycles: %d\nBuffer Mismatch", g.system.CPU.Cycles))
		return
	}

	// Prepare byte slice for Ebiten (RGBA8888)
	// Ideally we cache this buffer in Game struct to avoid GC alloc per frame.
	// But for now, let's allocate or use a global usage pattern if performance allows.
	pixels := make([]byte, width*height*4)

	for i, col16 := range params {
		// Format: xBBBBBGGGGGRRRRR
		// R = (col16 & 0x1F)
		// G = (col16 >> 5) & 0x1F
		// B = (col16 >> 10) & 0x1F

		r5 := (col16) & 0x1F
		g5 := (col16 >> 5) & 0x1F
		b5 := (col16 >> 10) & 0x1F

		// Convert to 8-bit (x8 + x/4 approx, or just shift left 3)
		// More accurate: (c * 255) / 31
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

	centerPixel := params[(height/2)*width+(width/2)]
	msg := fmt.Sprintf(
		"SNES Running\nCycles: %d\nPC: %04X\nCenter Pixel: %04X",
		g.system.CPU.Cycles,
		g.system.CPU.PC,
		centerPixel,
	)
	ebitenutil.DebugPrint(screen, msg)

}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	return 256, 224
}

func main() {
	fmt.Println("BSNES STARTING...")
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
	} else {
		fmt.Println("No ROM provided. Usage: snes [rom.sfc]")
		// Proceed empty?
	}

	// Power On
	sys.Power()

	ebiten.SetWindowSize(512, 448)
	ebiten.SetWindowTitle("bsnes-go")

	game := &Game{
		system: sys,
	}

	// Audio Init
	game.audioContext = audio.NewContext(32000)
	stream := &AudioStream{system: sys}
	player, err := game.audioContext.NewPlayer(stream)
	if err != nil {
		log.Fatal(err)
	}
	player.SetBufferSize(time.Millisecond * 100) // Latency buffer
	player.Play()
	game.audioPlayer = player

	// DEBUG: Verify Opcode 0xCD
	// fmt.Printf("DEBUG: Opcode 0xCD Name=%s Mode=%d Size=%d\n",
	// 	cpu.Opcodes[0xCD].Name, cpu.Opcodes[0xCD].Mode, cpu.Opcodes[0xCD].Size)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}

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
