package entity

import "fmt"

// StateDefinition documents a known entity state.
type StateDefinition struct {
	ID          uint8  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Transition represents an observed or recovered edge between entity states.
type Transition struct {
	FromState      uint8  `json:"from_state"`
	ToState        uint8  `json:"to_state"`
	TriggerAddress uint32 `json:"trigger_address"` // Bus address of instruction writing the state (e.g. STA $0D80,X)
	Predicate      string `json:"predicate"`       // Witnessed condition (e.g. "timer == 0", "health <= 0")
	WitnessCount   uint64 `json:"witness_count"`   // Number of times witnessed
	Observed       bool   `json:"observed"`        // True if witnessed dynamically
}

// StateMachine models the state transitions and definitions for an entity.
type StateMachine struct {
	States      map[uint8]StateDefinition `json:"states"`
	Transitions []Transition              `json:"transitions"`
}

// NewStateMachine creates an empty state machine.
func NewStateMachine() *StateMachine {
	return &StateMachine{
		States:      make(map[uint8]StateDefinition),
		Transitions: make([]Transition, 0),
	}
}

// AddState adds or updates a state definition.
func (m *StateMachine) AddState(def StateDefinition) {
	m.States[def.ID] = def
}

// AddTransition adds a transition to the state machine (e.g., from static analysis).
func (m *StateMachine) AddTransition(t Transition) {
	m.ensureStates(t.FromState, t.ToState)
	m.Transitions = append(m.Transitions, t)
}

// RecordTransition updates the transition graph with dynamic witness receipts.
func (m *StateMachine) RecordTransition(from, to uint8, triggerPC uint32, predicate string) {
	m.ensureStates(from, to)

	for i := range m.Transitions {
		t := &m.Transitions[i]
		if t.FromState == from && t.ToState == to && t.TriggerAddress == triggerPC && t.Predicate == predicate {
			t.WitnessCount++
			t.Observed = true
			return
		}
	}

	m.Transitions = append(m.Transitions, Transition{
		FromState:      from,
		ToState:        to,
		TriggerAddress: triggerPC,
		Predicate:      predicate,
		WitnessCount:   1,
		Observed:       true,
	})
}

// FindTransitions returns all transitions between the given states.
func (m *StateMachine) FindTransitions(from, to uint8) []Transition {
	var matches []Transition
	for _, t := range m.Transitions {
		if t.FromState == from && t.ToState == to {
			matches = append(matches, t)
		}
	}
	return matches
}

func (m *StateMachine) ensureStates(states ...uint8) {
	for _, id := range states {
		if _, ok := m.States[id]; !ok {
			m.States[id] = StateDefinition{
				ID:          id,
				Name:        fmt.Sprintf("state_%02X", id),
				Description: "auto-discovered state",
			}
		}
	}
}
