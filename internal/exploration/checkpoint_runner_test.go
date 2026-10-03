package exploration

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cpu"
)

func TestNormalizePhysicalAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    uint32
		romSize int
		want    uint32
	}{
		{
			name:    "LoROM bank 00:8000 maps to physical offset 0",
			addr:    0x008000,
			romSize: 0x200000,
			want:    0,
		},
		{
			name:    "LoROM mirror bank 80:8000 maps to physical offset 0",
			addr:    0x808000,
			romSize: 0x200000,
			want:    0,
		},
		{
			name:    "LoROM bank 00:8000 without romSize limit",
			addr:    0x008000,
			romSize: 0,
			want:    0,
		},
		{
			name:    "LoROM mirror bank 80:8000 without romSize limit",
			addr:    0x808000,
			romSize: 0,
			want:    0,
		},
		{
			name:    "LoROM bank 00:8005",
			addr:    0x008005,
			romSize: 0x200000,
			want:    5,
		},
		{
			name:    "LoROM mirror bank 80:8005",
			addr:    0x808005,
			romSize: 0x200000,
			want:    5,
		},
		{
			name:    "LoROM bank 01:8000 (bank 1 start)",
			addr:    0x018000,
			romSize: 0x200000,
			want:    0x8000,
		},
		{
			name:    "LoROM mirror bank 81:8000",
			addr:    0x818000,
			romSize: 0x200000,
			want:    0x8000,
		},
		{
			name:    "LoROM bank 3F:FFFF (end of bank 63)",
			addr:    0x3FFFFF,
			romSize: 0x200000,
			want:    0x1FFFFF,
		},
		{
			name:    "LoROM mirror bank BF:FFFF",
			addr:    0xBFFFFF,
			romSize: 0x200000,
			want:    0x1FFFFF,
		},
		{
			name:    "LoROM wrapping with small 32KB ROM",
			addr:    0x018000,
			romSize: 0x8000,
			want:    0,
		},
		{
			name:    "LoROM linear bank 40:1234",
			addr:    0x401234,
			romSize: 0x200000,
			want:    0x1234,
		},
		{
			name:    "WRAM address passes through",
			addr:    0x7E0000,
			romSize: 0x200000,
			want:    0x7E0000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePhysicalAddress(tt.addr, tt.romSize)
			if got != tt.want {
				t.Fatalf("NormalizePhysicalAddress(%#x, %#x) = %#x, want %#x", tt.addr, tt.romSize, got, tt.want)
			}
		})
	}
}

func TestPhysicalStartVsContextVariantDeduplication(t *testing.T) {
	census := NewExplorationCensus()
	tracker := &CensusTracker{
		Census:        census,
		CheckpointID:  "cp-test",
		ScheduleIndex: 0,
		Schedule:      []uint16{0x0000},
		FrameOffset:   0,
		ROMSize:       0x200000,
	}

	// 1. Execute $00:8000 with M=0 (P=0x00).
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8000,
			P:  0x00, // M=0, X=0, C=0, E=0
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x008000, Value: 0xEA}}, // NOP
	})

	if got := len(census.PhysicalStarts); got != 1 {
		t.Fatalf("step 1: physical starts = %d, want 1", got)
	}
	if got := len(census.ContextVariants); got != 1 {
		t.Fatalf("step 1: context variants = %d, want 1", got)
	}
	if d, ok := census.PhysicalStarts[0]; !ok || d.Address != 0 {
		t.Fatalf("step 1: physical start at offset 0 missing or wrong address: %+v", d)
	}

	// 2. Execute $00:8000 with M=1 (P=0x20).
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8000,
			P:  0x20, // M=1
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x008000, Value: 0xEA}},
	})

	// Must deduplicate physical starts (still 1), but add a second context variant (now 2).
	if got := len(census.PhysicalStarts); got != 1 {
		t.Fatalf("step 2: physical starts = %d, want 1 (deduplicated)", got)
	}
	if got := len(census.ContextVariants); got != 2 {
		t.Fatalf("step 2: context variants = %d, want 2 (distinct M flag)", got)
	}

	// 3. Execute $80:8000 (mirrored bank) with M=0.
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x80,
			PC: 0x8000,
			P:  0x00, // M=0
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x808000, Value: 0xEA}},
	})

	// $80:8000 mirrors $00:8000 (physical offset 0), so neither physical starts
	// nor context variants should increase.
	if got := len(census.PhysicalStarts); got != 1 {
		t.Fatalf("step 3: physical starts = %d, want 1 (mirror deduplicated)", got)
	}
	if got := len(census.ContextVariants); got != 2 {
		t.Fatalf("step 3: context variants = %d, want 2 (mirror context deduplicated)", got)
	}

	// 4. Execute $80:8000 with M=1.
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x80,
			PC: 0x8000,
			P:  0x20, // M=1
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x808000, Value: 0xEA}},
	})
	if got := len(census.PhysicalStarts); got != 1 {
		t.Fatalf("step 4: physical starts = %d, want 1", got)
	}
	if got := len(census.ContextVariants); got != 2 {
		t.Fatalf("step 4: context variants = %d, want 2", got)
	}

	// 5. Execute $00:8000 with X=1, M=0 (P=0x10).
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8000,
			P:  0x10, // X=1
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x008000, Value: 0xEA}},
	})
	if got := len(census.PhysicalStarts); got != 1 {
		t.Fatalf("step 5: physical starts = %d, want 1", got)
	}
	if got := len(census.ContextVariants); got != 3 {
		t.Fatalf("step 5: context variants = %d, want 3", got)
	}

	// 6. Execute a different address $00:8005 with M=0.
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8005,
			P:  0x00,
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x008005, Value: 0xEA}},
	})
	if got := len(census.PhysicalStarts); got != 2 {
		t.Fatalf("step 6: physical starts = %d, want 2", got)
	}
	if got := len(census.ContextVariants); got != 4 {
		t.Fatalf("step 6: context variants = %d, want 4", got)
	}
}

