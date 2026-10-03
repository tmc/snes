package entity

import (
	"fmt"
	"strings"
)

// LifecycleEventKind identifies the nature of a lifecycle event.
type LifecycleEventKind string

const (
	// EventSpawn represents entity creation and initialization.
	EventSpawn LifecycleEventKind = "spawn"

	// EventTick represents a per-frame logic tick.
	EventTick LifecycleEventKind = "tick"

	// EventTransition represents a state machine transition.
	EventTransition LifecycleEventKind = "transition"

	// EventDespawn represents entity deactivation and cleanup.
	EventDespawn LifecycleEventKind = "despawn"
)

// LifecycleEvent represents a recorded action or observation in an entity's lifecycle.
type LifecycleEvent struct {
	Kind          LifecycleEventKind `json:"kind"`
	Slot          int                `json:"slot"`
	Frame         int                `json:"frame"`
	Fields        map[string]uint16  `json:"fields,omitempty"`
	ExpectedState uint8              `json:"expected_state"`
	EntityType    uint8              `json:"entity_type,omitempty"` // For spawn
	TargetState   uint8              `json:"target_state,omitempty"` // For transition
	TriggerPC     uint32             `json:"trigger_pc,omitempty"`   // For transition
	Predicate     string             `json:"predicate,omitempty"`    // For transition
}

// FieldMismatch records a discrepancy between expected and actual entity field values.
type FieldMismatch struct {
	EventIndex int    `json:"event_index"`
	Slot       int    `json:"slot"`
	Field      string `json:"field"`
	Expected   uint16 `json:"expected"`
	Actual     uint16 `json:"actual"`
}

func (m FieldMismatch) String() string {
	return fmt.Sprintf("event %d (slot %d) field %q mismatch: expected 0x%04X, got 0x%04X",
		m.EventIndex, m.Slot, m.Field, m.Expected, m.Actual)
}

// StateDiscrepancy records an illegal or unexpected state during lifecycle execution.
type StateDiscrepancy struct {
	EventIndex int    `json:"event_index"`
	Slot       int    `json:"slot"`
	Expected   uint8  `json:"expected"`
	Actual     uint8  `json:"actual"`
	Reason     string `json:"reason"`
}

func (d StateDiscrepancy) String() string {
	return fmt.Sprintf("event %d (slot %d) state discrepancy: expected 0x%02X, got 0x%02X (%s)",
		d.EventIndex, d.Slot, d.Expected, d.Actual, d.Reason)
}

// LifecycleReceipt summarizes the verification outcome of a replayed lifecycle.
type LifecycleReceipt struct {
	Valid              bool               `json:"valid"`
	MatchedEvents      int                `json:"matched_events"`
	TotalEvents        int                `json:"total_events"`
	Mismatches         []FieldMismatch    `json:"mismatches,omitempty"`
	StateDiscrepancies []StateDiscrepancy `json:"state_discrepancies,omitempty"`
	UnobservedPaths    []Transition       `json:"unobserved_paths,omitempty"`
}

