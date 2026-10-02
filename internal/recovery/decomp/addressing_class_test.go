package decomp

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
)

// These tests use a flat bus to distinguish addresses that WRAM mirroring
// otherwise makes equal. Expected address sequences follow bsnes's
// WDC65816 readDirect/readStack (16-bit) and readBank/readLong (24-bit).
// They do not compare cycles or instruction-fetch bus activity.
func TestAddressingClassRawBus(t *testing.T) {
	tests := []struct {
		name          string
		code          []byte
		d, x, stack   uint16
		db            uint8
		reads, writes []uint32
	}{
		{name: "direct_read", code: []byte{0xa5, 0xff}, d: 0xff00, reads: []uint32{0xffff, 0}},
		{name: "direct_write", code: []byte{0x85, 0xff}, d: 0xff00, writes: []uint32{0xffff, 0}},
		{name: "direct_index_read", code: []byte{0x15, 0xfe}, d: 0xff00, x: 1, reads: []uint32{0xffff, 0}},
		{name: "direct_index_write", code: []byte{0x95, 0xfe}, d: 0xff00, x: 1, writes: []uint32{0xffff, 0}},
		{name: "direct_index_d_zero", code: []byte{0x15, 0xfe}, x: 0xff01, reads: []uint32{0xffff, 0}},
		{name: "direct_index_write_d_zero", code: []byte{0x95, 0xfe}, x: 0xff01, writes: []uint32{0xffff, 0}},
		{name: "absolute_bank_zero", code: []byte{0xad, 0xff, 0xff}, reads: []uint32{0xffff, 0x10000}},
		{name: "direct_index_base_wrap", code: []byte{0x15, 0xff}, d: 0xff00, x: 1, reads: []uint32{0, 1}},
		{name: "absolute_read", code: []byte{0xad, 0xff, 0xff}, db: 0x7e, reads: []uint32{0x7effff, 0x7f0000}},
		{name: "absolute_write", code: []byte{0x8d, 0xff, 0xff}, db: 0x7e, writes: []uint32{0x7effff, 0x7f0000}},
		{name: "absolute_index_read", code: []byte{0x1d, 0xd5, 0xa6}, db: 0x6b, x: 0x592a, reads: []uint32{0x6bffff, 0x6c0000}},
		{name: "long_read", code: []byte{0xaf, 0xff, 0xff, 0x7e}, reads: []uint32{0x7effff, 0x7f0000}},
		{name: "long_write", code: []byte{0x8f, 0xff, 0xff, 0x7e}, writes: []uint32{0x7effff, 0x7f0000}},
		{name: "stack_return_wrap", code: []byte{0x60}, stack: 0xfffe, reads: []uint32{0xffff, 0}},
	}
	ctx := recovery.Context{E: "clear", M: "clear", X: "clear"}
	for _, tc := range tests {
		for _, generator := range []string{"block", "region"} {
			t.Run(tc.name+"/"+generator, func(t *testing.T) {
				inst := recovery.Instruction{ID: "raw", Architecture: "65816", Address: 0x008000, Bytes: hex.EncodeToString(tc.code), Opcode: tc.code[0], Context: ctx}
				block := &structure.BasicBlock{ID: "raw", StartAddress: 0x008000, EndAddress: 0x008000 + uint32(len(tc.code)), Instructions: []recovery.Instruction{inst}, Successors: []uint32{0x008000 + uint32(len(tc.code))}}
				ir, err := LiftBlock(block, ctx)
				if err != nil {
					t.Fatal(err)
				}
				src, err := GenerateCompilableC(ir)
				function := "execute_block_008000"
				if generator == "region" {
					code := append([]byte(nil), tc.code...)
					if tc.code[0] != 0x60 {
						code = append(code, 0x60)
					}
					region, decodeErr := DecodeRegionFromBytes(code, 0x008000, ctx, nil, 0, 100)
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					region.Name = "raw"
					src, err = GenerateRegionC(region)
					function = "execute_raw"
				}
				if err != nil {
					t.Fatal(err)
				}
				// Record the raw address at the bus boundary. Changing only this
				// canonicalization hook exposes any incorrectly computed bus address.
				src = strings.ReplaceAll(src, "uint32_t a = bus_canonical_addr(addr);", "uint32_t a = addr & 0xffffff;")
				var driver strings.Builder
				driver.WriteString("\nstatic uint8_t raw_read(void *ctx, uint32_t a, bool *missing) { (void)ctx; *missing=false; printf(\"R:%06x\\n\",a); return a ? 0x78 : 0x56; }\nint main(void) { cpu_state_t s = {0};\n")
				fmt.Fprintf(&driver, "s.p=0; s.a=0xbeef; s.pc=0x8000; s.d=%d; s.x=%d; s.s=%d; s.db=%d;\n", tc.d, tc.x, tc.stack, tc.db)
				refused := generator == "region" && (tc.d != 0 || tc.db == 0x7e || tc.db == 0x6b || tc.stack > 0x1ffd)
				fmt.Fprintf(&driver, "exec_result_t r=%s(s,raw_read,NULL);", function)
				if refused {
					driver.WriteString("if (!r.uninitialized_read) return 1;")
				}
				driver.WriteString("for(int i=0;i<r.num_writes;i++) printf(\"W:%06x\\n\",r.writes[i].address); return 0; }\n")
				dir := t.TempDir()
				file := filepath.Join(dir, "raw.c")
				bin := filepath.Join(dir, "raw")
				if err := os.WriteFile(file, []byte(src+driver.String()), 0600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("clang", "-std=c11", "-O0", "-w", file, "-o", bin).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				out, err := exec.Command(bin).CombinedOutput()
				if err != nil {
					t.Fatalf("execute: %v\n%s", err, out)
				}
				var reads, writes []uint32
				for _, line := range strings.Fields(string(out)) {
					a, err := strconv.ParseUint(line[2:], 16, 32)
					if err != nil {
						t.Fatal(err)
					}
					if line[0] == 'R' {
						reads = append(reads, uint32(a))
					} else {
						writes = append(writes, uint32(a))
					}
				}
				// Some existing load flag lowering reevaluates the memory expression.
				// Check each read address, not a hardware read-count assertion.
				wantReads := tc.reads
				if tc.code[0] == 0xa5 || tc.code[0] == 0xad || tc.code[0] == 0xaf {
					wantReads = append(append([]uint32(nil), tc.reads...), tc.reads...)
				}
				if generator == "region" && tc.code[0] != 0x60 {
					wantReads = append(append([]uint32(nil), wantReads...), uint32(tc.stack+1), uint32(tc.stack+2))
				}
				wantWrites := tc.writes
				if refused {
					wantReads = nil
					wantWrites = nil
				}
				if !reflect.DeepEqual(reads, wantReads) || !reflect.DeepEqual(writes, wantWrites) {
					t.Fatalf("raw reads %x want %x; writes %x want %x", reads, wantReads, writes, wantWrites)
				}
			})
		}
	}
}

func TestAddressingClassUnsupportedPointers(t *testing.T) {
	ctx := recovery.Context{E: "clear", M: "clear", X: "clear"}
	// LDA [dp],Y ($B7) is supported and executed in TestConnectedNewOpcodes,
	// including an 8-bit load and a bank-crossing 16-bit load.
	for _, op := range []byte{0xa1, 0xb1, 0xb2, 0xa7, 0xa3, 0xb3} {
		t.Run(fmt.Sprintf("%02x", op), func(t *testing.T) {
			inst := recovery.Instruction{ID: "pointer", Architecture: "65816", Address: 0x008000, Bytes: fmt.Sprintf("%02xff", op), Opcode: op, Context: ctx}
			ir, err := LiftBlock(&structure.BasicBlock{ID: "pointer", StartAddress: 0x008000, EndAddress: 0x008002, Instructions: []recovery.Instruction{inst}}, ctx)
			if err == nil {
				_, err = GenerateCompilableC(ir)
			}
			if err == nil {
				t.Fatal("unsupported pointer or stack-relative mode generated C")
			}
		})
	}
}
