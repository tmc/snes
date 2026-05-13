package snes

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cartridge/chips/cx4"
)

type cx4InputSpan struct {
	start int
	end   int
	state uint16
}

type cx4SmokeCase struct {
	name  string
	input []cx4InputSpan
	roms  []string
}

type cx4SmokeTrace struct {
	frame int
	trace cx4.CommandTrace
}

var cx4StandardSmokeCases = []cx4SmokeCase{
	{name: "no_input"},
	{name: "start_held", input: []cx4InputSpan{{start: 0, end: -1, state: emulator.StandardButtonStart}}},
}

var cx4PostTitleSmokeCases = []cx4SmokeCase{
	{
		name: "post_title_start_a",
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonA},
			{start: 330, end: 345, state: emulator.StandardButtonA},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
		},
	},
	{
		name: "post_title_start_b",
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonB},
			{start: 330, end: 345, state: emulator.StandardButtonB},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
		},
	},
}

var cx4GameplaySmokeCases = []cx4SmokeCase{
	{
		name: "gameplay_probe_a",
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonA},
			{start: 330, end: 345, state: emulator.StandardButtonA},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
			{start: 720, end: 840, state: emulator.StandardButtonRight},
			{start: 900, end: 1020, state: emulator.StandardButtonDown},
			{start: 1080, end: 1200, state: emulator.StandardButtonA},
			{start: 1260, end: 1380, state: emulator.StandardButtonLeft},
			{start: 1440, end: 1560, state: emulator.StandardButtonUp},
		},
	},
	{
		name: "gameplay_probe_b",
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonB},
			{start: 330, end: 345, state: emulator.StandardButtonB},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
			{start: 720, end: 840, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 900, end: 1020, state: emulator.StandardButtonDown | emulator.StandardButtonY},
			{start: 1080, end: 1200, state: emulator.StandardButtonLeft | emulator.StandardButtonB},
			{start: 1260, end: 1380, state: emulator.StandardButtonUp | emulator.StandardButtonY},
			{start: 1440, end: 1560, state: emulator.StandardButtonA},
		},
	},
}

var cx4DeepGameplaySmokeCases = []cx4SmokeCase{
	{
		name: "deep_gameplay_route",
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonA},
			{start: 330, end: 345, state: emulator.StandardButtonA},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
			{start: 720, end: 840, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 900, end: 1020, state: emulator.StandardButtonRight | emulator.StandardButtonY},
			{start: 1080, end: 1200, state: emulator.StandardButtonA},
			{start: 1260, end: 1500, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 1620, end: 1740, state: emulator.StandardButtonDown | emulator.StandardButtonB},
			{start: 1860, end: 2100, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 2220, end: 2340, state: emulator.StandardButtonUp | emulator.StandardButtonA},
			{start: 2460, end: 2700, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 2820, end: 2940, state: emulator.StandardButtonB},
			{start: 3060, end: 3300, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 3420, end: 3540, state: emulator.StandardButtonA},
			{start: 3660, end: 3900, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 4200, end: 4440, state: emulator.StandardButtonLeft | emulator.StandardButtonB},
			{start: 4800, end: 5040, state: emulator.StandardButtonRight | emulator.StandardButtonY},
		},
	},
}

