package visualmap

import (
	"context"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/trace"
	"testing"
)

func TestAdditionalNonWRAMSource(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(reviewDMA(195000, "cpu", 0x008000))
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.DMATransfer != nil && r.DMATransfer.SourceRange.Space == "wram" {
		t.Fatalf("ROM source relabeled WRAM: %+v", r.DMATransfer)
	}
}
func TestAdditionalInvalidWRAMOffset(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(trace.Event{Kind: "bus", Space: "wram", Addr: 0xa00, Op: "write", Value: 100, Cycle: 150000})
	e.IngestEvent(reviewDMA(195000, "wram", 0x20a00))
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.CPUWrite != nil {
		t.Fatalf("invalid source 20a00 aliases valid a00 writer: %+v", r.CPUWrite)
	}
}
func TestAdditionalWRAMPortOmittedSpace(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(trace.Event{Kind: "wram_port", Addr: 0xa000, Op: "write", Value: 100, Cycle: 150000})
	e.IngestEvent(reviewDMA(195000, "wram", 0xa000))
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.CPUWrite == nil {
		t.Fatal("wram_port high normalized offset with omitted Space dropped")
	}
}
func TestAdditionalOutOfOrderWriters(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(trace.Event{Kind: "bus", Space: "wram", Addr: 0xa00, Op: "write", Value: 100, Cycle: 150000})
	e.IngestEvent(trace.Event{Kind: "bus", Space: "wram", Addr: 0xa00, Op: "write", Value: 99, Cycle: 140000})
	e.IngestEvent(reviewDMA(195000, "wram", 0xa00))
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.CPUWrite == nil || r.CPUWrite.Cycle != 150000 {
		t.Fatalf("older writer selected: %+v", r.CPUWrite)
	}
}
func TestAdditionalEqualCycleDMAIdentity(t *testing.T) {
	e := reviewEngine()
	d := reviewDMA(195000, "wram", 0xa00)
	d.ID = 10
	e.IngestEvent(d)
	d = reviewDMA(195000, "wram", 0xb00)
	d.ID = 11
	e.IngestEvent(d)
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.DMATransfer == nil || r.DMATransfer.SourceRange.Start != 0xb00 {
		t.Fatalf("later event ID11 at tied cycle ignored: %+v", r.DMATransfer)
	}
}
func TestAdditionalEqualCycleWriterIdentity(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(trace.Event{ID: 10, Kind: "bus", Space: "wram", Addr: 0xa00, Op: "write", Value: 100, Cycle: 195000})
	d := reviewDMA(195000, "wram", 0xa00)
	d.ID = 11
	e.IngestEvent(d)
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.CPUWrite == nil {
		t.Fatal("writer ID10 before DMA ID11 at equal cycle omitted")
	}
}
func TestAdditionalContradictingSnapshot(t *testing.T) {
	e := reviewEngine()
	e.IngestEvent(trace.Event{Kind: "bus", Space: "wram", Addr: 0xa00, Op: "write", Value: 99, Cycle: 150000})
	e.IngestEvent(reviewDMA(195000, "wram", 0xa00))
	r, _ := e.Query(context.Background(), 10, 104, 54)
	if r.CPUWrite != nil && r.CPUWrite.StoredValue != r.VisualEntity.PhysicalOAM[0] {
		t.Fatalf("inconsistent upload byte accepted: writer=%d physical OAM=%d", r.CPUWrite.StoredValue, r.VisualEntity.PhysicalOAM[0])
	}
}

func reviewEngine() *Engine {
	e := NewEngine(&recovery.Document{}, nil)
	var o [544]uint8
	o[0] = 100
	o[1] = 50
	e.SetOAMSnapshot(10, o)
	e.SetFrameBounds(10, FrameBounds{StartCycle: 200000})
	return e
}
func reviewDMA(c uint64, space string, addr uint32) trace.Event {
	return trace.Event{Kind: "dma", Cycle: c, Frame: 9, DMA: &trace.DMAContext{Count: 4, Target: 4}, Source: trace.Range{Space: space, Start: addr, End: addr + 3}, Dest: trace.Range{Space: "oam", Start: 0, End: 3}}
}
