package cpu_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/disasm"
)

// flatMem is 16 MiB of plain memory covering the whole CPU address space,
// so that every bank is distinct.
type flatMem struct{ b []uint8 }

func (m *flatMem) Read(a uint32) uint8     { return m.b[a&0xFFFFFF] }
func (m *flatMem) Write(a uint32, v uint8) { m.b[a&0xFFFFFF] = v }
func (m *flatMem) BlockRead(a uint32, n int) []byte {
	a &= 0xFFFFFF
	return m.b[a : a+uint32(n)]
}

// obsLog records everything an observer is given.
type obsLog struct {
	insns []cpu.Observation
	trans []cpu.Transition
}

func (l *obsLog) ObserveInstruction(o cpu.Observation) { l.insns = append(l.insns, o) }
func (l *obsLog) ObserveTransition(t cpu.Transition)   { l.trans = append(l.trans, t) }

type rig struct {
	bus *bus.Bus
	mem *flatMem
	cpu *cpu.CPU
	log *obsLog
}

// newRig returns a CPU in native mode at 00:8000 with an observer
// attached and all memory zeroed.
func newRig(t *testing.T) *rig {
	t.Helper()
	b := bus.NewBus()
	mem := &flatMem{b: make([]uint8, 1<<24)}
	b.Map(0x000000, 0xFFFFFF, mem)
	c := cpu.NewCPU(b)
	c.E, c.PB, c.PC, c.S = false, 0, 0x8000, 0x01FF
	r := &rig{bus: b, mem: mem, cpu: c, log: &obsLog{}}
	if _, err := c.Observe(r.log); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return r
}

func (r *rig) load(addr uint32, b ...uint8) {
	for i, v := range b {
		r.mem.b[(addr+uint32(i))&0xFFFFFF] = v
	}
}

func (r *rig) vector(addr uint16, target uint16) {
	r.load(uint32(addr), uint8(target), uint8(target>>8))
}

// regsOf reads the CPU registers directly, independent of cpu.Snapshot.
func regsOf(c *cpu.CPU) cpu.Snapshot {
	return cpu.Snapshot{
		A: c.A, X: c.X, Y: c.Y, S: c.S, D: c.D, PC: c.PC,
		DB: c.DB, PB: c.PB, P: c.P, E: c.E, Cycles: c.Cycles,
	}
}

// normalized applies the emulation-mode normalization Run performs
// before each dispatch.
func normalized(s cpu.Snapshot) cpu.Snapshot {
	if s.E {
		s.P |= 0x30
		s.S = 0x0100 | s.S&0x00FF
	}
	return s
}

// step runs one CPU step and returns what the observer saw during it.
func (r *rig) step() (insns []cpu.Observation, trans []cpu.Transition) {
	ni, nt := len(r.log.insns), len(r.log.trans)
	r.cpu.Step()
	return r.log.insns[ni:], r.log.trans[nt:]
}

// checkFetches verifies the fetch record of o against the program bytes
// in mem and the decoded instruction length.
func checkFetches(t *testing.T, mem *flatMem, o cpu.Observation) {
	t.Helper()
	if o.NumFetches == 0 {
		t.Fatalf("observation at %02X:%04X has no fetches", o.Entry.PB, o.Entry.PC)
	}
	if o.Overflow {
		t.Fatalf("observation at %02X:%04X overflowed", o.Entry.PB, o.Entry.PC)
	}
	op := o.Fetches[0].Value
	want := disasm.InstructionLength65816(op, o.Entry.MemoryWidth() == 8, o.Entry.IndexWidth() == 8)
	if o.NumFetches != want {
		t.Fatalf("op %02X at %02X:%04X: NumFetches = %d, want %d", op, o.Entry.PB, o.Entry.PC, o.NumFetches, want)
	}
	for i := 0; i < o.NumFetches; i++ {
		f := o.Fetches[i]
		addr := uint32(o.Entry.PB)<<16 | uint32(o.Entry.PC+uint16(i))
		if f.Addr != addr {
			t.Fatalf("op %02X at %02X:%04X: fetch %d addr = %06X, want %06X", op, o.Entry.PB, o.Entry.PC, i, f.Addr, addr)
		}
		if v := mem.b[addr]; f.Value != v {
			t.Fatalf("op %02X at %02X:%04X: fetch %d value = %02X, want %02X", op, o.Entry.PB, o.Entry.PC, i, f.Value, v)
		}
	}
}

// stepChecked runs one step that must produce exactly one observation
// and no transitions. It checks Entry and Exit against the registers
// read around the step and the fetches against memory.
func (r *rig) stepChecked(t *testing.T) cpu.Observation {
	t.Helper()
	before := normalized(regsOf(r.cpu))
	insns, trans := r.step()
	after := regsOf(r.cpu)
	if len(insns) != 1 || len(trans) != 0 {
		t.Fatalf("step at %02X:%04X: got %d observations, %d transitions; want 1, 0", before.PB, before.PC, len(insns), len(trans))
	}
	o := insns[0]
	if o.Entry != before {
		t.Fatalf("Entry = %+v, want %+v", o.Entry, before)
	}
	if o.Exit != after {
		t.Fatalf("Exit = %+v, want %+v", o.Exit, after)
	}
	if o.Fault != nil {
		t.Fatalf("unexpected fault: %v", o.Fault)
	}
	checkFetches(t, r.mem, o)
	return o
}

func TestObserveSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		e       bool
		p       uint8
		a, s    uint16
		stack   map[uint16]uint8 // bank 0 stack contents
		prog    []uint8          // at 00:8000
		sub     []uint8          // at 00:9000
		wantLen []int
		wantA   []uint16
		wantE   []bool
	}{
		{
			name:    "emulation 8-bit immediates",
			e:       true,
			p:       0x34,
			a:       0x1200,
			prog:    []uint8{0xA9, 0x34, 0xA2, 0x56, 0xA0, 0x78, 0xC2, 0x30, 0xA9, 0x9A},
			wantLen: []int{2, 2, 2, 2, 2},
			wantA:   []uint16{0x1234, 0x1234, 0x1234, 0x1234, 0x129A},
			wantE:   []bool{true, true, true, true, true},
		},
		{
			name:    "native 16-bit immediates",
			p:       0x00,
			prog:    []uint8{0xA9, 0x34, 0x12, 0xA2, 0x78, 0x56, 0xA0, 0xBC, 0x9A},
			wantLen: []int{3, 3, 3},
			wantA:   []uint16{0x1234, 0x1234, 0x1234},
			wantE:   []bool{false, false, false},
		},
		{
			name: "REP SEP retained high byte",
			p:    0x30,
			a:    0x1200,
			prog: []uint8{
				0xA9, 0x34, // LDA #$34
				0xC2, 0x20, // REP #$20
				0xA9, 0xCD, 0xAB, // LDA #$ABCD
				0xE2, 0x20, // SEP #$20
				0xA9, 0x01, // LDA #$01
				0xC2, 0x10, // REP #$10
				0xA2, 0x34, 0x12, // LDX #$1234
				0xE2, 0x10, // SEP #$10
				0xA0, 0x55, // LDY #$55
			},
			wantLen: []int{2, 2, 3, 2, 2, 2, 3, 2, 2},
			wantA:   []uint16{0x1234, 0x1234, 0xABCD, 0xABCD, 0xAB01, 0xAB01, 0xAB01, 0xAB01, 0xAB01},
		},
		{
			name: "XCE both directions",
			e:    true,
			p:    0x34,
			prog: []uint8{
				0x18,       // CLC
				0xFB,       // XCE -> native
				0xC2, 0x30, // REP #$30
				0xA9, 0xCD, 0xAB, // LDA #$ABCD
				0xA2, 0x34, 0x12, // LDX #$1234
				0x38,       // SEC
				0xFB,       // XCE -> emulation
				0xA9, 0x12, // LDA #$12
				0xA2, 0x56, // LDX #$56
			},
			wantLen: []int{1, 1, 2, 3, 3, 1, 1, 2, 2},
			wantA:   []uint16{0, 0, 0, 0xABCD, 0xABCD, 0xABCD, 0xABCD, 0xAB12, 0xAB12},
			wantE:   []bool{true, false, false, false, false, false, true, true, true},
		},
		{
			name: "PLP to 16-bit",
			p:    0x30,
			prog: []uint8{
				0xA9, 0x00, // LDA #$00
				0x48,             // PHA
				0x28,             // PLP
				0xA9, 0x34, 0x12, // LDA #$1234
				0xA2, 0x78, 0x56, // LDX #$5678
			},
			wantLen: []int{2, 1, 1, 3, 3},
			wantA:   []uint16{0, 0, 0, 0x1234, 0x1234},
		},
		{
			name: "PLP to 8-bit",
			p:    0x00,
			prog: []uint8{
				0xA9, 0x30, 0x00, // LDA #$0030
				0x48,       // PHA
				0x28,       // PLP
				0xA9, 0x12, // LDA #$12
			},
			wantLen: []int{3, 1, 1, 2},
			wantA:   []uint16{0x0030, 0x0030, 0x0030, 0x0012},
		},
		{
			name:    "RTI native",
			p:       0x00,
			a:       0xAB00,
			s:       0x01F0,
			stack:   map[uint16]uint8{0x01F1: 0x30, 0x01F2: 0x00, 0x01F3: 0x90, 0x01F4: 0x00},
			prog:    []uint8{0x40},
			sub:     []uint8{0xA9, 0x12, 0xA2, 0x34},
			wantLen: []int{1, 2, 2},
			wantA:   []uint16{0xAB00, 0xAB12, 0xAB12},
		},
		{
			name:    "RTI emulation",
			e:       true,
			p:       0x34,
			a:       0xAB00,
			s:       0x01F0,
			stack:   map[uint16]uint8{0x01F1: 0x00, 0x01F2: 0x00, 0x01F3: 0x90},
			prog:    []uint8{0x40},
			sub:     []uint8{0xA9, 0x12},
			wantLen: []int{1, 2},
			wantA:   []uint16{0xAB00, 0xAB12},
			wantE:   []bool{true, true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			c := r.cpu
			c.E, c.P, c.A = tt.e, tt.p, tt.a
			if tt.s != 0 {
				c.S = tt.s
			}
			for a, v := range tt.stack {
				r.load(uint32(a), v)
			}
			r.load(0x008000, tt.prog...)
			r.load(0x009000, tt.sub...)
			var prev *cpu.Observation
			for i, n := range tt.wantLen {
				o := r.stepChecked(t)
				if o.NumFetches != n {
					t.Fatalf("step %d: NumFetches = %d, want %d", i, o.NumFetches, n)
				}
				if tt.wantA != nil && o.Exit.A != tt.wantA[i] {
					t.Fatalf("step %d: Exit.A = %04X, want %04X", i, o.Exit.A, tt.wantA[i])
				}
				if tt.wantE != nil && o.Exit.E != tt.wantE[i] {
					t.Fatalf("step %d: Exit.E = %v, want %v", i, o.Exit.E, tt.wantE[i])
				}
				if prev != nil && o.Entry != normalized(prev.Exit) {
					t.Fatalf("step %d: Entry = %+v, want previous Exit %+v", i, o.Entry, normalized(prev.Exit))
				}
				prev = &o
			}
		})
	}
}

