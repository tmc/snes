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
		for _, start := range []bool{false, true} {
			name := rom.name + "_no_input"
			if start {
				name = rom.name + "_start"
			}
			t.Run(name, func(t *testing.T) {
				traces := runCX4TelemetrySmoke(t, rom.path, start, frames)
				checkCX4Telemetry(t, traces)
				t.Logf("%s", summarizeCX4Telemetry(traces))
			})
		}
	}
	if !ran {
		t.Skip("set SNES_CX4_X2_ROM and/or SNES_CX4_X3_ROM")
	}
}

func runCX4TelemetrySmoke(t *testing.T, romPath string, start bool, frames int) []cx4.CommandTrace {
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

	if start {
		if err := sys.SetInputState(0, emulator.StandardButtonStart); err != nil {
			t.Fatalf("set input state: %v", err)
		}
	}
	for i := 0; i < frames; i++ {
		if err := sys.Run(); err != nil {
			t.Fatalf("run frame %d: %v", i, err)
		}
	}
	return traces
}

func checkCX4Telemetry(t *testing.T, traces []cx4.CommandTrace) {
	t.Helper()

	for _, tr := range traces {
		switch {
		case tr.Command == 0x00 && tr.Subcommand == 0x00:
		case tr.Command == 0x00 && tr.Subcommand == 0x03:
			want := cx4.CommandTrace{
				Command:    0x00,
				Subcommand: 0x03,
				F80:        0x0000,
				F83:        0x0018,
				F86:        0x0020,
				F89:        0x30,
				F8C:        0x40,
				F8F:        0x1000,
				F92:        0x1000,
			}
			if tr != want {
				t.Fatalf("Cx4 00/03 params = %s, want %s", formatCX4Trace(tr), formatCX4Trace(want))
			}
		case tr.Command == 0x22 && tr.Subcommand == 0x02:
		case tr.Command == 0x5c && tr.Subcommand == 0x0e:
		case tr.Command == 0x89 && tr.Subcommand == 0x0e:
		default:
			t.Fatalf("uncovered Cx4 command/subcommand: %s", formatCX4Trace(tr))
		}
	}
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

func formatCX4Trace(tr cx4.CommandTrace) string {
	return fmt.Sprintf(
		"cmd=%02x sub=%02x f80=%04x f83=%04x f86=%04x f89=%02x f8c=%02x f8f=%04x f92=%04x",
		tr.Command, tr.Subcommand, tr.F80, tr.F83, tr.F86, tr.F89, tr.F8C, tr.F8F, tr.F92,
	)
}
