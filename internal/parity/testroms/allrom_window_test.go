package testroms

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

// TestAllROMVRAMWindow is an env-gated all-ROM VRAM parity scanner. It uses
// one subprocess per ROM so libretro/purego callback registrations do not
// accumulate in one process. A full scan can exceed Go's default test timeout;
// run it with -timeout 30m.
func TestAllROMVRAMWindow(t *testing.T) {
	if os.Getenv("SNES_TESTROM_ALL") == "" {
		t.Skip("set SNES_TESTROM_ALL=1 to scan ROM_SRC_DIR or ~/var/snes-roms; use -timeout 30m for full scans")
	}
	roms := allTestROMs(t)
	if len(roms) == 0 {
		t.Skip("no .sfc/.smc ROMs found in ROM_SRC_DIR or ~/var/snes-roms")
	}
	if limit := envInt("SNES_TESTROM_ALL_LIMIT", 0); limit > 0 && limit < len(roms) {
		roms = roms[:limit]
	}

	total := len(roms)
	skipped := 0
	exact := 0
	near100 := 0
	near1000 := 0
	for _, rom := range roms {
		res := runAllROMWorker(t, rom)
		t.Run(res.name, func(t *testing.T) {
			if res.skipped {
				t.Skip(res.skipReason)
			}
			t.Logf("best=%d frame=%d go_frame=%d exact=%v raw_frame=%d raw_diffs=%d",
				res.bestDiffs, res.bestFrame, res.bestGoFrame, res.exact, res.rawFrame, res.rawDiffs)
		})
		if res.skipped {
			skipped++
			continue
		}
		if res.exact {
			exact++
		}
		if res.bestDiffs <= 100 {
			near100++
		}
		if res.bestDiffs <= 1000 {
			near1000++
		}
	}
	measured := total - skipped
	t.Logf("all-ROM window summary: exact=%d/%d <=100=%d/%d <=1000=%d/%d skipped=%d total=%d",
		exact, measured, near100, measured, near1000, measured, skipped, total)
}

func TestAllROMVRAMWindowWorker(t *testing.T) {
	if os.Getenv("SNES_TESTROM_ALL_WORKER") == "" {
		t.Skip("worker helper")
	}
	romPath := os.Getenv("SNES_TESTROM_WORKER_ROM")
	if romPath == "" {
		t.Fatal("SNES_TESTROM_WORKER_ROM not set")
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}
	goFrame := envInt("SNES_TESTROM_GO_FRAME", 30)
	goWindow := envInt("SNES_TESTROM_GO_WINDOW", 0)
	refFrames := envInt("SNES_TESTROM_REF_WINDOW", 90)
	if refFrames < goFrame {
		refFrames = goFrame
	}
	if goWindow < 0 {
		goWindow = 0
	}
	refCore := os.Getenv("SNES_TESTROM_REF")
	if refCore == "" {
		refCore = "snes9x"
	}
	corePath, err := allROMCorePath(refCore)
	if err != nil {
		t.Fatal(err)
	}

	goSys := snes.NewSystem(nil)
	if err := goSys.LoadROM(rom); err != nil {
		if errors.Is(err, cartridge.ErrUnsupportedCoprocessor) {
			fmt.Printf("SKIP\t%s\t%s\n", filepath.Base(romPath), err)
			return
		}
		t.Fatalf("LoadROM: %v", err)
	}
	goSys.Power()
	runGoFrame := goSys.Run
	if os.Getenv("SNES_TESTROM_RUN_FULL_FRAME") != "" {
		runGoFrame = goSys.RunFrame
	}
	goFrames := 1
	if goWindow > 0 {
		goFrames = goWindow
	}
	goVRAM := make([][]byte, goFrames)
	for i := 0; i < goFrame+goFrames-1; i++ {
		if err := runGoFrame(); err != nil {
			t.Fatalf("Run frame %d: %v", i, err)
		}
		if i >= goFrame-1 {
			vram := make([]byte, len(goSys.PPU.VRAM))
			copy(vram, goSys.PPU.VRAM[:])
			goVRAM[i-goFrame+1] = vram
		}
	}
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatalf("libretro.New(%s): %v", corePath, err)
	}
	if refCore == "bsnes" {
		// bsnes defaults to its fast scanline PPU, which keeps a separate
		// VRAM store from retro_get_memory_data. Use the accuracy PPU for
		// memory parity, and make cold-power memory deterministic.
		core.SetCoreVariable("bsnes_ppu_fast", "OFF")
		core.SetCoreVariable("bsnes_entropy", "None")
	}
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("LoadGame %s", romPath)
	}

	bestFrame := 0
	bestGoFrame := 0
	bestDiffs := 1 << 30
	rawDiffs := -1
	for frame := 1; frame <= refFrames; frame++ {
		core.Run()
		diffs := normalizedDiffs(goVRAM[0], core)
		goMatch := goFrame
		if goWindow > 0 {
			for i, vram := range goVRAM[1:] {
				if d := normalizedDiffs(vram, core); d < diffs {
					diffs = d
					goMatch = goFrame + i + 1
				}
			}
		}
		if frame == goFrame {
			rawDiffs = diffs
		}
		if diffs < bestDiffs {
			bestDiffs = diffs
			bestFrame = frame
			bestGoFrame = goMatch
		}
		if diffs == 0 && frame >= goFrame {
			break
		}
	}
	fmt.Printf("RESULT\t%s\t%d\t%d\t%v\t%d\t%d\t%d\n",
		filepath.Base(romPath), bestDiffs, bestFrame, bestDiffs == 0, goFrame, rawDiffs, bestGoFrame)
}