func TestObserveFetches(t *testing.T) {
	tests := []struct {
		name string
		p    uint8
		prog []uint8
		end  uint16 // stop address; 0 means the end of prog
	}{
		{
			name: "8-bit",
			p:    0x30,
			prog: []uint8{
				0xA9, 0x01, // LDA #
				0xA2, 0x02, // LDX #
				0xA0, 0x03, // LDY #
				0x09, 0x04, // ORA #
				0xC9, 0x05, // CMP #
				0xE0, 0x06, // CPX #
				0xA5, 0x10, // LDA dp
				0xAD, 0x00, 0x20, // LDA abs
				0xAF, 0x00, 0x20, 0x7E, // LDA long
				0xB1, 0x10, // LDA (dp),Y
				0xA3, 0x01, // LDA sr,S
				0xB7, 0x10, // LDA [dp],Y
				0xF4, 0x34, 0x12, // PEA
				0x62, 0x00, 0x00, // PER
				0x68, 0x68, // PLA PLA
				0x68, 0x68, // PLA PLA
				0xEA,       // NOP
				0x42, 0x00, // WDM
				0x5C, 0x00, 0x90, 0x00, // JML $009000
			},
			end: 0x9000,
		},
		{
			name: "16-bit",
			p:    0x00,
			prog: []uint8{
				0xA9, 0x01, 0x00, // LDA #
				0xA2, 0x02, 0x00, // LDX #
				0xA0, 0x03, 0x00, // LDY #
				0x29, 0xFF, 0xFF, // AND #
				0x89, 0x00, 0x00, // BIT #
				0xC0, 0x06, 0x00, // CPY #
				0xB5, 0x10, // LDA dp,X
				0xBD, 0x00, 0x20, // LDA abs,X
				0xBF, 0x00, 0x20, 0x7E, // LDA long,X
				0xE2, 0x30, // SEP #$30
				0xA9, 0x01, // LDA # (now 8-bit)
				0xC2, 0x30, // REP #$30
				0xA9, 0x01, 0x00, // LDA # (16-bit again)
				0x82, 0x00, 0x00, // BRL +0
				0xEA, // NOP
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.cpu.P = tt.p
			r.load(0x008000, tt.prog...)
			end := tt.end
			if end == 0 {
				end = uint16(0x8000 + len(tt.prog))
			}
			for i := 0; r.cpu.PC != end; i++ {
				if i == len(tt.prog) {
					t.Fatalf("did not reach %04X; PC=%02X:%04X", end, r.cpu.PB, r.cpu.PC)
				}
				r.stepChecked(t)
			}
		})
	}
}

