package watches

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func makeSnapshot(runID string, seq, frame uint64, base uint32, data []byte) *Snapshot {
	h := sha256.Sum256(data)
	return &Snapshot{
		Format:        FileFormatSnapshot,
		SchemaVersion: 1,
		RunID:         runID,
		ROMSHA256:     "test-rom-sha",
		MemorySpace:   "wram",
		BaseOffset:    base,
		Length:        uint32(len(data)),
		Sequence:      seq,
		Frame:         frame,
		ContentSHA256: hex.EncodeToString(h[:]),
		Data:          data,
	}
}

func TestWatches_NumericDecodings(t *testing.T) {
	tests := []struct {
		name         string
		def          WatchDefinition
		data         []byte
		base         uint32
		wantRaw      uint32
		wantMasked   uint32
		wantNum      float64
		wantStr      string
		wantValidity ValidityState
	}{
		{
			name: "1-byte unsigned",
			def: WatchDefinition{
				ID:          "hp",
				Name:        "Hit Points",
				MemorySpace: "wram",
				Offset:      0x10,
				Width:       1,
			},
			base:         0x00,
			data:         append(make([]byte, 0x10), 0x2A), // 42 at 0x10
			wantRaw:      42,
			wantMasked:   42,
			wantNum:      42,
			wantStr:      "42",
			wantValidity: ValidityValid,
		},
		{
			name: "1-byte signed negative",
			def: WatchDefinition{
				ID:          "vel_y",
				Name:        "Y Velocity",
				MemorySpace: "wram",
				Offset:      0x05,
				Width:       1,
				Signed:      true,
			},
			base:         0x00,
			data:         append(make([]byte, 0x05), 0xFE), // -2 in 8-bit two's complement
			wantRaw:      0xFE,
			wantMasked:   0xFE,
			wantNum:      -2,
			wantStr:      "-2",
			wantValidity: ValidityValid,
		},
		{
			name: "2-byte little-endian signed with scale",
			def: WatchDefinition{
				ID:               "x_pos",
				Name:             "X Position",
				MemorySpace:      "wram",
				Offset:           0x02,
				Width:            2,
				Signed:           true,
				ScaleDenominator: int64Ptr(16),
				Unit:             "px",
			},
			base: 0x00,
			// 0xFFF0 = -16 in 16-bit. Scaled by 1/16 -> -1 px
			data:         append(make([]byte, 0x02), 0xF0, 0xFF),
			wantRaw:      0xFFF0,
			wantMasked:   0xFFF0,
			wantNum:      -1,
			wantStr:      "-1 px",
			wantValidity: ValidityValid,
		},
		{
			name: "2-byte big-endian unsigned",
			def: WatchDefinition{
				ID:          "be_word",
				Name:        "Big Endian Word",
				MemorySpace: "wram",
				Offset:      0x00,
				Width:       2,
				ByteOrder:   "big",
			},
			base:         0x00,
			data:         []byte{0x12, 0x34},
			wantRaw:      0x1234,
			wantMasked:   0x1234,
			wantNum:      0x1234,
			wantStr:      "4660",
			wantValidity: ValidityValid,
		},
		{
			name: "3-byte pointer unsigned",
			def: WatchDefinition{
				ID:          "ptr24",
				Name:        "Long Pointer",
				MemorySpace: "wram",
				Offset:      0x00,
				Width:       3,
			},
			base:         0x00,
			data:         []byte{0x78, 0x56, 0x34},
			wantRaw:      0x345678,
			wantMasked:   0x345678,
			wantNum:      0x345678,
			wantStr:      "3430008",
			wantValidity: ValidityValid,
		},
		{
			name: "mask and shift with enum mapping",
			def: WatchDefinition{
				ID:          "substate",
				Name:        "Sub-state",
				MemorySpace: "wram",
				Offset:      0x00,
				Width:       1,
				Mask:        uint32Ptr(0x0C), // bits 2,3
				Shift:       2,
				EnumLabels: map[string]string{
					"0": "Idle",
					"1": "Walking",
					"2": "Attacking",
					"3": "Dying",
				},
			},
			base:         0x00,
			data:         []byte{0x08}, // binary 0000 1000 -> masked 0x08 -> shifted >> 2 = 2
			wantRaw:      0x08,
			wantMasked:   2,
			wantNum:      2,
			wantStr:      "Attacking",
			wantValidity: ValidityValid,
		},
		{
			name: "unknown enum value falls back to raw number",
			def: WatchDefinition{
				ID:          "substate",
				Name:        "Sub-state",
				MemorySpace: "wram",
				Offset:      0x00,
				Width:       1,
				Mask:        uint32Ptr(0x0C),
				Shift:       2,
				EnumLabels: map[string]string{
					"0": "Idle",
				},
			},
			base:         0x00,
			data:         []byte{0x08}, // shifted = 2 (not in enum)
			wantRaw:      0x08,
			wantMasked:   2,
			wantNum:      2,
			wantStr:      "2",
			wantValidity: ValidityValid,
		},
		{
			name: "absent bytes from snapshot",
			def: WatchDefinition{
				ID:          "out_of_range",
				Name:        "Out of Range",
				MemorySpace: "wram",
				Offset:      0x2000,
				Width:       2,
			},
			base:         0x00,
			data:         []byte{0x01, 0x02}, // snapshot length 2, but offset 0x2000
			wantValidity: ValidityMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := makeSnapshot("run-1", 1, 0, tt.base, tt.data)
			eval := Evaluate(&tt.def, snap, nil)

			if eval.Validity != tt.wantValidity {
				t.Fatalf("validity: got %q, want %q (reason: %s)", eval.Validity, tt.wantValidity, eval.ValidityReason)
			}
			if tt.wantValidity == ValidityValid {
				if eval.RawValue != tt.wantRaw {
					t.Errorf("raw value: got 0x%X, want 0x%X", eval.RawValue, tt.wantRaw)
				}
				if eval.MaskedValue != tt.wantMasked {
					t.Errorf("masked value: got 0x%X, want 0x%X", eval.MaskedValue, tt.wantMasked)
				}
				if eval.DecodedNumber != tt.wantNum {
					t.Errorf("decoded number: got %v, want %v", eval.DecodedNumber, tt.wantNum)
				}
				if eval.DecodedString != tt.wantStr {
					t.Errorf("decoded string: got %q, want %q", eval.DecodedString, tt.wantStr)
				}
			}
		})
	}
}

