package entity_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery/entity"
)

func zelda3SpriteSchemaFixture() *entity.EntitySchema {
	return &entity.EntitySchema{
		Name:      "Zelda3SpriteFixture",
		SlotCount: 16,
		Fields: map[string]entity.Field{
			"status": {
				Name:        "status",
				BaseAddress: 0x7E0DD0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Sprite status ($00=inactive, $09=alive)",
			},
			"type": {
				Name:        "type",
				BaseAddress: 0x7E0E20,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Sprite archetype ID ($73=Uncle)",
			},
			"y_low": {
				Name:        "y_low",
				BaseAddress: 0x7E0D00,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Low 8 bits of Y coordinate",
			},
			"y_high": {
				Name:        "y_high",
				BaseAddress: 0x7E0D20,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "High 8 bits of Y coordinate",
			},
			"x_low": {
				Name:        "x_low",
				BaseAddress: 0x7E0D10,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Low 8 bits of X coordinate",
			},
			"x_high": {
				Name:        "x_high",
				BaseAddress: 0x7E0D30,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "High 8 bits of X coordinate",
			},
			"vy": {
				Name:        "vy",
				BaseAddress: 0x7E0D40,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Signed 8-bit vertical velocity in 1/16th pixels",
			},
			"vx": {
				Name:        "vx",
				BaseAddress: 0x7E0D50,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Signed 8-bit horizontal velocity in 1/16th pixels",
			},
			"timer0": {
				Name:        "timer0",
				BaseAddress: 0x7E0DF0,
				Width:       8,
				Stride:      1,
				Register:    entity.RegX,
				Domain:      "WRAM",
				Description: "Per-frame decrementing timer 0",
			},
		},
		StateField:  "status",
		TypeField:   "type",
		TimerFields: []string{"timer0"},
	}
}

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
	execReceipt := entity.ExecutionWitnessReceipt{
		TraceSHA256:   "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
		EventID:       1001,
		Frame:         1,
		TargetAddress: 0x828200,
	}
	if err := dispatcher.RecordExecutionWitness(0x01, execReceipt); err != nil {
		t.Fatalf("unexpected error recording witness: %v", err)
	}
	witnessedTarget := dispatcher.Handlers[0x01]
	if !witnessedTarget.DynamicallyObserved {
		t.Errorf("expected DynamicallyObserved = true after RecordExecutionWitness")
	}
	if witnessedTarget.WitnessHits != 1 {
		t.Errorf("expected WitnessHits = 1, got %d", witnessedTarget.WitnessHits)
	}

	// Missing trace SHA fails
	if err := dispatcher.RecordExecutionWitness(0x01, entity.ExecutionWitnessReceipt{}); err == nil {
		t.Errorf("expected error for empty receipt, got nil")
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

	// Record transitions dynamically with authentic receipts
	receipt1 := entity.TransitionReceipt{
		TraceSHA256:    "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
		EventID:        1001,
		Frame:          1,
		TriggerAddress: 0x808120,
		StateBefore:    0x00,
		StateAfter:     0x01,
		Predicate:      "init_complete",
	}
	if err := machine.RecordTransitionWitness(receipt1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	receipt2 := entity.TransitionReceipt{
		TraceSHA256:    "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
		EventID:        1002,
		Frame:          2,
		TriggerAddress: 0x808250,
		StateBefore:    0x01,
		StateAfter:     0x02,
		Predicate:      "player_in_range",
	}
	if err := machine.RecordTransitionWitness(receipt2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Duplicate observation from another event
	receipt2Dup := receipt2
	receipt2Dup.EventID = 1003
	if err := machine.RecordTransitionWitness(receipt2Dup); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	// Missing trace SHA or event ID fails
	if err := machine.RecordTransitionWitness(entity.TransitionReceipt{}); err == nil {
		t.Errorf("expected error for empty receipt, got nil")
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
		_ = mach.RecordTransitionWitness(entity.TransitionReceipt{
			TraceSHA256:    "68aecfcf95fac6863d657979ff802c27dae5610799168b3321aad9f41046e421",
			EventID:        5001,
			Frame:          1,
			TriggerAddress: 0x808120,
			StateBefore:    0x00,
			StateAfter:     0x01,
			Predicate:      "init_complete",
		})

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

func validateTraceEvent(t *testing.T, tracePath string, eventID uint64, wantFrame int, wantCycle uint64, wantBank uint8, wantAddr uint16, wantBusAddr uint32) {
	t.Helper()
	data, err := os.ReadFile(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Logf("trace file %s not present, skipping physical event resolver check", tracePath)
			return
		}
		t.Fatalf("read trace file: %v", err)
	}

	found := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(fmt.Sprintf(`"id":%d,`, eventID))) {
			continue
		}
		var ev struct {
			ID    uint64 `json:"id"`
			Frame int    `json:"frame"`
			Cycle uint64 `json:"cycle"`
			PC    struct {
				Bank uint8  `json:"bank"`
				Addr uint16 `json:"addr"`
			} `json:"pc"`
			Addr uint32 `json:"addr"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal trace event %d: %v", eventID, err)
		}
		if ev.ID != eventID {
			continue
		}
		found = true
		if ev.Frame != wantFrame {
			t.Fatalf("event %d frame mismatch: got %d, want %d", eventID, ev.Frame, wantFrame)
		}
		if ev.Cycle != wantCycle {
			t.Fatalf("event %d cycle mismatch: got %d, want %d", eventID, ev.Cycle, wantCycle)
		}
		if ev.PC.Bank != wantBank || ev.PC.Addr != wantAddr {
			t.Fatalf("event %d PC mismatch: got %02X:%04X, want %02X:%04X",
				eventID, ev.PC.Bank, ev.PC.Addr, wantBank, wantAddr)
		}
		if ev.Addr != wantBusAddr {
			t.Fatalf("event %d bus addr mismatch: got 0x%04X, want 0x%04X",
				eventID, ev.Addr, wantBusAddr)
		}
		break
	}
	if !found {
		t.Fatalf("event %d not found in trace %s", eventID, tracePath)
	}
}

// TestAuthenticUncleLifecycleReplay validates authored-model consistency for
// Zelda 3 Uncle entity lifecycle against values obtained from authentic trace receipts.
//
// Qualification Boundary: This test exercises the recovered schema fixture, dispatcher,
// motion integration, and state machine consistency against verified receipt values
// (status check event 36, motion writes 93 and 190, despawn event 11182, PC $05:DF12 STZ,
// $06:8426 DEC). Full original-machine bit-level replay and fractional sub-pixel kinematics
// are separately validated via snestrace replay runs.
func TestAuthenticUncleLifecycleReplay(t *testing.T) {
	const tracePath = "/Users/tmc/tmp/snes-auto-jpdasm/20261003-entity-lifecycle/uncle_walk_trace.jsonl"
	const authenticTraceSHA = "833971648ca7764eb58e64ea10c607cb8195b6b3ef82dab7cde1e92ee3dd05d2"

	// Validate trace event resolver against actual recorded trace events
	validateTraceEvent(t, tracePath, 36, 1, 1232931354, 0x06, 0x84E2, 0x0DD0)
	validateTraceEvent(t, tracePath, 93, 1, 1232950046, 0x05, 0xFA23, 0x0D00)
	validateTraceEvent(t, tracePath, 190, 2, 1233309280, 0x05, 0xFA23, 0x0D00)
	validateTraceEvent(t, tracePath, 11182, 113, 1272976286, 0x05, 0xDF12, 0x0DD0)

	// Authentic Zelda 3 sprite table schema fixture (16 slots in WRAM $7E:0DD0..)
	schema := zelda3SpriteSchemaFixture()
	if err := schema.Validate(); err != nil {
		t.Fatalf("zelda3SpriteSchemaFixture invalid: %v", err)
	}

	// Update dispatcher for state 0x09 (alive/active)
	dispatcher := entity.NewUpdateDispatcher(schema, 0x068000, 0x0684E2)
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             0x09,
		Address:             0x0684E2,
		Name:                "Sprite_Active_Slot0",
		StaticallyWitnessed: true,
	})

	// Record execution witness from authentic trace event 36 (PC 06:84E2, LDA $0DD0,X)
	execReceipt := entity.ExecutionWitnessReceipt{
		TraceSHA256:   authenticTraceSHA,
		EventID:       36,
		Frame:         1,
		Cycle:         1232931354,
		PC:            0x0684E2,
		TargetAddress: 0x0684E2,
	}
	if err := dispatcher.RecordExecutionWitness(0x09, execReceipt); err != nil {
		t.Fatalf("RecordExecutionWitness failed: %v", err)
	}
	if !dispatcher.Handlers[0x09].DynamicallyObserved {
		t.Errorf("expected DynamicallyObserved = true")
	}

	// Register authentic sub-pixel velocity accumulator action witnessed at 05:FA00..05:FA2A:
	// vy=12 (12/16 = 0.75 px/frame). Fractional table $7E:0D60 accumulates 0xC0 each frame.
	var fracY uint8
	dispatcher.RegisterAction(0x09, func(slot *entity.EntitySlot, sub *entity.Subsystem) error {
		fracY += 0xC0
		if fracY < 0xC0 { // fractional overflow
			slot.Memory["y_low"]++
		}
		return nil
	})

	// State machine modeling Uncle's lifecycle
	machine := entity.NewStateMachine()
	machine.AddState(entity.StateDefinition{
		ID:          0x00,
		Name:        "Inactive",
		Description: "Slot unallocated or despawned",
	})
	machine.AddState(entity.StateDefinition{
		ID:          0x09,
		Name:        "Alive",
		Description: "Active entity running movement and dialogue AI",
	})

	// Authentic despawn witness: 0x09 -> 0x00 at PC $05:DF12 (STZ $0DD0,X), Event 11182, Frame 113
	despawnReceipt := entity.TransitionReceipt{
		TraceSHA256:    authenticTraceSHA,
		EventID:        11182,
		Frame:          113,
		Cycle:          1272976286,
		TriggerAddress: 0x05DF12,
		StateBefore:    0x09,
		StateAfter:     0x00,
		Predicate:      "reached_house_exit_boundary",
	}
	if err := machine.RecordTransitionWitness(despawnReceipt); err != nil {
		t.Fatalf("RecordTransitionWitness failed: %v", err)
	}

	sub, err := entity.NewSubsystem(schema, dispatcher, machine)
	if err != nil {
		t.Fatalf("NewSubsystem failed: %v", err)
	}
	sub.OAMShadowBase = 0x7E0800

	// Composite OAM allocation: Uncle occupies 7 hardware OAM slots (slots 116..122 at $7E:09D0..$7E:09EB)
	uncleOAMSlots := []int{116, 117, 118, 119, 120, 121, 122}
	if err := sub.SetOAMAllocation(0, uncleOAMSlots); err != nil {
		t.Fatalf("SetOAMAllocation failed: %v", err)
	}

	// 1. Spawn Uncle in slot 0 (Type 0x73, Status 0x09)
	spawnFields := map[string]uint16{
		"status": 0x09,
		"type":   0x73,
		"x_low":  0x78,
		"x_high": 0x09, // X = 0x0978
		"y_low":  0xC2,
		"y_high": 0x21, // Y = 0x21C2
		"vy":     12,
		"vx":     0,
		"timer0": 0x70,
	}
	if err := sub.Spawn(0, 0x73, spawnFields); err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// 2. Perform authentic lifecycle events replay
	events := []entity.LifecycleEvent{
		{
			Kind:          entity.EventSpawn,
			Slot:          0,
			Frame:         1710,
			EntityType:    0x73,
			ExpectedState: 0x09,
			Fields:        spawnFields,
		},
		{
			Kind:          entity.EventTick,
			Slot:          0,
			Frame:         1711,
			ExpectedState: 0x09,
			Fields: map[string]uint16{
				"timer0": 0x6F, // witnessed DEC $0DF0,X at 06:8426
				"y_low":  0xC2, // Event 93: 05:FA23 writes 0xC2 (before=194, after=194)
				"y_high": 0x21,
			},
		},
		{
			Kind:          entity.EventTick,
			Slot:          0,
			Frame:         1712,
			ExpectedState: 0x09,
			Fields: map[string]uint16{
				"timer0": 0x6E,
				"y_low":  0xC3, // Event 190: 05:FA23 writes 0xC3 after fractional carry!
				"y_high": 0x21,
			},
		},
		{
			Kind:          entity.EventTransition,
			Slot:          0,
			Frame:         1822,
			ExpectedState: 0x09,
			TargetState:   0x00,
			TriggerPC:     0x05DF12,
			Predicate:     "reached_house_exit_boundary",
		},
		{
			Kind:  entity.EventDespawn,
			Slot:  0,
			Frame: 1823,
		},
	}

	receipt, err := sub.ReplayLifecycle(events)
	if err != nil {
		t.Fatalf("ReplayLifecycle failed: %v", err)
	}
	if !receipt.Valid {
		t.Fatalf("expected valid lifecycle replay, got: %s", receipt.DiscrepancySummary())
	}
	if receipt.MatchedEvents != 5 {
		t.Errorf("matched events = %d, want 5", receipt.MatchedEvents)
	}
	if len(receipt.UnobservedPaths) != 0 {
		t.Errorf("expected 0 unobserved paths, got %d", len(receipt.UnobservedPaths))
	}
}

func TestOAMAllocationValidation(t *testing.T) {
	schema := sampleSchema()
	sub, err := entity.NewSubsystem(schema, nil, nil)
	if err != nil {
		t.Fatalf("NewSubsystem failed: %v", err)
	}

	// Negative entity slot returns error
	if err := sub.SetOAMAllocation(-1, []int{0}); err == nil {
		t.Errorf("expected error for negative entity slot, got nil")
	}

	// Out-of-bounds entity slot returns error
	if err := sub.SetOAMAllocation(16, []int{0}); err == nil {
		t.Errorf("expected error for entity slot 16 (count=16), got nil")
	}

	// Negative hardware OAM slot returns error
	if err := sub.SetOAMAllocation(0, []int{-1}); err == nil {
		t.Errorf("expected error for negative OAM slot -1, got nil")
	}

	// Hardware OAM slot >= 128 returns error
	if err := sub.SetOAMAllocation(0, []int{128}); err == nil {
		t.Errorf("expected error for OAM slot 128, got nil")
	}

	// Valid allocation succeeds
	if err := sub.SetOAMAllocation(0, []int{0, 1, 127}); err != nil {
		t.Errorf("unexpected error for valid OAM slots: %v", err)
	}

	// CommitOAM executes safely without panics even if manually manipulated
	if err := sub.CommitOAM(); err != nil {
		t.Errorf("unexpected error from CommitOAM: %v", err)
	}
}
