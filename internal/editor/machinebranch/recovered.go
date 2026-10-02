package machinebranch

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// RecoveredConfig pins automatically emitted instruction semantics and timing.
// These identities do not grant captured recovery qualification.
type RecoveredConfig struct {
	SourceSHA256   string `json:"source_sha256"`
	IRSHA256       string `json:"ir_sha256"`
	EditedIRSHA256 string `json:"edited_ir_sha256"`
	PlanSHA256     string `json:"plan_sha256"`
	EditSHA256     string `json:"edit_sha256"`
}

func (c RecoveredConfig) validate() error {
	for _, p := range []struct{ name, value string }{{"source", c.SourceSHA256}, {"IR", c.IRSHA256}, {"edited IR", c.EditedIRSHA256}, {"plan", c.PlanSHA256}, {"edit", c.EditSHA256}} {
		b, err := hex.DecodeString(p.value)
		if err != nil || len(b) != 32 || hex.EncodeToString(b) != p.value {
			return fmt.Errorf("invalid recovered %s SHA-256", p.name)
		}
	}
	return nil
}

// RegionConfig selects a bounded native 8-bit instruction replacement.
// All game addresses and permitted edits are supplied by the consumer.
type RegionConfig struct {
	Start        uint32   `json:"start"`
	Bytes        int      `json:"bytes"`
	CodeSHA256   string   `json:"code_sha256"`
	DataBank     uint8    `json:"data_bank"`
	Cells        []uint32 `json:"cells"`
	EditAddress  uint32   `json:"edit_address"`
	Original     uint8    `json:"original"`
	Replacements []uint8  `json:"replacements"`
}

func (r RegionConfig) validate() error {
	if r.Start > 0xffffff || r.Start&0xffff < 0x8000 || r.Bytes < 1 || r.Bytes > 32768 || uint64(r.Start&0xffff)+uint64(r.Bytes) > 65536 || len(r.Cells) == 0 || len(r.Cells) > 256 || r.EditAddress < r.Start || uint64(r.EditAddress) >= uint64(r.Start)+uint64(r.Bytes) {
		return fmt.Errorf("invalid bounded replacement profile")
	}
	if r.DataBank > 0x3f && (r.DataBank < 0x80 || r.DataBank > 0xbf) {
		return fmt.Errorf("replacement data bank must map low WRAM")
	}
	if b, err := hex.DecodeString(r.CodeSHA256); err != nil || len(b) != 32 || hex.EncodeToString(b) != r.CodeSHA256 {
		return fmt.Errorf("invalid replacement code SHA-256")
	}
	seen := map[uint32]bool{}
	for _, a := range r.Cells {
		if a>>16 != uint32(r.DataBank) || a&0xffff >= 0x2000 || seen[a] {
			return fmt.Errorf("invalid replacement RAM cell")
		}
		seen[a] = true
	}
	if len(r.Replacements) == 0 || len(r.Replacements) > 256 {
		return fmt.Errorf("missing bounded replacement values")
	}
	values := map[uint8]bool{}
	for _, v := range r.Replacements {
		if values[v] {
			return fmt.Errorf("duplicate replacement value")
		}
		values[v] = true
	}
	if !values[r.Original] {
		return fmt.Errorf("original immediate is absent from replacement values")
	}
	return nil
}

// Allows reports whether value is an explicitly permitted immediate replacement.
func (r RegionConfig) Allows(value uint8) bool {
	for _, v := range r.Replacements {
		if v == value {
			return true
		}
	}
	return false
}

// PrepareRecovered derives source identities from owned ROM bytes and a profile.
// Exactly one profile is required; there is no built-in game selection.
func PrepareRecovered(rom []byte, addend uint8, profiles ...RegionConfig) (RecoveredConfig, error) {
	if len(rom) == 0 || len(rom) > 4<<20 || len(profiles) != 1 {
		return RecoveredConfig{}, fmt.Errorf("owned ROM and one replacement profile required")
	}
	source, err := recoverSource(append([]byte(nil), rom...), addend, profiles[0])
	if err != nil {
		return RecoveredConfig{}, err
	}
	return recoveredPins(source), nil
}