func TestWatches_ConditionsAndCycles(t *testing.T) {
	t.Run("validity condition on another watch", func(t *testing.T) {
		wf := &File{
			Format:        FileFormatWatches,
			SchemaVersion: 1,
			ROMSHA256:     "test-rom-sha",
			Watches: []WatchDefinition{
				{
					ID:          "game_mode",
					Name:        "Game Mode",
					MemorySpace: "wram",
					Offset:      0x10,
					Width:       1,
				},
				{
					ID:          "dungeon_room",
					Name:        "Dungeon Room",
					MemorySpace: "wram",
					Offset:      0x20,
					Width:       2,
					Conditions: []Condition{
						{
							WatchID: "game_mode",
							Op:      "==",
							Value:   int64Ptr(7), // Mode 7 = Dungeon
						},
					},
				},
			},
		}

		// Snapshot where mode is 7 -> Dungeon Room is valid
		data := make([]byte, 0x30)
		data[0x10] = 7
		data[0x20] = 0x1A
		data[0x21] = 0x00
		snap1 := makeSnapshot("run-1", 1, 10, 0, data)

		evals, err := EvaluateAll(wf, snap1)
		if err != nil {
			t.Fatalf("EvaluateAll failed: %v", err)
		}
		if evals["dungeon_room"].Validity != ValidityValid {
			t.Errorf("expected dungeon_room valid, got %q (%s)",
				evals["dungeon_room"].Validity, evals["dungeon_room"].ValidityReason)
		}

		// Snapshot where mode is 5 -> Dungeon Room is invalid-in-context
		data[0x10] = 5
		snap2 := makeSnapshot("run-1", 2, 20, 0, data)
		evals2, err := EvaluateAll(wf, snap2)
		if err != nil {
			t.Fatalf("EvaluateAll failed: %v", err)
		}
		if evals2["dungeon_room"].Validity != ValidityInvalidInContext {
			t.Errorf("expected dungeon_room invalid-in-context, got %q", evals2["dungeon_room"].Validity)
		}
	})

	t.Run("missing condition input produces unknown-validity", func(t *testing.T) {
		wf := &File{
			Format:        FileFormatWatches,
			SchemaVersion: 1,
			ROMSHA256:     "test-rom-sha",
			Watches: []WatchDefinition{
				{
					ID:          "derived_val",
					Name:        "Derived",
					MemorySpace: "wram",
					Offset:      0x05,
					Width:       1,
					Conditions: []Condition{
						{
							WatchID: "nonexistent_watch",
							Op:      "==",
							Value:   int64Ptr(1),
						},
					},
				},
			},
		}
		snap := makeSnapshot("run-1", 1, 0, 0, make([]byte, 0x10))
		evals, err := EvaluateAll(wf, snap)
		if err != nil {
			t.Fatalf("EvaluateAll failed: %v", err)
		}
		if evals["derived_val"].Validity != ValidityUnknown {
			t.Errorf("expected unknown-validity, got %q", evals["derived_val"].Validity)
		}
	})

	t.Run("cyclic conditions rejected", func(t *testing.T) {
		wf := &File{
			Format:        FileFormatWatches,
			SchemaVersion: 1,
			Watches: []WatchDefinition{
				{
					ID:          "watch_a",
					Name:        "A",
					MemorySpace: "wram",
					Offset:      0x01,
					Width:       1,
					Conditions: []Condition{
						{WatchID: "watch_b", Op: "==", Value: int64Ptr(1)},
					},
				},
				{
					ID:          "watch_b",
					Name:        "B",
					MemorySpace: "wram",
					Offset:      0x02,
					Width:       1,
					Conditions: []Condition{
						{WatchID: "watch_a", Op: "==", Value: int64Ptr(1)},
					},
				},
			},
		}
		err := CheckCycles(wf.Watches)
		if err == nil {
			t.Fatalf("expected cycle error, got nil")
		}
		if !strings.Contains(err.Error(), "dependency cycle detected") {
			t.Errorf("expected cycle error message, got %v", err)
		}
	})

	t.Run("wrong ROM rejected", func(t *testing.T) {
		wf := &File{
			Format:        FileFormatWatches,
			SchemaVersion: 1,
			ROMSHA256:     "correct-sha",
			Watches: []WatchDefinition{
				{ID: "w1", Name: "W1", MemorySpace: "wram", Offset: 0, Width: 1},
			},
		}
		snap := makeSnapshot("run-1", 1, 0, 0, []byte{0})
		snap.ROMSHA256 = "different-sha"

		_, err := EvaluateAll(wf, snap)
		if err == nil {
			t.Fatalf("expected ROM mismatch error, got nil")
		}
		if !strings.Contains(err.Error(), "ROM hash mismatch") {
			t.Errorf("expected ROM hash mismatch error, got %v", err)
		}
	})
}

