package decomp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

func timedFixture(t *testing.T) (*RegionIR, []byte, TimedPlan) {
	t.Helper()
	// Authored hardware fixture, with no commercial game bytes or addresses.
	code := []byte{0xad, 0x40, 0, 0x18, 0x69, 5, 0x8d, 0x40, 0, 0x60}
	rom := make([]byte, 32768)
	copy(rom, code)
	region, err := DecodeRegionFromBytes(code, 0x8000, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	plan := TimedPlan{Instructions: []TimedInstruction{{Opcode: 0xee, OperandBytes: 2, IdleBefore: 6}, {Opcode: 0xad, OperandBytes: 2}, {Opcode: 0xc9, OperandBytes: 1}, {Opcode: 0xd0, OperandBytes: 1, TakenIdle: 6}, {Opcode: 0x18, IdleBefore: 6}, {Opcode: 0x69, OperandBytes: 1}, {Opcode: 0x8d, OperandBytes: 2}, {Opcode: 0x60, IdleBefore: 12, IdleAfter: 6}}}
	return region, rom, plan
}

func TestTimedSemantics(t *testing.T) {
	region, rom, plan := timedFixture(t)
	before, err := GenerateRegionC(region)
	if err != nil {
		t.Fatal(err)
	}
	for _, addend := range []byte{5, 6} {
		t.Run(fmt.Sprint(addend), func(t *testing.T) {
			var edit *TimedImmediateEdit
			if addend != 5 {
				edit = &TimedImmediateEdit{Address: 0x8004, Expected: 5, Replacement: addend}
			}
			generated, err := GenerateTimedRegionC(region, rom, plan, edit)
			if err != nil {
				t.Fatal(err)
			}
			for _, phase := range []byte{0, 127, 255} {
				t.Run(fmt.Sprint(phase), func(t *testing.T) { runTimedVector(t, generated.Source, rom, addend, phase) })
			}

		})
	}
	after, err := GenerateRegionC(region)
	if err != nil || before != after {
		t.Fatal("timed emission mutated generic region source")
	}
}
func runTimedVector(t *testing.T, source string, rom []byte, addend, phase byte) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "runner.c")
	bin := filepath.Join(dir, "runner")
	if err := os.WriteFile(src, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-std=c99", "-O0", "-Wall", "-Werror", "-Wno-unused-label", src, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	scan := bufio.NewScanner(output)
	mem := map[uint32]byte{0x40: phase, 0x1f8: 0x10, 0x1f9: 0x80}
	var writes []MemoryWrite
	requests := map[string]int{}
	s := CPUState{A: 0xaa00, X: 3, Y: 4, S: 0x1f7, D: 0, PC: 0x8000, DB: 0, PB: 0, P: 0x31}
	for steps := 0; steps < 20; steps++ {
		at := uint32(s.PB)<<16 | uint32(s.PC)
		fetchPC := s.PC + 1
		fmt.Fprintf(input, "%d %d %d %d %d %d %d %d %d %d\n", at, s.A, s.X, s.Y, s.S, s.D, fetchPC, s.DB, s.PB, s.P)
		done := false
		for scan.Scan() {
			fields := strings.Fields(scan.Text())
			if len(fields) == 0 {
				t.Fatal("empty protocol")
			}
			if fields[0] == "D" {
				if len(fields) != 10 {
					t.Fatal("bad done")
				}
				v := make([]uint64, 9)
				for i := range v {
					v[i], err = strconv.ParseUint(fields[i+1], 10, 16)
					if err != nil {
						t.Fatal(err)
					}
				}
				s = CPUState{A: uint16(v[0]), X: uint16(v[1]), Y: uint16(v[2]), S: uint16(v[3]), D: uint16(v[4]), PC: uint16(v[5]), DB: byte(v[6]), PB: byte(v[7]), P: byte(v[8])}
				done = true
				break
			}
			if len(fields) != 3 {
				t.Fatal("bad request")
			}
			addr, e := strconv.ParseUint(fields[1], 10, 32)
			if e != nil {
				t.Fatal(e)
			}
			value, e := strconv.ParseUint(fields[2], 10, 8)
			if e != nil {
				t.Fatal(e)
			}
			reply := byte(0)
			requests[fields[0]]++
			switch fields[0] {
			case "F":
				off, ok := timedROMOffset(uint32(s.PB)<<16|uint32(fetchPC), len(rom))
				if !ok {
					t.Fatal("bad fetch")
				}
				reply = rom[off]
				fetchPC++
			case "R":
				v, ok := mem[uint32(addr)&0xffff]
				if !ok {
					t.Fatalf("unknown read %x", addr)
				}
				reply = v
			case "W":
				mem[uint32(addr)&0xffff] = byte(value)
				writes = append(writes, MemoryWrite{Address: 0x7e0000 | uint32(addr)&0xffff, Value: byte(value)})
			case "I":
			default:
				t.Fatal("bad operation")
			}
			fmt.Fprintln(input, reply)
		}
		if !done {
			t.Fatalf("no done: %v", scan.Err())
		}
		if s.PC == 0x8011 {
			break
		}
		if steps == 19 {
			t.Fatal("unterminated")
		}
	}
	if mem[0x40] != phase+addend {
		t.Fatalf("wrong memory %v", mem)
	}
	sum := uint16(phase) + uint16(addend)
	result := byte(sum)
	wantP := byte(0x30)
	if sum > 255 {
		wantP |= 1
	}
	if result == 0 {
		wantP |= 2
	}
	wantP |= result & 0x80
	if (^(phase ^ addend))&(phase^result)&0x80 != 0 {
		wantP |= 0x40
	}
	if s.A != (0xaa00|uint16(result)) || s.P != wantP || s.X != 3 || s.Y != 4 || s.S != 0x1f9 || s.PB != 0 || s.DB != 0 || s.PC != 0x8011 {
		t.Fatalf("wrong state %+v", s)
	}
	if requests["R"] != 3 || requests["F"] != 5 || requests["I"] != 3 {
		t.Fatalf("unexpected timed operation counts %v", requests)
	}
	if len(writes) != 1 {
		t.Fatalf("writes %v", writes)
	}

}

