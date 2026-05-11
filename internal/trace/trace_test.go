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