func recoveredPins(s decomp.TimedSource) RecoveredConfig {
	return RecoveredConfig{SourceSHA256: s.SourceSHA256, IRSHA256: s.IRSHA256, EditedIRSHA256: s.EditedIRSHA256, PlanSHA256: s.PlanSHA256, EditSHA256: s.EditSHA256}
}

func recoverSource(rom []byte, addend uint8, profile RegionConfig) (decomp.TimedSource, error) {
	if err := profile.validate(); err != nil {
		return decomp.TimedSource{}, err
	}
	if !profile.Allows(addend) {
		return decomp.TimedSource{}, fmt.Errorf("immediate outside replacement profile")
	}
	offset := int((profile.Start>>16&127)*32768 + (profile.Start & 32767))
	if offset+profile.Bytes > len(rom) || digest(rom[offset:offset+profile.Bytes]) != profile.CodeSHA256 {
		return decomp.TimedSource{}, fmt.Errorf("replacement code differs from pinned ROM")
	}
	editOffset := int(profile.EditAddress - profile.Start)
	code := rom[offset : offset+profile.Bytes]
	boundary := false
	for i := 0; i < len(code); {
		if i == editOffset {
			boundary = true
			break
		}
		n := instructionLength(code[i])
		if n == 0 || i+n > len(code) {
			break
		}
		i += n
	}
	if !boundary || editOffset+1 >= len(code) || code[editOffset] != 0x69 || code[editOffset+1] != profile.Original {
		return decomp.TimedSource{}, fmt.Errorf("profile edit is not the pinned ADC immediate")
	}
	region, err := decomp.DecodeRegionFromBytes(rom[offset:offset+profile.Bytes], profile.Start, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 100)
	if err != nil {
		return decomp.TimedSource{}, fmt.Errorf("decode replacement: %w", err)
	}
	plan := decomp.TimedPlan{Instructions: []decomp.TimedInstruction{
		{Opcode: 0xee, OperandBytes: 2, IdleBefore: 6},
		{Opcode: 0xad, OperandBytes: 2},
		{Opcode: 0xc9, OperandBytes: 1},
		{Opcode: 0xd0, OperandBytes: 1, TakenIdle: 6},
		{Opcode: 0x18, IdleBefore: 6},
		{Opcode: 0x69, OperandBytes: 1},
		{Opcode: 0x8d, OperandBytes: 2},
		{Opcode: 0x60, IdleBefore: 12, IdleAfter: 6},
	}}
	var edit *decomp.TimedImmediateEdit
	if addend != profile.Original {
		edit = &decomp.TimedImmediateEdit{Address: profile.EditAddress, Expected: profile.Original, Replacement: addend}
	}
	return decomp.GenerateTimedRegionC(region, rom, plan, edit)
}

func startRecovered(ctx context.Context, rom []byte, addend uint8, pins RecoveredConfig, profile RegionConfig) (*compiledSession, error) {
	if err := pins.validate(); err != nil {
		return nil, err
	}
	source, err := recoverSource(rom, addend, profile)
	if err != nil {
		return nil, err
	}
	if digest(source.OriginalIRJSON) != source.IRSHA256 || digest(source.EditedIRJSON) != source.EditedIRSHA256 || digest([]byte(source.Source)) != source.SourceSHA256 || digest(rom) != source.ROMSHA256 {
		return nil, fmt.Errorf("generated recovery material identity mismatch")
	}
	if recoveredPins(source) != pins {
		return nil, fmt.Errorf("recovered source, IR, plan or edit identity mismatch")
	}
	session, err := compileTimedSource(ctx, source.Source, addend, "generic_machine_ir")
	if err != nil {
		return nil, err
	}
	session.profile = profile
	session.code = append([]byte(nil), rom[int((profile.Start>>16&127)*32768+(profile.Start&32767)):int((profile.Start>>16&127)*32768+(profile.Start&32767))+profile.Bytes]...)
	session.report.IRSHA256 = source.IRSHA256
	session.report.EditedIRSHA256 = source.EditedIRSHA256
	session.report.OriginalIRJSON = append([]byte(nil), source.OriginalIRJSON...)
	session.report.EditedIRJSON = append([]byte(nil), source.EditedIRJSON...)
	session.report.ROMSHA256 = source.ROMSHA256
	session.report.PlanSHA256 = source.PlanSHA256
	session.report.EditSHA256 = source.EditSHA256
	return session, nil
}