func TestObserveBankWrap(t *testing.T) {
	tests := []struct {
		name      string
		pc        uint16
		p         uint8
		mem       map[uint32]uint8
		wantFetch []uint32
		wantPB    uint8
		wantPC    uint16
		wantA     uint16
		wantStack map[uint32]uint8
	}{
		{
			name:      "LDA imm16 at FFFE",
			pc:        0xFFFE,
			p:         0x00,
			mem:       map[uint32]uint8{0x01FFFE: 0xA9, 0x01FFFF: 0x34, 0x010000: 0x12, 0x020000: 0x99},
			wantFetch: []uint32{0x01FFFE, 0x01FFFF, 0x010000},
			wantPB:    0x01,
			wantPC:    0x0001,
			wantA:     0x1234,
		},
		{
			name:      "JMP abs at FFFF",
			pc:        0xFFFF,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFF: 0x4C, 0x010000: 0x00, 0x010001: 0x90, 0x020000: 0x55, 0x020001: 0x66},
			wantFetch: []uint32{0x01FFFF, 0x010000, 0x010001},
			wantPB:    0x01,
			wantPC:    0x9000,
		},
		{
			name:      "JSR abs at FFFE",
			pc:        0xFFFE,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFE: 0x20, 0x01FFFF: 0x00, 0x010000: 0x90, 0x020000: 0x77},
			wantFetch: []uint32{0x01FFFE, 0x01FFFF, 0x010000},
			wantPB:    0x01,
			wantPC:    0x9000,
			// Return address is the last operand byte, 01:0000.
			wantStack: map[uint32]uint8{0x0001FF: 0x00, 0x0001FE: 0x00},
		},
		{
			name:      "BRA forward across FFFF",
			pc:        0xFFFE,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFE: 0x80, 0x01FFFF: 0x04},
			wantFetch: []uint32{0x01FFFE, 0x01FFFF},
			wantPB:    0x01,
			wantPC:    0x0004,
		},
		{
			name:      "BRA backward across 0000",
			pc:        0x0000,
			p:         0x30,
			mem:       map[uint32]uint8{0x010000: 0x80, 0x010001: 0xFC},
			wantFetch: []uint32{0x010000, 0x010001},
			wantPB:    0x01,
			wantPC:    0xFFFE,
		},
		{
			name:      "BRL at FFFD",
			pc:        0xFFFD,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFD: 0x82, 0x01FFFE: 0x03, 0x01FFFF: 0x00},
			wantFetch: []uint32{0x01FFFD, 0x01FFFE, 0x01FFFF},
			wantPB:    0x01,
			wantPC:    0x0003,
		},
		{
			name:      "BRK at FFFF",
			pc:        0xFFFF,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFF: 0x00, 0x010000: 0x42, 0x020000: 0x43},
			wantFetch: []uint32{0x01FFFF, 0x010000},
			wantPB:    0x00,
			wantPC:    0xA000,
			// Pushed PB:PC is 01:0001.
			wantStack: map[uint32]uint8{0x0001FF: 0x01, 0x0001FE: 0x00, 0x0001FD: 0x01},
		},
		{
			name:      "COP at FFFF",
			pc:        0xFFFF,
			p:         0x30,
			mem:       map[uint32]uint8{0x01FFFF: 0x02, 0x010000: 0x07, 0x020000: 0x08},
			wantFetch: []uint32{0x01FFFF, 0x010000},
			wantPB:    0x00,
			wantPC:    0xB000,
			wantStack: map[uint32]uint8{0x0001FF: 0x01, 0x0001FE: 0x00, 0x0001FD: 0x01},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.vector(cpu.VectorNativeBRK, 0xA000)
			r.vector(cpu.VectorNativeCOP, 0xB000)
			for a, v := range tt.mem {
				r.load(a, v)
			}
			r.cpu.PB, r.cpu.PC, r.cpu.P = 0x01, tt.pc, tt.p
			o := r.stepChecked(t)
			var got []uint32
			for i := 0; i < o.NumFetches; i++ {
				got = append(got, o.Fetches[i].Addr)
			}
			if !reflect.DeepEqual(got, tt.wantFetch) {
				t.Fatalf("fetch addrs = %06X, want %06X", got, tt.wantFetch)
			}
			if o.Exit.PB != tt.wantPB || o.Exit.PC != tt.wantPC {
				t.Fatalf("exit = %02X:%04X, want %02X:%04X", o.Exit.PB, o.Exit.PC, tt.wantPB, tt.wantPC)
			}
			if tt.wantA != 0 && o.Exit.A != tt.wantA {
				t.Fatalf("A = %04X, want %04X", o.Exit.A, tt.wantA)
			}
			for a, v := range tt.wantStack {
				if got := r.mem.b[a]; got != v {
					t.Fatalf("stack %06X = %02X, want %02X", a, got, v)
				}
			}
		})
	}
}

func TestObserveSoftwareInterrupts(t *testing.T) {
	tests := []struct {
		name   string
		e      bool
		op     uint8
		vector uint16
	}{
		{"BRK native", false, 0x00, cpu.VectorNativeBRK},
		{"COP native", false, 0x02, cpu.VectorNativeCOP},
		{"BRK emulation", true, 0x00, cpu.VectorEmulationIRQ},
		{"COP emulation", true, 0x02, cpu.VectorEmulationCOP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.cpu.E, r.cpu.P = tt.e, 0x30
			r.vector(tt.vector, 0xC000)
			r.load(0x008000, tt.op, 0x5A)
			o := r.stepChecked(t)
			if o.NumFetches != 2 || o.Fetches[0].Value != tt.op || o.Fetches[1].Value != 0x5A {
				t.Fatalf("fetches = %v (n=%d), want [%02X 5A]", o.Bytes(), o.NumFetches, tt.op)
			}
			if o.Exit.PB != 0 || o.Exit.PC != 0xC000 {
				t.Fatalf("exit = %02X:%04X, want 00:C000", o.Exit.PB, o.Exit.PC)
			}
		})
	}
}

