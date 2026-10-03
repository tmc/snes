package visualmap

import (
 "context"
 "testing"
 "github.com/tmc/snes/internal/recovery"
 "github.com/tmc/snes/internal/trace"
)

func contractEngine() *Engine {
 e:=NewEngine(&recovery.Document{},nil)
 var oam [544]uint8
 oam[0]=100; oam[1]=50; oam[512]=2
 e.SetOAMSnapshot(1,oam)
 e.SetFrameBounds(1,FrameBounds{StartCycle:100,VBlankCycle:200})
 return e
}
func contractDMA(id,cycle uint64,space string,source uint32) trace.Event {
 return trace.Event{ID:id,Kind:"dma",Cycle:cycle,PC:&trace.PC{Addr:0x806d},DMA:&trace.DMAContext{Count:1,Target:4},Source:trace.Range{Space:space,Start:source,End:source},Dest:trace.Range{Space:"oam",Start:512,End:512}}
}
func contractWrite(id,cycle uint64,addr uint32,value uint64) trace.Event {
 return trace.Event{ID:id,Kind:"bus",Op:"write",Space:"wram",Addr:addr,Value:value,Cycle:cycle,PC:&trace.PC{Addr:0x8040}}
}
func TestDirectionPartialOAMIsUnknown(t *testing.T) {
 e:=NewEngine(&recovery.Document{},nil)
 e.SetFrameBounds(1,FrameBounds{StartCycle:100,VBlankCycle:200})
 e.IngestEvent(trace.Event{ID:1,Kind:"ppu",Space:"oam",Op:"write",Addr:543,Value:1,Cycle:50})
 r,err:=e.Query(context.Background(),1,1,1)
 if err==nil && r.VisualEntity.Kind=="sprite" { t.Fatalf("unknown sprite0 became observed entity: %+v known=%d physical=%v",r.VisualEntity,r.KnownOAMBytes,r.VisualEntity.PhysicalOAM) }
}
func TestDirectionInvalidSourceCannotBecomeWRAM(t *testing.T) {
 for _,tt:=range []struct{name,space string;source uint32}{{"CPUROM","cpu",0x8000},{"OutOfRangeWRAM","wram",0x20000}} {
 t.Run(tt.name,func(t *testing.T){e:=contractEngine();e.IngestEvent(contractWrite(1,80,0,2));e.IngestEvent(contractDMA(2,90,tt.space,tt.source));r,err:=e.Query(context.Background(),1,101,51);if err!=nil {t.Fatal(err)};if r.DMATransfer!=nil && r.DMATransfer.SourceRange.Space=="wram" {t.Fatalf("unsupported source relabeledWRAM: %+v writer=%+v consistency=%q",r.DMATransfer,r.CPUWrite,r.ValueConsistency)}})
 }
}
func TestDirectionCurrentPCIsNotTriggerPC(t *testing.T) {
 e:=contractEngine();e.IngestEvent(contractDMA(2,90,"cpu",0x7e0a00));r,err:=e.Query(context.Background(),1,101,51);if err!=nil {t.Fatal(err)};if r.DMATransfer.TriggerPC!="" {t.Fatalf("unwitnessed trigger_pc=%s current_pc=%s",r.DMATransfer.TriggerPC,r.DMATransfer.CurrentPC)}
}
func TestDirectionSameCyclePrecedingWriter(t *testing.T) {
 e:=contractEngine();e.IngestEvent(contractWrite(1,80,0xa00,1));e.IngestEvent(contractWrite(2,90,0xa00,2));e.IngestEvent(contractDMA(3,90,"cpu",0x7e0a00));r,err:=e.Query(context.Background(),1,101,51);if err!=nil {t.Fatal(err)};if r.CPUWrite==nil||r.CPUWrite.StoredValue!=2 {t.Fatalf("latestprecedingwriter lost: %+v consistency=%q",r.CPUWrite,r.ValueConsistency)}
}
func TestDirectionSameCycleLatestDMA(t *testing.T) {
 e:=contractEngine();e.IngestEvent(contractDMA(2,90,"cpu",0x7e0a00));e.IngestEvent(contractDMA(3,90,"cpu",0x7e0b00));r,err:=e.Query(context.Background(),1,101,51);if err!=nil {t.Fatal(err)};if r.DMATransfer==nil || r.DMATransfer.WRAMSourceAddress!="7E0B00" {t.Fatalf("latesttransfer lost: %+v",r.DMATransfer)}
}
