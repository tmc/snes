package provenance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/trace"
)

func testWindow(from, to int, events []Event) Window {
	h := strings.Repeat("a", 64)
	frames := make([]FrameIdentity, 0, to-from)
	for f := from; f < to; f++ {
		frames = append(frames, FrameIdentity{
			Frame:       f,
			PPUFrame:    uint64(f + 224),
			StartCycle:  0,
			VBlankCycle: 50000,
			EndCycle:    100000,
			StateSHA256: h,
			BusSHA256:   h,
			PixelSHA256: h,
		})
	}
	evs := make([]Event, len(events))
	copy(evs, events)
	for i := range evs {
		evs[i].ID = uint64(i)
	}
	return Window{
		Schema: "snes-observation-window-v1",
		Identity: Identity{
			ROMSHA256:    h,
			StateSHA256:  h,
			InputsSHA256: h,
			RunSHA256:    h,
			Mode:         "original_interpreter",
		},
		From:     from,
		To:       to,
		Complete: true,
		Coverage: WriterCoverage,
		Events:   evs,
		Frames:   frames,
	}
}

func TestBuildByteInterval_TableDriven(t *testing.T) {
	tests := []struct {
		name                 string
		from                 int
		to                   int
		events               []Event
		writerID             uint64
		correlator           OccurrenceCorrelator
		wantErr              bool
		wantVal              uint8
		wantAddr             uint32
		wantReaders          int
		wantTerm             string
		wantHostStart        int
		wantHostEnd          int
		wantPPUStart         int
		wantPPUEnd           int
		wantCyclesStart      uint64
		wantCyclesEnd        uint64
		wantHasReplacement   bool
		wantReplVal          uint8
		wantCorrStatus       string
		wantInitialRetID     uint64
		wantInitialPrecRetID uint64
	}{
		{
			name: "single_version_with_readers_and_replacement",
			from: 108,
			to:   111,
			events: []Event{
				{Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1000, Frame: 108, PPUFrame: 332},
				{Kind: "bus", Op: "read", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1500, Frame: 108, PPUFrame: 332},
				{Kind: "bus", Op: "read", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 2000, Frame: 109, PPUFrame: 333},
				{Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 120, Cycle: 3000, Frame: 110, PPUFrame: 334},
			},
			writerID: 0,
			correlator: OccurrenceCorrelatorFunc(func(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool) {
				switch cycle {
				case 1000:
					return &RetirementCorrespondence{
						TraceBusID:            30147,
						RetirementID:          30148,
						Seq:                   8546,
						PrecedingRetirementID: 30143,
						Instruction:           "0C:C46E",
						InstructionBytes:      "8d051f",
						RegisterChanges:       []string{"write 115"},
					}, true
				case 1500:
					return &RetirementCorrespondence{
						TraceBusID:            40000,
						RetirementID:          40001,
						Seq:                   9000,
						PrecedingRetirementID: 39999,
						Instruction:           "09:F882",
						RegisterChanges:       []string{"Y $0000 → $0073"},
					}, true
				case 2000:
					return &RetirementCorrespondence{
						TraceBusID:            52076,
						RetirementID:          52077,
						Seq:                   13199,
						PrecedingRetirementID: 52073,
						Instruction:           "09:F882",
						InstructionBytes:      "a405",
						RegisterChanges:       []string{"Y $0045 → $0073"},
					}, true
				case 3000:
					return &RetirementCorrespondence{
						TraceBusID:            139219,
						RetirementID:          139220,
						Seq:                   35514,
						PrecedingRetirementID: 139215,
						Instruction:           "0C:C46E",
						InstructionBytes:      "8d051f",
						RegisterChanges:       []string{"write 120"},
					}, true
				}
				return nil, false
			}),
			wantErr:              false,
			wantVal:              115,
			wantAddr:             0x7E1F05,
			wantReaders:          2,
			wantTerm:             "overwritten",
			wantHostStart:        108,
			wantHostEnd:          110,
			wantPPUStart:         332,
			wantPPUEnd:           334,
			wantCyclesStart:      1000,
			wantCyclesEnd:        3000,
			wantHasReplacement:   true,
			wantReplVal:          120,
			wantCorrStatus:       "complete",
			wantInitialRetID:     30148,
			wantInitialPrecRetID: 30143,
		},
		{
			name: "termination_at_window_end",
			from: 110,
			to:   111,
			events: []Event{
				{Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 120, Cycle: 3000, Frame: 110, PPUFrame: 334},
				{Kind: "bus", Op: "read", Actor: "cpu", Addr: 0x7E1F05, Value: 120, Cycle: 3500, Frame: 110, PPUFrame: 334},
			},
			writerID:           0,
			correlator:         nil,
			wantErr:            false,
			wantVal:            120,
			wantAddr:           0x7E1F05,
			wantReaders:        1,
			wantTerm:           "window_end",
			wantHostStart:      110,
			wantHostEnd:        110,
			wantPPUStart:       334,
			wantPPUEnd:         334,
			wantCyclesStart:    3000,
			wantCyclesEnd:      3500,
			wantHasReplacement: false,
			wantCorrStatus:     "unavailable",
		},
		{
			name: "error_on_read_conflict",
			from: 108,
			to:   109,
			events: []Event{
				{Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1000, Frame: 108, PPUFrame: 332},
				{Kind: "bus", Op: "read", Actor: "cpu", Addr: 0x7E1F05, Value: 99, Cycle: 1500, Frame: 108, PPUFrame: 332},
			},
			writerID: 0,
			wantErr:  true,
		},
		{
			name: "error_on_non_attributed_event",
			from: 108,
			to:   109,
			events: []Event{
				{Kind: "bus", Op: "read", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1000, Frame: 108, PPUFrame: 332},
			},
			writerID: 0,
			wantErr:  true,
		},
		{
			name: "error_on_writer_out_of_bounds",
			from: 108,
			to:   109,
			events: []Event{
				{Kind: "bus", Op: "write", Actor: "cpu", Addr: 0x7E1F05, Value: 115, Cycle: 1000, Frame: 108, PPUFrame: 332},
			},
			writerID: 10,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testWindow(tt.from, tt.to, tt.events)
			pin, err := WindowSHA256(w)
			if err != nil {
				t.Fatalf("WindowSHA256 error = %v", err)
			}

			interval, err := BuildByteInterval(w, pin, tt.writerID, tt.correlator)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildByteInterval() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if interval.Value != tt.wantVal {
				t.Errorf("Value = %d, want %d", interval.Value, tt.wantVal)
			}
			if interval.PhysicalAddress != tt.wantAddr {
				t.Errorf("PhysicalAddress = $%06X, want $%06X", interval.PhysicalAddress, tt.wantAddr)
			}
			if len(interval.Readers) != tt.wantReaders {
				t.Errorf("Readers count = %d, want %d", len(interval.Readers), tt.wantReaders)
			}
			if interval.Termination != tt.wantTerm {
				t.Errorf("Termination = %q, want %q", interval.Termination, tt.wantTerm)
			}
			if interval.HostFrames.Start != tt.wantHostStart || interval.HostFrames.End != tt.wantHostEnd {
				t.Errorf("HostFrames = %d..%d, want %d..%d", interval.HostFrames.Start, interval.HostFrames.End, tt.wantHostStart, tt.wantHostEnd)
			}
			if interval.PPUFrames.Start != tt.wantPPUStart || interval.PPUFrames.End != tt.wantPPUEnd {
				t.Errorf("PPUFrames = %d..%d, want %d..%d", interval.PPUFrames.Start, interval.PPUFrames.End, tt.wantPPUStart, tt.wantPPUEnd)
			}
			if interval.Cycles.Start != tt.wantCyclesStart || interval.Cycles.End != tt.wantCyclesEnd {
				t.Errorf("Cycles = %d..%d, want %d..%d", interval.Cycles.Start, interval.Cycles.End, tt.wantCyclesStart, tt.wantCyclesEnd)
			}
			if (interval.Replacement != nil) != tt.wantHasReplacement {
				t.Errorf("Replacement != nil is %v, want %v", interval.Replacement != nil, tt.wantHasReplacement)
			}
			if tt.wantHasReplacement && interval.Replacement != nil && interval.Replacement.Value != tt.wantReplVal {
				t.Errorf("Replacement value = %d, want %d", interval.Replacement.Value, tt.wantReplVal)
			}
			if interval.CorrespondenceStatus != tt.wantCorrStatus {
				t.Errorf("CorrespondenceStatus = %q, want %q", interval.CorrespondenceStatus, tt.wantCorrStatus)
			}
			if tt.wantInitialRetID > 0 {
				if interval.InitialStore.RetirementID != tt.wantInitialRetID {
					t.Errorf("InitialStore.RetirementID = %d, want %d", interval.InitialStore.RetirementID, tt.wantInitialRetID)
				}
				if interval.InitialStore.PrecedingRetirementID != tt.wantInitialPrecRetID {
					t.Errorf("InitialStore.PrecedingRetirementID = %d, want %d", interval.InitialStore.PrecedingRetirementID, tt.wantInitialPrecRetID)
				}
			}
		})
	}
}