func TestDiscoveryRecipeRetention(t *testing.T) {
	census := NewExplorationCensus()
	schedule := []uint16{0x0010, 0x0020, 0x0040, 0x0080}
	tracker := &CensusTracker{
		Census:        census,
		CheckpointID:  "cp-repro-1",
		ScheduleIndex: 2,
		Schedule:      schedule,
		FrameOffset:   2, // Discovery triggered on frame offset 2
		ROMSize:       0x200000,
	}

	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x9000,
			P:  0x31, // M=1, X=1, C=1
			E:  true,
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x009000, Value: 0xEA}},
	})

	physOffset := NormalizePhysicalAddress(0x009000, 0x200000)
	d, ok := census.PhysicalStarts[physOffset]
	if !ok {
		t.Fatalf("discovery not found at offset %#x", physOffset)
	}

	// Verify exact recipe reproduction fields.
	if d.CheckpointID != "cp-repro-1" {
		t.Errorf("CheckpointID = %q, want %q", d.CheckpointID, "cp-repro-1")
	}
	if d.ScheduleIndex != 2 {
		t.Errorf("ScheduleIndex = %d, want 2", d.ScheduleIndex)
	}
	if !reflect.DeepEqual(d.Schedule, schedule) {
		t.Errorf("Schedule = %v, want %v", d.Schedule, schedule)
	}
	if d.FrameOffset != 2 {
		t.Errorf("FrameOffset = %d, want 2", d.FrameOffset)
	}
	if d.Recipe.CheckpointID != "cp-repro-1" {
		t.Errorf("Recipe.CheckpointID = %q, want %q", d.Recipe.CheckpointID, "cp-repro-1")
	}
	wantInputs := []uint16{0x0010, 0x0020, 0x0040}
	if !reflect.DeepEqual(d.Recipe.Inputs, wantInputs) {
		t.Errorf("Recipe.Inputs = %v, want prefix %v", d.Recipe.Inputs, wantInputs)
	}

	// Check register flags in context.
	if !d.Context.E || !d.Context.M || !d.Context.X || !d.Context.C {
		t.Errorf("expected all EMXC flags set, got %+v", d.Context)
	}
}

