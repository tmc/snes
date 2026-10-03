package decomp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func semanticTestRegion(t *testing.T, code []byte) *RegionIR {
	t.Helper()
	r, err := DecodeRegionFromBytes(append(append([]byte{}, code...), 0x60), 0x008000, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func semanticRun(t *testing.T, region *RegionIR, source string, cases []ReplayCase) []ExecResult {
	t.Helper()
	p := filepath.Join(t.TempDir(), "semantic.c")
	if err := os.WriteFile(p, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := NewCompiledRegionRunner(context.Background(), p, "execute_"+region.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	for i := range cases {
		cases[i].InitialMemory = append(append([]MemoryCell{}, cases[i].InitialMemory...), MemoryCell{0x7e01fa, 0}, MemoryCell{0x7e01fb, 0x80})
	}
	results, err := runner.RunBatch(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func TestSemanticFiniteADC(t *testing.T) {
	for _, addend := range []byte{0, 3, 5, 255} {
		t.Run(fmt.Sprint(addend), func(t *testing.T) { testSemanticFiniteADC(t, addend) })
	}
}

func testSemanticFiniteADC(t *testing.T, addend byte) {
	t.Helper()
	// Byte load, immediate ADC and write: incoming carry is deliberately live.
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0x69, addend, 0x85, 0x11})
	transformed, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transformed.Source, "_sum = value_") {
		t.Fatal("ADC not recovered")
	}
	original, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	var cases []ReplayCase
	for v := 0; v < 256; v++ {
		for carry := 0; carry < 2; carry++ {
			cases = append(cases, ReplayCase{InitialState: CPUState{A: 0xab00, S: 0x1f9, P: uint8(0x30 | carry), PC: 0x8000}, InitialMemory: []MemoryCell{{0x7e0010, uint8(v)}}})
		}
	}
	a := semanticRun(t, r, original, cases)
	b := semanticRun(t, r, transformed.Source, cases)
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("case%d original%+v transformed%+v", i, a[i], b[i])
		}
		v := i / 2
		c := i % 2
		sum := v + int(addend) + c
		want := uint8(0x30)
		if sum > 255 {
			want |= 1
		}
		if uint8(sum) == 0 {
			want |= 2
		}
		if sum&128 != 0 {
			want |= 128
		}
		if ^(v^int(addend))&(v^sum)&128 != 0 {
			want |= 64
		}
		if b[i].State.A != 0xab00|uint16(uint8(sum)) || b[i].State.P != want {
			t.Fatalf("finite state%d: %+v", i, b[i])
		}
	}
	flagMutation := strings.ReplaceAll(transformed.Source, "s.p |= 0x40", "s.p &= ~0x40")
	flagBad := semanticRun(t, r, flagMutation, cases)
	detected := false
	for i := range flagBad {
		if !reflect.DeepEqual(flagBad[i], b[i]) {
			detected = true
			break
		}
	}
	if !detected && addend != 0 {
		t.Fatal("overflow flag mutation undetected")
	}
	mutated := strings.Replace(transformed.Source, fmt.Sprintf("+ 0x%02X +", addend), fmt.Sprintf("+ 0x%02X +", addend+1), 1)
	bad := semanticRun(t, r, mutated, cases[:1])
	if reflect.DeepEqual(bad[0], b[0]) {
		t.Fatal("arithmetic mutation undetected")
	}
}

func TestSemanticReadWriteOrder(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0x85, 0x11, 0xa5, 0x11, 0x69, 5, 0x85, 0x10})
	transformed, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := GenerateRegionC(r)
	cases := []ReplayCase{{InitialState: CPUState{S: 0x1f9, P: 0x30, PC: 0x8000}, InitialMemory: []MemoryCell{{0x7e0010, 9}}}}
	a := semanticRun(t, r, original, cases)
	b := semanticRun(t, r, transformed.Source, cases)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("order original%+v transformed%+v", a, b)
	}
	lines := strings.Split(transformed.Source, "\n")
	for i, line := range lines {
		if strings.Contains(line, "mem_write8(&res,") {
			lines[i] = ""
			break
		}
	}
	dropped := semanticRun(t, r, strings.Join(lines, "\n"), cases)
	if reflect.DeepEqual(dropped, b) {
		t.Fatal("ordered write mutation undetected")
	}
	// Missing input and MMIO paths must preserve refusal flags too.
	for _, code := range [][]byte{{0xa5, 0x12, 0x69, 5}, {0xad, 0x00, 0x21, 0x69, 5}} {
		r := semanticTestRegion(t, code)
		s, err := GenerateSemanticRegionC(r)
		if err != nil {
			t.Fatal(err)
		}
		o, _ := GenerateRegionC(r)
		if !reflect.DeepEqual(semanticRun(t, r, o, cases), semanticRun(t, r, s.Source, cases)) {
			t.Fatal("refusal changed")
		}
	}
}