func TestObserveControlFlow(t *testing.T) {
	r := newRig(t)
	c := r.cpu
	c.P, c.X = 0x30, 2
	// Program, as (address, bytes) pairs.
	code := []struct {
		addr uint32
		b    []uint8
	}{
		{0x008000, []uint8{0x20, 0x00, 0x90}},       // JSR $9000
		{0x009000, []uint8{0x60}},                   // RTS
		{0x008003, []uint8{0x22, 0x00, 0xA0, 0x02}}, // JSL $02A000
		{0x02A000, []uint8{0x6B}},                   // RTL
		{0x008007, []uint8{0x7C, 0x00, 0x91}},       // JMP ($9100,X)
		{0x009102, []uint8{0x00, 0x81}},             // pointer -> $8100
		{0x008100, []uint8{0xA9, 0x00}},             // LDA #$00
		{0x008102, []uint8{0xF0, 0x02}},             // BEQ +2 (taken)
		{0x008106, []uint8{0xD0, 0x10}},             // BNE (not taken)
		{0x008108, []uint8{0x6C, 0x00, 0x92}},       // JMP ($9200)
		{0x009200, []uint8{0x00, 0x83}},             // pointer -> $8300
		{0x008300, []uint8{0xDC, 0x10, 0x92}},       // JML [$9210]
		{0x009210, []uint8{0x00, 0x84, 0x03}},       // pointer -> $03:8400
		{0x038400, []uint8{0x5C, 0x00, 0x85, 0x00}}, // JML $008500
		{0x008500, []uint8{0xFC, 0x20, 0x93}},       // JSR ($9320,X)
		{0x009322, []uint8{0x00, 0x86}},             // pointer -> $8600
		{0x008600, []uint8{0x60}},                   // RTS
		{0x008503, []uint8{0x82, 0xFD, 0x00}},       // BRL -> $8603
		{0x008603, []uint8{0x80, 0xFE}},             // BRA self
	}
	for _, c := range code {
		r.load(c.addr, c.b...)
	}
	pointers := map[uint32]bool{
		0x009102: true, 0x009103: true, 0x009200: true, 0x009201: true,
		0x009210: true, 0x009211: true, 0x009212: true, 0x009322: true, 0x009323: true,
	}
	want := []struct {
		entry, exit uint32
	}{
		{0x008000, 0x009000},
		{0x009000, 0x008003},
		{0x008003, 0x02A000},
		{0x02A000, 0x008007},
		{0x008007, 0x008100},
		{0x008100, 0x008102},
		{0x008102, 0x008106},
		{0x008106, 0x008108},
		{0x008108, 0x008300},
		{0x008300, 0x038400},
		{0x038400, 0x008500},
		{0x008500, 0x008600},
		{0x008600, 0x008503},
		{0x008503, 0x008603},
		{0x008603, 0x008603},
	}
	for i, w := range want {
		o := r.stepChecked(t)
		entry := uint32(o.Entry.PB)<<16 | uint32(o.Entry.PC)
		exit := uint32(o.Exit.PB)<<16 | uint32(o.Exit.PC)
		if entry != w.entry || exit != w.exit {
			t.Fatalf("step %d: %06X -> %06X, want %06X -> %06X", i, entry, exit, w.entry, w.exit)
		}
		for j := 0; j < o.NumFetches; j++ {
			if pointers[o.Fetches[j].Addr] {
				t.Fatalf("step %d: pointer byte %06X recorded as fetch", i, o.Fetches[j].Addr)
			}
		}
	}
}

func TestObserveBlockMove(t *testing.T) {
	tests := []struct {
		name     string
		op       uint8
		x, y     uint16
		wantDest []uint32
	}{
		{"MVN", 0x54, 0x1000, 0x2000, []uint32{0x7E2000, 0x7E2001, 0x7E2002}},
		{"MVP", 0x44, 0x1002, 0x2002, []uint32{0x7E2000, 0x7E2001, 0x7E2002}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			c := r.cpu
			c.P, c.A, c.X, c.Y = 0x00, 2, tt.x, tt.y
			r.load(0x7F1000, 0x11, 0x22, 0x33)
			r.load(0x008000, tt.op, 0x7E, 0x7F, 0xEA) // MVx $7E,$7F; NOP
			wantA := []uint16{1, 0, 0xFFFF}
			for i, a := range wantA {
				o := r.stepChecked(t)
				if o.Entry.PC != 0x8000 || o.Fetches[0].Value != tt.op {
					t.Fatalf("repeat %d: entry %04X op %02X, want 8000 %02X", i, o.Entry.PC, o.Fetches[0].Value, tt.op)
				}
				wantPC := uint16(0x8000)
				if i == len(wantA)-1 {
					wantPC = 0x8003
				}
				if o.Exit.PC != wantPC || o.Exit.A != a {
					t.Fatalf("repeat %d: exit PC=%04X A=%04X, want PC=%04X A=%04X", i, o.Exit.PC, o.Exit.A, wantPC, a)
				}
			}
			if o := r.stepChecked(t); o.Fetches[0].Value != 0xEA {
				t.Fatalf("after block move: op %02X, want EA", o.Fetches[0].Value)
			}
			for i, a := range tt.wantDest {
				if got, want := r.mem.b[a], uint8(0x11*(i+1)); got != want {
					t.Fatalf("dest %06X = %02X, want %02X", a, got, want)
				}
			}
		})
	}
}