func TestBranchDiscoveryDeduplication(t *testing.T) {
	census := NewExplorationCensus()
	tracker := &CensusTracker{
		Census:       census,
		CheckpointID: "cp-branch",
		Schedule:     []uint16{0x0000},
		ROMSize:      0x200000,
	}

	branchAddr := uint32(0x008020)
	normAddr := NormalizePhysicalAddress(branchAddr, 0x200000)

	// 1. BEQ ($F0) taken (Z flag = 1, P&0x02 != 0).
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: uint16(branchAddr & 0xFFFF),
			P:  0x02, // Z=1
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: branchAddr, Value: 0xF0}}, // BEQ
	})

	takenKey := BranchKey(normAddr, true)
	if !BranchTaken(takenKey) {
		t.Errorf("BranchTaken(%#x) = false, want true", takenKey)
	}
	if BranchAddress(takenKey) != normAddr {
		t.Errorf("BranchAddress(%#x) = %#x, want %#x", takenKey, BranchAddress(takenKey), normAddr)
	}
	if len(census.BranchesDiscovered) != 1 {
		t.Fatalf("branches discovered = %d, want 1", len(census.BranchesDiscovered))
	}
	if _, ok := census.BranchesDiscovered[takenKey]; !ok {
		t.Fatalf("expected branch key %#x in census", takenKey)
	}

	// 2. BEQ ($F0) taken again -> should deduplicate.
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: uint16(branchAddr & 0xFFFF),
			P:  0x02,
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: branchAddr, Value: 0xF0}},
	})
	if len(census.BranchesDiscovered) != 1 {
		t.Fatalf("branches discovered = %d, want 1 (deduplicated)", len(census.BranchesDiscovered))
	}

	// 3. BEQ ($F0) untaken (Z flag = 0, P&0x02 == 0).
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: uint16(branchAddr & 0xFFFF),
			P:  0x00, // Z=0
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: branchAddr, Value: 0xF0}},
	})
	untakenKey := BranchKey(normAddr, false)
	if BranchTaken(untakenKey) {
		t.Errorf("BranchTaken(%#x) = true, want false", untakenKey)
	}
	if len(census.BranchesDiscovered) != 2 {
		t.Fatalf("branches discovered = %d, want 2 (both taken and untaken witnessed)", len(census.BranchesDiscovered))
	}
	if _, ok := census.BranchesDiscovered[untakenKey]; !ok {
		t.Fatalf("expected untaken branch key %#x in census", untakenKey)
	}

	// 4. Non-branch opcode (NOP $EA) should not add to branches.
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8030,
			P:  0x00,
		},
		NumFetches: 1,
		Fetches:    [cpu.MaxFetches]cpu.Fetch{{Addr: 0x008030, Value: 0xEA}},
	})
	if len(census.BranchesDiscovered) != 2 {
		t.Fatalf("non-branch instruction added to branches: count = %d", len(census.BranchesDiscovered))
	}
}

func testSystem(t *testing.T) *snes.System {
	t.Helper()
	rom := make([]byte, 32768)
	rom[0x7fd5] = 0x20 // LoROM
	rom[0x7ffd] = 0x80 // Reset vector $8000
	// At $8000 (offset 0):
	// $80, $FE: BRA -2 (loop)
	copy(rom, []byte{0x80, 0xFE})

	s := snes.NewSystem(nil)
	if err := s.LoadROM(rom); err != nil {
		t.Fatalf("load rom: %v", err)
	}
	s.Power()
	return s
}

func TestRunCheckpointCampaign(t *testing.T) {
	s := testSystem(t)
	cp, err := NewCheckpoint("boot-cp", 0, "Initial boot checkpoint", s)
	if err != nil {
		t.Fatalf("new checkpoint: %v", err)
	}
	if cp.ID != "boot-cp" || len(cp.State) == 0 || cp.StateSHA256 == "" {
		t.Fatalf("checkpoint improperly initialized: %+v", cp)
	}

	schedules := [][]uint16{
		{0x0000, 0x0000, 0x0000},
		{0x0001, 0x0002, 0x0004},
	}

	census := NewExplorationCensus()
	ctx := context.Background()

	report, err := RunCheckpointCampaign(ctx, s, cp, schedules, census, 10)
	if err != nil {
		t.Fatalf("RunCheckpointCampaign failed: %v", err)
	}

	if report.CheckpointID != "boot-cp" {
		t.Errorf("report CheckpointID = %q, want %q", report.CheckpointID, "boot-cp")
	}
	if report.SchedulesRun != 2 {
		t.Errorf("report SchedulesRun = %d, want 2", report.SchedulesRun)
	}
	if report.TotalFrames != 6 {
		t.Errorf("report TotalFrames = %d, want 6", report.TotalFrames)
	}
	if report.TotalInstructions == 0 {
		t.Errorf("report TotalInstructions = 0, want > 0")
	}
	if report.NewPhysicalStarts == 0 {
		t.Errorf("report NewPhysicalStarts = 0, want > 0")
	}
	if report.Census != census {
		t.Errorf("report Census != input census")
	}

	// Verify discovery recipes were retained in the census.
	for addr, d := range census.PhysicalStarts {
		if d.CheckpointID != "boot-cp" {
			t.Errorf("discovery at addr %#x has CheckpointID %q, want %q", addr, d.CheckpointID, "boot-cp")
		}
		if d.Recipe.CheckpointID != "boot-cp" {
			t.Errorf("discovery recipe at addr %#x has CheckpointID %q, want %q", addr, d.Recipe.CheckpointID, "boot-cp")
		}
		if len(d.Recipe.Inputs) == 0 {
			t.Errorf("discovery recipe at addr %#x has empty inputs", addr)
		}

		// Replay recipe to verify reproducibility.
		if err := ReplayRecipe(s, cp, d.Recipe); err != nil {
			t.Errorf("replay recipe for discovery at addr %#x failed: %v", addr, err)
		}
	}
}

