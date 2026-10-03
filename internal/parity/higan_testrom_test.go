package parity

import (
	"encoding/json"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

const higanTestROMManifestPath = "testdata/higan_testrom_manifest.json"

type higanTestROMCase struct {
	Name             string                        `json:"name"`
	Owner            string                        `json:"owner"`
	Path             string                        `json:"path"`
	SHA256           string                        `json:"sha256"`
	Frames           int                           `json:"frames"`
	Expected         []higanTestROMExpectedRegion  `json:"expected_regions"`
	ExpectedValues   []higanTestROMExpectedValue   `json:"expected_values,omitempty"`
	KnownDivergences []higanTestROMKnownDivergence `json:"known_divergences,omitempty"`
}

type higanTestROMExpectedRegion struct {
	Name                 string `json:"name"`
	MinReferenceAgreeing uint32 `json:"min_reference_agreeing"`
}

type higanTestROMKnownDivergence struct {
	Region string `json:"region"`
	Addr   uint32 `json:"addr"`
	Go     uint8  `json:"go"`
	Ref    uint8  `json:"ref"`
}

type higanTestROMExpectedValue struct {
	Region  string `json:"region"`
	Addr    uint32 `json:"addr"`
	Value   uint8  `json:"value"`
	Comment string `json:"comment,omitempty"`
}

func TestHiganTestROMParity(t *testing.T) {
	if testing.Short() {
		t.Skip("external hardware-test ROM comparison omitted in short mode")
	}
	cases := readHiganTestROMManifest(t)
	checkHiganRunSelector(t, cases)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			checkFile(t, tc.Path)
			rom, err := os.ReadFile(tc.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.SHA256 != "" {
				if got := hashBytes(rom); got != tc.SHA256 {
					t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
				}
			}

			goSys := runHiganGoSystem(t, rom, tc.Frames)
			// Diagnostic ROMs run with distinct WRAM preconditions. Agreement
			// then proves at least one reference changed the byte, even if the
			// final value equals the other reference's startup fill.
			bsn := runHiganReferenceWithWRAM(t, bsnes.DefaultPath(), tc.Path, tc.Frames, 0xaa)
			s9x := runHiganReferenceWithWRAM(t, snes9x.DefaultPath(), tc.Path, tc.Frames, 0x55)

			regions := higanComparableRegions(bsn, s9x)
			for _, expected := range tc.Expected {
				region, ok := regions[expected.Name]
				if !ok {
					t.Fatalf("%s: region %s is not exposed by both references", tc.Name, expected.Name)
				}
				agreeing := compareHiganRegion(t, tc.Name, region, goSys, bsn, s9x)
				if uint32(agreeing) < expected.MinReferenceAgreeing {
					t.Fatalf("%s %s reference-agreeing bytes = %d, want at least %d", tc.Name, expected.Name, agreeing, expected.MinReferenceAgreeing)
				}
			}
			for _, divergence := range tc.KnownDivergences {
				region, ok := regions[divergence.Region]
				if !ok {
					t.Fatalf("%s: known divergence region %s is not exposed by both references", tc.Name, divergence.Region)
				}
				checkHiganKnownDivergence(t, tc.Name, region, divergence, goSys, bsn, s9x)
			}
			for _, expected := range tc.ExpectedValues {
				region, ok := regions[expected.Region]
				if !ok {
					t.Fatalf("%s: expected value region %s is not exposed by both references", tc.Name, expected.Region)
				}
				checkHiganExpectedValue(t, tc.Name, region, expected, goSys, bsn, s9x)
			}
			t.Logf("%s frame_hashes Go=%s bsnes=%s snes9x=%s",
				tc.Name, hashHiganGoFrame(goSys, bsn), hashBytes(bsn.Frame), hashBytes(s9x.Frame))
		})
	}
}

type higanRegion struct {
	name string
	size uint32
	read func(*snes.System, uint32) uint8
	id   uint32
}

