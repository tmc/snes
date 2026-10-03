package entity

import (
	"fmt"
)

// MainLoopPhase represents execution phase boundaries in the SNES main loop.
type MainLoopPhase int

const (
	// PhaseLogicUpdate is the game frame phase where entity states, physics, and AI are updated.
	PhaseLogicUpdate MainLoopPhase = iota

	// PhaseOAMBufferCommit is the phase where active entity visual states are converted
	// into the 544-byte shadow OAM buffer at $7E:0200.
	PhaseOAMBufferCommit

	// PhaseVBlankDMA is the vertical blanking phase where DMA transfers the shadow OAM
	// buffer into PPU OAM memory.
	PhaseVBlankDMA
)

func (p MainLoopPhase) String() string {
	switch p {
	case PhaseLogicUpdate:
		return "LogicUpdate"
	case PhaseOAMBufferCommit:
		return "OAMBufferCommit"
	case PhaseVBlankDMA:
		return "VBlankDMA"
	default:
		return fmt.Sprintf("Phase(%d)", int(p))
	}
}

// OAM constants matching SNES hardware structure.
const (
	OAMBufferBase      uint32 = 0x7E0200
	OAMHighTableBase   uint32 = 0x7E0400
	OAMLowTableSize    int    = 512 // 128 sprites * 4 bytes
	OAMHighTableSize   int    = 32  // 128 sprites * 2 bits
	OAMTotalBufferSize int    = OAMLowTableSize + OAMHighTableSize // 544 bytes
)

// EntitySlot represents runtime memory and status for a single entity slot.
type EntitySlot struct {
	Slot   int               `json:"slot"`
	Active bool              `json:"active"`
	Type   uint8             `json:"type"`
	State  uint8             `json:"state"`
	Memory map[string]uint16 `json:"memory"` // Field name -> value
}

// Subsystem coordinates entity schemas, state dispatching, transitions, and frame phases.
type Subsystem struct {
	Schema        *EntitySchema
	Dispatcher    *UpdateDispatcher
	Machine       *StateMachine
	Slots         []EntitySlot
	MainLoopPhase MainLoopPhase
	OAMShadowBase uint32                   // Base address of shadow OAM buffer in WRAM ($7E:0200 or $7E:0800)
	OAMAllocations map[int][]int           // Entity slot -> composite hardware OAM slots
	OAMBuffer     [OAMTotalBufferSize]byte // Shadow OAM buffer
	VRAMOAM       [OAMTotalBufferSize]byte // Hardware OAM after V-Blank DMA
	FrameCount    int
}

// NewSubsystem initializes a new entity subsystem.
func NewSubsystem(schema *EntitySchema, dispatcher *UpdateDispatcher, machine *StateMachine) (*Subsystem, error) {
	if schema == nil {
		return nil, fmt.Errorf("subsystem: schema cannot be nil")
	}
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("subsystem: invalid schema: %w", err)
	}
	if dispatcher == nil {
		dispatcher = NewUpdateDispatcher(schema, 0, 0)
	}
	if machine == nil {
		machine = NewStateMachine()
	}

	slots := make([]EntitySlot, schema.SlotCount)
	for i := range slots {
		slots[i] = EntitySlot{
			Slot:   i,
			Active: false,
			Memory: make(map[string]uint16),
		}
	}

	shadowBase := OAMBufferBase
	if schema.Name == "Zelda3Sprite" {
		shadowBase = 0x7E0800
	}

	return &Subsystem{
		Schema:         schema,
		Dispatcher:     dispatcher,
		Machine:        machine,
		Slots:          slots,
		MainLoopPhase:  PhaseLogicUpdate,
		OAMShadowBase:  shadowBase,
		OAMAllocations: make(map[int][]int),
	}, nil
}

// SetOAMAllocation assigns composite hardware OAM slots to an entity slot.
// Validates that the entity slot is within bounds and all OAM slots are in [0, 127].
func (s *Subsystem) SetOAMAllocation(slot int, oamSlots []int) error {
	if slot < 0 || slot >= len(s.Slots) {
		return fmt.Errorf("set oam allocation: entity slot %d out of bounds (count=%d)", slot, len(s.Slots))
	}
	for _, o := range oamSlots {
		if o < 0 || o >= 128 {
			return fmt.Errorf("set oam allocation: invalid hardware OAM slot %d (must be 0..127)", o)
		}
	}
	if s.OAMAllocations == nil {
		s.OAMAllocations = make(map[int][]int)
	}
	s.OAMAllocations[slot] = append([]int(nil), oamSlots...)
	return nil
}