type allROMResult struct {
	name        string
	bestDiffs   int
	bestFrame   int
	bestGoFrame int
	exact       bool
	rawFrame    int
	rawDiffs    int
	skipped     bool
	skipReason  string
}

func runAllROMWorker(t *testing.T, rom string) allROMResult {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestAllROMVRAMWindowWorker", "-test.v")
	cmd.Env = append(os.Environ(),
		"SNES_TESTROM_ALL_WORKER=1",
		"SNES_TESTROM_WORKER_ROM="+rom,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("worker %s: %v\n%s", rom, err, out.String())
	}
	res, err := parseAllROMWorker(out.Bytes())
	if err != nil {
		t.Fatalf("parse worker %s: %v\n%s", rom, err, out.String())
	}
	return res
}

func parseAllROMWorker(out []byte) (allROMResult, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "RESULT\t") {
			if strings.HasPrefix(line, "SKIP\t") {
				fields := strings.SplitN(line, "\t", 3)
				if len(fields) != 3 {
					return allROMResult{}, fmt.Errorf("malformed SKIP line %q", line)
				}
				return allROMResult{name: fields[1], skipped: true, skipReason: fields[2]}, nil
			}
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			return allROMResult{}, fmt.Errorf("malformed RESULT line %q", line)
		}
		return allROMResult{
			name:        fields[1],
			bestDiffs:   mustAtoi(fields[2]),
			bestFrame:   mustAtoi(fields[3]),
			exact:       fields[4] == "true",
			rawFrame:    mustAtoi(fields[5]),
			rawDiffs:    mustAtoi(fields[6]),
			bestGoFrame: mustAtoi(fields[7]),
		}, nil
	}
	if err := sc.Err(); err != nil {
		return allROMResult{}, err
	}
	return allROMResult{}, fmt.Errorf("missing RESULT line")
}

func allTestROMs(t *testing.T) []string {
	t.Helper()
	var roms []string
	for _, dir := range testROMDirs(t) {
		if !dirExists(dir) {
			continue
		}
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != dir && strings.Count(strings.TrimPrefix(p, dir+string(os.PathSeparator)), string(os.PathSeparator)) >= 2 {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(path.Ext(p))
			if ext == ".sfc" || ext == ".smc" {
				roms = append(roms, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	sort.Strings(roms)
	return roms
}

func allROMCorePath(name string) (string, error) {
	switch name {
	case "bsnes":
		return bsnes.DefaultPath(), nil
	case "snes9x":
		return snes9x.DefaultPath(), nil
	default:
		return "", fmt.Errorf("unknown SNES_TESTROM_REF %q", name)
	}
}

func normalizedDiffs(goVRAM []byte, ref *libretro.Bridge) int {
	diffs := 0
	for addr := uint32(0); addr < 0x10000; addr++ {
		g := goVRAM[addr]
		r := ref.PeekMemory(3, addr)
		if g == r || (g == 0x00 && r == 0x55) {
			continue
		}
		diffs++
	}
	return diffs
}

func envInt(name string, def int) int {
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

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}
