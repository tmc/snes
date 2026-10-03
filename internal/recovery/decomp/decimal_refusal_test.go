package decomp

import (
	"context"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func TestAcceptanceCalleeDecimalRefusal(t *testing.T) {
	code := []byte{0x20, 0x06, 0x80, 0x69, 0x01, 0x6b, 0xf8, 0x60}
	region, err := DecodeRegionWithConfig(DecodeRegionConfig{CodeBytes: code, EntryAddr: 0x8000, EntryCtx: recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}, AllowInternalJSR: true, MaxSteps: 100})
	if err != nil {
		t.Logf("decode refused: %v", err)
		return
	}
	region.Name = "decimal_probe"
	source, err := GenerateRegionC(region)
	if err != nil {
		t.Logf("generate refused: %v", err)
		return
	}
	rom := make([]byte, 32768)
	copy(rom, code)
	state := CPUState{A: 0x5a09, S: 0x1fa, PC: 0x8000, P: 0x30}
	memory := []MemoryCell{{Address: 0x7e01fb, Value: 0}, {Address: 0x7e01fc, Value: 0x90}, {Address: 0x7e01fd, Value: 0}}
	reference, err := runSyntheticCPU(t, rom, state, memory, 0, 0x9000)
	if err != nil {
		t.Fatal(err)
	}
	results, err := compileAndRunRegionWithROM(context.Background(), t, source, "execute_decimal_probe", rom, []ReplayCase{{InitialState: state, InitialMemory: memory}})
	if err != nil {
		t.Fatal(err)
	}
	result := results[0]
	t.Logf("reference A=%04x P=%02x C A=%04x P=%02x refusal=%v", reference.State.A, reference.State.P, result.State.A, result.State.P, result.MissingRead)
	if !result.MissingRead {
		t.Fatalf("unsupported callee SED accepted; expected refusal")
	}
}

func TestRegionDecimalEnablingInstructions(t *testing.T) {
	tests := []struct {
		name string
		code []byte
	}{
		{"direct SED", []byte{0xf8, 0x69, 1, 0x6b}},
		{"callee SED", []byte{0x20, 6, 0x80, 0x69, 1, 0x6b, 0xf8, 0x60}},
		{"nested SED", []byte{0x20, 4, 0x80, 0x6b, 0x20, 8, 0x80, 0x60, 0xf8, 0x60}},
		{"direct SEP D", []byte{0xe2, 8, 0x69, 1, 0x6b}},
		{"callee SEP D", []byte{0x20, 4, 0x80, 0x6b, 0xe2, 8, 0x60}},
		{"SED then CLD", []byte{0xf8, 0xd8, 0x6b}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeRegionWithConfig(compositionConfig(tt.code)); err == nil {
				t.Fatal("accepted unsupported decimal enabling instruction")
			}
		})
	}
	if _, err := DecodeRegionWithConfig(compositionConfig([]byte{0xd8, 0x69, 1, 0x6b})); err != nil {
		t.Fatalf("CLD unexpectedly refused: %v", err)
	}
}

func TestRegionDecimalIRWrites(t *testing.T) {
	tests := []struct {
		name   string
		stmt   Statement
		refuse bool
	}{
		{"set D", Statement{Kind: "set_flag", TargetFlag: FlagD, FlagVal: true}, true},
		{"dynamic D", Statement{Kind: "set_flag", TargetFlag: FlagD, Expr: &ConstExpr{Value: 1}}, true},
		{"status mask D", Statement{Kind: "set_flag_mask", Expr: &ConstExpr{Value: 8}}, true},
		{"unknown mask", Statement{Kind: "set_flag_mask"}, true},
		{"assign P", Statement{Kind: "assign_reg", TargetReg: RegP, Expr: &ConstExpr{Value: 0x38}}, true},
		{"pull P", Statement{Kind: "pull_reg", TargetReg: RegP}, true},
		{"clear D", Statement{Kind: "set_flag", TargetFlag: FlagD}, false},
		{"set C", Statement{Kind: "set_flag_mask", Expr: &ConstExpr{Value: 1}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := DecodeRegionWithConfig(compositionConfig([]byte{0xea, 0x6b}))
			if err != nil {
				t.Fatal(err)
			}
			reg.Blocks[0].Statements = append([]Statement{tt.stmt}, reg.Blocks[0].Statements...)
			_, err = GenerateRegionC(reg)
			if (err != nil) != tt.refuse {
				t.Fatalf("generation error=%v, refusal want %v", err, tt.refuse)
			}
		})
	}
}
