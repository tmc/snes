package entity_test

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/entity"
)

func sampleSchema() *entity.EntitySchema {
	return &entity.EntitySchema{
		Name:      "StandardSprite",
		SlotCount: 16,
		Fields: map[string]entity.Field{
			"state": {
				Name:        "state",
				BaseAddress: 0x7E0D80,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Sprite primary state machine selector",
			},
			"type": {
				Name:        "type",
				BaseAddress: 0x7E0E20,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Sprite archetype ID",
			},
			"timer": {
				Name:        "timer",
				BaseAddress: 0x7E0EE0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "State duration decrementing timer",
			},
			"x": {
				Name:        "x",
				BaseAddress: 0x7E0F00,
				Width:       16,
				Stride:      2,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "16-bit horizontal coordinate",
			},
			"y": {
				Name:        "y",
				BaseAddress: 0x7E0F40,
				Width:       16,
				Stride:      2,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "16-bit vertical coordinate",
			},
			"vx": {
				Name:        "vx",
				BaseAddress: 0x7E0F80,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Horizontal velocity",
			},
			"vy": {
				Name:        "vy",
				BaseAddress: 0x7E0FA0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Vertical velocity",
			},
			"tile": {
				Name:        "tile",
				BaseAddress: 0x7E0FC0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Starting OAM tile number",
			},
			"attr": {
				Name:        "attr",
				BaseAddress: 0x7E0FE0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "OAM attribute bits",
			},
			"size": {
				Name:        "size",
				BaseAddress: 0x7E1000,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "OAM size selection bit",
			},
		},
		StateField:  "state",
		TypeField:   "type",
		TimerFields: []string{"timer"},
	}
}

func TestSoAFieldAddressing(t *testing.T) {
	tests := []struct {
		name        string
		field       entity.Field
		slot        int
		wantAddress uint32
	}{
		{
			name: "state slot 0",
			field: entity.Field{
				BaseAddress: 0x7E0D80,
				Stride:      1,
			},
			slot:        0,
			wantAddress: 0x7E0D80,
		},
		{
			name: "state slot 7",
			field: entity.Field{
				BaseAddress: 0x7E0D80,
				Stride:      1,
			},
			slot:        7,
			wantAddress: 0x7E0D87,
		},
		{
			name: "state slot 15",
			field: entity.Field{
				BaseAddress: 0x7E0D80,
				Stride:      1,
			},
			slot:        15,
			wantAddress: 0x7E0D8F,
		},
		{
			name: "16-bit x coord slot 0 (stride 2)",
			field: entity.Field{
				BaseAddress: 0x7E0F00,
				Stride:      2,
			},
			slot:        0,
			wantAddress: 0x7E0F00,
		},
		{
			name: "16-bit x coord slot 5 (stride 2)",
			field: entity.Field{
				BaseAddress: 0x7E0F00,
				Stride:      2,
			},
			slot:        5,
			wantAddress: 0x7E0F0A,
		},
		{
			name: "16-bit y coord slot 15 (stride 2)",
			field: entity.Field{
				BaseAddress: 0x7E0F40,
				Stride:      2,
			},
			slot:        15,
			wantAddress: 0x7E0F5E,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := entity.SlotAddress(tc.field, tc.slot)
			if got != tc.wantAddress {
				t.Errorf("SlotAddress(%+v, %d) = 0x%06X, want 0x%06X", tc.field, tc.slot, got, tc.wantAddress)
			}
		})
	}

	// Test schema lookup and bounds checks.
	schema := sampleSchema()
	addr, err := schema.SlotAddress("x", 4)
	if err != nil {
		t.Fatalf("unexpected error looking up 'x' slot 4: %v", err)
	}
	if addr != 0x7E0F08 {
		t.Errorf("schema.SlotAddress('x', 4) = 0x%06X, want 0x7E0F08", addr)
	}

	// Bounds checks:
	if _, err := schema.SlotAddress("x", -1); err == nil {
		t.Errorf("expected error for slot -1, got nil")
	}
	if _, err := schema.SlotAddress("x", 16); err == nil {
		t.Errorf("expected error for slot 16 (out of bounds), got nil")
	}
	if _, err := schema.SlotAddress("nonexistent", 0); err == nil {
		t.Errorf("expected error for nonexistent field, got nil")
	}

	// Schema validation tests:
	invalidSchema := sampleSchema()
	invalidSchema.SlotCount = 0
	if err := invalidSchema.Validate(); err == nil {
		t.Errorf("expected validation error for SlotCount=0")
	}

	invalidSchema = sampleSchema()
	invalidSchema.StateField = "missing_field"
	if err := invalidSchema.Validate(); err == nil {
		t.Errorf("expected validation error for missing state field")
	}
}

