package parity

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

func TestSPCTimerReferenceObservability(t *testing.T) {
	tc, ok := higanManifestCase(t, "SPCTimer")
	if !ok {
		t.Fatalf("%s has no SPCTimer row", higanTestROMManifestPath)
	}
	checkFile(t, tc.Path)
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes(rom); got != tc.SHA256 {
		t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
	}

	goSys := runHiganGoSystem(t, rom, tc.Frames)
	bsn := runHiganReference(t, bsnes.DefaultPath(), tc.Path, tc.Frames)
	s9x := runHiganReference(t, snes9x.DefaultPath(), tc.Path, tc.Frames)

	goResult := goSys.Bus.Read(0x7E0001)
	bsnesResult := bsn.PeekWRAM(1)
	snes9xResult := s9x.PeekWRAM(1)
	if bsnesResult != snes9xResult {
		t.Fatalf("SPCTimer reference result mismatch: bsnes WRAM $0001=%02X snes9x=%02X", bsnesResult, snes9xResult)
	}
	if bsnesResult != 0x38 {
		t.Fatalf("SPCTimer reference result WRAM $0001=%02X, want 38", bsnesResult)
	}
	if goResult != 0x10 {
		t.Fatalf("SPCTimer Go result WRAM $0001=%02X, want current diagnostic value 10", goResult)
	}
	t.Logf("SPCTimer WRAM $0001: Go=%02X bsnes=%02X snes9x=%02X", goResult, bsnesResult, snes9xResult)
	t.Logf("bsnes memory ids: %s", libretroMemoryMap(bsn, 31))
	t.Logf("snes9x memory ids: %s", libretroMemoryMap(s9x, 31))

	bsnesAPU := apuramCandidateIDs(bsn)
	snes9xAPU := apuramCandidateIDs(s9x)
	if len(bsnesAPU) != 0 || len(snes9xAPU) != 0 {
		t.Fatalf("unexpected APURAM-sized libretro memory ids outside standard regions: bsnes=%v snes9x=%v; wire these before keeping the blocker", bsnesAPU, snes9xAPU)
	}
	t.Log("standard libretro memory API exposes no APURAM-sized id outside WRAM/VRAM/CGRAM; SPCTimer reference S-SMP PC, $F4-$F7, $FD, and APURAM $00DC-$00DF remain unavailable")
}

func higanManifestCase(t *testing.T, name string) (higanTestROMCase, bool) {
	t.Helper()
	for _, tc := range readHiganTestROMManifest(t) {
		if tc.Name == name {
			return tc, true
		}
	}
	return higanTestROMCase{}, false
}

func libretroMemoryMap(core *libretro.Bridge, maxID uint32) string {
	var parts []string
	for id := uint32(0); id <= maxID; id++ {
		if size := core.GetMemorySize(id); size != 0 {
			parts = append(parts, strings.Join([]string{strconv.FormatUint(uint64(id), 10), strconv.FormatUint(size, 10)}, ":"))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

func apuramCandidateIDs(core *libretro.Bridge) []uint32 {
	var ids []uint32
	for id := uint32(5); id <= 31; id++ {
		if core.GetMemorySize(id) == 0x10000 {
			ids = append(ids, id)
		}
	}
	return ids
}