var cx4StageProbeSmokeCases = []cx4SmokeCase{
	{
		// Stage-probe v2 is intentionally opt-in: use its 10k command and
		// framebuffer summary as a coverage-expansion check before any
		// longer run.
		name: "x2_stage_probe_v2",
		roms: []string{"x2"},
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonA},
			{start: 330, end: 345, state: emulator.StandardButtonA},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
			{start: 540, end: 570, state: emulator.StandardButtonDown},
			{start: 630, end: 660, state: emulator.StandardButtonRight},
			{start: 720, end: 750, state: emulator.StandardButtonA},
			{start: 900, end: 1140, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 1260, end: 1500, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 1620, end: 1860, state: emulator.StandardButtonUp | emulator.StandardButtonB},
			{start: 1980, end: 2220, state: emulator.StandardButtonDown | emulator.StandardButtonA},
			{start: 2400, end: 2880, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 3060, end: 3540, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 3720, end: 4200, state: emulator.StandardButtonRight | emulator.StandardButtonA},
			{start: 4500, end: 4980, state: emulator.StandardButtonB},
		},
	},
	{
		// Stage-probe v2 is intentionally opt-in: use its 10k command and
		// framebuffer summary as a coverage-expansion check before any
		// longer run.
		name: "x3_stage_select_probe_v2",
		roms: []string{"x3"},
		input: []cx4InputSpan{
			{start: 60, end: 75, state: emulator.StandardButtonStart},
			{start: 150, end: 165, state: emulator.StandardButtonStart},
			{start: 240, end: 255, state: emulator.StandardButtonA},
			{start: 330, end: 345, state: emulator.StandardButtonA},
			{start: 420, end: 435, state: emulator.StandardButtonStart},
			{start: 540, end: 570, state: emulator.StandardButtonRight},
			{start: 630, end: 660, state: emulator.StandardButtonRight},
			{start: 720, end: 750, state: emulator.StandardButtonDown},
			{start: 810, end: 840, state: emulator.StandardButtonA},
			{start: 960, end: 1200, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 1320, end: 1560, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 1680, end: 1920, state: emulator.StandardButtonDown | emulator.StandardButtonB},
			{start: 2040, end: 2280, state: emulator.StandardButtonUp | emulator.StandardButtonA},
			{start: 2460, end: 2940, state: emulator.StandardButtonRight | emulator.StandardButtonB},
			{start: 3120, end: 3600, state: emulator.StandardButtonLeft | emulator.StandardButtonY},
			{start: 3780, end: 4260, state: emulator.StandardButtonRight | emulator.StandardButtonA},
			{start: 4560, end: 5040, state: emulator.StandardButtonB},
		},
	},
}

func TestCX4TelemetrySmoke(t *testing.T) {
	if os.Getenv("SNES_CX4_TELEMETRY") == "" {
		t.Skip("set SNES_CX4_TELEMETRY=1 with SNES_CX4_X2_ROM/SNES_CX4_X3_ROM for opt-in Cx4 telemetry")
	}

	frames := 3000
	if s := os.Getenv("SNES_CX4_TELEMETRY_FRAMES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			t.Fatalf("SNES_CX4_TELEMETRY_FRAMES=%q, want positive integer", s)
		}
		frames = n
	}

	roms := []struct {
		name string
		path string
	}{
		{"x2", os.Getenv("SNES_CX4_X2_ROM")},
		{"x3", os.Getenv("SNES_CX4_X3_ROM")},
	}

	ran := false
	for _, rom := range roms {
		if rom.path == "" {
			t.Logf("%s: no ROM path configured", rom.name)
			continue
		}
		ran = true
		for _, tc := range cx4StandardSmokeCases {
			name := rom.name + "_" + tc.name
			t.Run(name, func(t *testing.T) {
				traces := runCX4TelemetrySmoke(t, rom.path, tc.input, frames)
				checkCX4Telemetry(t, traces)
				t.Logf("%s", summarizeCX4Telemetry(traces))
			})
		}
	}
	if !ran {
		t.Skip("set SNES_CX4_X2_ROM and/or SNES_CX4_X3_ROM")
	}
}

func TestCX4ExperimentalLoadROMSmoke(t *testing.T) {
	if os.Getenv("SNES_CX4_EXPERIMENTAL_SMOKE") == "" {
		t.Skip("set SNES_CX4_EXPERIMENTAL_SMOKE=1 with SNES_CX4_X2_ROM/SNES_CX4_X3_ROM for opt-in Cx4 LoadROM smoke")
	}
	t.Setenv("SNES_CX4_EXPERIMENTAL", "1")

	frames := 3000
	if s := os.Getenv("SNES_CX4_SMOKE_FRAMES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			t.Fatalf("SNES_CX4_SMOKE_FRAMES=%q, want positive integer", s)
		}
		frames = n
	}

	roms := []struct {
		name string
		path string
	}{
		{"x2", os.Getenv("SNES_CX4_X2_ROM")},
		{"x3", os.Getenv("SNES_CX4_X3_ROM")},
	}

	ran := false
	for _, rom := range roms {
		if rom.path == "" {
			t.Logf("%s: no ROM path configured", rom.name)
			continue
		}
		ran = true
		for _, tc := range cx4ExperimentalSmokeCases() {
			if !cx4SmokeCaseApplies(tc, rom.name) {
				continue
			}
			name := rom.name + "_" + tc.name
			t.Run(name, func(t *testing.T) {
				traces, fbHash := runCX4ExperimentalLoadROMSmoke(t, rom.path, tc.input, frames)
				checkCX4SmokeTelemetry(t, traces)
				t.Logf("%s framebuffer=%s", summarizeCX4SmokeTelemetry(traces), fbHash)
			})
		}
	}
	if !ran {
		t.Skip("set SNES_CX4_X2_ROM and/or SNES_CX4_X3_ROM")
	}
}

