package snes

import "fmt"

// Cheat applies a value override to a CPU-visible memory address.
type Cheat struct {
	Name       string
	Address    uint32
	Value      uint8
	HasCompare bool
	Compare    uint8
	Enabled    bool
}

// SetCheats replaces the active cheat list.
func (s *System) SetCheats(cheats []Cheat) error {
	dst := make([]Cheat, len(cheats))
	copy(dst, cheats)
	for i, cheat := range dst {
		if cheat.Address > 0xFFFFFF {
			return fmt.Errorf("set cheats: cheat %d has out-of-range address %06X", i, cheat.Address)
		}
	}
	s.cheats = dst
	return nil
}

// Cheats returns a copy of the active cheat list.
func (s *System) Cheats() []Cheat {
	dst := make([]Cheat, len(s.cheats))
	copy(dst, s.cheats)
	return dst
}

// ClearCheats disables all cheats.
func (s *System) ClearCheats() {
	s.cheats = nil
}

func (s *System) applyCheats() {
	for _, cheat := range s.cheats {
		if !cheat.Enabled {
			continue
		}
		if cheat.HasCompare {
			if got := s.Bus.Read(cheat.Address); got != cheat.Compare {
				continue
			}
		}
		s.Bus.Write(cheat.Address, cheat.Value)
	}
}