func TestUpdateDispatcher(t *testing.T) {
	schema := sampleSchema()
	dispatcher := entity.NewUpdateDispatcher(schema, 0x828000, 0x828500)

	// Register known handlers
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             0x00,
		Address:             0x828100,
		Name:                "StateInit",
		StaticallyWitnessed: true,
	})
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             0x01,
		Address:             0x828200,
		Name:                "StatePatrol",
		StaticallyWitnessed: true,
	})
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             0x02,
		Address:             0x828300,
		Name:                "StateAttack",
		StaticallyWitnessed: true,
	})

	tests := []struct {
		name        string
		state       uint8
		wantAddress uint32
		wantName    string
		wantWitness bool
		wantDynamic bool
		wantErr     bool
	}{
		{
			name:        "state 0 dispatch",
			state:       0x00,
			wantAddress: 0x828100,
			wantName:    "StateInit",
			wantWitness: true,
			wantDynamic: false, // Simulation dispatch does NOT grant DynamicallyObserved authority
		},
		{
			name:        "state 1 dispatch",
			state:       0x01,
			wantAddress: 0x828200,
			wantName:    "StatePatrol",
			wantWitness: true,
			wantDynamic: false,
		},
		{
			name:        "unobserved state falls back to default handler",
			state:       0x05,
			wantAddress: 0x828500,
			wantName:    "default",
			wantWitness: false,
			wantDynamic: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, err := dispatcher.Dispatch(tc.state)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected dispatch error: %v", err)
			}
			if target.Address != tc.wantAddress {
				t.Errorf("target.Address = 0x%06X, want 0x%06X", target.Address, tc.wantAddress)
			}
			if target.Name != tc.wantName {
				t.Errorf("target.Name = %q, want %q", target.Name, tc.wantName)
			}
			if target.StaticallyWitnessed != tc.wantWitness {
				t.Errorf("target.StaticallyWitnessed = %v, want %v", target.StaticallyWitnessed, tc.wantWitness)
			}
			if target.DynamicallyObserved != tc.wantDynamic {
				t.Errorf("target.DynamicallyObserved = %v, want %v", target.DynamicallyObserved, tc.wantDynamic)
			}
		})
	}

	// Verify hit counter increment on repeated dispatch
	t1, _ := dispatcher.Dispatch(0x01)
	if t1.SimulationHits != 2 {
		t.Errorf("expected 2 simulation hits for state 0x01, got %d", t1.SimulationHits)
	}

	// Test recording an authentic dynamic execution witness
	if err := dispatcher.RecordExecutionWitness(0x01); err != nil {
		t.Fatalf("unexpected error recording witness: %v", err)
	}
	witnessedTarget := dispatcher.Handlers[0x01]
	if !witnessedTarget.DynamicallyObserved {
		t.Errorf("expected DynamicallyObserved = true after RecordExecutionWitness")
	}
	if witnessedTarget.WitnessHits != 1 {
		t.Errorf("expected WitnessHits = 1, got %d", witnessedTarget.WitnessHits)
	}

	// Test dispatcher without default handler fails for unknown state
	strictDispatcher := entity.NewUpdateDispatcher(schema, 0x828000, 0)
	if _, err := strictDispatcher.Dispatch(0xFF); err == nil {
		t.Errorf("expected error for unhandled state 0xFF with no default handler")
	}
}

func TestStateMachineWitnesses(t *testing.T) {
	machine := entity.NewStateMachine()
	machine.AddState(entity.StateDefinition{
		ID:          0x00,
		Name:        "Init",
		Description: "Entity is being initialized",
	})
	machine.AddState(entity.StateDefinition{
		ID:          0x01,
		Name:        "Patrol",
		Description: "Entity moves back and forth",
	})
	machine.AddState(entity.StateDefinition{
		ID:          0x02,
		Name:        "Attack",
		Description: "Entity charges towards player",
	})

	// Record transitions dynamically
	machine.RecordTransition(0x00, 0x01, 0x808120, "init_complete")
	machine.RecordTransition(0x01, 0x02, 0x808250, "player_in_range")
	machine.RecordTransition(0x01, 0x02, 0x808250, "player_in_range") // duplicate observation

	transitions := machine.FindTransitions(0x01, 0x02)
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition edge between 0x01 and 0x02, got %d", len(transitions))
	}
	tr := transitions[0]
	if tr.WitnessCount != 2 {
		t.Errorf("tr.WitnessCount = %d, want 2", tr.WitnessCount)
	}
	if !tr.Observed {
		t.Errorf("tr.Observed = false, want true")
	}
	if tr.Predicate != "player_in_range" {
		t.Errorf("tr.Predicate = %q, want 'player_in_range'", tr.Predicate)
	}
	if tr.TriggerAddress != 0x808250 {
		t.Errorf("tr.TriggerAddress = 0x%06X, want 0x808250", tr.TriggerAddress)
	}
}

