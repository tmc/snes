package parity

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

func TestVRAMStartupProbe(t *testing.T) {
	if os.Getenv("SNES_VRAM_STARTUP_PROBE") == "" {
		t.Skip("set SNES_VRAM_STARTUP_PROBE=1")
	}
	name := os.Getenv("SNES_VRAM_STARTUP_ROM")
	if name == "" {
		t.Fatal("SNES_VRAM_STARTUP_ROM not set")
	}
	frames := envStartupFrames("SNES_VRAM_STARTUP_FRAMES", 3)
	start := envStartupFrames("SNES_VRAM_STARTUP_START", 0)
	count := envStartupFrames("SNES_VRAM_STARTUP_COUNT", 512)

	romPath, err := findStartupROM(name)
	if err != nil {
		t.Fatal(err)
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}

	goSys := snes.NewSystem(nil)
	if err := goSys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	goSys.Power()

	refs := []struct {
		name string
		path string
	}{
		{name: "bsnes", path: bsnes.DefaultPath()},
		{name: "snes9x", path: snes9x.DefaultPath()},
	}
	cores := make([]*libretro.Bridge, len(refs))
	for i, ref := range refs {
		core, err := libretro.New(ref.path)
		if err != nil {
			t.Fatalf("%s libretro.New: %v", ref.name, err)
		}
		if ref.name == "bsnes" {
			core.SetCoreVariable("bsnes_ppu_fast", "OFF")
			core.SetCoreVariable("bsnes_entropy", "None")
		}
		core.Init()
		if !core.LoadGame(romPath) {
			t.Fatalf("%s LoadGame %s failed", ref.name, romPath)
		}
		cores[i] = core
	}

	fmt.Printf("STARTUP\t%s\trom=%s\tstart=%04X\tcount=%d\n", name, romPath, start, count)
	printStartupRow("go", 0, goSys.PPU.VRAM[start:start+count], goStartupPC(goSys))
	for i, ref := range refs {
		printStartupRefRow(ref.name, 0, cores[i], start, count)
	}
	for frame := 1; frame <= frames; frame++ {
		if err := goSys.Run(); err != nil {
			t.Fatalf("Go frame %d: %v", frame, err)
		}
		for _, core := range cores {
			core.Run()
		}
		printStartupRow("go", frame, goSys.PPU.VRAM[start:start+count], goStartupPC(goSys))
		for i, ref := range refs {
			printStartupRefRow(ref.name, frame, cores[i], start, count)
		}
	}
}

func printStartupRefRow(name string, frame int, core *libretro.Bridge, start, count int) {
	buf := make([]byte, count)
	for i := range buf {
		buf[i] = core.PeekMemory(3, uint32(start+i))
	}
	printStartupRow(name, frame, buf, "")
}

func printStartupRow(name string, frame int, buf []byte, extra string) {
	nonzero := 0
	first := -1
	last := -1
	for i, v := range buf {
		if v == 0 {
			continue
		}
		nonzero++
		if first < 0 {
			first = i
		}
		last = i
	}
	fmt.Printf("ROW\t%s\tframe=%d\tnonzero=%d\tfirst=%04X\tlast=%04X\tsample=%s%s\n",
		name, frame, nonzero, first, last, startupSample(buf, first), extra)
}

func goStartupPC(sys *snes.System) string {
	return fmt.Sprintf("\tpbpc=%02X:%04X\tcycles=%d", sys.CPU.PB, sys.CPU.PC, sys.CPU.Cycles)
}

func startupSample(buf []byte, first int) string {
	if first < 0 {
		return ""
	}
	end := first + 16
	if end > len(buf) {
		end = len(buf)
	}
	var b strings.Builder
	for i := first; i < end; i++ {
		if i > first {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02X", buf[i])
	}
	return b.String()
}

func envStartupFrames(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func findStartupROM(name string) (string, error) {
	candidates := []string{}
	if filepath.IsAbs(name) {
		candidates = append(candidates, name)
	}
	if dir := os.Getenv("ROM_SRC_DIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	candidates = append(candidates, filepath.Join(os.Getenv("HOME"), "var", "snes-roms", name))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ROM %q not found; checked ROM_SRC_DIR and ~/var/snes-roms", name)
}