func TestTraceCorrelatorFromEvents(t *testing.T) {
	events := []trace.Event{
		{
			ID:    30143,
			Kind:  "cpu_insn",
			Cycle: 118888434,
			Insn: &trace.Insn{
				Seq:    8545,
				Status: trace.StatusRetired,
				Entry:  trace.Registers{Cycles: 118888434, PB: 0x0C, PC: 0xC460},
				Exit:   trace.Registers{Cycles: 118888446, PB: 0x0C, PC: 0xC464},
			},
		},
		{
			ID:    30147,
			Kind:  "bus",
			Cycle: 118888482,
			Space: "wram",
			Addr:  0x1F05,
			Op:    "write",
			Value: 115,
		},
		{
			ID:    30148,
			Kind:  "cpu_insn",
			Cycle: 118888450,
			Insn: &trace.Insn{
				Seq:    8546,
				Status: trace.StatusRetired,
				Entry:  trace.Registers{Cycles: 118888450, PB: 0x0C, PC: 0xC46E, A: 50291},
				Exit:   trace.Registers{Cycles: 118888482, PB: 0x0C, PC: 0xC471, A: 50291},
				Fetches: []trace.FetchRecord{
					{Value: 0x8D},
					{Value: 0x05},
					{Value: 0x1F},
				},
			},
		},
	}

	correlator := TraceCorrelatorFromEvents(events)
	corr, ok := correlator.CorrelateBus(118888482, 0x7E1F05, "write", 115)
	if !ok || corr == nil {
		t.Fatalf("CorrelateBus failed to correlate write at 118888482")
	}

	if corr.TraceBusID != 30147 {
		t.Errorf("TraceBusID = %d, want 30147", corr.TraceBusID)
	}
	if corr.RetirementID != 30148 {
		t.Errorf("RetirementID = %d, want 30148", corr.RetirementID)
	}
	if corr.Seq != 8546 {
		t.Errorf("Seq = %d, want 8546", corr.Seq)
	}
	if corr.PrecedingRetirementID != 30143 {
		t.Errorf("PrecedingRetirementID = %d, want 30143", corr.PrecedingRetirementID)
	}
	if corr.Instruction != "0C:C46E" {
		t.Errorf("Instruction = %s, want 0C:C46E", corr.Instruction)
	}
	if corr.InstructionBytes != "8d051f" {
		t.Errorf("InstructionBytes = %s, want 8d051f", corr.InstructionBytes)
	}
	if corr.InstructionCycles.Start != 118888450 || corr.InstructionCycles.End != 118888482 {
		t.Errorf("InstructionCycles = %d..%d, want 118888450..118888482", corr.InstructionCycles.Start, corr.InstructionCycles.End)
	}
}

