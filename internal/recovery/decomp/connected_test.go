package decomp

import (
	"context"
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func connectedFixture() ConnectedConfig {
	rom := make([]byte, 65536)
	put := func(a uint32, b []byte) { off := int((a>>16&127)*32768 + (a & 32767)); copy(rom[off:], b) }
	put(0x018000, []byte{0x20, 0x10, 0x80, 0x60})
	put(0x018010, []byte{0xad, 0x00, 0x1e, 0x22, 0x00, 0x90, 0x00, 0x20, 0x80})
	put(0x009000, []byte{0x84, 0x03, 0x7a, 0x84, 0x00, 0xc2, 0x30, 0x29, 0xff, 0x00, 0x0a, 0xa8, 0x68, 0x85, 0x01, 0xc8, 0xb7, 0x00, 0x85, 0x00, 0xe2, 0x30, 0xa4, 0x03, 0xdc, 0x00, 0x00})
	put(0x018020, []byte{0xa9, 0x01, 0x8d, 0x00, 0x10, 0x60})
	return ConnectedConfig{ROM: rom, Spans: []CodeSpan{{0x018000, 0x018004}, {0x018010, 0x018017}, {0x009000, 0x00901b}, {0x018020, 0x018026}}, Entry: 0x018000, Context: recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, MaxInstructions: 100, MaxSteps: 100, IndirectTargets: map[uint32][]uint32{0x009018: {0x018020}}}
}
func ExampleCodeSpan() {
	fmt.Printf("%06X\n", CodeSpan{Start: 0x008000, End: 0x008001}.Start)
	// Output: 008000
}
func ExampleConnectedConfig() {
	c := ConnectedConfig{MaxInstructions: 100, MaxSteps: 100}
	fmt.Println(c.MaxSteps)
	// Output: 100
}
func ExampleDecodeConnected() {
	_, err := DecodeConnected(ConnectedConfig{})
	fmt.Println(err)
	// Output: decode connected: invalid bounds
}
func TestConnectedDispatchSynthetic(t *testing.T) {
	c := connectedFixture()
	r, err := DecodeConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	source, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	state := CPUState{A: 0xab00, X: 3, Y: 7, S: 0x1fd, PB: 1, PC: 0x8000, P: 0x30}
	memory := []MemoryCell{{0x7e1e00, 0}, {0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}
	results, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, []ReplayCase{{CaseID: "synthetic", InitialState: state, InitialMemory: memory}})
	if err != nil {
		t.Fatal(err)
	}
	got := results[0]
	if got.MissingRead || got.MMIOAccess {
		t.Fatalf("refused %+v", got)
	}
	wantState := CPUState{A: 0x8001, X: 3, Y: 7, S: 0x1ff, PB: 1, PC: 0x9000, P: 0x30}
	if got.State != wantState {
		t.Fatalf("state %+v want %+v", got.State, wantState)
	}

	want := []MemoryWrite{{0x7e01fd, 0x80}, {0x7e01fc, 0x02}, {0x7e01fb, 1}, {0x7e01fa, 0x80}, {0x7e01f9, 0x16}, {0x7e0003, 7}, {0x7e0000, 0x16}, {0x7e0001, 0x80}, {0x7e0002, 1}, {0x7e0000, 0x20}, {0x7e0001, 0x80}, {0x7e1000, 1}}
	if ok, detail := CompareWrites(want, got.Writes); !ok {
		t.Fatal(detail)
	}
}
func TestConnectedRefusals(t *testing.T) {
	for _, name := range []string{"width", "overlap", "budget", "missing indirect", "operand target"} {
		t.Run(name, func(t *testing.T) {
			c := connectedFixture()
			switch name {
			case "width":
				c.Context.M = "unknown"
			case "overlap":
				c.Spans = append(c.Spans, CodeSpan{0x018000, 0x018001})
			case "budget":
				c.MaxInstructions = 2
			case "missing indirect":
				c.IndirectTargets = nil
			case "operand target":
				c.IndirectTargets[0x009018] = []uint32{0x018021}
			}
			if _, err := DecodeConnected(c); err == nil {
				t.Fatal("accepted invalid contract")
			}
		})
	}
}

func TestConnectedNoCallEntryGuards(t *testing.T) {
	c := connectedFixture()
	c.Entry = 0x008000
	c.Spans = []CodeSpan{{0x008000, 0x008003}, {0x008010, 0x008011}}
	copy(c.ROM[0:], []byte{0xdc, 0, 0})
	c.ROM[0x10] = 0x60
	c.IndirectTargets = map[uint32][]uint32{0x008000: {0x008010}}
	r, err := DecodeConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.CallSites) != 0 {
		t.Fatal("control must have no call sites")
	}
	source, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	initial := CPUState{S: 0x1fd, PC: 0x8000, P: 0x30}
	memory := []MemoryCell{{0x7e0000, 0x10}, {0x7e0001, 0x80}, {0x7e0002, 0}, {0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}
	wrongPC := initial
	wrongPC.PC++
	wrongPB := initial
	wrongPB.PB++
	cases := []ReplayCase{{CaseID: "valid", InitialState: initial, InitialMemory: memory}, {CaseID: "wrong-pc", InitialState: wrongPC, InitialMemory: memory}, {CaseID: "wrong-pb", InitialState: wrongPB, InitialMemory: memory}}
	got, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, cases)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].MissingRead || got[0].State.PC != 0x9000 {
		t.Fatalf("valid %+v", got[0])
	}
	for _, result := range got[1:] {
		if !result.MissingRead {
			t.Fatal("missing entry guard")
		}
	}
	r.Blocks[1].EntryContext.M = "clear"
	source, err = GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err = compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, cases[:1])
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].MissingRead || got[0].MissingAddr != 0x008010 {
		t.Fatalf("missing block-width guard %+v", got[0])
	}
}
func TestConnectedRepeatedCallee(t *testing.T) {
	c := connectedFixture()
	c.Entry = 0x008000
	c.Spans = []CodeSpan{{0x008000, 0x008007}, {0x008010, 0x008013}}
	c.IndirectTargets = nil
	copy(c.ROM, []byte{0x20, 0x10, 0x80, 0x20, 0x10, 0x80, 0x60})
	copy(c.ROM[0x10:], []byte{0xa9, 0x42, 0x60})
	r, err := DecodeConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	var successors []uint32
	for _, b := range r.Blocks {
		if b.StartAddress == 0x008012 {
			successors = b.Successors
		}
	}
	if len(successors) != 2 {
		t.Fatalf("callee return edges %+v", successors)
	}
	source, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, []ReplayCase{{CaseID: "twice", InitialState: CPUState{S: 0x1fd, PC: 0x8000, P: 0x30}, InitialMemory: []MemoryCell{{0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].MissingRead || got[0].State.A != 0x42 || got[0].State.S != 0x1ff || got[0].State.PC != 0x9000 {
		t.Fatalf("%+v", got[0])
	}
}
func TestConnectedUnknownSelectorRefuses(t *testing.T) {
	c := connectedFixture()
	r, err := DecodeConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	source, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	// A second table entry is mapped but outside the explicit allowed handler.
	c.ROM[0x8019] = 0x30
	c.ROM[0x801a] = 0x80
	got, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, []ReplayCase{{CaseID: "selector-one", InitialState: CPUState{S: 0x1fd, PC: 0x8000, PB: 1, P: 0x30}, InitialMemory: []MemoryCell{{0x7e1e00, 1}, {0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].MissingRead || got[0].NextPC != 0x018030 {
		t.Fatalf("unsupported selector %+v", got[0])
	}
}
func TestConnectedOuterPullRefuses(t *testing.T) {
	c := connectedFixture()
	c.Entry = 0x008000
	c.Spans = []CodeSpan{{0x008000, 0x008002}}
	copy(c.ROM, []byte{0x68, 0x60})
	if _, err := DecodeConnected(c); err == nil {
		t.Fatal("accepted unmodeled outer pull")
	}
}

func TestConnectedStackControlBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		code []byte
	}{{"push loop", []byte{0x8b, 0x80, 0xfd}}, {"TXS", []byte{0x9a, 0x60}}, {"PHA", []byte{0x48, 0x60}}, {"PHX", []byte{0xda, 0x60}}, {"PHP", []byte{0x08, 0x60}}} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectedFixture()
			c.Entry = 0x008000
			c.Spans = []CodeSpan{{0x008000, 0x008000 + uint32(len(tc.code))}}
			copy(c.ROM, tc.code)
			c.MaxInstructions = 65536
			if _, err := DecodeConnected(c); err == nil {
				t.Fatal("accepted unsupported stack control")
			}
		})
	}
}
func TestConnectedWidthJoinRefuses(t *testing.T) {
	c := connectedFixture()
	c.Entry = 0x008000
	c.Spans = []CodeSpan{{0x008000, 0x008008}}
	copy(c.ROM, []byte{0xd0, 4, 0xc2, 0x20, 0x80, 1, 0xea, 0x60})
	if _, err := DecodeConnected(c); err == nil {
		t.Fatal("accepted conflicting widths")
	}
}

func TestConnectedDeclaredCarryGuard(t *testing.T) {
	c := connectedFixture()
	c.Context.C = "set"
	r, err := DecodeConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	source, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	memory := []MemoryCell{{0x7e1e00, 0}, {0x7e01fe, 0xff}, {0x7e01ff, 0x8f}}
	got, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_"+r.Name, c.ROM, []ReplayCase{{CaseID: "wrong-carry", InitialState: CPUState{S: 0x1fd, PC: 0x8000, PB: 1, P: 0x30}, InitialMemory: memory}, {CaseID: "declared-carry", InitialState: CPUState{S: 0x1fd, PC: 0x8000, PB: 1, P: 0x31}, InitialMemory: memory}})
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].MissingRead || got[0].TotalWrites != 0 || got[1].MissingRead {
		t.Fatalf("carry guard %+v", got)
	}
}
