package dsp

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// konScript drives a DSP as a sequence of ops: even op 2k delivers KON if
// k == sample, odd op 2k+1 runs Sample. State may be saved between any ops.
type konScript struct {
	ram    []byte
	sample int  // Sample index the KON write precedes.
	late   bool // Deliver KON via WriteLateKON.
}

func (s konScript) newDSP() *DSP {
	d := New()
	d.SetRAMReader(func(addr uint16) uint8 { return s.ram[addr] })
	for _, w := range [][2]uint8{
		{0x6C, 0x20},               // FLG: unmuted, echo writes off.
		{0x0C, 0x7F}, {0x1C, 0x7F}, // MVOLL, MVOLR.
		{0x00, 0x7F}, {0x01, 0x7F}, // V0 VOLL, VOLR.
		{0x02, 0x00}, {0x03, 0x10}, // V0 pitch.
		{0x04, 0x00}, {0x07, 0x7F}, // V0 SRCN, GAIN.
		{0x5D, 0x20}, // DIR.
	} {
		d.Write(w[0], w[1])
	}
	return d
}

func (s konScript) run(d *DSP, from, n int) []int16 {
	var pcm []int16
	for op := from; op < from+n; op++ {
		switch {
		case op%2 == 1:
			l, r := d.Sample()
			pcm = append(pcm, l, r)
		case op/2 != s.sample:
		case s.late:
			d.WriteLateKON(0x01)
		default:
			d.Write(0x4C, 0x01)
		}
	}
	return pcm
}

// DSP state saved between any two ops around a KON write, including a
// write delivered through WriteLateKON, must restore to the same future.
func TestDSPStateKONLatchPhases(t *testing.T) {
	ram := make([]byte, 0x10000)
	ram[0x2000], ram[0x2001], ram[0x2002], ram[0x2003] = 0x00, 0x30, 0x00, 0x30
	ram[0x3000] = 0xC0
	for i := 1; i <= 8; i++ {
		ram[0x3000+i] = 0x11
	}
	const ops, resume = 2 * 24, 2 * 16
	for _, sample := range []int{3, 4} {
		for _, late := range []bool{false, true} {
			s := konScript{ram: ram, sample: sample, late: late}
			t.Run(fmt.Sprintf("sample=%d/late=%v", sample, late), func(t *testing.T) {
				var pendingNewKON, latePending int
				idle := map[bool]int{}
				delay := map[uint8]int{}
				d := s.newDSP()
				for op := 0; op <= ops; op++ {
					if op > 0 {
						s.run(d, op-1, 1)
					}
					state := d.SaveState()
					if state.NewKON&^state.LatchKON != 0 {
						pendingNewKON++
					}
					if state.LateKONPending {
						latePending++
					}
					idle[state.KONIdle]++
					delay[state.Voices[0].KONDelay]++

					restored := New()
					restored.SetRAMReader(func(addr uint16) uint8 { return ram[addr] })
					if err := restored.LoadState(state); err != nil {
						t.Fatalf("op %d: LoadState: %v", op, err)
					}
					ref := s.newDSP()
					s.run(ref, 0, op)
					want := s.run(ref, op, resume)
					got := s.run(restored, op, resume)
					if !slices.Equal(got, want) {
						t.Fatalf("op %d (NewKON=%02x LatchKON=%02x KONIdle=%v LateKONPending=%v KONDelay=%d): PCM after restore differs\ngot  %v\nwant %v",
							op, state.NewKON, state.LatchKON, state.KONIdle, state.LateKONPending, state.Voices[0].KONDelay, got, want)
					}
					if got, want := restored.SaveState(), ref.SaveState(); !reflect.DeepEqual(got, want) {
						t.Fatalf("op %d: state after restore differs\ngot  %+v\nwant %+v", op, got, want)
					}
				}
				t.Logf("pendingNewKON=%d latePending=%d KONIdle=%v KONDelay=%v", pendingNewKON, latePending, idle, delay)
				if pendingNewKON == 0 {
					t.Error("no save point with an unlatched NewKON")
				}
				if late && latePending == 0 {
					t.Error("no save point with LateKONPending")
				}
				if idle[true] == 0 || idle[false] == 0 {
					t.Errorf("KONIdle coverage = %v, want both", idle)
				}
				for v := uint8(1); v <= 7; v++ {
					if delay[v] == 0 {
						t.Errorf("no save point with voice 0 KONDelay=%d", v)
					}
				}
			})
		}
	}
}