func TestObserveInterrupts(t *testing.T) {
	tests := []struct {
		name     string
		e        bool
		kind     cpu.TransitionKind
		vector   uint16
		wai      bool
		wantWait bool
	}{
		{"NMI native", false, cpu.TransitionNMI, cpu.VectorNativeNMI, false, false},
		{"IRQ native", false, cpu.TransitionIRQ, cpu.VectorNativeIRQ, false, false},
		{"NMI emulation", true, cpu.TransitionNMI, cpu.VectorEmulationNMI, false, false},
		{"IRQ emulation", true, cpu.TransitionIRQ, cpu.VectorEmulationIRQ, false, false},
		{"NMI from WAI", false, cpu.TransitionNMI, cpu.VectorNativeNMI, true, true},
		{"IRQ from WAI", false, cpu.TransitionIRQ, cpu.VectorNativeIRQ, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			c := r.cpu
			c.E, c.P = tt.e, 0x30 // I clear
			r.vector(tt.vector, 0xA000)
			r.load(0x00A000, 0xEA)
			if tt.wai {
				r.load(0x008000, 0xCB, 0xEA)
			} else {
				r.load(0x008000, 0xEA, 0xEA)
			}
			r.stepChecked(t)
			if tt.wai {
				for i := 0; i < 3; i++ {
					if insns, trans := r.step(); len(insns)+len(trans) != 0 {
						t.Fatalf("while waiting: %d observations, %d transitions", len(insns), len(trans))
					}
				}
			}
			if tt.kind == cpu.TransitionNMI {
				c.TriggerNMI()
			} else {
				c.TriggerIRQ()
			}
			before := normalized(regsOf(c))
			insns, trans := r.step()
			after := regsOf(c)
			if len(insns) != 0 || len(trans) != 1 {
				t.Fatalf("got %d observations, %d transitions; want 0, 1", len(insns), len(trans))
			}
			tr := trans[0]
			if tr.Kind != tt.kind {
				t.Fatalf("Kind = %v, want %v", tr.Kind, tt.kind)
			}
			if tr.Vector != uint32(tt.vector) {
				t.Fatalf("Vector = %06X, want %06X", tr.Vector, tt.vector)
			}
			if tr.Before != before || tr.After != after {
				t.Fatalf("Before/After = %+v / %+v, want %+v / %+v", tr.Before, tr.After, before, after)
			}
			if tr.After.PB != 0 || tr.After.PC != 0xA000 {
				t.Fatalf("handler = %02X:%04X, want 00:A000", tr.After.PB, tr.After.PC)
			}
			if tr.FromWait != tt.wantWait {
				t.Fatalf("FromWait = %v, want %v", tr.FromWait, tt.wantWait)
			}
			if o := r.stepChecked(t); o.Entry.PC != 0xA000 {
				t.Fatalf("first handler insn at %04X, want A000", o.Entry.PC)
			}
		})
	}
}

func TestObserveMaskedWake(t *testing.T) {
	r := newRig(t)
	c := r.cpu
	c.P = 0x34                   // I set
	r.load(0x008000, 0xCB, 0xEA) // WAI; NOP
	r.vector(cpu.VectorNativeIRQ, 0xA000)
	r.stepChecked(t)
	if insns, trans := r.step(); len(insns)+len(trans) != 0 {
		t.Fatalf("while waiting: %d observations, %d transitions", len(insns), len(trans))
	}
	c.TriggerIRQ()
	before := regsOf(c)
	insns, trans := r.step()
	if len(trans) != 1 || trans[0].Kind != cpu.TransitionWake {
		t.Fatalf("transitions = %+v, want one wake", trans)
	}
	tr := trans[0]
	if !tr.FromWait || tr.Vector != 0 {
		t.Fatalf("wake FromWait=%v Vector=%X, want true 0", tr.FromWait, tr.Vector)
	}
	if tr.Before != before || tr.After.PC != 0x8001 || tr.After.PB != 0 {
		t.Fatalf("wake Before=%+v After=%+v, want Before=%+v and After at 00:8001", tr.Before, tr.After, before)
	}
	// The instruction after WAI runs in the same step.
	if len(insns) != 1 || insns[0].Entry.PC != 0x8001 || insns[0].Fetches[0].Value != 0xEA {
		t.Fatalf("observations after wake = %+v, want NOP at 8001", insns)
	}
	checkFetches(t, r.mem, insns[0])
}

