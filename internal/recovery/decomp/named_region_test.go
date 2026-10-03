package decomp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func namedCase(value byte, more ...MemoryCell) ReplayCase {
	mem := []MemoryCell{{Address: 0x7e0010, Value: value}, {Address: 0x7e01fa, Value: 0}, {Address: 0x7e01fb, Value: 0x80}}
	mem = append(mem, more...)
	return ReplayCase{InitialState: CPUState{A: 0xab00, S: 0x1f9, P: 0x30, PC: 0x8000}, InitialMemory: mem}
}

func namedRun(t *testing.T, r *RegionIR, source string, cases []ReplayCase) []ExecResult {
	t.Helper()
	path := filepath.Join(t.TempDir(), "region.c")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := NewCompiledRegionRunner(context.Background(), path, "execute_"+r.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	got, err := runner.RunBatch(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func namedTrace(t *testing.T, r *RegionIR, source string) string {
	t.Helper()
	const needle = "    uint32_t a = bus_canonical_addr(addr);"
	// The first occurrence is the write helper; the second is the read helper.
	pos := strings.Index(source, "static inline uint8_t mem_read8_raw(")
	if pos < 0 || !strings.Contains(source[pos:], needle) {
		t.Fatal("missing raw read helper")
	}
	source = source[:pos] + strings.Replace(source[pos:], needle, needle+"\n    printf(\"%06x,\", addr);", 1)
	driver := "\nstatic uint8_t input(void *ctx, uint32_t addr, bool *missing) { (void)ctx; (void)addr; *missing=false; return 1; }\n"
	driver += "int main(void) { cpu_state_t s={0}; s.s=0x1f9; s.pc=0x8000; s.p=0x30; " + "exec_result_t r=execute_" + r.Name + "(s,input,0); "
	driver += "printf(\"|%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u,%u\", "
	driver += "r.state.a,r.state.x,r.state.y,r.state.s,r.state.pc,r.state.d,r.state.db,r.state.pb,r.state.p,r.state.e,"
	driver += "r.next_pc,r.has_next,r.num_writes,r.total_writes,r.write_overflow,r.uninitialized_read,r.uninitialized_addr,r.mmio_access,r.mmio_addr,r.fuel_exhausted,MAX_WRITES); "
	driver += "for(int i=0;i<r.num_writes;i++)printf(\";%u:%u\",r.writes[i].address,r.writes[i].value); return 0; }\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.c")
	bin := filepath.Join(dir, "trace")
	if err := os.WriteFile(path, []byte("#include <stdio.h>\n"+source+driver), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-O0", "-Wall", "-Werror", "-Wno-unused-function", "-Wno-unused-label", path, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("compile trace: %v: %s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run trace: %v: %s", err, out)
	}
	return string(out)
}

func TestNamedRegionByteAccessors(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xa5, 0x10, 0xf0, 0x02, 0xe6, 0x11, 0xa5, 0x11, 0x69, 0xff, 0x85, 0x12})
	symbols := []ByteSymbol{
		{Name: "third_byte", Address: 0x7e0012, Evidence: "authored third byte"},
		{Name: "first_byte", Address: 0x7e0010, Evidence: "authored first byte"},
		{Name: "second_byte", Address: 0x7e0011, Evidence: "authored second byte"},
	}
	named, err := GenerateNamedRegionC(r, symbols)
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateNamedRegionC(r, []ByteSymbol{symbols[1], symbols[2], symbols[0]})
	if err != nil || !reflect.DeepEqual(named, other) {
		t.Fatalf("nondeterministic output: %v", err)
	}
	if !strings.Contains(named.Source, named.VariablesH) || !strings.Contains(named.Source, "first_byte_read(&res, ((s.d + 0x10) & 0xFFFF),") || !strings.Contains(named.Source, "third_byte_write(&res, ((s.d + 0x12) & 0xFFFF),") {
		t.Fatal("source lacks embedded named accessors with original addresses")
	}
	if err := (&CompiledRoutineRunner{sourceCode: named.Source}).BindRegion(r, "synthetic"); err == nil {
		t.Fatal("named source unexpectedly gained captured binding")
	}
	original, err := GenerateRegionC(r)
	if err != nil {
		t.Fatal(err)
	}
	cases := []ReplayCase{
		namedCase(0, MemoryCell{Address: 0x7e0011, Value: 0xff}), // taken branch
		namedCase(1, MemoryCell{Address: 0x7e0011, Value: 0xff}), // INC, read after write, ADC wrap
		namedCase(1), // missing INC source
	}
	want := namedRun(t, r, original, cases)
	got := namedRun(t, r, named.Source, cases)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state or effects differ: original=%+v named=%+v", want, got)
	}
	if !got[2].MissingRead || got[2].MissingAddr != 0x7e0011 || got[1].TotalWrites < 2 {
		t.Fatalf("refusal or writes missing: %+v", got)
	}
	if a, b := namedTrace(t, r, original), namedTrace(t, r, named.Source); a != b {
		t.Fatalf("ordered reads differ: original=%q named=%q", a, b)
	}
	badAddress := strings.Replace(named.Source, "third_byte_write(&res, ((s.d + 0x12) & 0xFFFF),", "third_byte_write(&res, 0x13,", 1)
	if reflect.DeepEqual(namedRun(t, r, badAddress, cases), got) {
		t.Fatal("wrong address mutant survived")
	}
	bypass := strings.ReplaceAll(named.Source, "mem_write8(res, original_addr, value);", "(void)res; (void)original_addr; (void)value;")
	if reflect.DeepEqual(namedRun(t, r, bypass, cases), got) {
		t.Fatal("bypassed write log mutant survived")
	}
}

func TestNamedRegionMirrorAndValidation(t *testing.T) {
	r := semanticTestRegion(t, []byte{0xaf, 0x10, 0x00, 0x7e, 0x85, 0x10, 0xa5, 0x10})
	symbol := ByteSymbol{Name: "shared_byte", Address: 0x7e0010, Evidence: "authored mirror control"}
	named, err := GenerateNamedRegionC(r, []ByteSymbol{symbol})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(named.Source, "shared_byte_read(&res, 0x7E0010,") || !strings.Contains(named.Source, "shared_byte_read(&res, ((s.d + 0x10) & 0xFFFF),") {
		t.Fatal("mirror aliases did not select the same accessor")
	}
	original, _ := GenerateRegionC(r)
	cases := []ReplayCase{namedCase(7)}
	if a, b := namedRun(t, r, original, cases), namedRun(t, r, named.Source, cases); !reflect.DeepEqual(a, b) {
		t.Fatalf("mirror effects differ: original=%+v named=%+v", a, b)
	}
	if a, b := namedTrace(t, r, original), namedTrace(t, r, named.Source); a != b {
		t.Fatalf("mirror reads differ: %q %q", a, b)
	}
	for _, symbols := range [][]ByteSymbol{
		{{Name: "bad-name", Address: 0x7e0010, Evidence: "x"}},
		{{Name: "shared_byte", Address: 0x0010, Evidence: "x"}},
		{{Name: "shared_byte", Address: 0x7e0010}},
		{symbol, {Name: "overlap", Address: 0x7e0010, Evidence: "x"}},
		{symbol, {Name: "unused", Address: 0x7e0011, Evidence: "x"}},
	} {
		if _, err := GenerateNamedRegionC(r, symbols); err == nil {
			t.Fatalf("accepted invalid symbols: %+v", symbols)
		}
	}
}