func readHiganTestROMManifest(t *testing.T) []higanTestROMCase {
	t.Helper()
	raw, err := os.ReadFile(higanTestROMManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []higanTestROMCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatalf("%s has no cases", higanTestROMManifestPath)
	}
	for _, tc := range cases {
		if tc.Name == "" || tc.Owner == "" || tc.Path == "" || tc.SHA256 == "" || tc.Frames <= 0 {
			t.Fatalf("invalid higan test ROM manifest entry: %+v", tc)
		}
		if len(tc.Expected) == 0 && len(tc.KnownDivergences) == 0 {
			t.Fatalf("%s has no expected regions or known divergences", tc.Name)
		}
		for _, divergence := range tc.KnownDivergences {
			if divergence.Region == "" {
				t.Fatalf("%s has known divergence without region", tc.Name)
			}
		}
		for _, expected := range tc.ExpectedValues {
			if expected.Region == "" {
				t.Fatalf("%s has expected value without region", tc.Name)
			}
		}
	}
	return cases
}

func checkHiganRunSelector(t *testing.T, cases []higanTestROMCase) {
	t.Helper()
	run := flag.Lookup("test.run")
	if run == nil {
		return
	}
	const prefix = "TestHiganTestROMParity/"
	pattern := run.Value.String()
	i := strings.Index(pattern, prefix)
	if i < 0 {
		return
	}
	rowPattern := pattern[i+len(prefix):]
	if rowPattern == "" {
		return
	}
	if slash := strings.IndexByte(rowPattern, '/'); slash >= 0 {
		rowPattern = rowPattern[:slash]
	}
	re, err := regexp.Compile(rowPattern)
	if err != nil {
		t.Fatalf("compile higan manifest row selector %q: %v", rowPattern, err)
	}
	for _, tc := range cases {
		if re.MatchString(tc.Name) {
			return
		}
	}
	t.Fatalf("-run selector %q matches no rows in %s", rowPattern, higanTestROMManifestPath)
}

func runHiganGoSystem(t *testing.T, rom []byte, frames int) *snes.System {
	t.Helper()
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()
	for frame := 0; frame < frames; frame++ {
		if err := sys.Run(); err != nil {
			t.Fatal(err)
		}
	}
	return sys
}

func runHiganReference(t *testing.T, corePath, romPath string, frames int) *libretro.Bridge {
	t.Helper()
	return runHiganReferenceStart(t, corePath, romPath, frames, nil)
}

func runHiganReferenceWithWRAM(t *testing.T, corePath, romPath string, frames int, fill byte) *libretro.Bridge {
	t.Helper()
	return runHiganReferenceStart(t, corePath, romPath, frames, &fill)
}

func runHiganReferenceStart(t *testing.T, corePath, romPath string, frames int, fill *byte) *libretro.Bridge {
	t.Helper()
	checkFile(t, corePath)
	core, err := libretro.New(corePath)
	if err != nil {
		t.Fatal(err)
	}
	closeReference(t, core)
	core.Logger = t
	core.Init()
	if !core.LoadGame(romPath) {
		t.Fatalf("load %s", romPath)
	}
	if fill != nil {
		if err := core.SetInitialWRAM(*fill); err != nil {
			t.Fatal(err)
		}
		t.Logf("reference startup=controlled-complementary-wram fill=%02X bytes=131072 (not native default)", *fill)
	}
	captureReferenceWRAMStart(t, core, corePath)
	for frame := 0; frame < frames; frame++ {
		core.Run()
	}
	return core
}

func higanComparableRegions(bsn, s9x *libretro.Bridge) map[string]higanRegion {
	regions := []higanRegion{
		{name: "WRAM", size: 0x10000, read: func(sys *snes.System, addr uint32) uint8 { return sys.Bus.Read(0x7E0000 | addr) }, id: 2},
		{name: "VRAM", size: 0x10000, read: func(sys *snes.System, addr uint32) uint8 { return sys.PPU.VRAM[addr] }, id: 3},
		{name: "CGRAM", size: 0x200, read: func(sys *snes.System, addr uint32) uint8 { return sys.PPU.CGRAM[addr] }, id: 4},
	}
	out := make(map[string]higanRegion)
	for _, region := range regions {
		if bsn.GetMemorySize(region.id) >= uint64(region.size) && s9x.GetMemorySize(region.id) >= uint64(region.size) {
			out[region.name] = region
		}
	}
	return out
}

