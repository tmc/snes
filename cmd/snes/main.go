package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cartridge"
)

type Game struct {
	system *snes.System
}

func (g *Game) Update() error {
	// Run one frame (approx 600 cycles line * 262 lines?)
	// Turbo Mode for Verification
	for i := 0; i < 100; i++ {
		g.system.Run()

		if i == 0 {
			fmt.Printf("STATUS: PC=%04X Cycles=%d INIDISP=%02X\n", g.system.CPU.PC, g.system.CPU.Cycles, g.system.PPU.INIDISP)
		}

		// Monitor for Screen On (INIDISP bit 7 == 0)
		if g.system.PPU.INIDISP&0x80 == 0 {
			fmt.Printf("MONITOR: Screen On! INIDISP=%02X Cycles=%d\n", g.system.PPU.INIDISP, g.system.CPU.Cycles)
		}
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Render PPU FrontBuffer
	// PPU FrontBuffer is []uint16 (RGB555: 0BBBBBGGGGGRRRRR or 00BBBBBGGGGGRRRR?)
	// SNES native is 0BBBBBGGGGGRRRRR (15-bit).
	// We convert to RGBA8888.

	width, height := 256, 224
	params := g.system.PPU.FrontBuffer
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

	inidisp := g.system.PPU.INIDISP
	centerPixel := params[(height/2)*width+(width/2)]
	// if g.system.PPU.FrameCount%60 == 0 {
	// 	fmt.Println("CONSOLE DEBUG: SNES Running")
	// 	fmt.Printf("Cycles: %d\n", g.system.CPU.Cycles)
	// 	fmt.Printf("INIDISP: %02X\n", g.system.PPU.INIDISP)
	// }
	msg := fmt.Sprintf("SNES Running\nCycles: %d\nINIDISP: %02X\nCenter Pixel: %04X", g.system.CPU.Cycles, inidisp, centerPixel)

	inidisp = g.system.PPU.INIDISP
	centerPixel = params[(height/2)*width+(width/2)]
	// if g.system.PPU.FrameCount%60 == 0 {
	// ... debug logs ...
	// }
	msg = fmt.Sprintf("SNES Running\nCycles: %d\nPC: %04X\nINIDISP: %02X\nCenter Pixel: %04X",
		g.system.CPU.Cycles, g.system.CPU.PC, inidisp, centerPixel)
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
		cart := cartridge.New(data)
		cart.MapToBus(sys.Bus)
	} else {
		fmt.Println("No ROM provided. Usage: snes [rom.sfc]")
		// Proceed empty?
	}

	// Power On
	sys.Load()

	ebiten.SetWindowSize(512, 448)
	ebiten.SetWindowTitle("bsnes-go")

	game := &Game{
		system: sys,
	}
	game.system.APU.OutPorts[1] = 0xBB

	// DEBUG: Verify Opcode 0xCD
	// fmt.Printf("DEBUG: Opcode 0xCD Name=%s Mode=%d Size=%d\n",
	// 	cpu.Opcodes[0xCD].Name, cpu.Opcodes[0xCD].Mode, cpu.Opcodes[0xCD].Size)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