// DiscrepancySummary formats a human-readable list of all discrepancies found during replay.
func (r *LifecycleReceipt) DiscrepancySummary() string {
	if r.Valid {
		return "lifecycle verified with no discrepancies"
	}
	var sb strings.Builder
	for _, sd := range r.StateDiscrepancies {
		sb.WriteString(sd.String())
		sb.WriteByte('\n')
	}
	for _, fm := range r.Mismatches {
		sb.WriteString(fm.String())
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ReplayLifecycle verifies recorded lifecycle events against subsystem execution.
func (s *Subsystem) ReplayLifecycle(events []LifecycleEvent) (*LifecycleReceipt, error) {
	receipt := &LifecycleReceipt{
		TotalEvents: len(events),
		Valid:       true,
	}

	for idx, ev := range events {
		eventHadError := false

		if ev.Slot < 0 || ev.Slot >= len(s.Slots) {
			receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
				EventIndex: idx,
				Slot:       ev.Slot,
				Expected:   ev.ExpectedState,
				Reason:     fmt.Sprintf("slot %d out of bounds", ev.Slot),
			})
			receipt.Valid = false
			continue
		}

		slot := &s.Slots[ev.Slot]

		switch ev.Kind {
		case EventSpawn:
			if err := s.Spawn(ev.Slot, ev.EntityType, ev.Fields); err != nil {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Actual:     slot.State,
					Reason:     fmt.Sprintf("spawn failed: %v", err),
				})
				eventHadError = true
			}

		case EventTick:
			if !slot.Active {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Reason:     "tick on inactive slot",
				})
				eventHadError = true
			} else {
				if err := s.Tick(ev.Slot); err != nil {
					receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
						EventIndex: idx,
						Slot:       ev.Slot,
						Expected:   ev.ExpectedState,
						Actual:     slot.State,
						Reason:     fmt.Sprintf("tick error: %v", err),
					})
					eventHadError = true
				}
			}

		case EventTransition:
			if !slot.Active {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Reason:     "transition on inactive slot",
				})
				eventHadError = true
				break
			}

			// Verify current state before transition matches ExpectedState.
			if slot.State != ev.ExpectedState {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Actual:     slot.State,
					Reason:     "illegal state jump: current state does not match transition source",
				})
				eventHadError = true
			}

			// Check if transition was previously observed in the state machine.
			transitions := s.Machine.FindTransitions(ev.ExpectedState, ev.TargetState)
			pathObserved := false
			for _, t := range transitions {
				if t.TriggerAddress == ev.TriggerPC && t.Predicate == ev.Predicate && t.Observed {
					pathObserved = true
					break
				}
			}
			if !pathObserved {
				receipt.UnobservedPaths = append(receipt.UnobservedPaths, Transition{
					FromState:      ev.ExpectedState,
					ToState:        ev.TargetState,
					TriggerAddress: ev.TriggerPC,
					Predicate:      ev.Predicate,
					WitnessCount:   0,
					Observed:       false,
				})
			}

			if err := s.TransitionState(ev.Slot, ev.TargetState, ev.TriggerPC, ev.Predicate); err != nil {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.TargetState,
					Actual:     slot.State,
					Reason:     fmt.Sprintf("transition error: %v", err),
				})
				eventHadError = true
			}

		case EventDespawn:
			if !slot.Active {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Reason:     "despawn on inactive slot",
				})
				eventHadError = true
			} else {
				if err := s.Despawn(ev.Slot); err != nil {
					receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
						EventIndex: idx,
						Slot:       ev.Slot,
						Expected:   ev.ExpectedState,
						Reason:     fmt.Sprintf("despawn failed: %v", err),
					})
					eventHadError = true
				}
			}

		default:
			receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
				EventIndex: idx,
				Slot:       ev.Slot,
				Reason:     fmt.Sprintf("unknown event kind %q", ev.Kind),
			})
			eventHadError = true
		}

		// Verify expected state for non-transition/despawn events.
		if ev.Kind != EventTransition && ev.Kind != EventDespawn && slot.Active {
			if slot.State != ev.ExpectedState {
				receipt.StateDiscrepancies = append(receipt.StateDiscrepancies, StateDiscrepancy{
					EventIndex: idx,
					Slot:       ev.Slot,
					Expected:   ev.ExpectedState,
					Actual:     slot.State,
					Reason:     "state mismatch after event execution",
				})
				eventHadError = true
			}
		}

		// Verify field values if specified in event.
		if ev.Fields != nil && slot.Active {
			for fieldName, expectedVal := range ev.Fields {
				actualVal, ok := slot.Memory[fieldName]
				if !ok || actualVal != expectedVal {
					receipt.Mismatches = append(receipt.Mismatches, FieldMismatch{
						EventIndex: idx,
						Slot:       ev.Slot,
						Field:      fieldName,
						Expected:   expectedVal,
						Actual:     actualVal,
					})
					eventHadError = true
				}
			}
		}

		if !eventHadError {
			receipt.MatchedEvents++
		}
	}

	if len(receipt.Mismatches) > 0 || len(receipt.StateDiscrepancies) > 0 {
		receipt.Valid = false
	}

	return receipt, nil
}
