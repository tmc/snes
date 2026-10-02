package machinebranch

import (
	"encoding/json"
	"fmt"
	"testing"
)

func ExampleInstructionTimeline() {
	t := InstructionTimeline{Frames: []InstructionFrame{{RelativeFrame: 0}}}
	b, _ := json.Marshal(t)
	fmt.Println(string(b))
	// Output: {"frames":[{"relative_frame":0,"sites":null}]}
}

func TestInstructionTimeline(t *testing.T) {
	x := newInstructionTimeline(2)
	for _, at := range []uint32{0x008000, 0x008000, 0x008008} {
		if err := x.record(at); err != nil {
			t.Fatal(err)
		}
	}
	if len(x.Frames[0].Sites) != 2 || x.Frames[0].Sites[0].Count != 2 || len(x.Frames[1].Sites) != 0 {
		t.Fatal(x)
	}
	x.frame = 2
	if x.record(0x008000) == nil {
		t.Fatal("accepted unbounded frame")
	}
	x.frame = 1
	if x.record(0x1000000) == nil {
		t.Fatal("accepted invalid address")
	}
	for i := uint32(0); i < 1024; i++ {
		if err := x.record(i); err != nil {
			t.Fatal(err)
		}
	}
	if x.record(1024) == nil {
		t.Fatal("accepted excess sites")
	}
	x.Frames[1].Sites[0].Count = ^uint64(0)
	if x.record(0) == nil {
		t.Fatal("accepted count overflow")
	}
}

func ExampleCheckInstructionTimeline() {
	t := &InstructionTimeline{Frames: []InstructionFrame{{RelativeFrame: 0, Sites: []InstructionSite{{Address: 0x008008, Count: 1}}}}}
	fmt.Println(CheckInstructionTimeline(t, 1, 1))
	// Output: <nil>
}

func ExampleInstructionFrame() {
	f := InstructionFrame{RelativeFrame: 3, Sites: []InstructionSite{{Address: 0x008008, Count: 2}}}
	fmt.Println(f.RelativeFrame, f.Sites[0].Count)
	// Output: 3 2
}

func ExampleInstructionSite() {
	s := InstructionSite{Address: 0x008008, Count: 2}
	fmt.Printf("$%06X: %d\n", s.Address, s.Count)
	// Output: $008008: 2
}
