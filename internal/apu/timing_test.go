package apu_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/apu/aputest"
)

func ExampleTimingEvent() {
	a := apu.NewAPU()
	a.Trace = func(e apu.TimingEvent) {
		fmt.Printf("%s tick=%d address=%02x value=%02x\n", e.Kind, e.Cycle, e.Address, e.Value)
	}
	a.Write(0xf2, 0x4c)
	a.Write(0xf3, 1)
	// Output: dsp-write tick=0 address=4c value=01
}

func TestTimingTracePreservesExecution(t *testing.T) {
	a, b := apu.NewAPU(), apu.NewAPU()
	aputest.ProgramNonSilentSPC(a)
	aputest.ProgramNonSilentSPC(b)
	var events []apu.TimingEvent
	a.Trace = func(e apu.TimingEvent) { events = append(events, e) }
	for range 1024 {
		a.Run()
		b.Run()
	}
	if !reflect.DeepEqual(a.SaveState(), b.SaveState()) {
		t.Fatal("tracing changed execution or PCM")
	}
	var writes []apu.TimingEvent
	var samples int
	for _, e := range events {
		switch e.Kind {
		case "dsp-write":
			writes = append(writes, e)
		case "sample":
			samples++
			if e.Cycle != uint64(samples*64) {
				t.Fatalf("sample %d at tick %d", samples, e.Cycle)
			}
		}
	}
	want := aputest.NonSilentSPCFixtureInfo().DSPWrites
	if len(writes) != len(want) || samples != 16 {
		t.Fatalf("writes=%d samples=%d", len(writes), samples)
	}
	for i, e := range writes {
		// Each pair of MOV dp,#imm instructions takes twenty ticks on the
		// current atomic path. This is an observation, not a hardware golden.
		if e.Address != uint16(want[i].Register) || e.Value != want[i].Value || e.Cycle != uint64(i*20+12) {
			t.Fatalf("write %d: %+v", i, e)
		}
	}
}

func TestTimingTraceAssignmentBoundary(t *testing.T) {
	a := apu.NewAPU()
	a.Control = 0
	a.Processor.PC = 0x200
	a.Processor.A = 0x5a
	copy(a.RAM[0x200:], []byte{0xc4, 0xf4, 0x2f, 0xfe})
	var events []apu.TimingEvent
	a.Trace = func(e apu.TimingEvent) {
		if e.Kind == "output-port" {
			events = append(events, e)
		}
	}
	a.RunUntilTarget(8, apu.SyncPostCPU)
	if len(events) != 0 {
		t.Fatalf("published before resume: %+v", events)
	}
	a.RunUntilTarget(9, apu.SyncPortRead)
	if len(events) != 1 || events[0].Kind != "output-port" || events[0].Cycle != 8 || events[0].Value != 0x5a {
		t.Fatalf("assignment events: %+v", events)
	}
}
