package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/parity/libretro"
	"github.com/tmc/snes/internal/parity/libretro/bsnes"
	"github.com/tmc/snes/internal/parity/libretro/snes9x"
)

const bsnesSPCTimerTraceCoreSHA256 = "fc950fb0d814b77f6751e1b9af15d4b45af13921ae081d95313c2dba020ebc60"
const bsnesSPCTimerTraceSHA256 = "c563794c21270d365383161f85f7859643141eca4ce601d1a3a1e5dd7dc34385"

func TestSPCTimerReferenceObservability(t *testing.T) {
	if os.Getenv("SNES_SPCTIMER_REFERENCE_OBSERVABILITY") != "1" {
		t.Skip("set SNES_SPCTIMER_REFERENCE_OBSERVABILITY=1 to run the SPCTimer reference observability probe")
	}
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
	if goResult != 0x38 {
		t.Fatalf("SPCTimer Go result WRAM $0001=%02X, want 38", goResult)
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

type spcTimerTraceEvent struct {
	Event       string             `json:"event"`
	Frame       int                `json:"frame"`
	Addr        string             `json:"addr"`
	Data        string             `json:"data"`
	APURAMDCDF  string             `json:"apuram_dc_df"`
	APURAMF4FF  string             `json:"apuram_f4_ff"`
	SPCPC       string             `json:"spc_pc"`
	CPUAPUCycle int64              `json:"cpu_cycle"`
	SMPAPUCycle int64              `json:"smp_cycle"`
	T0          spcTimerTraceTimer `json:"t0"`
	T1          spcTimerTraceTimer `json:"t1"`
	T2          spcTimerTraceTimer `json:"t2"`
}

type spcTimerTraceTimer struct {
	Stage0 int `json:"stage0"`
	Stage1 int `json:"stage1"`
	Stage2 int `json:"stage2"`
	Stage3 int `json:"stage3"`
	Line   int `json:"line"`
	Enable int `json:"enable"`
	Target int `json:"target"`
}

type spcTimerTraceSummary struct {
	rows                int
	sha256              string
	nonzeroFDRead       bool
	apuramDCDFSignal    bool
	cpuAPUPort          bool
	cpu2141Read0A       bool
	smpPort             bool
	smpF5Write50        bool
	firstNonzeroFDFrame int
	firstCPUPortFrame   int
	firstSMPPortFrame   int
}

func readSPCTimerBsnesTrace(t *testing.T, path string) spcTimerTraceSummary {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatalf("%s is empty", path)
	}
	summary := spcTimerTraceSummary{
		sha256:              hashBytes(raw),
		firstNonzeroFDFrame: -1,
		firstCPUPortFrame:   -1,
		firstSMPPortFrame:   -1,
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev spcTimerTraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s line %d: %v", path, summary.rows+1, err)
		}
		summary.rows++
		event := strings.ToLower(ev.Event)
		addr := strings.ToLower(ev.Addr)
		data := strings.ToLower(ev.Data)
		if event == "smp-read" && addr == "00fd" && data != "" && data != "00" {
			summary.nonzeroFDRead = true
			if summary.firstNonzeroFDFrame < 0 {
				summary.firstNonzeroFDFrame = ev.Frame
			}
		}
		if strings.ToLower(ev.APURAMDCDF) == "d06cd0a9" {
			summary.apuramDCDFSignal = true
		}
		if (event == "cpu-apu-read" || event == "cpu-apu-write") && addr >= "2140" && addr <= "2143" {
			summary.cpuAPUPort = true
			if summary.firstCPUPortFrame < 0 {
				summary.firstCPUPortFrame = ev.Frame
			}
			if event == "cpu-apu-read" && addr == "2141" && data == "0a" {
				summary.cpu2141Read0A = true
			}
		}
		if (event == "smp-read" || event == "smp-write") && addr >= "00f4" && addr <= "00f7" {
			summary.smpPort = true
			if summary.firstSMPPortFrame < 0 {
				summary.firstSMPPortFrame = ev.Frame
			}
			if event == "smp-write" && addr == "00f5" && data == "50" {
				summary.smpF5Write50 = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if summary.rows == 0 {
		t.Fatalf("%s has no trace rows", path)
	}
	return summary
}

func findSPCTimerTraceEvent(t *testing.T, path string, match func(spcTimerTraceEvent) bool) (spcTimerTraceEvent, string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatalf("%s is empty", path)
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev spcTimerTraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if match(ev) {
			return ev, hashBytes(raw), true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return spcTimerTraceEvent{}, hashBytes(raw), false
}