func TestObserveStop(t *testing.T) {
	r := newRig(t)
	r.load(0x008000, 0xDB, 0xEA) // STP; NOP
	r.stepChecked(t)
	cycles := r.cpu.Cycles
	for i := 0; i < 10; i++ {
		if insns, trans := r.step(); len(insns)+len(trans) != 0 {
			t.Fatalf("stopped step %d: %d observations, %d transitions", i, len(insns), len(trans))
		}
	}
	if !r.cpu.Stopped || r.cpu.Cycles <= cycles {
		t.Fatalf("Stopped=%v cycles %d->%d, want stopped and advancing", r.cpu.Stopped, cycles, r.cpu.Cycles)
	}
}

func TestObserveFault(t *testing.T) {
	invalid := -1
	for i, op := range cpu.Opcodes {
		if op.Op == nil {
			invalid = i
			break
		}
	}
	if invalid < 0 {
		// Every opcode is implemented. Remove WDM for the duration of
		// the test to reach the unimplemented-opcode fault path.
		invalid = 0x42
		saved := cpu.Opcodes[invalid]
		cpu.Opcodes[invalid].Op = nil
		t.Cleanup(func() { cpu.Opcodes[invalid] = saved })
	}
	r := newRig(t)
	r.load(0x008000, uint8(invalid))
	insns, trans := r.step()
	if len(insns) != 1 || len(trans) != 0 {
		t.Fatalf("got %d observations, %d transitions; want 1, 0", len(insns), len(trans))
	}
	if insns[0].Fault == nil || insns[0].NumFetches != 1 {
		t.Fatalf("Fault=%v NumFetches=%d, want fault and 1 fetch", insns[0].Fault, insns[0].NumFetches)
	}
	for i := 0; i < 5; i++ {
		if insns, trans := r.step(); len(insns)+len(trans) != 0 {
			t.Fatalf("after fault: %d observations, %d transitions", len(insns), len(trans))
		}
	}
}

func TestObserveAttach(t *testing.T) {
	r := newRig(t) // r.log attached
	r.load(0x008000, 0xEA, 0xEA, 0xEA, 0xEA)
	c := r.cpu

	if _, err := c.Observe(&obsLog{}); !errors.Is(err, cpu.ErrObserverAttached) {
		t.Fatalf("second Observe err = %v, want ErrObserverAttached", err)
	}
	if _, err := c.Observe(nil); err == nil {
		t.Fatalf("Observe(nil) succeeded")
	}

	c2 := cpu.NewCPU(r.bus)
	a := &obsLog{}
	detachA, err := c2.Observe(a)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	detachA()
	b := &obsLog{}
	detachB, err := c2.Observe(b)
	if err != nil {
		t.Fatalf("Observe after detach: %v", err)
	}
	detachA() // stale detach must not remove b
	c2.PC = 0x8000
	c2.Step()
	if len(a.insns) != 0 || len(b.insns) != 1 {
		t.Fatalf("observations a=%d b=%d, want 0 1", len(a.insns), len(b.insns))
	}
	detachB()
	c2.Step()
	if len(b.insns) != 1 {
		t.Fatalf("observations after detach = %d, want 1", len(b.insns))
	}
}

// sideEffectDev is a device whose reads change its state.
type sideEffectDev struct {
	n     uint8
	reads int
}

func (d *sideEffectDev) Read(uint32) uint8 { d.n++; d.reads++; return d.n }
func (d *sideEffectDev) Write(_ uint32, v uint8) {
	d.n = v
}
func (d *sideEffectDev) BlockRead(a uint32, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = d.Read(a + uint32(i))
	}
	return b
}

type access struct {
	write bool
	addr  uint32
	val   uint8
}

type invarianceResult struct {
	accesses []access
	state    cpu.CPUState
	regs     cpu.Snapshot
	dev      sideEffectDev
	mdr      uint8
	mem      []uint8
	insns    int
	trans    int
}