func runCX4TelemetrySmoke(t *testing.T, romPath string, input []cx4InputSpan, frames int) []cx4.CommandTrace {
	t.Helper()

	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}

	cart := cartridge.New(rom)
	if cart.CoprocessorID != "cx4" {
		t.Fatalf("ROM %q coprocessor = %q, want cx4", romPath, cart.CoprocessorID)
	}
	dev := cart.AttachCx4()

	var traces []cx4.CommandTrace
	dev.SetTrace(func(tr cx4.CommandTrace) {
		traces = append(traces, tr)
	})

	sys := NewSystem(nil)
	sys.cart = cart
	sys.romHash = sha256.Sum256(cart.ROM)
	sys.palTiming = cart.PAL
	sys.Scheduler.SetPAL(sys.palTiming)
	sys.remapBaseDevices()
	sys.Power()

	for i := 0; i < frames; i++ {
		if err := sys.SetInputState(0, cx4InputStateAt(input, i, frames)); err != nil {
			t.Fatalf("set input state: %v", err)
		}
		if err := sys.Run(); err != nil {
			t.Fatalf("run frame %d: %v", i, err)
		}
	}
	return traces
}

func runCX4ExperimentalLoadROMSmoke(t *testing.T, romPath string, input []cx4InputSpan, frames int) ([]cx4SmokeTrace, string) {
	t.Helper()

	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}

	sys := NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	if sys.cart.CoprocessorID != "cx4" {
		t.Fatalf("ROM %q coprocessor = %q, want cx4", romPath, sys.cart.CoprocessorID)
	}
	dev := sys.cart.AttachCx4()

	frame := -1
	var traces []cx4SmokeTrace
	dev.SetTrace(func(tr cx4.CommandTrace) {
		traces = append(traces, cx4SmokeTrace{frame: frame, trace: tr})
	})

	sys.Power()
	for i := 0; i < frames; i++ {
		frame = i
		if err := sys.SetInputState(0, cx4InputStateAt(input, i, frames)); err != nil {
			t.Fatalf("set input state: %v", err)
		}
		if err := sys.Run(); err != nil {
			t.Fatalf("run frame %d: %v", i, err)
		}
	}
	return traces, hashTelemetryFrameBuffer(sys.FrameBuffer())
}

func cx4ExperimentalSmokeCases() []cx4SmokeCase {
	cases := append([]cx4SmokeCase(nil), cx4StandardSmokeCases...)
	if os.Getenv("SNES_CX4_SMOKE_POST_TITLE") != "" {
		cases = append(cases, cx4PostTitleSmokeCases...)
	}
	if os.Getenv("SNES_CX4_SMOKE_GAMEPLAY") != "" {
		cases = append(cases, cx4GameplaySmokeCases...)
	}
	if os.Getenv("SNES_CX4_SMOKE_DEEP_GAMEPLAY") != "" {
		cases = append(cases, cx4DeepGameplaySmokeCases...)
	}
	if os.Getenv("SNES_CX4_SMOKE_STAGE_PROBE") != "" {
		cases = append(cases, cx4StageProbeSmokeCases...)
	}
	return cases
}

func cx4SmokeCaseApplies(tc cx4SmokeCase, romName string) bool {
	if len(tc.roms) == 0 {
		return true
	}
	for _, name := range tc.roms {
		if name == romName {
			return true
		}
	}
	return false
}