func TestRunCheckpointCampaignCancelled(t *testing.T) {
	s := testSystem(t)
	cp, err := NewCheckpoint("boot-cp", 0, "Initial boot checkpoint", s)
	if err != nil {
		t.Fatalf("new checkpoint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	schedules := [][]uint16{{0x0001, 0x0002}}
	census := NewExplorationCensus()

	_, err = RunCheckpointCampaign(ctx, s, cp, schedules, census, 10)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestRegisterFlags(t *testing.T) {
	tests := []struct {
		flags RegisterFlags
		b     uint8
	}{
		{RegisterFlags{}, 0},
		{RegisterFlags{E: true}, 8},
		{RegisterFlags{M: true}, 4},
		{RegisterFlags{X: true}, 2},
		{RegisterFlags{C: true}, 1},
		{RegisterFlags{E: true, M: true, X: true, C: true}, 15},
		{RegisterFlags{M: true, C: true}, 5},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("byte_%02x", tt.b), func(t *testing.T) {
			if got := tt.flags.Byte(); got != tt.b {
				t.Errorf("flags.Byte() = %d, want %d", got, tt.b)
			}
			roundtrip := FlagsFromByte(tt.b)
			if roundtrip != tt.flags {
				t.Errorf("FlagsFromByte(%d) = %+v, want %+v", tt.b, roundtrip, tt.flags)
			}
		})
	}
}

func TestProcessObservation_ExcludesRAMFromPhysicalStarts(t *testing.T) {
	census := NewExplorationCensus()
	tracker := &CensusTracker{
		Census:  census,
		ROMSize: 0x200000,
	}

	// Execution from WRAM $7E:2000
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x7E,
			PC: 0x2000,
		},
	})

	if len(census.PhysicalStarts) != 0 {
		t.Fatalf("expected 0 PhysicalStarts for RAM execution, got %d", len(census.PhysicalStarts))
	}

	// Execution from low RAM $00:1000
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x1000,
		},
	})

	if len(census.PhysicalStarts) != 0 {
		t.Fatalf("expected 0 PhysicalStarts for low RAM execution, got %d", len(census.PhysicalStarts))
	}

	// Execution from ROM $00:8000
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{
			PB: 0x00,
			PC: 0x8000,
		},
	})

	if len(census.PhysicalStarts) != 1 {
		t.Fatalf("expected 1 PhysicalStart for ROM execution, got %d", len(census.PhysicalStarts))
	}
}

func ExampleNormalizePhysicalAddress() {
	// LoROM mirrors $00:8000 and $80:8000 map to the same physical offset 0.
	offset1 := NormalizePhysicalAddress(0x008000, 0x200000)
	offset2 := NormalizePhysicalAddress(0x808000, 0x200000)
	fmt.Printf("%d %d\n", offset1, offset2)
	// Output:
	// 0 0
}

func ExampleExplorationCensus() {
	census := NewExplorationCensus()
	tracker := &CensusTracker{
		Census:       census,
		CheckpointID: "boot",
		Schedule:     []uint16{0x0000},
	}
	tracker.ProcessObservation(cpu.Observation{
		Entry: cpu.Snapshot{PC: 0x8000},
	})
	fmt.Printf("starts: %d\n", len(census.PhysicalStarts))
	// Output:
	// starts: 1
}
