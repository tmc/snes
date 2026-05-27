package disasm

import (
	"strings"
	"testing"

	"github.com/tmc/snes/internal/apu/spc700"
	"github.com/tmc/snes/internal/cpu"
)

// memBus is a trivial 16MB memory backing used to feed operand bytes into the
// disassembler without standing up a real Bus. Only Read is required by the
// PeekBus65816 interface.
type memBus [0x1000000]byte

func (m *memBus) Read(addr uint32) uint8 { return m[addr&0xFFFFFF] }

// memBusSPC is a 64KB memory for the SPC700 tests.
type memBusSPC [0x10000]byte

func (m *memBusSPC) Read(addr uint16) uint8 { return m[addr] }

func loadBytes(m *memBus, base uint32, bytes ...byte) {
	for i, b := range bytes {
		m[(base+uint32(i))&0xFFFFFF] = b
	}
}

func loadBytesSPC(m *memBusSPC, base uint16, bytes ...byte) {
	for i, b := range bytes {
		m[base+uint16(i)] = b
	}
}

func TestDisassemble65816(t *testing.T) {
	tests := []struct {
		name     string
		pb, _pad uint8
		pc       uint16
		bytes    []byte
		// CPU state that affects decoding
		p        uint8
		eflag    bool
		a, x, y  uint16
		s, d     uint16
		db       uint8
		wantSubs []string // substrings that must appear in the output
	}{
		{
			name: "LDA immediate 8-bit (m=1)",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0xA9, 0x34},
			p:        0x20, // m=1
			a:        0x0000,
			wantSubs: []string{"00:8000", "A9 34", "LDA #$34", "A:0000"},
		},
		{
			name: "LDA immediate 16-bit (m=0)",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0xA9, 0x34, 0x12},
			p:        0x00, // m=0
			eflag:    false,
			a:        0x1234,
			wantSubs: []string{"00:8000", "A9 34 12", "LDA #$1234", "A:1234"},
		},
		{
			name: "LDX immediate 8-bit via E mode forcing x=1",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0xA2, 0x42},
			p:        0x00, // x clear in P, but E forces 8-bit
			eflag:    true,
			wantSubs: []string{"LDX #$42"},
		},
		{
			name: "LDA absolute",
			pb:   0x00, pc: 0x88AB,
			bytes:    []byte{0xAD, 0x34, 0x12},
			p:        0x20,
			wantSubs: []string{"00:88AB", "AD 34 12", "LDA $1234"},
		},
		{
			name: "LDA absolute,X",
			pb:   0x00, pc: 0x88AB,
			bytes:    []byte{0xBD, 0x34, 0x12},
			p:        0x20,
			wantSubs: []string{"BD 34 12", "LDA $1234,X"},
		},
		{
			name: "BNE relative forward",
			pb:   0x00, pc: 0x88AB,
			bytes: []byte{0xD0, 0x05},
			// PC+2+5 = 0x88B2 as shown in the task example.
			p: 0x20,
			a: 0x2E00, x: 0x0A6F, y: 0x00FE,
			s: 0x01FA, d: 0x0000, db: 0x00,
			wantSubs: []string{"00:88AB", "D0 05", "BNE $88B2", "A:2E00", "X:0A6F", "Y:00FE", "S:01FA", "D:0000", "DB:00"},
		},
		{
			name: "LDA long",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0xAF, 0x34, 0x12, 0x7E},
			p:        0x20,
			wantSubs: []string{"AF 34 12 7E", "LDA $7E1234"},
		},
		{
			name: "JMP absolute",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0x4C, 0x00, 0x90},
			wantSubs: []string{"4C 00 90", "JMP $9000"},
		},
		{
			name: "NOP implied",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0xEA},
			wantSubs: []string{"EA", "NOP"},
		},
		{
			name: "MVN block move",
			pb:   0x00, pc: 0x8000,
			bytes:    []byte{0x54, 0x7F, 0x7E}, // MVN 7E, 7F  (dest bank = byte2 = 0x7E)
			wantSubs: []string{"54 7F 7E", "MVN $7E, $7F"},
		},
	}

	var mem memBus
	c := &cpu.CPU{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Zero the bytes we care about (don't bother with whole-mem reset).
			base := uint32(tc.pb)<<16 | uint32(tc.pc)
			for i := range uint32(4) {
				mem[base+i] = 0
			}
			loadBytes(&mem, base, tc.bytes...)

			c.PB = tc.pb
			c.PC = tc.pc
			c.P = tc.p
			c.E = tc.eflag
			c.A = tc.a
			c.X = tc.x
			c.Y = tc.y
			c.S = tc.s
			c.D = tc.d
			c.DB = tc.db

			got := Disassemble65816(c, &mem)
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\n got: %s", want, got)
				}
			}

			// Disassembly must not advance PC or otherwise mutate CPU state.
			if c.PC != tc.pc || c.PB != tc.pb {
				t.Errorf("disassembly mutated PC/PB: %02X:%04X (want %02X:%04X)",
					c.PB, c.PC, tc.pb, tc.pc)
			}
		})
	}
}