func compareHiganRegion(t *testing.T, testName string, region higanRegion, sys *snes.System, bsn, s9x *libretro.Bridge) int {
	t.Helper()
	agreeing := 0
	for addr := uint32(0); addr < region.size; addr++ {
		bsnesByte := bsn.PeekMemory(region.id, addr)
		snes9xByte := s9x.PeekMemory(region.id, addr)
		if bsnesByte != snes9xByte {
			continue
		}
		if region.name == "WRAM" {
			observed, err := compareReferenceWRAM(t, bsn, s9x, addr, region.read(sys, addr), bsnesByte)
			if !observed {
				continue
			}
			if err != nil {
				t.Fatalf("%s WRAM divergence at $%04X: %v", testName, addr, err)
			}
		}
		agreeing++
		if got := region.read(sys, addr); got != bsnesByte {
			t.Fatalf("%s %s divergence at $%04X: Go=%02X Ref=%02X", testName, region.name, addr, got, bsnesByte)
		}
	}
	if agreeing == 0 {
		t.Fatalf("%s %s has no reference-agreeing bytes", testName, region.name)
	}
	t.Logf("%s %s: Go matches %d reference-agreeing bytes (WRAM excludes unchanged startup coincidences)", testName, region.name, agreeing)
	return agreeing
}

func checkHiganKnownDivergence(t *testing.T, testName string, region higanRegion, divergence higanTestROMKnownDivergence, sys *snes.System, bsn, s9x *libretro.Bridge) {
	t.Helper()
	if divergence.Addr >= region.size {
		t.Fatalf("%s %s known divergence address $%04X outside region size $%04X", testName, region.name, divergence.Addr, region.size)
	}
	bsnesByte := bsn.PeekMemory(region.id, divergence.Addr)
	snes9xByte := s9x.PeekMemory(region.id, divergence.Addr)
	if bsnesByte != snes9xByte {
		t.Fatalf("%s %s known divergence address $%04X no longer reference-agreeing: bsnes=%02X snes9x=%02X", testName, region.name, divergence.Addr, bsnesByte, snes9xByte)
	}
	got := region.read(sys, divergence.Addr)
	if got != divergence.Go || bsnesByte != divergence.Ref {
		t.Fatalf("%s %s known divergence at $%04X changed: Go=%02X Ref=%02X, want Go=%02X Ref=%02X",
			testName, region.name, divergence.Addr, got, bsnesByte, divergence.Go, divergence.Ref)
	}
	t.Logf("%s %s known divergence at $%04X: Go=%02X Ref=%02X", testName, region.name, divergence.Addr, got, bsnesByte)
}

func checkHiganExpectedValue(t *testing.T, testName string, region higanRegion, expected higanTestROMExpectedValue, sys *snes.System, bsn, s9x *libretro.Bridge) {
	t.Helper()
	if expected.Addr >= region.size {
		t.Fatalf("%s %s expected value address $%04X outside region size $%04X", testName, region.name, expected.Addr, region.size)
	}
	bsnesByte := bsn.PeekMemory(region.id, expected.Addr)
	snes9xByte := s9x.PeekMemory(region.id, expected.Addr)
	if bsnesByte != snes9xByte {
		t.Fatalf("%s %s expected value address $%04X no longer reference-agreeing: bsnes=%02X snes9x=%02X",
			testName, region.name, expected.Addr, bsnesByte, snes9xByte)
	}
	got := region.read(sys, expected.Addr)
	if got != expected.Value || bsnesByte != expected.Value {
		t.Fatalf("%s %s expected value at $%04X changed: Go=%02X Ref=%02X, want %02X",
			testName, region.name, expected.Addr, got, bsnesByte, expected.Value)
	}
	t.Logf("%s %s expected value at $%04X: Go=%02X Ref=%02X", testName, region.name, expected.Addr, got, bsnesByte)
}

func hashHiganGoFrame(sys *snes.System, ref *libretro.Bridge) string {
	width := int(referenceFrameBGR555Width(ref))
	height := int(ref.FrameHeight)
	if width <= 0 || height <= 0 {
		width = 256
		height = 224
	}
	return hashBytes(sys.PPU.AppendFrameBGR555Size(nil, width, height))
}
