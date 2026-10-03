package entity

import (
	"fmt"
)

// Register represents an indexing register used for SoA table operations.
type Register string

const (
	// RegX indicates the X register indexes the field table.
	RegX Register = "X"
	// RegY indicates the Y register indexes the field table.
	RegY Register = "Y"
)

// Field defines a single property stored across entity slots in Structure-of-Arrays layout.
type Field struct {
	Name        string   `json:"name"`
	BaseAddress uint32   `json:"base_address"` // Bus address of slot 0 (e.g., 0x7E0D80)
	Width       int      `json:"width"`        // Value width in bits (8 or 16)
	Stride      int      `json:"stride"`       // Byte distance between consecutive slots
	Register    Register `json:"register"`     // Indexing register (RegX or RegY)
	Domain      string   `json:"domain"`       // Memory domain, typically "WRAM"
	Description string   `json:"description"`  // Human-readable description
}

// EntitySchema defines the structure and layout of an entity table in memory.
type EntitySchema struct {
	Name        string           `json:"name"`
	SlotCount   int              `json:"slot_count"`
	Fields      map[string]Field `json:"fields"`
	StateField  string           `json:"state_field"`  // Field name holding current state ID
	TypeField   string           `json:"type_field"`   // Field name holding entity type ID
	TimerFields []string         `json:"timer_fields"` // Field names for auto-decrementing timers
}

// SlotAddress calculates the effective memory address for a given field and slot.
func SlotAddress(field Field, slot int) uint32 {
	return field.BaseAddress + uint32(slot*field.Stride)
}

// SlotAddress looks up the named field in the schema and computes its address for the slot.
func (s *EntitySchema) SlotAddress(name string, slot int) (uint32, error) {
	if slot < 0 || slot >= s.SlotCount {
		return 0, fmt.Errorf("slot address: invalid slot %d for schema %s (count=%d)", slot, s.Name, s.SlotCount)
	}
	f, ok := s.Fields[name]
	if !ok {
		return 0, fmt.Errorf("slot address: field %q not found in schema %s", name, s.Name)
	}
	return SlotAddress(f, slot), nil
}

// Validate checks whether the schema definition is well-formed.
func (s *EntitySchema) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("validate schema: name is required")
	}
	if s.SlotCount <= 0 {
		return fmt.Errorf("validate schema: slot count must be positive, got %d", s.SlotCount)
	}
	if len(s.Fields) == 0 {
		return fmt.Errorf("validate schema: fields map cannot be empty")
	}
	if s.StateField != "" {
		f, ok := s.Fields[s.StateField]
		if !ok {
			return fmt.Errorf("validate schema: state field %q not defined in fields", s.StateField)
		}
		if f.Width != 8 {
			return fmt.Errorf("validate schema: state field %q width must be 8 bits, got %d", s.StateField, f.Width)
		}
	}
	if s.TypeField != "" {
		if _, ok := s.Fields[s.TypeField]; !ok {
			return fmt.Errorf("validate schema: type field %q not defined in fields", s.TypeField)
		}
	}
	for _, tf := range s.TimerFields {
		if _, ok := s.Fields[tf]; !ok {
			return fmt.Errorf("validate schema: timer field %q not defined in fields", tf)
		}
	}
	for name, f := range s.Fields {
		if f.Stride <= 0 {
			return fmt.Errorf("validate schema: field %q stride must be positive, got %d", name, f.Stride)
		}
		if f.Width != 8 && f.Width != 16 {
			return fmt.Errorf("validate schema: field %q width must be 8 or 16 bits, got %d", name, f.Width)
		}
	}
	return nil
}

// Zelda3SpriteSchema returns the authentic Structure-of-Arrays sprite table schema for
// The Legend of Zelda: A Link to the Past.
func Zelda3SpriteSchema() *EntitySchema {
	return &EntitySchema{
		Name:      "Zelda3Sprite",
		SlotCount: 16,
		Fields: map[string]Field{
			"status": {
				Name:        "status",
				BaseAddress: 0x7E0DD0,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Sprite status and active state selector ($00=inactive, $09=alive)",
			},
			"type": {
				Name:        "type",
				BaseAddress: 0x7E0E20,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Sprite archetype identifier",
			},
			"y_low": {
				Name:        "y_low",
				BaseAddress: 0x7E0D00,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Low 8 bits of Y coordinate",
			},
			"y_high": {
				Name:        "y_high",
				BaseAddress: 0x7E0D20,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "High 8 bits of Y coordinate",
			},
			"x_low": {
				Name:        "x_low",
				BaseAddress: 0x7E0D10,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Low 8 bits of X coordinate",
			},
			"x_high": {
				Name:        "x_high",
				BaseAddress: 0x7E0D30,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "High 8 bits of X coordinate",
			},
			"vy": {
				Name:        "vy",
				BaseAddress: 0x7E0D40,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Signed 8-bit vertical velocity",
			},
			"vx": {
				Name:        "vx",
				BaseAddress: 0x7E0D50,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Signed 8-bit horizontal velocity",
			},
			"timer0": {
				Name:        "timer0",
				BaseAddress: 0x7E0DF0,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Per-frame decrementing sprite timer 0",
			},
			"timer1": {
				Name:        "timer1",
				BaseAddress: 0x7E0E00,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Per-frame decrementing sprite timer 1",
			},
			"timer2": {
				Name:        "timer2",
				BaseAddress: 0x7E0E10,
				Width:       8,
				Stride:      1,
				Register:    RegX,
				Domain:      "WRAM",
				Description: "Per-frame decrementing sprite timer 2",
			},
		},
		StateField:  "status",
		TypeField:   "type",
		TimerFields: []string{"timer0", "timer1", "timer2"},
	}
}
