package entity

import (
	"fmt"
)

// HandlerTarget represents a code destination for an entity state handler.
type HandlerTarget struct {
	StateID             uint8  `json:"state_id"`
	Address             uint32 `json:"address"` // Bus address of the routine
	Name                string `json:"name"`
	StaticallyWitnessed bool   `json:"statically_witnessed"` // True if discovered via static jump table analysis
	DynamicallyObserved bool   `json:"dynamically_observed"` // True if observed during authentic dynamic execution
	SimulationHits      uint64 `json:"simulation_hits"`      // Simulation dispatch counter
	WitnessHits         uint64 `json:"witness_hits"`         // Authentic dynamic observation counter
}

// ActionFunc represents optional simulation logic associated with a state handler.
type ActionFunc func(slot *EntitySlot, sub *Subsystem) error

// UpdateDispatcher manages indirect dispatching across entity states.
type UpdateDispatcher struct {
	Schema         *EntitySchema                 `json:"schema"`
	TableBase      uint32                        `json:"table_base"` // Base address of dispatch table
	Handlers       map[uint8]HandlerTarget       `json:"handlers"`
	DefaultHandler uint32                        `json:"default_handler"` // Fallback handler address (0 if none)
	actions        map[uint8]ActionFunc
}

// NewUpdateDispatcher creates a new dispatcher for an entity schema.
func NewUpdateDispatcher(schema *EntitySchema, tableBase uint32, defaultHandler uint32) *UpdateDispatcher {
	return &UpdateDispatcher{
		Schema:         schema,
		TableBase:      tableBase,
		Handlers:       make(map[uint8]HandlerTarget),
		DefaultHandler: defaultHandler,
		actions:        make(map[uint8]ActionFunc),
	}
}

// RegisterHandler registers or updates a state handler target.
func (d *UpdateDispatcher) RegisterHandler(target HandlerTarget) {
	d.Handlers[target.StateID] = target
}

// RegisterAction associates simulated handler logic with a state ID.
func (d *UpdateDispatcher) RegisterAction(stateID uint8, fn ActionFunc) {
	d.actions[stateID] = fn
}

// Dispatch looks up the handler target for a given state ID during simulation.
// Calling Dispatch increments SimulationHits, but does NOT grant DynamicallyObserved authority.
func (d *UpdateDispatcher) Dispatch(state uint8) (HandlerTarget, error) {
	if target, ok := d.Handlers[state]; ok {
		target.SimulationHits++
		d.Handlers[state] = target
		return target, nil
	}

	if d.DefaultHandler != 0 {
		return HandlerTarget{
			StateID:             state,
			Address:             d.DefaultHandler,
			Name:                "default",
			StaticallyWitnessed: false,
			DynamicallyObserved: false,
			SimulationHits:      0,
			WitnessHits:         0,
		}, nil
	}

	return HandlerTarget{}, fmt.Errorf("dispatch: unhandled state 0x%02X", state)
}

// ExecutionWitnessReceipt encapsulates verified trace provenance for an executed state handler.
type ExecutionWitnessReceipt struct {
	TraceSHA256   string `json:"trace_sha256"`
	EventID       uint64 `json:"event_id"`
	Frame         int    `json:"frame"`
	Cycle         uint64 `json:"cycle"`
	PC            uint32 `json:"pc"`
	TargetAddress uint32 `json:"target_address"`
}

// RecordExecutionWitness records an authentic dynamic execution witness for a state handler
// from a verified execution witness receipt, establishing DynamicallyObserved authority.
func (d *UpdateDispatcher) RecordExecutionWitness(state uint8, receipt ExecutionWitnessReceipt) error {
	if receipt.TraceSHA256 == "" || receipt.EventID == 0 {
		return fmt.Errorf("record execution witness: missing trace identity or event ID")
	}
	target, ok := d.Handlers[state]
	if !ok {
		return fmt.Errorf("record execution witness: unknown state 0x%02X", state)
	}
	if receipt.TargetAddress != 0 && receipt.TargetAddress != target.Address {
		return fmt.Errorf("record execution witness: receipt target 0x%06X != handler 0x%06X",
			receipt.TargetAddress, target.Address)
	}
	target.WitnessHits++
	target.DynamicallyObserved = true
	d.Handlers[state] = target
	return nil
}