func TestCX4SmokeCaseApplies(t *testing.T) {
	tests := []struct {
		name string
		tc   cx4SmokeCase
		rom  string
		want bool
	}{
		{name: "default applies", rom: "x2", want: true},
		{name: "listed applies", tc: cx4SmokeCase{roms: []string{"x2"}}, rom: "x2", want: true},
		{name: "listed excludes", tc: cx4SmokeCase{roms: []string{"x3"}}, rom: "x2", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cx4SmokeCaseApplies(tt.tc, tt.rom); got != tt.want {
				t.Fatalf("cx4SmokeCaseApplies = %v, want %v", got, tt.want)
			}
		})
	}
}

func cx4InputStateAt(spans []cx4InputSpan, frame int, frames int) uint16 {
	var state uint16
	for _, span := range spans {
		end := span.end
		if end < 0 {
			end = frames - 1
		}
		if frame >= span.start && frame <= end {
			state |= span.state
		}
	}
	return state
}

func checkCX4Telemetry(t *testing.T, traces []cx4.CommandTrace) {
	t.Helper()

	for _, tr := range traces {
		if err := checkCX4Trace(tr); err != nil {
			t.Fatal(err)
		}
	}
}

func checkCX4SmokeTelemetry(t *testing.T, traces []cx4SmokeTrace) {
	t.Helper()

	for i, tr := range traces {
		if err := checkCX4Trace(tr.trace); err != nil {
			t.Fatalf("frame=%d index=%d %v", tr.frame, i, err)
		}
	}
}

func checkCX4Trace(tr cx4.CommandTrace) error {
	switch {
	case tr.Command == 0x00 && tr.Subcommand == 0x00:
		return nil
	case tr.Command == 0x00 && tr.Subcommand == 0x08:
		return nil
	case tr.Command == 0x01 && tr.Subcommand == 0x08:
		return nil
	case tr.Command == 0x00 && tr.Subcommand == 0x03:
		if tr.F80 != 0 {
			return fmt.Errorf("Cx4 00/03 params = %s, want no-rotation path", formatCX4Trace(tr))
		}
		return nil
	case tr.Command == 0x00 && tr.Subcommand == 0x05:
		return nil
	case tr.Command == 0x22 && tr.Subcommand == 0x02:
		return nil
	case tr.Command == 0x5c && tr.Subcommand == 0x0e:
		return nil
	case tr.Command == 0x89 && tr.Subcommand == 0x0e:
		return nil
	default:
		return fmt.Errorf("uncovered Cx4 command/subcommand: %s", formatCX4Trace(tr))
	}
}

func hashTelemetryFrameBuffer(fb []uint16) string {
	h := sha256.New()
	var b [2]byte
	for _, px := range fb {
		b[0] = byte(px)
		b[1] = byte(px >> 8)
		_, _ = h.Write(b[:])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func summarizeCX4Telemetry(traces []cx4.CommandTrace) string {
	counts := make(map[string]int)
	order := make([]string, 0, len(traces))
	seen := make(map[string]bool)
	for _, tr := range traces {
		key := fmt.Sprintf("%02x/%02x", tr.Command, tr.Subcommand)
		counts[key]++
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}

	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "commands=%d", len(traces))
	for _, key := range keys {
		fmt.Fprintf(&b, " %s=%d", key, counts[key])
	}
	if len(order) > 0 {
		fmt.Fprintf(&b, " order=%s", strings.Join(order, "->"))
	}
	return b.String()
}

func summarizeCX4SmokeTelemetry(traces []cx4SmokeTrace) string {
	raw := make([]cx4.CommandTrace, 0, len(traces))
	for _, tr := range traces {
		raw = append(raw, tr.trace)
	}
	return summarizeCX4Telemetry(raw)
}

func formatCX4Trace(tr cx4.CommandTrace) string {
	return fmt.Sprintf(
		"cmd=%02x sub=%02x f80=%04x f83=%04x f86=%04x f89=%02x f8c=%02x f8f=%04x f92=%04x",
		tr.Command, tr.Subcommand, tr.F80, tr.F83, tr.F86, tr.F89, tr.F8C, tr.F8F, tr.F92,
	)
}
