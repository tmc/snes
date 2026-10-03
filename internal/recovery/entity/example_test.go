package entity_test

import (
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery/entity"
)

func ExampleSubsystem() {
	schema := &entity.EntitySchema{
		Name:      "Koopa",
		SlotCount: 8,
		Fields: map[string]entity.Field{
			"state": {Name: "state", BaseAddress: 0x7E0D80, Width: 8, Stride: 1, Register: entity.RegX},
			"type":  {Name: "type", BaseAddress: 0x7E0E20, Width: 8, Stride: 1, Register: entity.RegX},
			"timer": {Name: "timer", BaseAddress: 0x7E0EE0, Width: 8, Stride: 1, Register: entity.RegX},
			"x":     {Name: "x", BaseAddress: 0x7E0F00, Width: 16, Stride: 2, Register: entity.RegX},
			"vx":    {Name: "vx", BaseAddress: 0x7E0F80, Width: 8, Stride: 1, Register: entity.RegX},
		},
		StateField:  "state",
		TypeField:   "type",
		TimerFields: []string{"timer"},
	}

	dispatcher := entity.NewUpdateDispatcher(schema, 0x828000, 0x828500)
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             0,
		Address:             0x828100,
		Name:                "Patrol",
		StaticallyWitnessed: true,
	})
	dispatcher.RegisterHandler(entity.HandlerTarget{
		StateID:             1,
		Address:             0x828200,
		Name:                "InShell",
		StaticallyWitnessed: true,
	})

	machine := entity.NewStateMachine()
	machine.AddState(entity.StateDefinition{ID: 0, Name: "Patrol", Description: "Pacing left and right"})
	machine.AddState(entity.StateDefinition{ID: 1, Name: "InShell", Description: "Retracted into shell"})

	sub, err := entity.NewSubsystem(schema, dispatcher, machine)
	if err != nil {
		log.Fatalf("failed to create subsystem: %v", err)
	}

	// Spawn slot 0 as Patrol state with horizontal speed
	if err := sub.Spawn(0, 0x05, map[string]uint16{
		"state": 0,
		"timer": 3,
		"x":     120,
		"vx":    2,
	}); err != nil {
		log.Fatalf("spawn failed: %v", err)
	}

	// Frame 1 Logic Tick
	if err := sub.Tick(0); err != nil {
		log.Fatalf("tick failed: %v", err)
	}
	fmt.Printf("Tick 1: state=%d timer=%d x=%d\n", sub.Slots[0].State, sub.Slots[0].Memory["timer"], sub.Slots[0].Memory["x"])

	// State transition triggered by stomping event
	if err := sub.TransitionState(0, 1, 0x829100, "stomped_by_player"); err != nil {
		log.Fatalf("transition failed: %v", err)
	}
	fmt.Printf("After transition: state=%d\n", sub.Slots[0].State)

	// Advance through frame phases
	p1, _ := sub.AdvancePhase()
	p2, _ := sub.AdvancePhase()
	p3, _ := sub.AdvancePhase()
	fmt.Printf("Phases: %s -> %s -> %s (FrameCount=%d)\n", p1, p2, p3, sub.FrameCount)

	// Output:
	// Tick 1: state=0 timer=2 x=122
	// After transition: state=1
	// Phases: OAMBufferCommit -> VBlankDMA -> LogicUpdate (FrameCount=1)
}
