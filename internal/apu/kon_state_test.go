package apu_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/apu/aputest"
)

const (
	konSweepTicks  = 1200 // Save points: before KON through several samples after key-on.
	konResumeTicks = 640  // Ticks run after each save point (10 samples).
)

// newKONFixture returns the non-silent SPC fixture. A non-negative nops
// prepends that many NOPs and a JMP $0200 at $0100, delaying the KON store
// by 4*nops+6 S-SMP ticks.
func newKONFixture(nops int) *apu.APU {
	a := apu.NewAPU()
	aputest.ProgramNonSilentSPC(a)
	if nops >= 0 {
		copy(a.RAM[0x100:], make([]byte, nops))
		copy(a.RAM[0x100+nops:], []byte{0x5f, 0x00, 0x02}) // JMP $0200.
		a.Processor.PC = 0x100
	}
	return a
}

func runDrain(a *apu.APU, ticks int) []int16 {
	var pcm []int16
	buf := make([]int16, 64)
	for i := 0; i < ticks; i++ {
		a.Run()
		for {
			n := a.DrainAudio(buf)
			if n == 0 {
				break
			}
			pcm = append(pcm, buf[:n]...)
		}
	}
	return pcm
}

// Saving and loading at any S-SMP tick around a KON write must not change
// the machine's future: the S-DSP key-on latch (newKON, kon, konIdle), the
// per-voice konDelay countdown and a pending late KON write all have to
// round-trip through APUState.
func TestAPUStateKONLatchPhases(t *testing.T) {
	tests := []struct {
		name string
		nops int  // -1: no prefix.
		late bool // KON $F3 store lands with dspCycles == 62.
	}{
		{"direct", -1, false},
		{"late", 7, true}, // KON store at tick 254 = 3*64+62.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				pendingNewKON int
				latePending   int
				idle          = map[bool]int{}
				delay         = map[uint8]int{}
			)
			a := newKONFixture(tt.nops)
			for tick := 0; tick <= konSweepTicks; tick++ {
				if tick > 0 {
					runDrain(a, 1)
				}
				state := a.SaveState()
				d := state.DSP
				if d.NewKON&^d.LatchKON != 0 {
					pendingNewKON++
				}
				if d.LateKONPending {
					latePending++
				}
				idle[d.KONIdle]++
				delay[d.Voices[0].KONDelay]++

				b := apu.NewAPU()
				if err := b.LoadState(state); err != nil {
					t.Fatalf("tick %d: LoadState: %v", tick, err)
				}
				if got := b.SaveState(); !reflect.DeepEqual(got, state) {
					t.Fatalf("tick %d: SaveState after LoadState differs from saved state", tick)
				}

				// Continue a fresh replay of the original alongside the
				// restored machine so the sweep's own APU is undisturbed.
				ref := newKONFixture(tt.nops)
				runDrain(ref, tick)
				wantPCM := runDrain(ref, konResumeTicks)
				gotPCM := runDrain(b, konResumeTicks)
				if !slices.Equal(gotPCM, wantPCM) {
					t.Fatalf("tick %d (NewKON=%02x LatchKON=%02x KONIdle=%v LateKON=%02x LateKONPending=%v KONDelay=%d): PCM after restore differs\ngot  %v\nwant %v",
						tick, d.NewKON, d.LatchKON, d.KONIdle, d.LateKON, d.LateKONPending, d.Voices[0].KONDelay, gotPCM, wantPCM)
				}
				if got, want := b.SaveState(), ref.SaveState(); !reflect.DeepEqual(got, want) {
					t.Fatalf("tick %d (NewKON=%02x LatchKON=%02x KONIdle=%v LateKON=%02x LateKONPending=%v KONDelay=%d): state after restore differs\ngot  DSP %+v\nwant DSP %+v",
						tick, d.NewKON, d.LatchKON, d.KONIdle, d.LateKON, d.LateKONPending, d.Voices[0].KONDelay, got.DSP, want.DSP)
				}
			}

			t.Logf("pendingNewKON=%d latePending=%d KONIdle=%v KONDelay=%v", pendingNewKON, latePending, idle, delay)
			if pendingNewKON == 0 {
				t.Error("no save point with an unlatched NewKON")
			}
			if tt.late && latePending == 0 {
				t.Error("no save point with LateKONPending; late variant no longer covers WriteLateKON")
			}
			if !tt.late && latePending != 0 {
				t.Errorf("direct variant hit LateKONPending at %d save points", latePending)
			}
			if idle[true] == 0 || idle[false] == 0 {
				t.Errorf("KONIdle coverage = %v, want both true and false", idle)
			}
			for v := uint8(1); v <= 7; v++ {
				if delay[v] == 0 {
					t.Errorf("no save point with voice 0 KONDelay=%d", v)
				}
			}
		})
	}
}
