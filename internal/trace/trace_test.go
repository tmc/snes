package trace

import (
	"strings"
	"testing"
)

func TestParseRange(t *testing.T) {
	r, err := ParseRange("wram:0x20-0x23")
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}
	if !r.Contains("wram", 0x22) || r.Contains("wram", 0x24) {
		t.Fatalf("range contains wrong addresses: %+v", r)
	}
}

func TestParseWatches(t *testing.T) {
	watches, err := ParseWatches(strings.NewReader("link_x = u16(wram:0x0022)\n"))
	if err != nil {
		t.Fatalf("ParseWatches: %v", err)
	}
	if len(watches) != 1 || watches[0].Name != "link_x" || watches[0].Width != 2 || watches[0].Range.End != 0x23 {
		t.Fatalf("watches = %+v", watches)
	}
}

func TestQueryWriters(t *testing.T) {
	q := Query{Events: []Event{
		{ID: 1, Kind: "bus", Op: "write", Space: "wram", Addr: 0x22},
		{ID: 2, Kind: "bus", Op: "read", Space: "wram", Addr: 0x22},
	}}
	got := q.Writers(Range{Space: "wram", Start: 0x22, End: 0x22})
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("Writers = %+v", got)
	}
}

func TestQueryFrameRange(t *testing.T) {
	q := Query{Events: []Event{
		{ID: 1, Kind: "bus", Frame: 1, Op: "write", Space: "wram", Addr: 0x22},
		{ID: 2, Kind: "bus", Frame: 2, Op: "write", Space: "wram", Addr: 0x22},
		{ID: 3, Kind: "bus", Frame: 3, Op: "read", Space: "wram", Addr: 0x22},
		{ID: 4, Kind: "dma", Frame: 3, Dest: Range{Space: "vram", Start: 0x20, End: 0x2f}},
		{ID: 5, Kind: "hdma", Frame: 3, Dest: Range{Space: "vram", Start: 0x30, End: 0x33}},
		{ID: 6, Kind: "mmio", Frame: 4, PC: &PC{Bank: 0x80, Addr: 0x8123}},
		{ID: 7, Kind: "ppu", Frame: 4, Op: "write", Space: "vram", Addr: 0x40, PC: &PC{Bank: 0x80, Addr: 0x8124}},
	}}
	if got := q.WritersInFrameRange(Range{Space: "wram", Start: 0x22, End: 0x22}, 2, 2); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("WritersInFrameRange = %+v", got)
	}
	if got := q.ReadersInFrameRange(Range{Space: "wram", Start: 0x22, End: 0x22}, 3, 3); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("ReadersInFrameRange = %+v", got)
	}
	if got := q.DMAForDestInFrameRange(Range{Space: "vram", Start: 0x20, End: 0x30}, 3, 3); len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Fatalf("DMAForDestInFrameRange = %+v", got)
	}
	if got := q.BusForPCInFrameRange(Range{Space: "cpu", Start: 0x808000, End: 0x808fff}, 4, 4); len(got) != 2 || got[0].ID != 6 || got[1].ID != 7 {
		t.Fatalf("BusForPCInFrameRange = %+v", got)
	}
	if got := q.WritersInFrameRange(Range{Space: "vram", Start: 0x40, End: 0x40}, 4, 4); len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("WritersInFrameRange ppu = %+v", got)
	}
}

func TestQueryBusForPC(t *testing.T) {
	q := Query{Events: []Event{
		{ID: 1, Kind: "bus", Op: "write", PC: &PC{Bank: 0x80, Addr: 0x8123}},
		{ID: 2, Kind: "bus", Op: "write", PC: &PC{Bank: 0x80, Addr: 0x9000}},
	}}
	got := q.BusForPC(Range{Space: "cpu", Start: 0x808000, End: 0x808fff})
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("BusForPC = %+v", got)
	}
}

func TestMMIORegister(t *testing.T) {
	tests := []struct {
		addr         uint32
		wantName     string
		wantCategory string
	}{
		{0x2118, "VMDATAL", "vram_data"},
		{0x2122, "CGDATA", "cgram_data"},
		{0x2140, "APUIO0", "apu_port"},
		{0x4301, "DMA0_BBAD", "dma_register"},
		{0x4315, "DMA1_DAS", "dma_register"},
	}
	for _, tt := range tests {
		name, category := MMIORegister(tt.addr)
		if name != tt.wantName || category != tt.wantCategory {
			t.Fatalf("MMIORegister(%#x) = %q, %q, want %q, %q", tt.addr, name, category, tt.wantName, tt.wantCategory)
		}
	}
}

func TestInputRegister(t *testing.T) {
	tests := []struct {
		addr         uint32
		wantName     string
		wantCategory string
	}{
		{0x4016, "JOYSER0", "controller_serial"},
		{0x4017, "JOYSER1", "controller_serial"},
		{0x4218, "JOY1L", "auto_joypad"},
		{0x421f, "JOY4H", "auto_joypad"},
	}
	for _, tt := range tests {
		name, category := InputRegister(tt.addr)
		if name != tt.wantName || category != tt.wantCategory {
			t.Fatalf("InputRegister(%#x) = %q, %q, want %q, %q", tt.addr, name, category, tt.wantName, tt.wantCategory)
		}
	}
}