func TestBuildByteInterval_NaturalWindow(t *testing.T) {
	windowPath := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/natural-producer-capture/project/window.json"
	data, err := os.ReadFile(windowPath)
	if err != nil {
		t.Skipf("natural window not found: %v", err)
	}
	var w Window
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatalf("unmarshal window: %v", err)
	}
	pin, err := WindowSHA256(w)
	if err != nil {
		t.Fatalf("WindowSHA256: %v", err)
	}

	correlator := OccurrenceCorrelatorFunc(func(cycle uint64, addr uint32, op string, val uint8) (*RetirementCorrespondence, bool) {
		switch cycle {
		case 118888482:
			return &RetirementCorrespondence{
				TraceBusID:            30147,
				RetirementID:          30148,
				Seq:                   8546,
				PrecedingRetirementID: 30143,
				Instruction:           "0C:C46E",
				InstructionBytes:      "8d051f",
				RegisterChanges:       []string{"write 115"},
			}, true
		case 119041698:
			return &RetirementCorrespondence{
				TraceBusID:            52076,
				RetirementID:          52077,
				Seq:                   13199,
				PrecedingRetirementID: 52073,
				Instruction:           "09:F882",
				InstructionBytes:      "a405",
				RegisterChanges:       []string{"Y $0045 → $0073"},
			}, true
		case 119603144:
			return &RetirementCorrespondence{
				TraceBusID:            139209,
				RetirementID:          139210,
				Seq:                   35511,
				PrecedingRetirementID: 139205,
				Instruction:           "0C:C468",
				InstructionBytes:      "ad051f",
				RegisterChanges:       []string{"A $C438 → $C473"},
			}, true
		case 119603210:
			return &RetirementCorrespondence{
				TraceBusID:            139219,
				RetirementID:          139220,
				Seq:                   35514,
				PrecedingRetirementID: 139215,
				Instruction:           "0C:C46E",
				InstructionBytes:      "8d051f",
				RegisterChanges:       []string{"write 120"},
			}, true
		}
		return nil, false
	})

	interval, err := BuildByteInterval(w, pin, 21601, correlator)
	if err != nil {
		t.Fatalf("BuildByteInterval(21601): %v", err)
	}

	if interval.PhysicalAddress != 0x7E1F05 {
		t.Errorf("PhysicalAddress = $%06X, want $7E1F05", interval.PhysicalAddress)
	}
	if interval.Value != 115 {
		t.Errorf("Value = %d, want 115", interval.Value)
	}
	if len(interval.Readers) != 2 {
		t.Fatalf("Readers count = %d, want 2", len(interval.Readers))
	}
	if interval.Readers[0].Event.ID != 38877 {
		t.Errorf("Reader 0 ID = %d, want 38877", interval.Readers[0].Event.ID)
	}
	if interval.Readers[1].Event.ID != 103698 {
		t.Errorf("Reader 1 ID = %d, want 103698", interval.Readers[1].Event.ID)
	}
	if interval.Replacement == nil || interval.Replacement.Event.ID != 103705 {
		t.Fatalf("Replacement = %+v, want Event 103705", interval.Replacement)
	}
	if interval.HostFrames.Start != 108 || interval.HostFrames.End != 110 {
		t.Errorf("HostFrames = %d..%d, want 108..110", interval.HostFrames.Start, interval.HostFrames.End)
	}
	if interval.PPUFrames.Start != 332 || interval.PPUFrames.End != 334 {
		t.Errorf("PPUFrames = %d..%d, want 332..334", interval.PPUFrames.Start, interval.PPUFrames.End)
	}
	if interval.CorrespondenceStatus != "complete" {
		t.Errorf("CorrespondenceStatus = %q, want complete", interval.CorrespondenceStatus)
	}
}
