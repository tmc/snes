package snes

import (
	"bytes"
	"encoding/gob"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/dma"
)

type hdmaRequestProbe struct {
	d          *dma.DMA
	requesting bool
	requests   int
}

func (p *hdmaRequestProbe) RequestHDMA(at uint64, setup bool) {
	p.requesting = true
	p.requests++
	p.d.RequestHDMA(at, setup)
	p.requesting = false
}

func TestSystemDMAEdgeSuspendsWithoutReentry(t *testing.T) {
	s := newBeamTestSystem(t, false, 0)
	s.Scheduler.SetNMI(false)
	probe := &hdmaRequestProbe{d: s.DMA}
	s.PPU.DMA = probe
	c := &s.DMA.Channels[0]
	c.Control = 0
	c.SrcBank = 0x7e
	c.SrcAddr = 0x100
	c.Target = 0x00
	c.Size = 2
	s.Bus.Write(0x7e0100, 0x8f)
	s.Bus.Write(0x7e0101, 0x80)
	depth, maxDepth := 0, 0
	edge := s.CPU.BusEdge
	s.CPU.BusEdge = func(n uint64) {
		depth++
		if depth > maxDepth {
			maxDepth = depth
		}
		edge(n)
		depth--
	}
	traces := 0
	s.DMA.Trace = func(dma.TransferTrace) {
		traces++
		if probe.requesting {
			t.Error("bus work inside PPU request")
		}
		if _, err := s.Serialize(); err == nil || !strings.Contains(err.Error(), "execution is active") {
			t.Errorf("active Serialize error=%v", err)
		}
	}
	beforeHook := 0
	s.CPU.BeforeExecute = func() { beforeHook++ }
	s.io.Write(0x420b, 1)
	start := s.CPU.Cycles
	s.CPU.Run()
	if maxDepth != 1 || traces != 1 || beforeHook != 1 || s.CPU.Cycles <= start+20 || s.PPU.INIDISP != 0x80 {
		t.Fatalf("depth=%d traces=%d hook=%d cycles=%d/%d display=%02x", maxDepth, traces, beforeHook, s.CPU.Cycles, start, s.PPU.INIDISP)
	}
	if _, err := s.Serialize(); err != nil {
		t.Fatal(err)
	}
}

func TestSystemHDMARequestsAndTimedWrites(t *testing.T) {
	s := newBeamTestSystem(t, false, 0)
	s.Scheduler.SetNMI(false)
	probe := &hdmaRequestProbe{d: s.DMA}
	s.PPU.DMA = probe
	c := &s.DMA.Channels[0]
	c.Control = 0
	c.SrcBank = 0x7e
	c.SrcAddr = 0x100
	c.Target = 0x00
	s.Bus.Write(0x7e0100, 1)
	s.Bus.Write(0x7e0101, 0x8f)
	s.Bus.Write(0x7e0102, 0)
	s.DMA.HDMAEnable = 1
	s.DMA.RequestHDMA(s.CPU.Cycles, true)
	for s.CPU.Cycles < 1000 {
		s.CPU.Run()
	}
	writes := 0
	s.DMA.HDMATrace = func(dma.TransferTrace) {
		writes++
		if probe.requesting {
			t.Error("HDMA bus work reentered PPU request callback")
		}
		if s.CPU.Cycles < 1104 {
			t.Error("early HDMA")
		}
	}
	for s.CPU.Cycles < 1200 {
		s.CPU.Run()
	}
	if probe.requests == 0 {
		t.Fatal("no production PPU event observed")
	}
	if writes != 1 || s.PPU.INIDISP != 0x8f {
		t.Fatalf("transfers=%d display=%02x", writes, s.PPU.INIDISP)
	}
}

func TestSystemDMAArmedStateRestore(t *testing.T) {
	s := newBeamTestSystem(t, false, 0)
	s.Scheduler.SetNMI(false)
	c := &s.DMA.Channels[0]
	c.Control = 0
	c.SrcBank = 0x7e
	c.SrcAddr = 0x100
	c.Target = 0x00
	c.Size = 1
	s.Bus.Write(0x7e0100, 0x8f)
	s.DMA.Request(1)
	s.DMA.BeginEdge(8)
	data, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	s.CPU.Run()
	want := s.CPU.Cycles
	wantDMA := s.DMA.SaveState()
	wantDisplay := s.PPU.INIDISP
	if err := s.Unserialize(data); err != nil {
		t.Fatal(err)
	}
	s.CPU.Run()
	if s.CPU.Cycles != want || s.DMA.SaveState() != wantDMA || s.PPU.INIDISP != wantDisplay {
		t.Fatal("armed continuation changed after root gob restore")
	}
	// System cannot restore a standalone controller continuation without a CPU
	// instruction continuation, even when the DMA-only snapshot is well formed.
	var state systemState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		t.Fatal(err)
	}
	state.DMA.Execution.Phase = 1
	state.DMA.Execution.ClockCount = 8
	var bad bytes.Buffer
	if err := gob.NewEncoder(&bad).Encode(state); err != nil {
		t.Fatal(err)
	}
	before := s.CPU.SaveState()
	if err := s.Unserialize(bad.Bytes()); err == nil {
		t.Fatal("accepted active controller as system save")
	}
	if s.CPU.SaveState() != before {
		t.Fatal("invalid state mutated CPU")
	}
}

func TestSystemPowerClearsArmedDMA(t *testing.T) {
	s := newBeamTestSystem(t, false, 0)
	for i := 0; i < 2; i++ {
		s.DMA.Request(0xff)
		s.DMA.RequestHDMA(s.CPU.Cycles, true)
		s.DMA.BeginEdge(8)
		calls := 0
		s.DMA.Trace = func(dma.TransferTrace) { calls++ }
		s.Power()
		if calls != 0 || s.DMA.SaveState().Execution.Pending || s.DMA.Busy() || s.DMA.SaveState().Execution.Armed {
			t.Fatalf("reset ran stale DMA: calls=%d state=%+v", calls, s.DMA.SaveState().Execution)
		}
	}
}
