package updsp

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"testing"
)

func ExampleMapper_SetPAL() {
	m := NewMapper(NewIO(NewCore()), MapLoROM)
	m.SetPAL(true)
	m.Step(3)
	fmt.Println(m.IO.Core.CycleCount)
	// Output: 1
}

func clockTestMapper(pal bool) *Mapper {
	core := NewCore()
	for i := range core.PRG {
		core.PRG[i] = 3<<22 | uint32(i)<<6 | regDR
	}
	m := NewMapper(NewIO(core), MapLoROM)
	m.SetPAL(pal)
	return m
}

func TestMapperStepPartitionInvariant(t *testing.T) {
	for _, tt := range []struct {
		pal    bool
		cycles uint64
	}{{false, 3538}, {true, 3571}} {
		t.Run(fmt.Sprintf("pal=%v", tt.pal), func(t *testing.T) {
			whole := clockTestMapper(tt.pal)
			whole.Step(10000)
			if whole.IO.Core.CycleCount != tt.cycles {
				t.Fatalf("DSP cycles = %d, want %d", whole.IO.Core.CycleCount, tt.cycles)
			}
			if whole.IO.Core.DR != uint16((tt.cycles-1)%2048) {
				t.Fatal("firmware instruction did not publish expected DR value")
			}
			want, err := whole.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			for _, partition := range []uint64{1, 2, 3, 7, 100, 999} {
				split := clockTestMapper(tt.pal)
				for left := uint64(10000); left > 0; {
					n := min(left, partition)
					split.Step(n)
					left -= n
				}
				got, err := split.Serialize()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("partition %d changed core or fractional clock", partition)
				}
			}
		})
	}
}

func TestMapperClockStateResume(t *testing.T) {
	for _, pal := range []bool{false, true} {
		t.Run(fmt.Sprintf("pal=%v", pal), func(t *testing.T) {
			source := clockTestMapper(pal)
			source.Step(2)
			if source.IO.Core.CycleCount != 0 || source.clockRemainder == 0 {
				t.Fatal("checkpoint lacks pending fractional cycle")
			}
			checkpoint, err := source.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			source.Step(1)
			if source.IO.Core.CycleCount != 1 {
				t.Fatal("fraction did not retire one instruction")
			}
			target := clockTestMapper(!pal)
			if err := target.Unserialize(checkpoint); err != nil {
				t.Fatal(err)
			}
			target.Step(1)
			want, err := source.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			got, err := target.Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("restore lost fractional cycle or region")
			}
		})
	}
}

func TestMapperClockStateRejectsInvalidFraction(t *testing.T) {
	m := clockTestMapper(false)
	m.Step(2)
	before, err := m.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	var state mapperState
	if err := gob.NewDecoder(bytes.NewReader(before)).Decode(&state); err != nil {
		t.Fatal(err)
	}
	state.ClockRemainder = masterFrequency(state.PAL)
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		t.Fatal(err)
	}
	if err := m.Unserialize(buf.Bytes()); err == nil {
		t.Fatal("invalid clock fraction accepted")
	}
	after, err := m.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected clock state changed mapper")
	}
}
