package decomp

import (
	"context"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func compositionConfig(code []byte) DecodeRegionConfig {
	return DecodeRegionConfig{CodeBytes: code, EntryAddr: 0x8000, EntryCtx: recovery.Context{E: "clear", M: "set", X: "set", C: "clear"}, AllowInternalJSR: true, MaxSteps: 100}
}

func TestCallContextBoundaries(t *testing.T) {
	tests := []struct {
		name string
		code []byte
		want string
	}{
		{"PLP", []byte{0x20, 4, 0x80, 0x6b, 0x28, 0x60}, "PLP"},
		{"XCE", []byte{0x20, 4, 0x80, 0x6b, 0xfb, 0x60}, "XCE"},
		{"RTI", []byte{0x20, 4, 0x80, 0x6b, 0x40}, "RTI"},
		{"JSL", []byte{0x20, 4, 0x80, 0x6b, 0x22, 4, 0x80, 0, 0x60}, "JSL"},
		{"RTL callee", []byte{0x20, 4, 0x80, 0x6b, 0x6b}, "rather than RTS"},
		{"decimal", []byte{0x20, 4, 0x80, 0x6b, 0xe2, 8, 0x60}, "decimal"},
		{"direct recursion", []byte{0x20, 4, 0x80, 0x6b, 0x20, 4, 0x80, 0x60}, "recursive"},
		{"mutual recursion", []byte{0x20, 4, 0x80, 0x6b, 0x20, 8, 0x80, 0x60, 0x20, 4, 0x80, 0x60}, "recursive"},
		{"nonreturning loop", []byte{0x20, 4, 0x80, 0x6b, 0x80, 0xfe}, "no reachable RTS"},
		{"conflicting join", []byte{0x20, 4, 0x80, 0x6b, 0xd0, 2, 0xc2, 0x20, 0x60}, "conflicting contexts"},
		{"unknown M", []byte{0x20, 4, 0x80, 0x6b, 0x60}, "unresolved entry context M"},
		{"unknown X", []byte{0x20, 4, 0x80, 0x6b, 0x60}, "unresolved entry context X"},
		{"unknown E", []byte{0x20, 4, 0x80, 0x6b, 0x60}, "unresolved entry context E"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := compositionConfig(tt.code)
			if tt.name == "unknown M" {
				cfg.EntryCtx.M = "unknown"
			}
			if tt.name == "unknown X" {
				cfg.EntryCtx.X = "unknown"
			}
			if tt.name == "unknown E" {
				cfg.EntryCtx.E = "unknown"
			}
			_, err := DecodeRegionWithConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("decode error=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestCallContextOperandTarget(t *testing.T) {
	// The branch targets the operand of a reachable LDA, not an instruction.
	cfg := compositionConfig([]byte{0x20, 4, 0x80, 0x6b, 0xd0, 1, 0xa9, 0x60, 0x60})
	if _, err := DecodeRegionWithConfig(cfg); err == nil {
		t.Fatal("accepted callee operand target")
	}
}

func TestCallContextDepthAndBudget(t *testing.T) {
	code := []byte{0x20, 4, 0x80, 0x6b}
	for i := 0; i < maxCallContextDepth; i++ {
		addr := uint16(0x8004 + 4*(i+1))
		code = append(code, 0x20, byte(addr), byte(addr>>8), 0x60)
	}
	code = append(code, 0x60)
	if _, err := DecodeRegionWithConfig(compositionConfig(code)); err == nil || !strings.Contains(err.Error(), "depth exceeds") {
		t.Fatalf("depth error=%v", err)
	}
	cfg := compositionConfig([]byte{0x20, 4, 0x80, 0x6b, 0xc2, 0x20, 0x60})
	a := &callContextAnalyzer{code: cfg.CodeBytes, entry: cfg.EntryAddr, cfg: cfg, cache: make(map[callContextKey]callContextWidths), active: make(map[uint32]bool), remaining: 1}
	if _, err := a.returnContext(0x8004, cfg.EntryCtx); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("budget error=%v", err)
	}
}

func TestCallContextNestedAndMultipleCalls(t *testing.T) {
	tests := []struct {
		name  string
		code  []byte
		wantX uint16
	}{
		{"nested", []byte{0x20, 8, 0x80, 0xa2, 0x34, 0x6b, 0x6b, 0xea, 0x20, 12, 0x80, 0x60, 0xc2, 0x10, 0x60}, 0x6b34},
		{"multiple", []byte{0x20, 13, 0x80, 0xe2, 0x10, 0x20, 13, 0x80, 0xa2, 0x34, 0x6b, 0x6b, 0xea, 0xc2, 0x10, 0x60}, 0x6b34},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := compositionConfig(tt.code)
			reg, err := DecodeRegionWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			reg.Name = "test_calls"
			src, err := GenerateRegionC(reg)
			if err != nil {
				t.Fatal(err)
			}
			rom := make([]byte, 32768)
			copy(rom, tt.code)
			initial := CPUState{A: 0x5a00, S: 0x1fa, PC: 0x8000, P: 0x30}
			mem := []MemoryCell{{Address: 0x7e01fb, Value: 0}, {Address: 0x7e01fc, Value: 0x90}, {Address: 0x7e01fd, Value: 0}}
			ref, err := runSyntheticCPU(t, rom, initial, mem, 0, 0x9000)
			if err != nil {
				t.Fatal(err)
			}
			rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_test_calls", rom, []ReplayCase{{InitialState: initial, InitialMemory: mem}})
			if err != nil {
				t.Fatal(err)
			}
			r := rs[0]
			if r.MissingRead || r.MMIOAccess || r.WriteOverflow {
				t.Fatalf("execution refused: %+v", r)
			}
			if ok, why := CompareCPUStates(ref.State, r.State); !ok {
				t.Fatal(why)
			}
			if ref.NextPC != r.NextPC {
				t.Fatalf("next PC got%06x want%06x", r.NextPC, ref.NextPC)
			}
			if ok, why := CompareWrites(ref.Writes, r.Writes); !ok {
				t.Fatal(why)
			}
			if r.TotalWrites != uint32(len(ref.Writes)) {
				t.Fatal("write count mismatch")
			}
			if r.State.X != tt.wantX {
				t.Fatalf("X=%04x want%04x", r.State.X, tt.wantX)
			}
			mutated := strings.ReplaceAll(src, "0x6B34", "0x0034")
			if mutated == src {
				t.Fatal("mutation did not change generated immediate")
			}
			bad, err := compileAndRunRegionWithROM(context.Background(), t, mutated, "execute_test_calls", rom, []ReplayCase{{InitialState: initial, InitialMemory: mem}})
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := CompareCPUStates(ref.State, bad[0].State); ok {
				t.Fatal("operand mutation escaped CPU comparison")
			}

		})
	}
}

func TestCallContextMultipleReturns(t *testing.T) {
	// Both callee return paths set M=0; the caller consumes one wide immediate.
	code := []byte{0x20, 7, 0x80, 0xa9, 0x34, 0x6b, 0x6b, 0xd0, 3, 0xc2, 0x20, 0x60, 0xc2, 0x20, 0x60}
	reg, err := DecodeRegionWithConfig(compositionConfig(code))
	if err != nil {
		t.Fatal(err)
	}
	var wide bool
	for _, b := range reg.Blocks {
		for _, i := range b.Statements {
			if i.Address == 0x8003 && i.Width == Width16 {
				wide = true
			}
		}
	}
	if !wide {
		t.Fatal("caller immediate was not widened")
	}
}

func TestCallContextMemoization(t *testing.T) {
	cfg := compositionConfig([]byte{0x20, 4, 0x80, 0x6b, 0xc2, 0x20, 0x60})
	a := &callContextAnalyzer{code: cfg.CodeBytes, entry: cfg.EntryAddr, cfg: cfg, cache: make(map[callContextKey]callContextWidths), active: make(map[uint32]bool), remaining: 100}
	if _, err := a.returnContext(0x8004, cfg.EntryCtx); err != nil {
		t.Fatal(err)
	}
	left := a.remaining
	ctx := cfg.EntryCtx
	ctx.C = "set"
	got, err := a.returnContext(0x8004, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.C != "set" || got.M != "clear" || a.remaining != left {
		t.Fatalf("cached result=%+v remaining=%d want%d", got, a.remaining, left)
	}
}

func TestRegionRefusalTransfers(t *testing.T) {
	for _, tt := range []struct {
		name   string
		code   []byte
		target uint32
	}{{"BRA", []byte{0x80, 2}, 0x8004}, {"BRL", []byte{0x82, 1, 0}, 0x8004}, {"sequential", []byte{0xea}, 0x8001}} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := compositionConfig(tt.code)
			cfg.RefusalTargets = map[uint32]string{tt.target: "outside modeled closure"}
			reg, err := DecodeRegionWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			reg.Name = "refusal_transfer"
			src, err := GenerateRegionC(reg)
			if err != nil {
				t.Fatal(err)
			}
			initial := CPUState{A: 0x5a12, X: 0x12, Y: 0x34, P: 0x31, S: 0x1fa, PC: 0x8000}
			rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_refusal_transfer", nil, []ReplayCase{{InitialState: initial}})
			if err != nil {
				t.Fatal(err)
			}
			r := rs[0]
			expected := initial
			expected.PC = uint16(tt.target)
			expected.PB = uint8(tt.target >> 16)
			if !r.MissingRead || r.MissingAddr != tt.target || r.NextPC != tt.target || len(r.Writes) != 0 {
				t.Fatalf("bad refusal result: %+v", r)
			}
			if ok, why := CompareCPUStates(expected, r.State); !ok {
				t.Fatal(why)
			}
		})
	}
}

func TestCallContextLoopFuel(t *testing.T) {
	cfg := compositionConfig([]byte{0x20, 4, 0x80, 0x6b, 0xd0, 0xfe, 0x60})
	cfg.MaxSteps = 5
	reg, err := DecodeRegionWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reg.Name = "loop_fuel"
	src, err := GenerateRegionC(reg)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_loop_fuel", nil, []ReplayCase{{InitialState: CPUState{P: 0x30, S: 0x1fa, PC: 0x8000}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rs[0].MissingRead {
		t.Fatal("loop fuel exhausted without refusal")
	}
}