func TestSemanticOrderedWritesMutation(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0x85, 0x11, 0x85, 0x12})
	source, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	cases := []ReplayCase{{InitialState: CPUState{PC: 0x8000, S: 0x1f9, P: 0x30}, InitialMemory: []MemoryCell{{0x7e0010, 9}}}}
	expected := semanticRun(t, r, source.Source, cases)
	lines := strings.Split(source.Source, "\n")
	var stores []int
	for i, l := range lines {
		if strings.Contains(l, "mem_write8(&res,") {
			stores = append(stores, i)
		}
	}
	if len(stores) != 2 {
		t.Fatalf("stores%d", len(stores))
	}
	lines[stores[0]], lines[stores[1]] = lines[stores[1]], lines[stores[0]]
	got := semanticRun(t, r, strings.Join(lines, "\n"), cases)
	if reflect.DeepEqual(got, expected) {
		t.Fatal("write order mutation undetected")
	}
	if got[0].State != expected[0].State {
		t.Fatal("control should preserve terminal register state")
	}
	if equal, _ := CompareWrites(got[0].Writes, expected[0].Writes); equal {
		t.Fatal("ordered effects accepted swapped writes")
	}
}

func TestSemanticSignExtension(t *testing.T) {
	// LDA $10; CMP #$80; SBC $10; EOR #$FF; STA $11
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0xc9, 0x80, 0xe5, 0x10, 0x49, 0xff, 0x85, 0x11})
	transformed, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	if transformed.TransformedSignExtensions != 1 {
		t.Fatalf("expected 1 sign extension, got %d", transformed.TransformedSignExtensions)
	}
	if !strings.Contains(transformed.Source, "& 0x80) ? 0xFF : 0x00") {
		t.Fatal("sign extension expression not found in source")
	}
	original, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}

	var cases []ReplayCase
	for v := 0; v < 256; v++ {
		cases = append(cases, ReplayCase{
			InitialState:  CPUState{A: 0xab00, S: 0x1f9, P: 0x30, PC: 0x8000},
			InitialMemory: []MemoryCell{{0x7e0010, uint8(v)}},
		})
	}

	a := semanticRun(t, r, original, cases)
	b := semanticRun(t, r, transformed.Source, cases)
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("case%d original%+v transformed%+v", i, a[i], b[i])
		}
		wantByte := uint8(0x00)
		wantP := uint8(0x32) // Z set, N clear, C clear, V clear
		if i >= 128 {
			wantByte = 0xFF
			wantP = 0xB1 // N set, Z clear, C set, V clear
		}
		if b[i].State.A != 0xab00|uint16(wantByte) || b[i].State.P != wantP {
			t.Fatalf("case%d state mismatch: got A=%04X P=%02X, want A=%04X P=%02X", i, b[i].State.A, b[i].State.P, 0xab00|uint16(wantByte), wantP)
		}
		if len(b[i].Writes) != 1 || b[i].Writes[0].Address != 0x7e0011 || b[i].Writes[0].Value != wantByte {
			t.Fatalf("case%d writes mismatch: %+v", i, b[i].Writes)
		}
	}

	// Mutate arithmetic and verify detection
	mutated := strings.Replace(transformed.Source, "? 0xFF : 0x00", "? 0xFE : 0x00", 1)
	bad := semanticRun(t, r, mutated, cases[130:131])
	if reflect.DeepEqual(bad[0], b[130]) {
		t.Fatal("sign extension arithmetic mutation undetected")
	}
}