// runInvariance runs a program that exercises calls, jumps, block
// moves, BRK, WAI, NMI and reads with side effects.
func runInvariance(t *testing.T, observe bool) invarianceResult {
	b := bus.NewBus()
	mem := &flatMem{b: make([]uint8, 1<<24)}
	b.Map(0x000000, 0xFFFFFF, mem)
	dev := &sideEffectDev{}
	b.Map(0x002100, 0x0021FF, dev)
	var res invarianceResult
	b.ReadHook = func(a uint32, v uint8) { res.accesses = append(res.accesses, access{false, a, v}) }
	b.WriteHook = func(a uint32, v uint8) { res.accesses = append(res.accesses, access{true, a, v}) }

	c := cpu.NewCPU(b)
	c.E, c.P, c.PB, c.PC, c.S = true, 0x34, 0, 0x8000, 0x01FF
	load := func(addr uint32, bs ...uint8) { copy(mem.b[addr:], bs) }
	load(0x008000,
		0x18, 0xFB, // CLC; XCE
		0xE2, 0x30, // SEP #$30
		0xAD, 0x00, 0x21, // 8004: LDA $2100
		0x8D, 0x00, 0x03, // STA $0300
		0xAE, 0x00, 0x21, // LDX $2100
		0x20, 0x00, 0x90, // JSR $9000
		0xC2, 0x30, // REP #$30
		0xA9, 0x02, 0x00, // LDA #$0002
		0xA2, 0x00, 0x03, // LDX #$0300
		0xA0, 0x00, 0x04, // LDY #$0400
		0x54, 0x00, 0x00, // MVN $00,$00
		0xE2, 0x30, // SEP #$30
		0xA2, 0x00, // LDX #$00
		0x7C, 0x00, 0x91, // JMP ($9100,X)
	)
	load(0x009100, 0x00, 0x81)
	load(0x008100,
		0x00, 0x42, // BRK
		0xCB,             // WAI
		0xEE, 0x00, 0x21, // INC $2100
		0x82, 0xFB, 0xFE, // BRL $8004
	)
	load(0x009000, 0xAD, 0x00, 0x21, 0x60) // LDA $2100; RTS
	load(0x009800, 0x40)                   // BRK handler: RTI
	load(0x009A00, 0xAD, 0x01, 0x21, 0x40) // NMI handler: LDA $2101; RTI
	load(uint32(cpu.VectorNativeBRK), 0x00, 0x98)
	load(uint32(cpu.VectorNativeNMI), 0x00, 0x9A)

	var log obsLog
	if observe {
		detach, err := c.Observe(&log)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		defer detach()
	}
	for i := 0; i < 5000; i++ {
		if i%97 == 50 {
			c.TriggerNMI()
		}
		c.Step()
	}
	if c.Fault != nil {
		t.Fatalf("fault: %v", c.Fault)
	}
	res.state = c.SaveState()
	res.regs = regsOf(c)
	res.dev = *dev
	res.mdr = b.MDR
	res.mem = mem.b
	res.insns, res.trans = len(log.insns), len(log.trans)
	return res
}

func TestObserveInvariance(t *testing.T) {
	plain := runInvariance(t, false)
	obs := runInvariance(t, true)
	if obs.insns == 0 || obs.trans == 0 {
		t.Fatalf("observed %d instructions, %d transitions; want both > 0", obs.insns, obs.trans)
	}
	if len(plain.accesses) != len(obs.accesses) {
		t.Fatalf("bus accesses: %d without observer, %d with", len(plain.accesses), len(obs.accesses))
	}
	for i := range plain.accesses {
		if plain.accesses[i] != obs.accesses[i] {
			t.Fatalf("bus access %d: %+v without observer, %+v with", i, plain.accesses[i], obs.accesses[i])
		}
	}
	if plain.regs != obs.regs {
		t.Fatalf("registers: %+v without observer, %+v with", plain.regs, obs.regs)
	}
	if !reflect.DeepEqual(plain.state, obs.state) {
		t.Fatalf("CPU state differs:\n%+v\n%+v", plain.state, obs.state)
	}
	if plain.dev != obs.dev || plain.mdr != obs.mdr {
		t.Fatalf("device %+v mdr %02X without observer, %+v %02X with", plain.dev, plain.mdr, obs.dev, obs.mdr)
	}
	if plain.dev.reads == 0 {
		t.Fatalf("program never read the side-effect device")
	}
	if !bytes.Equal(plain.mem, obs.mem) {
		t.Fatalf("memory differs")
	}
}

type nopObserver struct{}

func (nopObserver) ObserveInstruction(cpu.Observation) {}
func (nopObserver) ObserveTransition(cpu.Transition)   {}

func TestObserveAllocs(t *testing.T) {
	b := bus.NewBus()
	mem := &flatMem{b: make([]uint8, 1<<24)}
	b.Map(0x000000, 0xFFFFFF, mem)
	c := cpu.NewCPU(b)
	c.PC = 0x8000
	copy(mem.b[0x8000:], []uint8{0xE6, 0x10, 0x80, 0xFC}) // INC $10; BRA -4

	if n := testing.AllocsPerRun(1000, c.Run); n != 0 {
		t.Fatalf("Run without observer: %v allocs, want 0", n)
	}
	detach, err := c.Observe(nopObserver{})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	defer detach()
	if n := testing.AllocsPerRun(1000, c.Run); n != 0 {
		t.Fatalf("Run with observer: %v allocs, want 0", n)
	}
}