// Spawn validates the slot, initializes its fields, and sets state to active.
func (s *Subsystem) Spawn(slot int, entityType uint8, initialFields map[string]uint16) error {
	if slot < 0 || slot >= len(s.Slots) {
		return fmt.Errorf("spawn: slot %d out of bounds (count=%d)", slot, len(s.Slots))
	}

	eSlot := &s.Slots[slot]
	eSlot.Active = true
	eSlot.Type = entityType
	eSlot.Memory = make(map[string]uint16)

	if s.Schema.TypeField != "" {
		eSlot.Memory[s.Schema.TypeField] = uint16(entityType)
	}

	for k, v := range initialFields {
		eSlot.Memory[k] = v
	}

	var state uint8
	if s.Schema.StateField != "" {
		if st, ok := initialFields[s.Schema.StateField]; ok {
			state = uint8(st)
		} else {
			state = 0
			eSlot.Memory[s.Schema.StateField] = 0
		}
	}
	eSlot.State = state

	return nil
}

// Tick decrements active timers, invokes the state dispatcher, and updates slot state.
func (s *Subsystem) Tick(slot int) error {
	if slot < 0 || slot >= len(s.Slots) {
		return fmt.Errorf("tick: slot %d out of bounds (count=%d)", slot, len(s.Slots))
	}

	eSlot := &s.Slots[slot]
	if !eSlot.Active {
		return fmt.Errorf("tick: slot %d is inactive", slot)
	}

	// 1. Decrement active timers.
	for _, tf := range s.Schema.TimerFields {
		if val, ok := eSlot.Memory[tf]; ok && val > 0 {
			eSlot.Memory[tf] = val - 1
		}
	}

	// 2. Invoke state dispatcher.
	currentState := eSlot.State
	target, err := s.Dispatcher.Dispatch(currentState)
	if err != nil {
		return fmt.Errorf("tick: slot %d state 0x%02X dispatch failed: %w", slot, currentState, err)
	}

	// 3. Execute handler action if registered.
	if act, ok := s.Dispatcher.actions[currentState]; ok {
		if err := act(eSlot, s); err != nil {
			return fmt.Errorf("tick: slot %d handler action error: %w", slot, err)
		}
	} else {
		// Authentic SNES 16-bit fixed point position accumulator (05:FA00..05:FA2A)
		if vy, ok := eSlot.Memory["vy"]; ok && vy != 0 {
			yLow := eSlot.Memory["y_low"]
			yHigh := eSlot.Memory["y_high"]
			curY := int32((yHigh << 8) | (yLow & 0xFF))
			newY := curY + int32(int8(vy))
			eSlot.Memory["y_low"] = uint16(uint8(newY & 0xFF))
			eSlot.Memory["y_high"] = uint16(uint8((newY >> 8) & 0xFF))
		}
		if vx, ok := eSlot.Memory["vx"]; ok && vx != 0 {
			xLow := eSlot.Memory["x_low"]
			xHigh := eSlot.Memory["x_high"]
			curX := int32((xHigh << 8) | (xLow & 0xFF))
			newX := curX + int32(int8(vx))
			eSlot.Memory["x_low"] = uint16(uint8(newX & 0xFF))
			eSlot.Memory["x_high"] = uint16(uint8((newX >> 8) & 0xFF))
		}
		if vx, ok := eSlot.Memory["vx"]; ok && vx != 0 {
			eSlot.Memory["x"] += vx
		}
		if vy, ok := eSlot.Memory["vy"]; ok && vy != 0 {
			eSlot.Memory["y"] += vy
		}
		_ = target
	}

	return nil
}

// TransitionState updates the slot's state and records the transition witness in the state machine.
func (s *Subsystem) TransitionState(slot int, toState uint8, triggerPC uint32, predicate string) error {
	if slot < 0 || slot >= len(s.Slots) {
		return fmt.Errorf("transition: slot %d out of bounds (count=%d)", slot, len(s.Slots))
	}
	eSlot := &s.Slots[slot]
	if !eSlot.Active {
		return fmt.Errorf("transition: slot %d is inactive", slot)
	}

	oldState := eSlot.State
	eSlot.State = toState
	if s.Schema.StateField != "" {
		eSlot.Memory[s.Schema.StateField] = uint16(toState)
	}

	s.Machine.AddSimulatedTransition(oldState, toState, triggerPC, predicate)
	return nil
}