func TestSemanticASLCascade(t *testing.T) {
	tests := []struct {
		name       string
		shifts     int
		code       []byte
		wantOp     string
		wantShifts int
	}{
		{"single_shift", 1, []byte{0xa5, 0x10, 0x0a, 0x85, 0x11}, "ASL", 1},
		{"double_shift", 2, []byte{0xa5, 0x10, 0x0a, 0x0a, 0x85, 0x11}, "ASL_CASCADE", 1},
		{"triple_shift", 3, []byte{0xa5, 0x10, 0x0a, 0x0a, 0x0a, 0x85, 0x11}, "ASL_CASCADE", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := semanticTestRegion(t, tt.code)
			transformed, err := GenerateSemanticRegionC(r)
			if err != nil {
				t.Fatal(err)
			}
			if transformed.TransformedShifts != tt.wantShifts {
				t.Fatalf("expected %d transformed shifts, got %d", tt.wantShifts, transformed.TransformedShifts)
			}
			shiftExpr := fmt.Sprintf("<< %d) & 0xFF", tt.shifts)
			if !strings.Contains(transformed.Source, shiftExpr) {
				t.Fatalf("shift expression %q not found in source:\n%s", shiftExpr, transformed.Source)
			}

			original, err := GenerateRegionC(r)
			if err != nil {
				t.Fatal(err)
			}

			var cases []ReplayCase
			for v := 0; v < 256; v++ {
				cases = append(cases, ReplayCase{
					InitialState:  CPUState{A: 0xcd00, S: 0x1f9, P: 0x30, PC: 0x8000},
					InitialMemory: []MemoryCell{{0x7e0010, uint8(v)}},
				})
			}

			a := semanticRun(t, r, original, cases)
			b := semanticRun(t, r, transformed.Source, cases)
			for i := range a {
				if !reflect.DeepEqual(a[i], b[i]) {
					t.Fatalf("case%d original%+v transformed%+v", i, a[i], b[i])
				}
				v := uint8(i)
				wantVal := uint8((int(v) << tt.shifts) & 0xFF)
				wantP := uint8(0x30)
				if (int(v) & (1 << (8 - tt.shifts))) != 0 {
					wantP |= 1 // Carry
				}
				if wantVal == 0 {
					wantP |= 2 // Zero
				}
				if (wantVal & 0x80) != 0 {
					wantP |= 0x80 // Negative
				}
				if b[i].State.A != 0xcd00|uint16(wantVal) || b[i].State.P != wantP {
					t.Fatalf("case%d mismatch: got A=%04X P=%02X, want A=%04X P=%02X", i, b[i].State.A, b[i].State.P, 0xcd00|uint16(wantVal), wantP)
				}
				if len(b[i].Writes) != 1 || b[i].Writes[0].Address != 0x7e0011 || b[i].Writes[0].Value != wantVal {
					t.Fatalf("case%d write mismatch: %+v", i, b[i].Writes)
				}
			}

			// Mutate shift count and verify detection
			mutated := strings.Replace(transformed.Source, shiftExpr, fmt.Sprintf("<< %d) & 0xFF", tt.shifts+1), 1)
			bad := semanticRun(t, r, mutated, cases[1:2])
			if reflect.DeepEqual(bad[0], b[1]) {
				t.Fatal("shift count mutation undetected")
			}
		})
	}
}

