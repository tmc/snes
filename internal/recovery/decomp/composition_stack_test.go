package decomp

import (
	"context"
	"testing"
)

func TestCompositionAlteredReturnTargets(t *testing.T) {
	tests := []struct {
		name   string
		code   []byte
		target uint16
	}{
		{"unknown continuation", []byte{0x20, 4, 0x80, 0x6b, 0xa9, 0, 0x8d, 0xf9, 1, 0x60}, 0x8001},
		{"other call continuation", []byte{0x20, 7, 0x80, 0x20, 0x10, 0x80, 0x6b, 0xa9, 5, 0x8d, 0xf9, 1, 0x60, 0xea, 0xea, 0xea, 0x60}, 0x8006},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := DecodeRegionWithConfig(compositionConfig(tt.code))
			if err != nil {
				t.Fatal(err)
			}
			reg.Name = "altered_return"
			src, err := GenerateRegionC(reg)
			if err != nil {
				t.Fatal(err)
			}
			rom := make([]byte, 32768)
			copy(rom, tt.code)
			initial := CPUState{A: 0x5A00, S: 0x1FA, PC: 0x8000, P: 0x30}
			mem := []MemoryCell{{Address: 0x7E01FB, Value: byte(tt.target - 1)}, {Address: 0x7E01FC, Value: byte((tt.target - 1) >> 8)}, {Address: 0x7E01FD, Value: 0}}
			ref, err := runSyntheticCPU(t, rom, initial, mem, 0, tt.target-1)
			if err != nil {
				t.Fatal(err)
			}
			rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_altered_return", rom, []ReplayCase{{InitialState: initial, InitialMemory: mem}})
			if err != nil {
				t.Fatal(err)
			}
			r := rs[0]
			if !r.MissingRead || r.MissingAddr != uint32(tt.target) || r.NextPC != uint32(tt.target) {
				t.Fatalf("altered return not refused: %+v", r)
			}
			if ok, why := CompareCPUStates(ref.State, r.State); !ok {
				t.Fatal(why)
			}
			if ok, why := CompareWrites(ref.Writes, r.Writes); !ok {
				t.Fatal(why)
			}
		})
	}
}

func TestCompositionUnexpectedReturnKind(t *testing.T) {
	cfg := compositionConfig([]byte{0x20, 4, 0x80, 0x6b, 0x60})
	reg, err := DecodeRegionWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reg.Name = "return_kind"
	for _, b := range reg.Blocks {
		for i := range b.Statements {
			if b.Statements[i].Address == 0x8004 {
				b.Statements[i].TargetTemp = "rtl"
			}
		}
	}
	src, err := GenerateRegionC(reg)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_return_kind", nil, []ReplayCase{{InitialState: CPUState{S: 0x1FA, PC: 0x8000, P: 0x30}}})
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	if !r.MissingRead || r.MissingAddr != 0x8004 || r.NextPC != 0x8004 || r.State.S != 0x1F8 || len(r.Writes) != 2 {
		t.Fatalf("unexpected RTL not refused before pull: %+v", r)
	}
}

func TestCompositionBlockContextGuard(t *testing.T) {
	cfg := compositionConfig([]byte{0x20, 7, 0x80, 0xa9, 0x34, 0x6b, 0x6b, 0xc2, 0x20, 0x60})
	reg, err := DecodeRegionWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reg.Name = "block_context"
	for _, b := range reg.Blocks {
		if b.StartAddress == 0x8003 {
			b.EntryContext.M = "set"
		}
	}
	src, err := GenerateRegionC(reg)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := compileAndRunRegionWithROM(context.Background(), t, src, "execute_block_context", nil, []ReplayCase{{InitialState: CPUState{A: 0x5A00, S: 0x1FA, PC: 0x8000, P: 0x30}}})
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	if !r.MissingRead || r.MissingAddr != 0x8003 || r.NextPC != 0x8003 || r.State.A != 0x5A00 {
		t.Fatalf("wrong continuation context not refused before LDA: %+v", r)
	}
}