// Despawn deactivates the slot and clears its memory.
func (s *Subsystem) Despawn(slot int) error {
	if slot < 0 || slot >= len(s.Slots) {
		return fmt.Errorf("despawn: slot %d out of bounds (count=%d)", slot, len(s.Slots))
	}
	eSlot := &s.Slots[slot]
	if !eSlot.Active {
		return fmt.Errorf("despawn: slot %d is already inactive", slot)
	}

	eSlot.Active = false
	eSlot.Type = 0
	eSlot.State = 0
	eSlot.Memory = make(map[string]uint16)
	return nil
}

// AdvancePhase steps through the main loop frame phases:
// LogicUpdate -> OAMBufferCommit -> VBlankDMA -> LogicUpdate.
func (s *Subsystem) AdvancePhase() (MainLoopPhase, error) {
	switch s.MainLoopPhase {
	case PhaseLogicUpdate:
		if err := s.CommitOAM(); err != nil {
			return s.MainLoopPhase, fmt.Errorf("advance phase to OAMBufferCommit: %w", err)
		}
		s.MainLoopPhase = PhaseOAMBufferCommit
		return s.MainLoopPhase, nil

	case PhaseOAMBufferCommit:
		if err := s.DMAVBlank(); err != nil {
			return s.MainLoopPhase, fmt.Errorf("advance phase to VBlankDMA: %w", err)
		}
		s.MainLoopPhase = PhaseVBlankDMA
		return s.MainLoopPhase, nil

	case PhaseVBlankDMA:
		s.FrameCount++
		s.MainLoopPhase = PhaseLogicUpdate
		return s.MainLoopPhase, nil

	default:
		return s.MainLoopPhase, fmt.Errorf("advance phase: unknown phase %v", s.MainLoopPhase)
	}
}

// CommitOAM generates the shadow OAM buffer from active entity slots.
// Supports composite multi-sprite mappings via OAMAllocations.
func (s *Subsystem) CommitOAM() error {
	// Clear shadow OAM buffer (put off-screen Y=224/0xE0)
	for i := range s.OAMBuffer {
		s.OAMBuffer[i] = 0
	}

	for i := range s.Slots {
		slot := &s.Slots[i]
		oamSlots := s.OAMAllocations[i]
		if len(oamSlots) == 0 {
			if i < 128 {
				oamSlots = []int{i}
			}
		}

		if !slot.Active {
			// Inactive sprite: set Y = 224 (0xE0) to place off-screen for all associated OAM slots
			for _, oamSlot := range oamSlots {
				if oamSlot >= 0 && oamSlot < 128 {
					s.OAMBuffer[oamSlot*4+1] = 0xE0
				}
			}
			continue
		}

		x := slot.Memory["x"]
		if x == 0 && (slot.Memory["x_low"] != 0 || slot.Memory["x_high"] != 0) {
			x = (slot.Memory["x_high"] << 8) | slot.Memory["x_low"]
		}
		y := slot.Memory["y"]
		if y == 0 && (slot.Memory["y_low"] != 0 || slot.Memory["y_high"] != 0) {
			y = (slot.Memory["y_high"] << 8) | slot.Memory["y_low"]
		}
		tile := slot.Memory["tile"]
		attr := slot.Memory["attr"]

		for _, oamSlot := range oamSlots {
			if oamSlot < 0 || oamSlot >= 128 {
				continue
			}
			// Low table entry
			s.OAMBuffer[oamSlot*4+0] = uint8(x & 0xFF)
			s.OAMBuffer[oamSlot*4+1] = uint8(y & 0xFF)
			s.OAMBuffer[oamSlot*4+2] = uint8(tile & 0xFF)
			s.OAMBuffer[oamSlot*4+3] = uint8(attr & 0xFF)

			// High table entry (2 bits per sprite: bit 0 = X high bit, bit 1 = size)
			highByteIdx := OAMLowTableSize + (oamSlot / 4)
			bitShift := (oamSlot % 4) * 2
			xBit := uint8((x >> 8) & 0x01)
			sizeBit := uint8(0)
			if sz, ok := slot.Memory["size"]; ok && sz > 0 {
				sizeBit = 1
			}
			val := (xBit | (sizeBit << 1)) << bitShift
			s.OAMBuffer[highByteIdx] |= val
		}
	}
	return nil
}

// DMAVBlank simulates the hardware DMA commit from shadow OAM buffer to PPU OAM memory.
func (s *Subsystem) DMAVBlank() error {
	copy(s.VRAMOAM[:], s.OAMBuffer[:])
	return nil
}