func TestFullLifecycle(t *testing.T) {
	schema := sampleSchema()
	dispatcher := entity.NewUpdateDispatcher(schema, 0x828000, 0x828500)
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID: 0x00, Address: 0x828100, Name: "Init", StaticallyWitnessed: true,
	})
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID: 0x01, Address: 0x828200, Name: "Move", StaticallyWitnessed: true,
	})
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID: 0x02, Address: 0x828300, Name: "DespawnState", StaticallyWitnessed: true,
	})

	machine := entity.NewStateMachine()
	sub, err := entity.NewSubsystem(schema, dispatcher, machine)
	if err != nil {
		t.Fatalf("NewSubsystem failed: %v", err)
	}

	// 1. Spawn slot 2
	err = sub.Spawn(2, 0x0A, map[string]uint16{
		"state": 0x00,
		"timer": 2,
		"x":     100,
		"y":     50,
		"vx":    2,
		"vy":    0,
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	if !sub.Slots[2].Active {
		t.Fatalf("slot 2 should be active")
	}
	if sub.Slots[2].State != 0x00 {
		t.Errorf("slot 2 state = 0x%02X, want 0x00", sub.Slots[2].State)
	}

	// 2. Tick 1: timer decrements 2 -> 1
	if err := sub.Tick(2); err != nil {
		t.Fatalf("Tick 1 failed: %v", err)
	}
	if sub.Slots[2].Memory["timer"] != 1 {
		t.Errorf("timer = %d, want 1", sub.Slots[2].Memory["timer"])
	}
	if sub.Slots[2].Memory["x"] != 102 {
		t.Errorf("x = %d, want 102", sub.Slots[2].Memory["x"])
	}

	// 3. Tick 2: timer decrements 1 -> 0
	if err := sub.Tick(2); err != nil {
		t.Fatalf("Tick 2 failed: %v", err)
	}
	if sub.Slots[2].Memory["timer"] != 0 {
		t.Errorf("timer = %d, want 0", sub.Slots[2].Memory["timer"])
	}

	// 4. State transition when timer reaches 0
	if err := sub.TransitionState(2, 0x01, 0x808150, "timer == 0"); err != nil {
		t.Fatalf("TransitionState failed: %v", err)
	}
	if sub.Slots[2].State != 0x01 {
		t.Errorf("state after transition = 0x%02X, want 0x01", sub.Slots[2].State)
	}

	// 5. Tick in state 0x01: moves further
	if err := sub.Tick(2); err != nil {
		t.Fatalf("Tick 3 failed: %v", err)
	}
	if sub.Slots[2].Memory["x"] != 106 {
		t.Errorf("x = %d, want 106", sub.Slots[2].Memory["x"])
	}

	// 6. Despawn slot 2
	if err := sub.Despawn(2); err != nil {
		t.Fatalf("Despawn failed: %v", err)
	}
	if sub.Slots[2].Active {
		t.Errorf("slot 2 should be inactive after despawn")
	}

	// Ticking inactive slot should fail
	if err := sub.Tick(2); err == nil {
		t.Errorf("expected error ticking inactive slot 2")
	}
}

func TestReplayLifecycleValidation(t *testing.T) {
	setupSubsystem := func() *entity.Subsystem {
		schema := sampleSchema()
		disp := entity.NewUpdateDispatcher(schema, 0x808000, 0x808000)
		disp.RegisterHandler(entity.HandlerTarget{StateID: 0x00, Address: 0x808100, Name: "Init"})
		disp.RegisterHandler(entity.HandlerTarget{StateID: 0x01, Address: 0x808200, Name: "Walk"})
		disp.RegisterHandler(entity.HandlerTarget{StateID: 0x02, Address: 0x808300, Name: "Die"})

		mach := entity.NewStateMachine()
		mach.RecordTransition(0x00, 0x01, 0x808120, "init_complete")

		sub, _ := entity.NewSubsystem(schema, disp, mach)
		return sub
	}

	t.Run("valid lifecycle replay", func(t *testing.T) {
		sub := setupSubsystem()
		events := []entity.LifecycleEvent{
			{
				Kind:          entity.EventSpawn,
				Slot:          1,
				Frame:         1,
				EntityType:    0x04,
				ExpectedState: 0x00,
				Fields: map[string]uint16{
					"state": 0x00,
					"timer": 1,
					"x":     50,
					"vx":    1,
				},
			},
			{
				Kind:          entity.EventTick,
				Slot:          1,
				Frame:         2,
				ExpectedState: 0x00,
				Fields: map[string]uint16{
					"timer": 0,
					"x":     51,
				},
			},
			{
				Kind:          entity.EventTransition,
				Slot:          1,
				Frame:         2,
				ExpectedState: 0x00,
				TargetState:   0x01,
				TriggerPC:     0x808120,
				Predicate:     "init_complete",
			},
			{
				Kind:          entity.EventTick,
				Slot:          1,
				Frame:         3,
				ExpectedState: 0x01,
				Fields: map[string]uint16{
					"x": 52,
				},
			},
			{
				Kind:  entity.EventDespawn,
				Slot:  1,
				Frame: 4,
			},
		}

		receipt, err := sub.ReplayLifecycle(events)
		if err != nil {
			t.Fatalf("unexpected error during replay: %v", err)
		}
		if !receipt.Valid {
			t.Errorf("expected valid receipt, got invalid: %s", receipt.DiscrepancySummary())
		}
		if receipt.MatchedEvents != 5 {
			t.Errorf("matched events = %d, want 5", receipt.MatchedEvents)
		}
		if len(receipt.UnobservedPaths) != 0 {
			t.Errorf("expected 0 unobserved paths, got %d", len(receipt.UnobservedPaths))
		}
	})

	t.Run("illegal state jump detected", func(t *testing.T) {
		sub := setupSubsystem()
		events := []entity.LifecycleEvent{
			{
				Kind:          entity.EventSpawn,
				Slot:          1,
				Frame:         1,
				EntityType:    0x04,
				ExpectedState: 0x00,
				Fields: map[string]uint16{
					"state": 0x00,
				},
			},
			{
				Kind:          entity.EventTransition,
				Slot:          1,
				Frame:         2,
				ExpectedState: 0x02, // Expecting 0x02, but slot is in 0x00!
				TargetState:   0x01,
				TriggerPC:     0x808999,
				Predicate:     "illegal_jump",
			},
		}

		receipt, err := sub.ReplayLifecycle(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Valid {
			t.Fatalf("expected replay to be invalid due to illegal state jump")
		}
		if len(receipt.StateDiscrepancies) == 0 {
			t.Fatalf("expected state discrepancies, got none")
		}
		disc := receipt.StateDiscrepancies[0]
		if !strings.Contains(disc.Reason, "illegal state jump") {
			t.Errorf("expected reason to contain 'illegal state jump', got %q", disc.Reason)
		}
	})

	t.Run("field mismatch detected", func(t *testing.T) {
		sub := setupSubsystem()
		events := []entity.LifecycleEvent{
			{
				Kind:          entity.EventSpawn,
				Slot:          1,
				Frame:         1,
				EntityType:    0x04,
				ExpectedState: 0x00,
				Fields: map[string]uint16{
					"state": 0x00,
					"x":     100,
					"vx":    2,
				},
			},
			{
				Kind:          entity.EventTick,
				Slot:          1,
				Frame:         2,
				ExpectedState: 0x00,
				Fields: map[string]uint16{
					"x": 999, // Actual after tick is 102!
				},
			},
		}

		receipt, err := sub.ReplayLifecycle(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Valid {
			t.Fatalf("expected replay to fail due to field mismatch")
		}
		if len(receipt.Mismatches) != 1 {
			t.Fatalf("expected 1 mismatch, got %d", len(receipt.Mismatches))
		}
		mm := receipt.Mismatches[0]
		if mm.Field != "x" || mm.Expected != 999 || mm.Actual != 102 {
			t.Errorf("mismatch details incorrect: %+v", mm)
		}
	})

	t.Run("unobserved path detected", func(t *testing.T) {
		sub := setupSubsystem()
		events := []entity.LifecycleEvent{
			{
				Kind:          entity.EventSpawn,
				Slot:          1,
				Frame:         1,
				EntityType:    0x04,
				ExpectedState: 0x00,
			},
			{
				Kind:          entity.EventTransition,
				Slot:          1,
				Frame:         2,
				ExpectedState: 0x00,
				TargetState:   0x02, // 0x00 -> 0x02 was never observed in setupSubsystem!
				TriggerPC:     0x808555,
				Predicate:     "unseen_trigger",
			},
		}

		receipt, err := sub.ReplayLifecycle(events)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(receipt.UnobservedPaths) != 1 {
			t.Fatalf("expected 1 unobserved path, got %d", len(receipt.UnobservedPaths))
		}
		up := receipt.UnobservedPaths[0]
		if up.FromState != 0x00 || up.ToState != 0x02 || up.TriggerAddress != 0x808555 {
			t.Errorf("unobserved path details incorrect: %+v", up)
		}
	})
}

func TestPhaseBoundaries(t *testing.T) {
	schema := sampleSchema()
	sub, err := entity.NewSubsystem(schema, nil, nil)
	if err != nil {
		t.Fatalf("NewSubsystem failed: %v", err)
	}

	// Spawn slot 0 with sprite coordinates and attributes
	err = sub.Spawn(0, 1, map[string]uint16{
		"x":    0x120, // 288 (needs high table X bit set)
		"y":    0x40,  // 64
		"tile": 0x1A,
		"attr": 0x31,
		"size": 1, // large sprite
	})
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// 1. Initial phase is PhaseLogicUpdate
	if sub.MainLoopPhase != entity.PhaseLogicUpdate {
		t.Errorf("initial phase = %v, want PhaseLogicUpdate", sub.MainLoopPhase)
	}

	// 2. Advance to PhaseOAMBufferCommit
	p, err := sub.AdvancePhase()
	if err != nil || p != entity.PhaseOAMBufferCommit {
		t.Fatalf("AdvancePhase to OAMBufferCommit failed: %v, got %v", err, p)
	}

	// Inspect shadow OAM buffer
	// Low table entry 0:
	if sub.OAMBuffer[0] != 0x20 { // X low byte = 0x20
		t.Errorf("OAMBuffer[0] = 0x%02X, want 0x20", sub.OAMBuffer[0])
	}
	if sub.OAMBuffer[1] != 0x40 { // Y = 0x40
		t.Errorf("OAMBuffer[1] = 0x%02X, want 0x40", sub.OAMBuffer[1])
	}
	if sub.OAMBuffer[2] != 0x1A { // Tile = 0x1A
		t.Errorf("OAMBuffer[2] = 0x%02X, want 0x1A", sub.OAMBuffer[2])
	}
	if sub.OAMBuffer[3] != 0x31 { // Attr = 0x31
		t.Errorf("OAMBuffer[3] = 0x%02X, want 0x31", sub.OAMBuffer[3])
	}
	// High table entry for slot 0: offset 512, bits: xBit(1) | (sizeBit(1)<<1) = 3
	if sub.OAMBuffer[512] != 0x03 {
		t.Errorf("OAMBuffer[512] = 0x%02X, want 0x03", sub.OAMBuffer[512])
	}
	// Inactive slots should be positioned off-screen (Y=0xE0 / 224)
	if sub.OAMBuffer[1*4+1] != 0xE0 {
		t.Errorf("inactive slot 1 Y = 0x%02X, want 0xE0", sub.OAMBuffer[1*4+1])
	}

	// 3. Advance to PhaseVBlankDMA
	p, err = sub.AdvancePhase()
	if err != nil || p != entity.PhaseVBlankDMA {
		t.Fatalf("AdvancePhase to VBlankDMA failed: %v, got %v", err, p)
	}
	// VRAMOAM should now match shadow OAM buffer
	if sub.VRAMOAM != sub.OAMBuffer {
		t.Errorf("VRAMOAM does not match OAMBuffer after V-Blank DMA")
	}

	// 4. Advance back to PhaseLogicUpdate for next frame
	p, err = sub.AdvancePhase()
	if err != nil || p != entity.PhaseLogicUpdate {
		t.Fatalf("AdvancePhase back to LogicUpdate failed: %v, got %v", err, p)
	}
	if sub.FrameCount != 1 {
		t.Errorf("FrameCount = %d, want 1", sub.FrameCount)
	}
}
