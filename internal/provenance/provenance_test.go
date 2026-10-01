package provenance

import "testing"

func fixture() []Event {
	return []Event{
		{Kind: "bus", Actor: "cpu", Op: "write", Addr: 0x800a00, Value: 7, Frame: 82, PC: 0x8600},
		{Kind: "dma", Channel: 0, Mode: 0, Target: 4, Count: 1, Addr: 0xa00},
		{Kind: "bus", Actor: "dma_or_hdma", Op: "read", Addr: 0xa00, Value: 7},
		{Kind: "bus", Actor: "dma_or_hdma", Op: "write", Addr: 0x2104, Value: 7},
		{Kind: "ppu", Space: "oam", Op: "write", Addr: 512, Value: 7},
	}
}
func ordered(e []Event) []Event {
	for i := range e {
		e[i].ID = uint64(i)
		e[i].Cycle = uint64(i)
	}
	return e
}
func TestAnalyze(t *testing.T) {
	tests := []struct {
		name    string
		modify  func([]Event) []Event
		want    string
		routine bool
		err     bool
	}{
		{"alias", func(e []Event) []Event { return e }, "observed_last_writer", true, false},
		{"overwrite", func(e []Event) []Event { w := e[0]; w.PC = 0x9000; return append([]Event{e[0], w}, e[1:]...) }, "observed_last_writer", false, false},
		{"device overwrite", func(e []Event) []Event {
			w := Event{Kind: "wram_port", Op: "write", Addr: 0xa00, Value: 7, PC: 0x9000}
			return append([]Event{e[0], w}, e[1:]...)
		}, "observed_last_writer", false, false},
		{"missing writer", func(e []Event) []Event { return e[1:] }, "unknown", false, false},
		{"changed value", func(e []Event) []Event { e[0].Value = 8; return e }, "unknown", false, false},
		{"wrong source", func(e []Event) []Event { e[2].Addr++; return e }, "", false, false},
		{"missing read", func(e []Event) []Event { return append(e[:2], e[3:]...) }, "", false, false},
		{"missing PPU", func(e []Event) []Event { return e[:4] }, "", false, false},
		{"reordered pair", func(e []Event) []Event { e[2], e[3] = e[3], e[2]; return e }, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ordered(tt.modify(fixture()))
			r, err := Analyze(e, 82, 0x85fc, 0x8781)
			if (err != nil) != tt.err {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(r.Links) != 0 {
					t.Fatalf("unexpected links %+v", r.Links)
				}
				return
			}
			if len(r.Links) != 1 || r.Links[0].Status != tt.want || r.Links[0].Routine != tt.routine {
				t.Fatalf("result %+v", r)
			}
		})
	}
}
func TestOrder(t *testing.T) {
	for _, change := range []func([]Event){func(e []Event) { e[1].ID++ }, func(e []Event) { e[2].Cycle = 0 }} {
		e := ordered(fixture())
		change(e)
		if _, err := Analyze(e, 82, 0, 0x10000); err == nil {
			t.Fatal("accepted out of order events")
		}
	}
}

func TestDMAJoinMutations(t *testing.T) {
	tests := []struct {
		name    string
		change  func([]Event) []Event
		links   int
		routine bool
	}{
		{"duplicate physical write", func(e []Event) []Event { w := e[4]; w.Addr++; return append(e, w) }, 1, true},
		{"DMA writer stale PC", func(e []Event) []Event { e[0].Actor = "dma_or_hdma"; return e }, 1, false},
		{"wrong read actor", func(e []Event) []Event { e[2].Actor = "cpu"; return e }, 0, false},
		{"wrong write channel", func(e []Event) []Event { e[3].Channel = 1; return e }, 0, false},
		{"wrong read channel", func(e []Event) []Event { e[2].Channel = 1; return e }, 0, false},
		{"missing register", func(e []Event) []Event { return append(e[:3], e[4:]...) }, 0, false},
		{"intervening alias writer", func(e []Event) []Event { w := e[0]; w.Addr = 0x7e0a00; return append(append(e[:3:3], w), e[3:]...) }, 0, false},
		{"intervening read", func(e []Event) []Event { return append(append(e[:4:4], e[2]), e[4:]...) }, 0, false},
		{"wrong storage op", func(e []Event) []Event { e[4].Op = "read"; return e }, 0, false},
		{"outside high OAM", func(e []Event) []Event { e[4].Addr = 544; return e }, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Analyze(ordered(tt.change(fixture())), 82, 0x85fc, 0x8781)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Links) != tt.links {
				t.Fatalf("links=%d want=%d", len(r.Links), tt.links)
			}
			if len(r.Links) > 0 && r.Links[0].Routine != tt.routine {
				t.Fatalf("routine=%v", r.Links[0].Routine)
			}
		})
	}
}

func TestDMABytePairConsumedOnce(t *testing.T) {
	e := fixture()
	e[1].Count = 2
	duplicate := e[4]
	duplicate.Addr = 513
	r2, w2, p2 := e[2], e[3], e[4]
	r2.Addr++
	p2.Addr = 514
	e = append(e, duplicate, r2, w2, p2)
	r, err := Analyze(ordered(e), 82, 0x85fc, 0x8781)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Links) != 1 || r.Links[0].OAMWrite.Addr != 512 {
		t.Fatalf("reused byte pair: %+v", r.Links)
	}
}

func TestDMAEventGap(t *testing.T) {
	e := ordered(fixture())
	e[4].ID++
	if _, err := Analyze(e, 82, 0x85fc, 0x8781); err == nil {
		t.Fatal("accepted missing event")
	}
}
