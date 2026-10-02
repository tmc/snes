package decomp

import (
	"context"
	"encoding/json"
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

func TestSemanticConnected116(t *testing.T) {
	out := os.Getenv("SNES_SEMANTIC_OUTPUT")
	if out == "" {
		t.Skip("set SNES_SEMANTIC_OUTPUT for retained 116-case gate")
	}
	cases, rom := loadConnected116Cases(t)
	if len(cases) != 116 {
		t.Fatalf("cases%d", len(cases))
	}
	policy := buildConnectedPolicy(cases)
	v, err := NewEvidenceVerifierWithPolicy("", policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.PrefetchFixtures(cases); err != nil {
		t.Fatal(err)
	}
	for i := range cases {
		rec, err := v.Admit(&cases[i], nil, nil)
		if err != nil || !rec.Admitted {
			t.Fatalf("admission%d %v %s", i, err, rec.Reason)
		}
	}
	r, err := DecodeConnected(ConnectedConfig{ROM: rom, Spans: []CodeSpan{{0x0cc435, 0x0cc44f}, {0x008781, 0x00879c}, {0x0cc45b, 0x0cc47b}}, Entry: 0x0cc435, Context: recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, MaxInstructions: 100, MaxSteps: 1000, IndirectTargets: map[uint32][]uint32{0x008799: {0x0cc45b}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := GenerateSemanticRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := GenerateRegionC(r)
	if err = os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(n string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(out, n), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("original.c", []byte(original))
	write("semantic.c", []byte(source.Source))
	manifest, _ := json.MarshalIndent(source, "", "  ")
	write("semantic-manifest.json", manifest)
	newRunner, err := NewCompiledRegionRunnerWithROM(context.Background(), filepath.Join(out, "semantic.c"), "execute_"+r.Name, rom)
	if err != nil {
		t.Fatal(err)
	}
	defer newRunner.Close()
	transformed, err := newRunner.RunBatch(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	oldRunner, err := NewCompiledRegionRunnerWithROM(context.Background(), filepath.Join(out, "original.c"), "execute_"+r.Name, rom)
	if err != nil {
		t.Fatal(err)
	}
	defer oldRunner.Close()
	if err = oldRunner.BindRegion(r, "semantic-local-gate"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		Original    ReplayReceipt `json:"original"`
		Transformed ExecResult    `json:"transformed"`
		Match       bool          `json:"match"`
	}
	results := make([]result, len(cases))
	for i, c := range cases {
		proof, err := v.ExecuteThreeWayRoutineReplay(context.Background(), oldRunner, c)
		if err != nil || !proof.Matched || !proof.EffectsMatch {
			t.Fatalf("original%d %v %+v", i, err, proof)
		}
		match := reflect.DeepEqual(proof.CompiledC, transformed[i])
		if !match {
			t.Fatalf("transformed%d original%+v new%+v", i, proof.CompiledC, transformed[i])
		}
		results[i] = result{proof, transformed[i], match}
	}
	caseBytes, _ := json.MarshalIndent(cases, "", "  ")
	write("cases.json", caseBytes)
	policyBytes, _ := json.MarshalIndent(policy, "", "  ")
	write("policy-test-only.json", policyBytes)
	qualification, _ := json.MarshalIndent(map[string]any{"schema": "semantic-local-values-fresh-comparison-v1", "status": "PASS", "revision": os.Getenv("SNES_SEMANTIC_REVISION"), "selected": len(cases), "transformed_loads": source.TransformedLoads, "transformed_adc": source.TransformedADC, "fused_chains": source.FusedChains, "transformed_captured_proof_eligible": false, "original_source_sha256": source.OriginalSHA256, "transformed_source_sha256": source.SourceSHA256, "region_sha256": source.RegionSHA256, "rom_sha256": newRunner.ROMSHA256(), "compiler": newRunner.Compiler, "compiler_flags": newRunner.CompilerFlags, "transformed_binary_sha256": newRunner.binaryHash, "transformed_wrapper_sha256": newRunner.wrapperHash, "limits": "fresh comparison against locally admitted cases; complete exit state/next PC/ordered WRAM writes/refusals, no timing/independent hardware/unseen-path claim; transformed artifact has no borrowed admission receipt"}, "", "  ")
	write("qualification.json", qualification)
	encoded, _ := json.MarshalIndent(results, "", "  ")
	write("fresh-comparison.json", encoded)
	t.Logf("fresh original and transformed C: %d trace/GoCPU/C/C matches; %d recovered expressions; transformed has no borrowed admission receipt", len(cases), len(source.Expressions))
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
