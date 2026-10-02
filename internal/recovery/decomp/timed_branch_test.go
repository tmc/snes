package decomp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/recovery"
)

type timedBranchEvent struct {
	Op     byte
	Addr   uint32
	Value  byte
	Clocks uint64
}

func TestTimedBranchAndRMW(t *testing.T) {
	// Authored fixture: increment a counter, compare it with one, and store
	// only on equality. Both paths return through the supplied stack frame.
	code := []byte{
		0xee, 0x40, 0x00, // INC $0040
		0xad, 0x40, 0x00, // LDA $0040
		0xc9, 0x01, // CMP #$01
		0xd0, 0x03, // BNE to RTS
		0x8d, 0x41, 0x00, // STA $0041
		0x60, // RTS
	}
	rom := make([]byte, 32768)
	copy(rom, code)
	region, err := DecodeRegionFromBytes(code, 0x8000, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	plan := TimedPlan{Instructions: []TimedInstruction{
		{Opcode: 0xee, OperandBytes: 2, IdleBefore: 6},
		{Opcode: 0xad, OperandBytes: 2},
		{Opcode: 0xc9, OperandBytes: 1},
		{Opcode: 0xd0, OperandBytes: 1, TakenIdle: 6},
		{Opcode: 0x8d, OperandBytes: 2},
		{Opcode: 0x60, IdleBefore: 12, IdleAfter: 6},
	}}
	generated, err := GenerateTimedRegionC(region, rom, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "runner.c"), filepath.Join(dir, "runner")
	if err := os.WriteFile(src, []byte(generated.Source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-std=c99", "-O0", "-Wall", "-Werror", "-Wno-unused-label", src, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	for _, tt := range []struct {
		name    string
		counter byte
		taken   bool
	}{
		{"fallthrough", 0, false},
		{"taken", 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runTimedBranch(t, bin, rom, tt.counter, tt.taken)
		})
	}
}

func runTimedBranch(t *testing.T, bin string, rom []byte, counter byte, taken bool) {
	t.Helper()
	b := bus.NewBus()
	ram := bus.NewWRAMDevice()
	b.Map(0, 0x1fff, ram)
	cartridge.New(rom).MapToBus(b)
	mem := map[uint32]byte{0x40: counter, 0x41: 0x7b, 0x1f8: 0xff, 0x1f9: 0x8f}
	for addr, value := range mem {
		b.Write(addr, value)
	}
	c := cpu.NewCPU(b)
	c.A, c.X, c.Y, c.S, c.PC, c.P, c.E = 0xab00, 3, 4, 0x1f7, 0x8000, 0x31, false
	var reference []timedBranchEvent
	c.BusEdge = func(clocks uint64) { reference = append(reference, timedBranchEvent{Op: 'I', Clocks: clocks}) }
	b.ReadHook = func(addr uint32, value byte) {
		op := byte('R')
		if addr >= 0x8000 {
			op = 'F'
		}
		last := &reference[len(reference)-1]
		last.Op, last.Addr, last.Value = op, addr, value
	}
	b.WriteHook = func(addr uint32, value byte) {
		last := &reference[len(reference)-1]
		last.Op, last.Addr, last.Value = 'W', addr, value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	scan := bufio.NewScanner(output)
	var writes []timedBranchEvent
	branchSeen := false
	for steps := 0; c.PC != 0x9000; steps++ {
		if steps == 8 {
			t.Fatal("fixture did not return")
		}
		pc := c.PC
		fetchPC := pc + 1
		if _, err := fmt.Fprintf(input, "%d %d %d %d %d %d %d %d %d %d\n", pc, c.A, c.X, c.Y, c.S, c.D, fetchPC, c.DB, c.PB, c.P); err != nil {
			t.Fatal(err)
		}
		var actual []timedBranchEvent
		var state CPUState
		done := false
		for scan.Scan() {
			fields := strings.Fields(scan.Text())
			if len(fields) == 10 && fields[0] == "D" {
				var v [9]uint64
				for i := range v {
					v[i], err = strconv.ParseUint(fields[i+1], 10, 16)
					if err != nil {
						t.Fatal(err)
					}
				}
				state = CPUState{A: uint16(v[0]), X: uint16(v[1]), Y: uint16(v[2]), S: uint16(v[3]), D: uint16(v[4]), PC: uint16(v[5]), DB: byte(v[6]), PB: byte(v[7]), P: byte(v[8])}
				done = true
				break
			}
			if len(fields) != 3 || len(fields[0]) != 1 {
				t.Fatalf("invalid request %q", scan.Text())
			}
			addr, err := strconv.ParseUint(fields[1], 10, 24)
			if err != nil {
				t.Fatal(err)
			}
			value, err := strconv.ParseUint(fields[2], 10, 8)
			if err != nil {
				t.Fatal(err)
			}
			e := timedBranchEvent{Op: fields[0][0], Addr: uint32(addr), Value: byte(value), Clocks: 8}
			var reply byte
			switch e.Op {
			case 'F':
				e.Addr = uint32(fetchPC)
				reply = rom[fetchPC-0x8000]
				fetchPC++
				e.Value = reply
			case 'R':
				var ok bool
				reply, ok = mem[e.Addr]
				if !ok {
					t.Fatalf("uninitialized read $%06x", e.Addr)
				}
				e.Value = reply
			case 'W':
				mem[e.Addr] = e.Value
				writes = append(writes, e)
			case 'I':
				// The CPU divides a 12-clock idle into two 6-clock edges.
				if addr%6 != 0 || addr == 0 {
					t.Fatalf("invalid idle %d", addr)
				}
				for n := uint64(0); n < addr; n += 6 {
					actual = append(actual, timedBranchEvent{Op: 'I', Clocks: 6})
				}
			default:
				t.Fatalf("unsupported operation %q", e.Op)
			}
			if e.Op != 'I' {
				actual = append(actual, e)
			}
			if _, err := fmt.Fprintln(input, reply); err != nil {
				t.Fatal(err)
			}
		}
		if !done {
			t.Fatalf("missing completion: %v", scan.Err())
		}
		reference = nil
		before := c.Cycles
		c.Step()
		if len(reference) == 0 || reference[0].Op != 'F' || reference[0].Addr != uint32(pc) {
			t.Fatal("missing reference opcode fetch")
		}
		// The timed driver starts after the host has fetched the opcode.
		if !reflect.DeepEqual(actual, reference[1:]) {
			t.Fatalf("$%04x bus schedule:\nC: %+v\nCPU: %+v", pc, actual, reference[1:])
		}
		clocks := uint64(8)
		for _, e := range actual {
			clocks += e.Clocks
		}
		if c.Cycles-before != clocks {
			t.Fatalf("$%04x clocks: C=%d CPU=%d", pc, clocks, c.Cycles-before)
		}
		want := CPUState{A: c.A, X: c.X, Y: c.Y, S: c.S, D: c.D, PC: c.PC, DB: c.DB, PB: c.PB, P: c.P, E: c.E}
		if state != want {
			t.Fatalf("$%04x state: C=%+v CPU=%+v", pc, state, want)
		}
		if pc == 0x8008 {
			branchSeen = true
			if (c.PC == 0x800d) != taken {
				t.Fatalf("unexpected branch outcome at $%04x", c.PC)
			}
		}
	}
	wantWrites := []timedBranchEvent{{Op: 'W', Addr: 0x40, Value: counter + 1, Clocks: 8}}
	if !taken {
		wantWrites = append(wantWrites, timedBranchEvent{Op: 'W', Addr: 0x41, Value: 1, Clocks: 8})
	}
	if !branchSeen || !reflect.DeepEqual(writes, wantWrites) {
		t.Fatalf("branch seen=%v, ordered writes=%+v, want %+v", branchSeen, writes, wantWrites)
	}
}