func TestTimedRefusals(t *testing.T) {
	for _, kind := range []string{"width", "ROM", "opcode", "timing", "wrong idle", "edit", "no-op edit", "MMIO"} {
		t.Run(kind, func(t *testing.T) {
			region, rom, plan := timedFixture(t)
			var edit *TimedImmediateEdit
			switch kind {
			case "width":
				region.EntryContext.M = "unknown"
			case "ROM":
				rom[0] ^= 1
			case "opcode":
				plan.Instructions = append(plan.Instructions[:1], plan.Instructions[2:]...)
			case "timing":
				plan.Instructions[0].IdleBefore = 100
			case "wrong idle":
				plan.Instructions[0].IdleBefore = 5
			case "edit":
				edit = &TimedImmediateEdit{Address: 0x8004, Expected: 4, Replacement: 6}
			case "no-op edit":
				edit = &TimedImmediateEdit{Address: 0x8004, Expected: 5, Replacement: 5}
			case "MMIO":
				region.Blocks[0].Instructions[0].Bytes = "ee0021"
			}
			if _, err := GenerateTimedRegionC(region, rom, plan, edit); err == nil {
				t.Fatal("unsupported input accepted")
			}
		})
	}
}

func TestTimedIdentitySeparation(t *testing.T) {
	region, rom, plan := timedFixture(t)
	base, err := GenerateTimedRegionC(region, rom, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := GenerateTimedRegionC(region, rom, plan, &TimedImmediateEdit{Address: 0x8004, Expected: 5, Replacement: 6})
	if err != nil {
		t.Fatal(err)
	}
	if base.IRSHA256 != edit.IRSHA256 {
		t.Fatal("original IR identity changed")
	}
	if base.EditedIRSHA256 == edit.EditedIRSHA256 {
		t.Fatal("edited semantic IR identity unchanged")
	}
	if base.SourceSHA256 == edit.SourceSHA256 || base.EditSHA256 == edit.EditSHA256 {
		t.Fatal("source/edit identity unchanged")
	}
}