func TestDisassembleSPC700(t *testing.T) {
	tests := []struct {
		name     string
		pc       uint16
		bytes    []byte
		a, x, y  uint8
		sp       uint8
		n        bool
		v, p, h  bool
		i, z, c  bool
		wantSubs []string
	}{
		{
			name:     "MOV A, #imm",
			pc:       0x0800,
			bytes:    []byte{0xE8, 0x42},
			wantSubs: []string{"0800", "MOV A, #$42", "A:00"},
		},
		{
			name:     "MOV A, dp",
			pc:       0x0800,
			bytes:    []byte{0xE4, 0x20},
			wantSubs: []string{"MOV A, $20"},
		},
		{
			name:  "MOV A, !abs",
			pc:    0x88C3,
			bytes: []byte{0xE5, 0x40, 0x21},
			a:     0xCC, x: 0x00, y: 0x69, sp: 0xEF,
			n: true, h: true,
			wantSubs: []string{"88C3", "MOV A, $2140", "A:CC", "X:00", "Y:69", "SP:EF", "NvpHizc"},
		},
		{
			name:  "CMP A, !abs (matches task example shape)",
			pc:    0x88C3,
			bytes: []byte{0x65, 0x40, 0x21}, // CMP A, !abs
			a:     0xCC, x: 0x00, y: 0x69, sp: 0xEF,
			n: true, h: true,
			wantSubs: []string{"88C3", "CMP A, $2140", "A:CC", "X:00", "Y:69", "SP:EF", "NvpHizc"},
		},
		{
			name:     "MOV A, !abs+X (indexed)",
			pc:       0x0800,
			bytes:    []byte{0xF5, 0x00, 0x20},
			wantSubs: []string{"MOV A, $2000+X"},
		},
		{
			name:     "BEQ relative",
			pc:       0x0800,
			bytes:    []byte{0xF0, 0x05},
			z:        true,
			wantSubs: []string{"BEQ $0807", "nvphiZc"},
		},
		{
			name:     "NOP implied",
			pc:       0x0100,
			bytes:    []byte{0x00},
			wantSubs: []string{"0100", "NOP"},
		},
		{
			name:     "CALL abs",
			pc:       0x0500,
			bytes:    []byte{0x3F, 0x00, 0x10},
			wantSubs: []string{"CALL $1000"},
		},
	}

	var mem memBusSPC
	s := &spc700.SPC700{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := range uint16(4) {
				mem[tc.pc+i] = 0
			}
			loadBytesSPC(&mem, tc.pc, tc.bytes...)

			s.PC = tc.pc
			s.A = tc.a
			s.X = tc.x
			s.Y = tc.y
			s.SP = tc.sp
			s.N, s.V, s.P, s.H = tc.n, tc.v, tc.p, tc.h
			s.I, s.Z, s.C = tc.i, tc.z, tc.c

			got := DisassembleSPC700(s, &mem)
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\n got: %s", want, got)
				}
			}

			if s.PC != tc.pc {
				t.Errorf("disassembly mutated PC: %04X (want %04X)", s.PC, tc.pc)
			}
		})
	}
}
