package experiment

import (
	"crypto/sha256"
	"fmt"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
	"testing"
)

func targetFixture() (Target, []byte, decomp.ReplayCase) {
	rom := make([]byte, 32768)
	rom[0] = 0x60
	sha := fmt.Sprintf("%x", sha256.Sum256(rom))
	code := fmt.Sprintf("%x", sha256.Sum256(rom[:1]))
	t := Target{ID: "synthetic", ROMSHA256: sha, Start: 0x8000, End: 0x8001, CodeSHA256: code, EntryContext: recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, Parameter: Parameter{Field: Field{Name: "synthetic delta", Address: 0x7e0b7c, Bytes: 2, Signed: true}, Minimum: -4, Maximum: 4, Units: "pixels per invocation"}, Effects: []Field{{Name: "synthetic position", Address: 0x7e0022, Bytes: 2}}, Crosswalk: "synthetic fixture, not observed game behavior", Scope: "input validation only"}
	c := decomp.ReplayCase{ROMSHA256: sha, InitialState: decomp.CPUState{PC: 0x8000, P: 0x30}, InitialMemory: []decomp.MemoryCell{{Address: 0xb7c, Value: 0}, {Address: 0x800b7d, Value: 0}}}
	return t, rom, c
}
func TestTargetValidation(t *testing.T) {
	d, rom, c := targetFixture()
	if err := d.Validate(rom, c); err != nil {
		t.Fatal(err)
	}
}
func TestTargetRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Target, []byte, *decomp.ReplayCase)
	}{
		{"ROM mutation", func(d *Target, r []byte, c *decomp.ReplayCase) { r[0]++ }},
		{"code identity", func(d *Target, r []byte, c *decomp.ReplayCase) { d.CodeSHA256 = "wrong" }},
		{"wrong entry", func(d *Target, r []byte, c *decomp.ReplayCase) { c.InitialState.PC++ }},
		{"unknown width", func(d *Target, r []byte, c *decomp.ReplayCase) { d.EntryContext.M = "unknown" }},
		{"decimal", func(d *Target, r []byte, c *decomp.ReplayCase) { c.InitialState.P |= 8 }},
		{"direct page", func(d *Target, r []byte, c *decomp.ReplayCase) { c.InitialState.D = 1 }},
		{"missing parameter", func(d *Target, r []byte, c *decomp.ReplayCase) { c.InitialMemory = nil }},
		{"alias conflict", func(d *Target, r []byte, c *decomp.ReplayCase) {
			c.InitialMemory = append(c.InitialMemory, decomp.MemoryCell{Address: 0x7e0b7c, Value: 1})
		}},
		{"MMIO parameter", func(d *Target, r []byte, c *decomp.ReplayCase) { d.Parameter.Field.Address = 0x2104 }},
		{"range too large", func(d *Target, r []byte, c *decomp.ReplayCase) { d.Parameter.Maximum = 65535 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, rom, c := targetFixture()
			tt.change(&d, rom, &c)
			if err := d.Validate(rom, c); err == nil {
				t.Fatal("accepted invalid target input")
			}
		})
	}
}
func ExampleTarget_ValidateValue() {
	t := Target{Parameter: Parameter{Field: Field{Bytes: 2, Signed: true}, Minimum: -4, Maximum: 4}}
	fmt.Println(t.ValidateValue(1) == nil, t.ValidateValue(5) == nil)
	// Output: true false
}

func TestCarryContract(t *testing.T) {
	d, rom, c := targetFixture()
	d.EntryContext.C = "clear"
	c.InitialState.P |= 1
	if err := d.Validate(rom, c); err == nil {
		t.Fatal("accepted carry mismatch")
	}
	d.EntryContext.C = "set"
	if err := d.Validate(rom, c); err != nil {
		t.Fatal(err)
	}
	d.EntryContext.C = "unknown"
	if err := d.Validate(rom, c); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalAddressBounds(t *testing.T) {
	for _, start := range []uint32{0x1008000, 0xff008000} {
		d, rom, c := targetFixture()
		d.Start = start
		d.End = start + 1
		if err := d.Validate(rom, c); err == nil || err.Error() != "unsupported bounded LoROM region" {
			t.Fatalf("address %x: %v", start, err)
		}
	}
}

func ExampleTarget_Validate() {
	d, rom, c := targetFixture()
	fmt.Println(d.Validate(rom, c) == nil)
	// Output: true
}