func TestWatches_SampledHistoryAndGaps(t *testing.T) {
	def := &WatchDefinition{
		ID:          "heart_quarters",
		Name:        "Hearts",
		MemorySpace: "wram",
		Offset:      0x10,
		Width:       1,
		ScaleDenominator: int64Ptr(4),
		Unit:        "hearts",
	}

	data1 := make([]byte, 0x20)
	data1[0x10] = 12 // 3 hearts
	snap1 := makeSnapshot("run-1", 1, 10, 0, data1)

	data2 := make([]byte, 0x20)
	data2[0x10] = 12 // still 12 at frame 20
	snap2 := makeSnapshot("run-1", 2, 20, 0, data2)

	data3 := make([]byte, 0x20)
	data3[0x10] = 10 // changed to 10 (2.5 hearts) at frame 30
	snap3 := makeSnapshot("run-1", 3, 30, 0, data3)

	// Gap: sequence jumps to 6 at frame 60
	data4 := make([]byte, 0x20)
	data4[0x10] = 10
	snap4 := makeSnapshot("run-1", 6, 60, 0, data4)

	snaps := []*Snapshot{snap1, snap2, snap3, snap4}

	history, err := BuildHistory(def, snaps, nil)
	if err != nil {
		t.Fatalf("BuildHistory failed: %v", err)
	}

	if len(history) != 4 {
		t.Fatalf("expected 4 history entries, got %d", len(history))
	}

	// Verify change detection
	if history[1].Changed {
		t.Errorf("entry 1 should not be changed (same value)")
	}
	if !history[2].Changed {
		t.Errorf("entry 2 should be changed (value 12 -> 10)")
	}

	// Verify gap detection
	if !history[3].Gap {
		t.Errorf("entry 3 should have gap flag set (seq 3 -> 6)")
	}

	// Verify change intervals
	changes := ExtractChanges(history)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].IntervalLabel != "[20, 30)" {
		t.Errorf("expected interval label [20, 30), got %s", changes[0].IntervalLabel)
	}

	// Verify compression
	compressed := CompressHistory(history)
	// snap1 and snap2 can be merged into [10, 30)
	// snap3 is [30, 60)
	// snap4 has a gap, so not merged
	if len(compressed) != 3 {
		t.Fatalf("expected 3 compressed entries, got %d", len(compressed))
	}
	if compressed[0].FrameStart != 10 || compressed[0].FrameEnd != 30 {
		t.Errorf("compressed[0] expected [10, 30), got [%d, %d)", compressed[0].FrameStart, compressed[0].FrameEnd)
	}

	// Verify duplicate imports are idempotent
	duplicateSnaps := []*Snapshot{snap1, snap1, snap2, snap2, snap3, snap4}
	dedupHistory, err := BuildHistory(def, duplicateSnaps, nil)
	if err != nil {
		t.Fatalf("BuildHistory with duplicates failed: %v", err)
	}
	if len(dedupHistory) != 4 {
		t.Fatalf("expected 4 deduped history entries, got %d", len(dedupHistory))
	}
}

func int64Ptr(v int64) *int64     { return &v }
func uint32Ptr(v uint32) *uint32 { return &v }
