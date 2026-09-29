package watches_test

import (
	"testing"

	"github.com/tmc/snes/internal/recovery/watches"
)

// TestFixture_SyntheticRoomHealthMode demonstrates a synthetic game state setup
// (mode, room, health, keys) with typed validity conditions, value scaling,
// enum mapping, and sampled change timelines without claiming correspondence to any real game.
func TestFixture_SyntheticRoomHealthMode(t *testing.T) {
	four := int64(4)
	modeDungeon := int64(2)

	wf := &watches.File{
		Format:        watches.FileFormatWatches,
		SchemaVersion: 1,
		ROMSHA256:     "synthetic-game-rom",
		Watches: []watches.WatchDefinition{
			{
				ID:          "game_mode",
				Name:        "Game Mode",
				Description: "Top-level game execution state",
				MemorySpace: "wram",
				Offset:      0x0010,
				Width:       1,
				EnumLabels: map[string]string{
					"0": "Title Screen",
					"1": "Overworld",
					"2": "Dungeon",
				},
				MeaningSource: "hypothesis",
			},
			{
				ID:          "room_id",
				Name:        "Dungeon Room ID",
				Description: "Active room index within dungeon, only valid in Dungeon mode",
				MemorySpace: "wram",
				Offset:      0x0020,
				Width:       2,
				Conditions: []watches.Condition{
					{
						WatchID: "game_mode",
						Op:      "==",
						Value:   &modeDungeon,
					},
				},
				MeaningSource: "hypothesis",
			},
			{
				ID:               "player_hp",
				Name:             "Player Health",
				Description:      "Player hit points measured in quarter hearts",
				MemorySpace:      "wram",
				Offset:           0x0030,
				Width:            1,
				ScaleDenominator: &four,
				Unit:             "hearts",
				MeaningSource:    "hypothesis",
			},
			{
				ID:          "dungeon_keys",
				Name:        "Small Keys",
				Description: "Small keys held in current dungeon",
				MemorySpace: "wram",
				Offset:      0x0040,
				Width:       1,
				Conditions: []watches.Condition{
					{
						WatchID: "game_mode",
						Op:      "==",
						Value:   &modeDungeon,
					},
				},
				MeaningSource: "hypothesis",
			},
		},
	}

	if err := wf.Watches[0].Validate(); err != nil {
		t.Fatalf("game_mode validate: %v", err)
	}
	if err := wf.Watches[1].Validate(); err != nil {
		t.Fatalf("room_id validate: %v", err)
	}

	// Create 4 chronological snapshots in Run A
	// Frame 10: Title screen (mode 0), room_id is invalid-in-context, HP is 12 (3 hearts)
	snap1Data := make([]byte, 0x100)
	snap1Data[0x10] = 0  // Title
	snap1Data[0x20] = 42 // room 42 in RAM, but invalid because not dungeon!
	snap1Data[0x30] = 12 // 3 hearts
	snap1 := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-A",
		ROMSHA256:     "synthetic-game-rom",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        uint32(len(snap1Data)),
		Sequence:      1,
		Frame:         10,
		Data:          snap1Data,
	}

	// Frame 30: Transition to Dungeon (mode 2), Room 0x05, HP 12, Keys 0
	snap2Data := make([]byte, 0x100)
	snap2Data[0x10] = 2    // Dungeon
	snap2Data[0x20] = 0x05 // Room 5
	snap2Data[0x30] = 12   // 3 hearts
	snap2Data[0x40] = 0    // 0 keys
	snap2 := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-A",
		ROMSHA256:     "synthetic-game-rom",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        uint32(len(snap2Data)),
		Sequence:      2,
		Frame:         30,
		Data:          snap2Data,
	}

	// Frame 50: Player takes damage (HP 12 -> 8, 2 hearts), collects 1 key (Keys 0 -> 1)
	snap3Data := make([]byte, 0x100)
	snap3Data[0x10] = 2    // Dungeon
	snap3Data[0x20] = 0x05 // Room 5
	snap3Data[0x30] = 8    // 2 hearts
	snap3Data[0x40] = 1    // 1 key
	snap3 := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-A",
		ROMSHA256:     "synthetic-game-rom",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        uint32(len(snap3Data)),
		Sequence:      3,
		Frame:         50,
		Data:          snap3Data,
	}

	// Frame 100: Room transition to Room 0x06 with gap in sequence (seq 3 -> 6)
	snap4Data := make([]byte, 0x100)
	snap4Data[0x10] = 2    // Dungeon
	snap4Data[0x20] = 0x06 // Room 6
	snap4Data[0x30] = 8    // 2 hearts
	snap4Data[0x40] = 1    // 1 key
	snap4 := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-A",
		ROMSHA256:     "synthetic-game-rom",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        uint32(len(snap4Data)),
		Sequence:      6, // Gap!
		Frame:         100,
		Data:          snap4Data,
	}

	// Separate Run B: Demonstrates run isolation
	snapBData := make([]byte, 0x100)
	snapBData[0x10] = 1 // Overworld
	snapB := &watches.Snapshot{
		Format:        watches.FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         "run-B",
		ROMSHA256:     "synthetic-game-rom",
		MemorySpace:   "wram",
		BaseOffset:    0,
		Length:        uint32(len(snapBData)),
		Sequence:      1,
		Frame:         15,
		Data:          snapBData,
	}

	allSnaps := []*watches.Snapshot{snap1, snap2, snap3, snap4, snapB}

	// 1. Verify Frame 10 evaluation
	evals1, err := watches.EvaluateAll(wf, snap1)
	if err != nil {
		t.Fatalf("evaluate snap1: %v", err)
	}
	if evals1["game_mode"].DecodedString != "Title Screen" {
		t.Errorf("expected Title Screen, got %q", evals1["game_mode"].DecodedString)
	}
	if evals1["room_id"].Validity != watches.ValidityInvalidInContext {
		t.Errorf("expected room_id invalid-in-context during Title Screen, got %q", evals1["room_id"].Validity)
	}
	if evals1["player_hp"].DecodedString != "3 hearts" {
		t.Errorf("expected '3 hearts', got %q", evals1["player_hp"].DecodedString)
	}

	// 2. Verify Frame 30 evaluation
	evals2, err := watches.EvaluateAll(wf, snap2)
	if err != nil {
		t.Fatalf("evaluate snap2: %v", err)
	}
	if evals2["game_mode"].DecodedString != "Dungeon" {
		t.Errorf("expected Dungeon, got %q", evals2["game_mode"].DecodedString)
	}
	if evals2["room_id"].Validity != watches.ValidityValid {
		t.Errorf("expected room_id valid in Dungeon mode, got %q", evals2["room_id"].Validity)
	}
	if evals2["room_id"].RawValue != 5 {
		t.Errorf("expected room_id 5, got %d", evals2["room_id"].RawValue)
	}

	// 3. Verify Health history in Run A
	hpDef := &wf.Watches[2]
	hpHistory, err := watches.BuildHistory(hpDef, allSnaps, wf)
	if err != nil {
		t.Fatalf("hp history failed: %v", err)
	}
	// Run isolation: run-A has 4 entries, run-B has 1 entry -> total 5
	if len(hpHistory) != 5 {
		t.Fatalf("expected 5 history entries across 2 runs, got %d", len(hpHistory))
	}

	// Check damage change interval in Run A
	changes := watches.ExtractChanges(hpHistory)
	var hpChange *watches.ChangeInterval
	for i := range changes {
		if changes[i].RunID == "run-A" {
			hpChange = &changes[i]
			break
		}
	}
	if hpChange == nil {
		t.Fatalf("expected observed health change in run-A")
	}
	if hpChange.IntervalLabel != "[30, 50)" {
		t.Errorf("expected damage interval [30, 50), got %s", hpChange.IntervalLabel)
	}
	if hpChange.PrevValue.DecodedString != "3 hearts" || hpChange.CurrValue.DecodedString != "2 hearts" {
		t.Errorf("expected 3 hearts -> 2 hearts, got %s -> %s",
			hpChange.PrevValue.DecodedString, hpChange.CurrValue.DecodedString)
	}

	// 4. Verify Gap detection at Frame 100
	var snap4Entry *watches.HistoryEntry
	for i := range hpHistory {
		if hpHistory[i].RunID == "run-A" && hpHistory[i].FrameStart == 100 {
			snap4Entry = &hpHistory[i]
			break
		}
	}
	if snap4Entry == nil || !snap4Entry.Gap {
		t.Errorf("expected gap flag set on sequence gap at frame 100")
	}
}
