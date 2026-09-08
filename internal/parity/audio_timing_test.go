package parity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/apu/aputest"
)

// TestAudioStartupTimeline locates the uploader's DSP writes and first audible
// sample on the production clocks. It does not replace the onset golden.
func TestAudioStartupTimeline(t *testing.T) {
	rom := nonSilentAPULoROM(t)
	sys := snes.NewSystem(nil)
	mapLoROM(sys, rom)
	if !sys.Load() {
		t.Fatal("load failed")
	}
	type clockEvent struct {
		apu.TimingEvent
		CPUCycle uint64
	}
	var timeline []clockEvent
	var writes []apu.TimingEvent
	var reads []apu.TimingEvent
	var lastInput, lastOutput apu.TimingEvent
	var first apu.TimingEvent
	samples := 0
	onset := -1
	sys.APU.Trace = func(e apu.TimingEvent) {
		timeline = append(timeline, clockEvent{e, sys.CPU.Cycles})
		switch e.Kind {
		case "input-read":
			reads = append(reads, e)
		case "input-port":
			lastInput = e
		case "output-port":
			lastOutput = e
		case "dsp-write":
			writes = append(writes, e)
			t.Logf("dsp register=%02x value=%02x apu_tick=%d cpu_tick=%d pc=%04x", e.Address, e.Value, e.Cycle, sys.CPU.Cycles, e.PC)
		case "sample":
			if onset < 0 && (e.Left != 0 || e.Right != 0) {
				onset = samples * 2
				first = e
			}
			samples++
		}
	}
	if err := sys.RunFrame(); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("SNES_APU_TIMING_TRACE"); path != "" {
		data, err := json.MarshalIndent(timeline, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := aputest.NonSilentSPCFixtureInfo().DSPWrites
	if len(writes) != len(want) {
		t.Fatalf("DSP writes=%d want=%d", len(writes), len(want))
	}
	for i, e := range writes {
		if e.Address != uint16(want[i].Register) || e.Value != want[i].Value {
			t.Fatalf("DSP write %d: %+v", i, e)
		}
	}
	if onset < 0 {
		t.Fatal("no audible sample")
	}
	kon := writes[len(writes)-1]
	if first.Cycle <= kon.Cycle || first.Cycle%64 != 0 {
		t.Fatalf("invalid key-on/sample order: %+v %+v", kon, first)
	}
	pcm := make([]int16, 8192)
	n := sys.DrainAudio(pcm)
	if got := firstNonZeroSample(pcm[:n]); got != onset {
		t.Fatalf("trace onset=%d drain onset=%d", onset, got)
	}
	t.Logf("rom_sha256=%x last_input=%+v last_output=%+v KON=%+v first_audible=%+v interleaved_onset=%d", sha256.Sum256(rom), lastInput, lastOutput, kon, first, onset)
	for _, e := range reads {
		if e.Cycle >= lastInput.Cycle {
			t.Logf("handoff input read: %+v", e)
		}
	}
	if len(reads) == 0 {
		t.Fatal("no SPC input reads observed")
	}
	fmt.Printf("QUALIFY comparisons=%d\n", len(writes)+4)
}
