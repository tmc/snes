package extractor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestWordBusAddresses(t *testing.T) {
	for _, op := range []string{"read", "write"} {
		t.Run(op, func(t *testing.T) {
			events := BusEventList{
				{Cycle: 11, ID: 1, Space: "wram", Addr: 0x1234, Op: op, Value: 0x34, CPU: &EventCPU{EffectiveAddr: 0x7e1234}},
				{Cycle: 12, ID: 2, Space: "wram", Addr: 0x1235, Op: op, Value: 0x12, CPU: &EventCPU{EffectiveAddr: 0x7e1234}},
			}
			got := ExtractMemoryTrace(events, nil, 10, 20, nil)
			if got.RefusalReason != "" {
				t.Fatal(got.RefusalReason)
			}
			cells := got.InitialMemory
			if op == "write" {
				cells = got.ObservedWrites
			}
			if len(cells) != 2 || cells[0].Address != 0x7e1234 || cells[1].Address != 0x7e1235 || cells[0].Value != 0x34 || cells[1].Value != 0x12 {
				t.Fatalf("word cells: %+v", cells)
			}
		})
	}
}

func TestWordHistoryAddresses(t *testing.T) {
	history := `{"kind":"bus","cycle":11,"id":1,"space":"wram","addr":4660,"op":"write","value":52,"after":52,"cpu":{"effective_addr":8262196}}
{"kind":"bus","cycle":12,"id":2,"space":"wram","addr":4661,"op":"write","value":18,"after":18,"cpu":{"effective_addr":8262196}}
`
	h, err := NewWriteHistory(bufio.NewScanner(strings.NewReader(history)))
	if err != nil {
		t.Fatal(err)
	}
	cells := []MemoryCell{{Address: 0x7e1234, Value: 0x34}, {Address: 0x7e1235, Value: 0x12}}
	sources, err := h.ClassifyInitialMemory(cells, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources: %+v", sources)
	}
}

func TestInvalidWRAMOffset(t *testing.T) {
	for _, addr := range []uint32{0x20000, 0x7e1234} {
		got := ExtractMemoryTrace(BusEventList{{Cycle: 11, Space: "wram", Op: "write", Addr: addr}}, nil, 10, 20, nil)
		if got.RefusalReason == "" {
			t.Fatalf("accepted malformed producer WRAM offset %#x", addr)
		}
	}
	h, err := NewWriteHistory(bufio.NewScanner(strings.NewReader(`{"kind":"bus","cycle":11,"space":"wram","addr":131072,"op":"write"}`)))
	if err == nil || h != nil {
		t.Fatal("history accepted out-of-range offset")
	}
}

func TestRetainedAddressProjection(t *testing.T) {
	dir := os.Getenv("SNES_EXTRACTOR_RETAINED_DIR")
	if dir == "" {
		t.Skip("set retained final corpus directory")
	}
	data, err := os.ReadFile(filepath.Join(dir, "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []RoutineCaseV1
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var c RoutineCaseV1
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	if len(cases) != 110 {
		t.Fatalf("cases %d !=110", len(cases))
	}
	// The final producer output references capture/history in its unchanged corpus evidence.
	f, err := os.Open(cases[0].Evidence.Capture.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1<<20)
	var events BusEventList
	var transitions []*RawEvent
	for scan.Scan() {
		var e RawEvent
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case "bus":
			events = append(events, &e)
		case "cpu_transition", "dma", "hdma", "mmio":
			transitions = append(transitions, &e)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Sort(events)
	f2, err := os.Open(cases[0].Evidence.History.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	scan2 := bufio.NewScanner(f2)
	scan2.Buffer(make([]byte, 4096), 1<<20)
	history, err := NewWriteHistory(scan2)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		trace := ExtractMemoryTrace(events, transitions, c.InitialState.Cycles, c.ObservedExitState.Cycles, nil)
		if trace.RefusalReason != "" {
			t.Fatalf("%s: %s", c.CaseID, trace.RefusalReason)
		}
		if !reflect.DeepEqual(trace.InitialMemory, c.InitialMemory) || !reflect.DeepEqual(trace.ObservedWrites, c.ObservedWrites) {
			t.Fatalf("%s projection changed", c.CaseID)
		}
		if _, err := history.ClassifyInitialMemory(trace.InitialMemory, c.InitialState.Cycles, ""); err != nil {
			t.Fatalf("%s: %v", c.CaseID, err)
		}
	}
	t.Logf("%d retained projections and prior-write classifications unchanged", len(cases))
}
