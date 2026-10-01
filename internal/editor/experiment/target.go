// Package experiment describes bounded headless game-editing experiments.
// A valid descriptor is a target contract, not captured proof or edit approval.
package experiment

import (
	"crypto/sha256"
	"fmt"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// Field identifies a canonical WRAM value and its interpretation.
// Name is advisory unless supported by an observed behavior crosswalk.
type Field struct {
	Name    string `json:"name"`
	Address uint32 `json:"address"`
	Bytes   int    `json:"bytes"`
	Signed  bool   `json:"signed"`
}

// Parameter describes an editable input and deliberately narrow experiment range.
type Parameter struct {
	Field   Field  `json:"field"`
	Minimum int    `json:"minimum"`
	Maximum int    `json:"maximum"`
	Units   string `json:"units"`
}

// Target binds a bounded routine contract to ROM bytes and advisory semantics.
// The half-open region is physical CPU address space, not a ROM file offset.
type Target struct {
	ID           string           `json:"id"`
	ROMSHA256    string           `json:"rom_sha256"`
	Start        uint32           `json:"start"`
	End          uint32           `json:"end"`
	CodeSHA256   string           `json:"code_sha256"`
	EntryContext recovery.Context `json:"entry_context"`
	Parameter    Parameter        `json:"parameter"`
	Effects      []Field          `json:"effects"`
	Crosswalk    string           `json:"advisory_crosswalk"`
	Scope        string           `json:"scope"`
}

// ValidateValue checks an edit against the descriptor's parameter range.
func (t Target) ValidateValue(value int) error {
	if t.Parameter.Minimum > t.Parameter.Maximum || value < t.Parameter.Minimum || value > t.Parameter.Maximum {
		return fmt.Errorf("parameter outside experiment range")
	}
	if t.Parameter.Field.Bytes != 1 && t.Parameter.Field.Bytes != 2 {
		return fmt.Errorf("unsupported parameter width")
	}
	lo, hi := 0, (1<<(8*t.Parameter.Field.Bytes))-1
	if t.Parameter.Field.Signed {
		hi >>= 1
		lo = -hi - 1
	}
	if t.Parameter.Minimum < lo || t.Parameter.Maximum > hi {
		return fmt.Errorf("parameter range exceeds field representation")
	}
	return nil
}

// Validate checks ROM, CPU entry, parameter initialization and address bounds.
// The caller must separately admit the capture and qualify original generated C.
// Edited executions must never inherit the original case's eligibility.
func (t Target) Validate(rom []byte, c decomp.ReplayCase) error {
	if t.ID == "" || t.Crosswalk == "" || t.Scope == "" {
		return fmt.Errorf("missing target identity or scope")
	}
	sha := fmt.Sprintf("%x", sha256.Sum256(rom))
	if t.ROMSHA256 == "" || sha != t.ROMSHA256 || c.ROMSHA256 != sha {
		return fmt.Errorf("target, ROM and case identities differ")
	}
	if t.Start >= 1<<24 || t.End > 1<<24 || t.Start&0xffff < 0x8000 || t.End <= t.Start || t.End-t.Start > 32768 || t.Start&0xff0000 != (t.End-1)&0xff0000 {
		return fmt.Errorf("unsupported bounded LoROM region")
	}
	off := int((t.Start>>16&127)*32768 + (t.Start & 32767))
	n := int(t.End - t.Start)
	if off+n > len(rom) || fmt.Sprintf("%x", sha256.Sum256(rom[off:off+n])) != t.CodeSHA256 {
		return fmt.Errorf("target code differs from pinned ROM")
	}
	s := c.InitialState
	if uint32(s.PB)<<16|uint32(s.PC) != t.Start {
		return fmt.Errorf("case does not enter target")
	}
	bit := func(b bool) string {
		if b {
			return "set"
		}
		return "clear"
	}
	actual := recovery.Context{E: bit(s.E), M: bit(s.P&32 != 0), X: bit(s.P&16 != 0), C: bit(s.P&1 != 0)}
	if t.EntryContext.C == "unknown" {
		actual.C = "unknown"
	}
	if actual != t.EntryContext || s.E || s.D != 0 || s.P&8 != 0 {
		return fmt.Errorf("unsupported target entry context")
	}
	if s.DB > 0x3f && (s.DB < 0x80 || s.DB > 0xbf) {
		return fmt.Errorf("entry data bank is not a low-WRAM mirror")
	}
	fields := append([]Field{t.Parameter.Field}, t.Effects...)
	for _, f := range fields {
		if f.Name == "" || (f.Bytes != 1 && f.Bytes != 2) || f.Address < 0x7e0000 || uint64(f.Address)+uint64(f.Bytes) > 0x800000 {
			return fmt.Errorf("invalid WRAM field")
		}
	}
	if err := t.ValidateValue(t.Parameter.Minimum); err != nil {
		return err
	}
	initialized := map[uint32]uint8{}
	for _, cell := range c.InitialMemory {
		a := decomp.CanonicalBusAddress(cell.Address)
		if prev, ok := initialized[a]; ok && prev != cell.Value {
			return fmt.Errorf("conflicting initial memory alias")
		}
		initialized[a] = cell.Value
	}
	for i := 0; i < t.Parameter.Field.Bytes; i++ {
		if _, ok := initialized[t.Parameter.Field.Address+uint32(i)]; !ok {
			return fmt.Errorf("parameter byte not initialized")
		}
	}
	return nil
}
