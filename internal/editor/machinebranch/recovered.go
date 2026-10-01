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

// PrepareRecovered derives explicit source identities from owned ROM bytes.
// Addend 5 retains the ROM behavior; 6 edits only the first ADC immediate.
func PrepareRecovered(rom []byte, addend uint8) (RecoveredConfig, error) {
	if len(rom) == 0 || len(rom) > 4<<20 {
		return RecoveredConfig{}, fmt.Errorf("recovered ROM must be nonempty and at most 4 MiB")
	}
	source, err := recoverSource(append([]byte(nil), rom...), addend)
	if err != nil {
		return RecoveredConfig{}, err
	}
	return recoveredPins(source), nil
}

func recoveredPins(s decomp.TimedSource) RecoveredConfig {
	return RecoveredConfig{SourceSHA256: s.SourceSHA256, IRSHA256: s.IRSHA256, EditedIRSHA256: s.EditedIRSHA256, PlanSHA256: s.PlanSHA256, EditSHA256: s.EditSHA256}
}

func recoverSource(rom []byte, addend uint8) (decomp.TimedSource, error) {
	const offset = 0x6445b
	if addend != 5 && addend != 6 {
		return decomp.TimedSource{}, fmt.Errorf("recovered addend must be 5 or 6")
	}
	if len(rom) < offset+len(rotationBytes) || string(rom[offset:offset+len(rotationBytes)]) != string(rotationBytes) {
		return decomp.TimedSource{}, fmt.Errorf("rotation ROM vocabulary mismatch")
	}
	region, err := decomp.DecodeRegionFromBytes(rom[offset:offset+len(rotationBytes)], 0x0cc45b, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 100)
	if err != nil {
		return decomp.TimedSource{}, fmt.Errorf("decode recovered rotation: %w", err)
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
	if addend == 6 {
		edit = &decomp.TimedImmediateEdit{Address: 0x0cc46c, Expected: 5, Replacement: 6}
	}
	return decomp.GenerateTimedRegionC(region, rom, plan, edit)
}

func startRecovered(ctx context.Context, rom []byte, addend uint8, pins RecoveredConfig) (*compiledSession, error) {
	if err := pins.validate(); err != nil {
		return nil, err
	}
	source, err := recoverSource(rom, addend)
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
	session.report.IRSHA256 = source.IRSHA256
	session.report.EditedIRSHA256 = source.EditedIRSHA256
	session.report.OriginalIRJSON = append([]byte(nil), source.OriginalIRJSON...)
	session.report.EditedIRJSON = append([]byte(nil), source.EditedIRJSON...)
	session.report.ROMSHA256 = source.ROMSHA256
	session.report.PlanSHA256 = source.PlanSHA256
	session.report.EditSHA256 = source.EditSHA256
	return session, nil
}
