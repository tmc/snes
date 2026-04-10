package spc700

import (
	"testing"
)

type TestBus struct {
	Mem [65536]uint8
}

func (b *TestBus) Read(addr uint16) uint8       { return b.Mem[addr] }
func (b *TestBus) Write(addr uint16, val uint8) { b.Mem[addr] = val }

type State struct {
	A, X, Y, SP            uint8
	PSW                    byte // Optional: use a helper to check specific flags if needed, or expected flags
	N, V, P, B, H, I, Z, C bool
}

type OpcodeTest struct {
	Name  string
	Init  func(c *SPC700, bus *TestBus)
	Code  []byte
	Check func(t *testing.T, c *SPC700)
}

func RunTests(t *testing.T, tests []OpcodeTest) {
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			bus := &TestBus{}
			c := New(bus)
			c.Reset()

			if test.Init != nil {
				test.Init(c, bus)
			}

			// Load code at Reset Vector (FFC0)
			startPC := uint16(0xFFC0)
			if c.PC != 0 {
				startPC = c.PC
			} else {
				c.PC = startPC
			}

			for i, b := range test.Code {
				bus.Mem[startPC+uint16(i)] = b
			}

			// Step for each byte? No, step until PC moves past code?
			// Or just Step() once?
			// usually one instruction.
			c.Step()

			if test.Check != nil {
				test.Check(t, c)
			}
		})
	}
}

func TestDataTransfer(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOV A, #imm (0xE8)",
			Code: []byte{0xE8, 0xAA},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xAA {
					t.Errorf("Expected A=AA, got %02X", c.A)
				}
				if !c.N {
					t.Error("Expected N flag set")
				}
				if c.Z {
					t.Error("Expected Z flag clear")
				}
			},
		},
		{
			Name: "MOV X, #imm (0xCD)",
			Code: []byte{0xCD, 0x00},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x00 {
					t.Errorf("Expected X=00, got %02X", c.X)
				}
				if c.N {
					t.Error("Expected N flag clear")
				}
				if !c.Z {
					t.Error("Expected Z flag set")
				}
			},
		},
		{
			Name: "MOV abs+X, A (0xD5)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x5A
				c.X = 0x03
			},
			Code: []byte{0xD5, 0x00, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x2003); got != 0x5A {
					t.Fatalf("mem[2003] = %02X, want 5A", got)
				}
			},
		},
		{
			Name: "MOV abs+Y, A (0xD6)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xA5
				c.Y = 0x04
			},
			Code: []byte{0xD6, 0x00, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x3004); got != 0xA5 {
					t.Fatalf("mem[3004] = %02X, want A5", got)
				}
			},
		},
		{
			Name: "MOV X, abs (0xE9)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x03E2] = 0x1A
			},
			Code: []byte{0xE9, 0xE2, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x1A {
					t.Fatalf("X = %02X, want 1A", c.X)
				}
			},
		},
		{
			Name: "MOV X, dp (0xF8)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0044] = 0x7F
			},
			Code: []byte{0xF8, 0x44},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x7F || c.Z || c.N {
					t.Fatalf("X=%02X Z=%v N=%v, want 7F false false", c.X, c.Z, c.N)
				}
			},
		},
		{
			Name: "MOV Y, dp+X (0xFB)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x0E
				b.Mem[0x00AE] = 0x42
			},
			Code: []byte{0xFB, 0xA0},
			Check: func(t *testing.T, c *SPC700) {
				if c.Y != 0x42 {
					t.Fatalf("Y=%02X, want 42", c.Y)
				}
			},
		},
		{
			Name: "XCN A (0x9F)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x0E
			},
			Code: []byte{0x9F},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xE0 || !c.N || c.Z {
					t.Fatalf("A=%02X N=%v Z=%v, want E0 true false", c.A, c.N, c.Z)
				}
			},
		},
		{
			Name: "CMP Y, #imm (0xAD)",
			Init: func(c *SPC700, b *TestBus) {
				c.Y = 0x0A
			},
			Code: []byte{0xAD, 0x05},
			Check: func(t *testing.T, c *SPC700) {
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "CMP dp, dp (0x69)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x004D] = 0x45
				b.Mem[0x004C] = 0x4D
			},
			Code: []byte{0x69, 0x4D, 0x4C},
			Check: func(t *testing.T, c *SPC700) {
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "AND dp, #imm (0x38)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0048] = 0x2F
			},
			Code: []byte{0x38, 0x20, 0x48},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0048); got != 0x20 {
					t.Fatalf("mem[0048] = %02X, want 20", got)
				}
				if c.Z || c.N {
					t.Fatalf("flags Z=%v N=%v, want false false", c.Z, c.N)
				}
			},
		},
		{
			Name: "LSR A (0x5C)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x03
			},
			Code: []byte{0x5C},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x01 || !c.C || c.Z || c.N {
					t.Fatalf("A=%02X C=%v Z=%v N=%v, want 01 true false false", c.A, c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ROR dp (0x6B)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x0018] = 0x03
			},
			Code: []byte{0x6B, 0x18},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0018); got != 0x81 {
					t.Fatalf("mem[0018] = %02X, want 81", got)
				}
				if !c.C || !c.N || c.Z {
					t.Fatalf("flags C=%v N=%v Z=%v, want true true false", c.C, c.N, c.Z)
				}
			},
		},
		{
			Name: "ROR abs (0x6C)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x03C1] = 0x01
			},
			Code: []byte{0x6C, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03C1); got != 0x80 {
					t.Fatalf("mem[03C1] = %02X, want 80", got)
				}
				if !c.C || !c.N || c.Z {
					t.Fatalf("flags C=%v N=%v Z=%v, want true true false", c.C, c.N, c.Z)
				}
			},
		},
		{
			Name: "ROR A (0x7C)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x01
				c.C = true
			},
			Code: []byte{0x7C},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x80 || !c.C || !c.N || c.Z {
					t.Fatalf("A=%02X C=%v N=%v Z=%v, want 80 true true false", c.A, c.C, c.N, c.Z)
				}
			},
		},
		{
			Name: "ROL A (0x3C)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x80
				c.C = true
			},
			Code: []byte{0x3C},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x01 || !c.C || c.Z || c.N {
					t.Fatalf("A=%02X C=%v Z=%v N=%v, want 01 true false false", c.A, c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ROL dp (0x2B)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x0015] = 0x80
			},
			Code: []byte{0x2B, 0x15},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0015); got != 0x01 {
					t.Fatalf("mem[0015] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ROL abs (0x2C)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x03C1] = 0x80
			},
			Code: []byte{0x2C, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03C1); got != 0x01 {
					t.Fatalf("mem[03C1] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ASL dp (0x0B)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0047] = 0x80
			},
			Code: []byte{0x0B, 0x47},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0047); got != 0x00 {
					t.Fatalf("mem[0047] = %02X, want 00", got)
				}
				if !c.C || !c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true true false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "LSR dp (0x4B)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0015] = 0x03
			},
			Code: []byte{0x4B, 0x15},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0015); got != 0x01 {
					t.Fatalf("mem[0015] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "LSR dp+X (0x5B)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x01
				b.Mem[0x0002] = 0x03
			},
			Code: []byte{0x5B, 0x01},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0002); got != 0x01 {
					t.Fatalf("mem[0002] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ASL dp+X (0x1B)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x01
				b.Mem[0x0012] = 0x80
			},
			Code: []byte{0x1B, 0x11},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0012); got != 0x00 {
					t.Fatalf("mem[0012] = %02X, want 00", got)
				}
				if !c.C || !c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true true false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ROL dp+X (0x3B)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x01
				c.C = true
				b.Mem[0x0013] = 0x80
			},
			Code: []byte{0x3B, 0x12},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0013); got != 0x01 {
					t.Fatalf("mem[0013] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ROR dp+X (0x7B)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x01
				c.C = true
				b.Mem[0x0014] = 0x01
			},
			Code: []byte{0x7B, 0x13},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0014); got != 0x80 {
					t.Fatalf("mem[0014] = %02X, want 80", got)
				}
				if !c.C || !c.N || c.Z {
					t.Fatalf("flags C=%v N=%v Z=%v, want true true false", c.C, c.N, c.Z)
				}
			},
		},
		{
			Name: "PUSH Y (0x6D)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xCF
				c.Y = 0x42
			},
			Code: []byte{0x6D},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x01CF); got != 0x42 {
					t.Fatalf("stack byte = %02X, want 42", got)
				}
				if c.SP != 0xCE {
					t.Fatalf("SP = %02X, want CE", c.SP)
				}
			},
		},
		{
			Name: "PUSH A (0x2D)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xCF
				c.A = 0x2C
			},
			Code: []byte{0x2D},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x01CF); got != 0x2C {
					t.Fatalf("stack byte = %02X, want 2C", got)
				}
				if c.SP != 0xCE {
					t.Fatalf("SP = %02X, want CE", c.SP)
				}
			},
		},
		{
			Name: "PUSH PSW (0x0D)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xCF
				c.N = true
				c.C = true
			},
			Code: []byte{0x0D},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x01CF); got != 0x81 {
					t.Fatalf("stack byte = %02X, want 81", got)
				}
				if c.SP != 0xCE {
					t.Fatalf("SP = %02X, want CE", c.SP)
				}
			},
		},
		{
			Name: "POP A (0xAE)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xCE
				b.Mem[0x01CF] = 0x2C
			},
			Code: []byte{0xAE},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x2C || c.SP != 0xCF {
					t.Fatalf("A=%02X SP=%02X, want 2C CF", c.A, c.SP)
				}
			},
		},
		{
			Name: "POP PSW (0x8E)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xCE
				b.Mem[0x01CF] = 0x81
			},
			Code: []byte{0x8E},
			Check: func(t *testing.T, c *SPC700) {
				if !c.N || !c.C || c.Z || c.SP != 0xCF {
					t.Fatalf("N=%v C=%v Z=%v SP=%02X, want true true false CF", c.N, c.C, c.Z, c.SP)
				}
			},
		},
		{
			Name: "INC abs (0xAC)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x03C7] = 0x7F
			},
			Code: []byte{0xAC, 0xC7, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03C7); got != 0x80 {
					t.Fatalf("mem[03C7] = %02X, want 80", got)
				}
				if !c.N || c.Z {
					t.Fatalf("flags N=%v Z=%v, want true false", c.N, c.Z)
				}
			},
		},
		{
			Name: "DEC dp+X (0x9B)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x0E
				b.Mem[0x00AE] = 0x01
			},
			Code: []byte{0x9B, 0xA0},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x00AE); got != 0x00 {
					t.Fatalf("mem[00AE] = %02X, want 00", got)
				}
				if !c.Z || c.N {
					t.Fatalf("flags Z=%v N=%v, want true false", c.Z, c.N)
				}
			},
		},
		{
			Name: "DEC dp (0x8B)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0017] = 0x01
			},
			Code: []byte{0x8B, 0x17},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0017); got != 0x00 {
					t.Fatalf("mem[0017] = %02X, want 00", got)
				}
				if !c.Z || c.N {
					t.Fatalf("flags Z=%v N=%v, want true false", c.Z, c.N)
				}
			},
		},
		{
			Name: "DEC abs (0x8C)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x03C1] = 0x00
			},
			Code: []byte{0x8C, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03C1); got != 0xFF {
					t.Fatalf("mem[03C1] = %02X, want FF", got)
				}
				if c.Z || !c.N {
					t.Fatalf("flags Z=%v N=%v, want false true", c.Z, c.N)
				}
			},
		},
		{
			Name: "CBNE dp, rel taken (0x2E)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xFF
				b.Mem[0x001A] = 0x02
			},
			Code: []byte{0x2E, 0x1A, 0x02, 0x00, 0x00},
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC5 {
					t.Fatalf("PC = %04X, want FFC5", c.PC)
				}
			},
		},
		{
			Name: "DBNZ dp, rel taken (0x6E)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0012] = 0x02
			},
			Code: []byte{0x6E, 0x12, 0x02, 0x00, 0x00},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0012); got != 0x01 {
					t.Fatalf("mem[0012] = %02X, want 01", got)
				}
				if c.PC != 0xFFC5 {
					t.Fatalf("PC = %04X, want FFC5", c.PC)
				}
			},
		},
		{
			Name: "AND A, abs (0x25)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xF0
				b.Mem[0x03C1] = 0x0F
			},
			Code: []byte{0x25, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x00 || !c.Z || c.N {
					t.Fatalf("A=%02X Z=%v N=%v, want 00 true false", c.A, c.Z, c.N)
				}
			},
		},
		{
			Name: "AND A, (X) (0x26)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x80
				c.X = 0x1A
				b.Mem[0x001A] = 0x7F
			},
			Code: []byte{0x26},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x00 || !c.Z || c.N {
					t.Fatalf("A=%02X Z=%v N=%v, want 00 true false", c.A, c.Z, c.N)
				}
			},
		},
		{
			Name: "OR A, abs (0x05)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x80
				b.Mem[0x03CD] = 0x01
			},
			Code: []byte{0x05, 0xCD, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x81 || !c.N || c.Z {
					t.Fatalf("A=%02X N=%v Z=%v, want 81 true false", c.A, c.N, c.Z)
				}
			},
		},
		{
			Name: "OR dp, dp (0x09)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0047] = 0x40
				b.Mem[0x005E] = 0x02
			},
			Code: []byte{0x09, 0x47, 0x5E},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x005E); got != 0x42 {
					t.Fatalf("mem[005E] = %02X, want 42", got)
				}
				if c.Z || c.N {
					t.Fatalf("flags Z=%v N=%v, want false false", c.Z, c.N)
				}
			},
		},
		{
			Name: "LSR abs (0x4C)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x03C1] = 0x03
			},
			Code: []byte{0x4C, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03C1); got != 0x01 {
					t.Fatalf("mem[03C1] = %02X, want 01", got)
				}
				if !c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ASL abs (0x0C)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x03CE] = 0x80
			},
			Code: []byte{0x0C, 0xCE, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x03CE); got != 0x00 {
					t.Fatalf("mem[03CE] = %02X, want 00", got)
				}
				if !c.C || !c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want true true false", c.C, c.Z, c.N)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestArithmetic(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "ADC A, #imm (No Carry)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0x00; c.C = false },
			Code: []byte{0x88, 0x10},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x10 {
					t.Errorf("Expected A=10, got %02X", c.A)
				}
				if c.C {
					t.Error("Expected Carry clear")
				}
			},
		},
		{
			Name: "ADC A, #imm (Overflow)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0x10; c.C = false },
			Code: []byte{0x88, 0xFF},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F {
					t.Errorf("Expected A=0F, got %02X", c.A)
				}
				if !c.C {
					t.Error("Expected Carry set")
				}
			},
		},
		{
			Name: "SBC A, #imm (No Borrow)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0x10; c.C = true }, // C=1 is no borrow
			Code: []byte{0xA8, 0x01},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F {
					t.Errorf("Expected A=0F, got %02X", c.A)
				}
				if !c.C {
					t.Error("Expected Carry set (no borrow)")
				}
			},
		},
		{
			Name: "SBC A, #imm (Borrow)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0x0F; c.C = true },
			Code: []byte{0xA8, 0x10},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xFF {
					t.Errorf("Expected A=FF, got %02X", c.A)
				}
				if c.C {
					t.Errorf("SBC Carry should be clear (borrow)")
				}
			},
		},
		{
			Name: "SBC A, abs (0xA5)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.C = true
				b.Mem[0x03C1] = 0x01
			},
			Code: []byte{0xA5, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F || !c.C {
					t.Fatalf("A=%02X C=%v, want 0F true", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, abs+Y (0xB6)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.C = true
				c.Y = 0x0A
				b.Mem[0x1182] = 0x01
			},
			Code: []byte{0xB6, 0x78, 0x11},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F || !c.C {
					t.Fatalf("A=%02X C=%v, want 0F true", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, abs+X (0xB5)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.C = true
				c.X = 0x0E
				b.Mem[0x036F] = 0x01
			},
			Code: []byte{0xB5, 0x61, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F || !c.C {
					t.Fatalf("A=%02X C=%v, want 0F true", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC A, abs+Y (0x96)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.C = true
				c.Y = 0x0A
				b.Mem[0x1182] = 0x01
			},
			Code: []byte{0x96, 0x78, 0x11},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x12 || c.C {
					t.Fatalf("A=%02X C=%v, want 12 false", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC A, abs (0x85)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x1E
				c.C = true
				b.Mem[0x03C1] = 0x01
			},
			Code: []byte{0x85, 0xC1, 0x03},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x20 || c.C {
					t.Fatalf("A=%02X C=%v, want 20 false", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC A, abs+X (0x95)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.X = 0x0E
				b.Mem[0x02FE] = 0x01
			},
			Code: []byte{0x95, 0xF0, 0x02},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x11 || c.C {
					t.Fatalf("A=%02X C=%v, want 11 false", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC dp, #imm (0x98)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x0016] = 0x10
			},
			Code: []byte{0x98, 0x1F, 0x16},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0016); got != 0x30 {
					t.Fatalf("mem[0016] = %02X, want 30", got)
				}
				if c.C || c.Z || c.N {
					t.Fatalf("flags C=%v Z=%v N=%v, want false false false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ADC A, (dp)+Y (0x97)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.Y = 0x02
				b.Mem[0x0014] = 0x00
				b.Mem[0x0015] = 0x20
				b.Mem[0x2002] = 0x03
			},
			Code: []byte{0x97, 0x14},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x13 || c.C {
					t.Fatalf("A=%02X C=%v, want 13 false", c.A, c.C)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestAccumulatorReadAddressingVariants(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "OR A, (X) (0x06)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.X = 0x22
				b.Mem[0x0022] = 0x05
			},
			Code: []byte{0x06},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x15 {
					t.Fatalf("A=%02X, want 15", c.A)
				}
			},
		},
		{
			Name: "OR A, (dp+X) (0x07)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.X = 0x02
				b.Mem[0x0032] = 0x00
				b.Mem[0x0033] = 0x20
				b.Mem[0x2000] = 0x05
			},
			Code: []byte{0x07, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x15 {
					t.Fatalf("A=%02X, want 15", c.A)
				}
			},
		},
		{
			Name: "AND A, (dp+X) (0x27)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xF3
				c.X = 0x02
				b.Mem[0x0032] = 0x00
				b.Mem[0x0033] = 0x20
				b.Mem[0x2000] = 0x0F
			},
			Code: []byte{0x27, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 {
					t.Fatalf("A=%02X, want 03", c.A)
				}
			},
		},
		{
			Name: "CMP A, abs (0x65)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x20
				b.Mem[0x2000] = 0x10
			},
			Code: []byte{0x65, 0x00, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x20 || !c.C || c.Z || c.N {
					t.Fatalf("A=%02X C=%v Z=%v N=%v, want 20 true false false", c.A, c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "CMP A, (X) (0x66)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x20
				c.X = 0x22
				b.Mem[0x0022] = 0x20
			},
			Code: []byte{0x66},
			Check: func(t *testing.T, c *SPC700) {
				if !c.C || !c.Z || c.N {
					t.Fatalf("C=%v Z=%v N=%v, want true true false", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "CMP A, (dp+X) (0x67)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x10
				c.X = 0x02
				b.Mem[0x0032] = 0x00
				b.Mem[0x0033] = 0x20
				b.Mem[0x2000] = 0x20
			},
			Code: []byte{0x67, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.C || c.Z || !c.N {
					t.Fatalf("C=%v Z=%v N=%v, want false false true", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ADC A, (X) (0x86)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x01
				c.C = true
				c.X = 0x22
				b.Mem[0x0022] = 0x02
			},
			Code: []byte{0x86},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x04 || c.C {
					t.Fatalf("A=%02X C=%v, want 04 false", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC A, (dp+X) (0x87)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x01
				c.X = 0x02
				b.Mem[0x0032] = 0x00
				b.Mem[0x0033] = 0x20
				b.Mem[0x2000] = 0x02
			},
			Code: []byte{0x87, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || c.C {
					t.Fatalf("A=%02X C=%v, want 03 false", c.A, c.C)
				}
			},
		},
		{
			Name: "ADC A, dp+X (0x94)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x01
				c.X = 0x02
				b.Mem[0x0032] = 0x02
			},
			Code: []byte{0x94, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || c.C {
					t.Fatalf("A=%02X C=%v, want 03 false", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, (X) (0xA6)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x05
				c.C = true
				c.X = 0x22
				b.Mem[0x0022] = 0x02
			},
			Code: []byte{0xA6},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || !c.C {
					t.Fatalf("A=%02X C=%v, want 03 true", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, (dp+X) (0xA7)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x05
				c.C = true
				c.X = 0x02
				b.Mem[0x0032] = 0x00
				b.Mem[0x0033] = 0x20
				b.Mem[0x2000] = 0x02
			},
			Code: []byte{0xA7, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || !c.C {
					t.Fatalf("A=%02X C=%v, want 03 true", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, dp+X (0xB4)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x05
				c.C = true
				c.X = 0x02
				b.Mem[0x0032] = 0x02
			},
			Code: []byte{0xB4, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || !c.C {
					t.Fatalf("A=%02X C=%v, want 03 true", c.A, c.C)
				}
			},
		},
		{
			Name: "SBC A, (dp)+Y (0xB7)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x05
				c.C = true
				c.Y = 0x03
				b.Mem[0x0040] = 0x00
				b.Mem[0x0041] = 0x20
				b.Mem[0x2003] = 0x02
			},
			Code: []byte{0xB7, 0x40},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x03 || !c.C {
					t.Fatalf("A=%02X C=%v, want 03 true", c.A, c.C)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestMemoryALUOpcodes(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "OR dp, #imm (0x18)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x20] = 0x10
			},
			Code: []byte{0x18, 0x05, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x20); got != 0x15 {
					t.Fatalf("mem[20]=%02X, want 15", got)
				}
			},
		},
		{
			Name: "OR (X), (Y) (0x19)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0x10
				b.Mem[0x20] = 0x05
			},
			Code: []byte{0x19},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x10); got != 0x15 {
					t.Fatalf("mem[10]=%02X, want 15", got)
				}
			},
		},
		{
			Name: "AND dp, dp (0x29)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x30] = 0x0F
				b.Mem[0x31] = 0xF3
			},
			Code: []byte{0x29, 0x30, 0x31},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x31); got != 0x03 {
					t.Fatalf("mem[31]=%02X, want 03", got)
				}
			},
		},
		{
			Name: "AND (X), (Y) (0x39)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0xF3
				b.Mem[0x20] = 0x0F
			},
			Code: []byte{0x39},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x10); got != 0x03 {
					t.Fatalf("mem[10]=%02X, want 03", got)
				}
			},
		},
		{
			Name: "EOR dp, dp (0x49)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x30] = 0x0F
				b.Mem[0x31] = 0xF0
			},
			Code: []byte{0x49, 0x30, 0x31},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x31); got != 0xFF {
					t.Fatalf("mem[31]=%02X, want FF", got)
				}
			},
		},
		{
			Name: "EOR dp, #imm (0x58)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x20] = 0xF0
			},
			Code: []byte{0x58, 0x0F, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x20); got != 0xFF {
					t.Fatalf("mem[20]=%02X, want FF", got)
				}
			},
		},
		{
			Name: "EOR (X), (Y) (0x59)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0xF0
				b.Mem[0x20] = 0x0F
			},
			Code: []byte{0x59},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x10); got != 0xFF {
					t.Fatalf("mem[10]=%02X, want FF", got)
				}
			},
		},
		{
			Name: "CMP (X), (Y) (0x79)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0x10
				b.Mem[0x20] = 0x20
			},
			Code: []byte{0x79},
			Check: func(t *testing.T, c *SPC700) {
				if c.C || c.Z || !c.N {
					t.Fatalf("C=%v Z=%v N=%v, want false false true", c.C, c.Z, c.N)
				}
			},
		},
		{
			Name: "ADC dp, dp (0x89)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x30] = 0x02
				b.Mem[0x31] = 0x01
			},
			Code: []byte{0x89, 0x30, 0x31},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x31); got != 0x04 {
					t.Fatalf("mem[31]=%02X, want 04", got)
				}
			},
		},
		{
			Name: "ADC (X), (Y) (0x99)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0x01
				b.Mem[0x20] = 0x02
			},
			Code: []byte{0x99},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x10); got != 0x03 {
					t.Fatalf("mem[10]=%02X, want 03", got)
				}
			},
		},
		{
			Name: "SBC dp, dp (0xA9)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x30] = 0x02
				b.Mem[0x31] = 0x05
			},
			Code: []byte{0xA9, 0x30, 0x31},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x31); got != 0x03 {
					t.Fatalf("mem[31]=%02X, want 03", got)
				}
			},
		},
		{
			Name: "SBC dp, #imm (0xB8)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x20] = 0x05
			},
			Code: []byte{0xB8, 0x02, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x20); got != 0x03 {
					t.Fatalf("mem[20]=%02X, want 03", got)
				}
			},
		},
		{
			Name: "SBC (X), (Y) (0xB9)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				c.X = 0x10
				c.Y = 0x20
				b.Mem[0x10] = 0x05
				b.Mem[0x20] = 0x02
			},
			Code: []byte{0xB9},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x10); got != 0x03 {
					t.Fatalf("mem[10]=%02X, want 03", got)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestDirectPageWordWrap(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOVW YA, dp wraps at page end",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x00FF] = 0x34
				b.Mem[0x0000] = 0x12
				b.Mem[0x0100] = 0x56
			},
			Code: []byte{0xBA, 0xFF},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x34 || c.Y != 0x12 {
					t.Fatalf("MOVW YA, dp wrap failed: A=%02X Y=%02X", c.A, c.Y)
				}
			},
		},
		{
			Name: "MOVW dp, YA wraps at page end",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x78
				c.Y = 0x56
				b.Mem[0x0000] = 0x00
				b.Mem[0x0100] = 0x00
			},
			Code: []byte{0xDA, 0xFF},
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x00FF) != 0x78 {
					t.Fatalf("MOVW dp, YA low byte wrong: %02X", c.bus.Read(0x00FF))
				}
				if c.bus.Read(0x0000) != 0x56 {
					t.Fatalf("MOVW dp, YA high byte did not wrap: %02X", c.bus.Read(0x0000))
				}
				if c.bus.Read(0x0100) != 0x00 {
					t.Fatalf("MOVW dp, YA wrote past direct page: %02X", c.bus.Read(0x0100))
				}
			},
		},
		{
			Name: "SUBW YA, dp subtracts direct word",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x34
				c.Y = 0x12
				b.Mem[0x0020] = 0x04
				b.Mem[0x0021] = 0x02
			},
			Code: []byte{0x9A, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x30 || c.Y != 0x10 || !c.C || c.Z || c.N {
					t.Fatalf("YA=%02X%02X C=%v Z=%v N=%v, want 1030 true false false", c.Y, c.A, c.C, c.Z, c.N)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestBranching(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "BRA rel (Forward)",
			Code: []byte{0x2F, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00}, // BRA +5. PC at FFC2 -> FFC7
			Check: func(t *testing.T, c *SPC700) {
				// Init PC = FFC0. +2 (fetch opcode+operand) = FFC2. +5 = FFC7.
				if c.PC != 0xFFC7 {
					t.Errorf("BRA forward failed. PC=%X", c.PC)
				}
			},
		},
		{
			Name: "BNE rel (Taken)",
			Init: func(c *SPC700, b *TestBus) { c.Z = false },
			Code: []byte{0xD0, 0x02, 0x00, 0x00}, // BNE +2. PC=FFC2 -> FFC4
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC4 {
					t.Errorf("BNE taken failed. PC=%X", c.PC)
				}
			},
		},
		{
			Name: "BNE rel (Not Taken)",
			Init: func(c *SPC700, b *TestBus) { c.Z = true },
			Code: []byte{0xD0, 0x02, 0x00, 0x00}, // BNE +2. PC=FFC2. Not taken.
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC2 {
					t.Errorf("BNE not taken failed. PC=%X", c.PC)
				}
			},
		},
		{
			Name: "BEQ rel (Taken)",
			Init: func(c *SPC700, b *TestBus) { c.Z = true },
			Code: []byte{0xF0, 0xFE}, // BEQ -2. PC=FFC2 -> FFC0
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC0 {
					t.Errorf("BEQ taken backward failed. PC=%X", c.PC)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestBitManipulation(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "SET1 dp.0 (0x02)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x10] = 0x00 },
			Code: []byte{0x02, 0x10}, // SET1 $10.0
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x10) != 0x01 {
					t.Errorf("SET1 bit 0 failed. Got %02X", c.bus.Read(0x10))
				}
			},
		},
		{
			Name: "SET1 dp.1 (0x22)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x20] = 0x00 },
			Code: []byte{0x22, 0x20}, // SET1 $20.1
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x20) != 0x02 {
					t.Errorf("SET1 bit 1 failed. Got %02X", c.bus.Read(0x20))
				}
			},
		},
		{
			Name: "CLR1 dp.0 (0x12)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x30] = 0xFF },
			Code: []byte{0x12, 0x30}, // CLR1 $30.0
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x30) != 0xFE {
					t.Errorf("CLR1 bit 0 failed. Got %02X", c.bus.Read(0x30))
				}
			},
		},
		{
			Name: "CLR1 dp.7 (0xF2)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x40] = 0xFF },
			Code: []byte{0xF2, 0x40}, // CLR1 $40.7
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x40) != 0x7F {
					t.Errorf("CLR1 bit 7 failed. Got %02X", c.bus.Read(0x40))
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestBitCarryOperations(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOV1 C, abs.bit (0xAA)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0010] = 0x04
			},
			Code: []byte{0xAA, 0x10, 0x40},
			Check: func(t *testing.T, c *SPC700) {
				if !c.C {
					t.Fatal("expected carry set from bit read")
				}
			},
		},
		{
			Name: "MOV1 abs.bit, C (0xCA)",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
				b.Mem[0x0010] = 0x00
			},
			Code: []byte{0xCA, 0x10, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0010); got != 0x02 {
					t.Fatalf("MOV1 abs.bit, C wrote %02X, want 02", got)
				}
			},
		},
		{
			Name: "NOT1 abs.bit (0xEA)",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0010] = 0x02
			},
			Code: []byte{0xEA, 0x10, 0x20},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x0010); got != 0x00 {
					t.Fatalf("NOT1 abs.bit wrote %02X, want 00", got)
				}
				if c.Stopped {
					t.Fatal("NOT1 abs.bit should not stop the CPU")
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestMoveAndStackExtensions(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOV !abs, Y (0xCC)",
			Init: func(c *SPC700, b *TestBus) {
				c.Y = 0x5A
			},
			Code: []byte{0xCC, 0x34, 0x12},
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x1234); got != 0x5A {
					t.Fatalf("MOV !abs, Y wrote %02X, want 5A", got)
				}
			},
		},
		{
			Name: "MOV A, (X)+ (0xBF)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x10
				b.Mem[0x0010] = 0xA5
			},
			Code: []byte{0xBF},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xA5 {
					t.Fatalf("MOV A, (X)+ set A=%02X, want A5", c.A)
				}
				if c.X != 0x11 {
					t.Fatalf("MOV A, (X)+ set X=%02X, want 11", c.X)
				}
				if !c.N || c.Z {
					t.Fatalf("MOV A, (X)+ flags N=%v Z=%v, want N=true Z=false", c.N, c.Z)
				}
			},
		},
		{
			Name: "POP X (0xCE)",
			Init: func(c *SPC700, b *TestBus) {
				c.SP = 0xEE
				b.Mem[0x01EF] = 0x7C
			},
			Code: []byte{0xCE},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x7C {
					t.Fatalf("POP X set X=%02X, want 7C", c.X)
				}
				if c.SP != 0xEF {
					t.Fatalf("POP X set SP=%02X, want EF", c.SP)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestLogic(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "AND A, #imm (0x28)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xFF },
			Code: []byte{0x28, 0x0F}, // AND A, #0x0F
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x0F {
					t.Errorf("AND failed. Got %02X", c.A)
				}
				if c.Z {
					t.Errorf("Z flag should be clear")
				}
				if c.N {
					t.Errorf("N flag should be clear")
				}
			},
		},
		{
			Name: "AND A, #imm (Zero Result)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xF0 },
			Code: []byte{0x28, 0x0F}, // AND A, #0x0F -> 0x00
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x00 {
					t.Errorf("AND Zero failed. Got %02X", c.A)
				}
				if !c.Z {
					t.Errorf("Z flag should be set")
				}
			},
		},
		{
			Name: "OR A, #imm (0x08)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xF0 },
			Code: []byte{0x08, 0x0F}, // OR A, #0x0F -> 0xFF
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xFF {
					t.Errorf("OR failed. Got %02X", c.A)
				}
				if !c.N {
					t.Errorf("N flag should be set")
				}
			},
		},
		{
			Name: "EOR A, #imm (0x48)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xAA },
			Code: []byte{0x48, 0xFF}, // EOR A, #0xFF -> 0x55
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x55 {
					t.Errorf("EOR failed. Got %02X", c.A)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestIncDec(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "INC A",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xFE },
			Code: []byte{0xBC},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xFF {
					t.Errorf("INC A failed. Got %02X", c.A)
				}
				if !c.N {
					t.Errorf("N flag should be set")
				}
			},
		},
		{
			Name: "INC A (Overflow)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0xFF },
			Code: []byte{0xBC},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x00 {
					t.Errorf("INC A overflow failed. Got %02X", c.A)
				}
				if !c.Z {
					t.Errorf("Z flag should be set")
				}
			},
		},
		{
			Name: "DEC X",
			Init: func(c *SPC700, b *TestBus) { c.X = 0x01 },
			Code: []byte{0x1D},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x00 {
					t.Errorf("DEC X failed. Got %02X", c.X)
				}
				if !c.Z {
					t.Errorf("Z flag should be set")
				}
			},
		},
		{
			Name: "DEC Y",
			Init: func(c *SPC700, b *TestBus) { c.Y = 0x00 },
			Code: []byte{0xDC},
			Check: func(t *testing.T, c *SPC700) {
				if c.Y != 0xFF {
					t.Errorf("DEC Y wrap failed. Got %02X", c.Y)
				}
				if !c.N {
					t.Errorf("N flag should be set")
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestAddressing(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOV A, dp (0xE4)",
			Init: func(c *SPC700, b *TestBus) {
				c.P = false
				b.Mem[0x0010] = 0xAA
			},
			Code: []byte{0xE4, 0x10}, // MOV A, $10
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xAA {
					t.Errorf("MOV A, dp failed. Got %02X", c.A)
				}
			},
		},
		{
			Name: "MOV dp, A (0xC4)",
			Init: func(c *SPC700, b *TestBus) { c.A = 0x55 },
			Code: []byte{0xC4, 0x20}, // MOV $20, A
			Check: func(t *testing.T, c *SPC700) {
				if c.bus.Read(0x0020) != 0x55 {
					t.Errorf("MOV dp, A failed. Got %02X", c.bus.Read(0x0020))
				}
			},
		},
		{
			Name: "MOV A, dp+X (0xF4)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x05
				b.Mem[0x0015] = 0xBB
			},
			Code: []byte{0xF4, 0x10}, // MOV A, $10+X
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xBB {
					t.Errorf("MOV A, dp+X failed. Got %02X", c.A)
				}
			},
		},
		{
			Name: "MOV X, dp+Y (0xF9)",
			Init: func(c *SPC700, b *TestBus) {
				c.Y = 0x02
				b.Mem[0x0032] = 0x81
			},
			Code: []byte{0xF9, 0x30},
			Check: func(t *testing.T, c *SPC700) {
				if c.X != 0x81 || !c.N || c.Z {
					t.Fatalf("X=%02X N=%v Z=%v, want 81 true false", c.X, c.N, c.Z)
				}
			},
		},
		{
			Name: "MOV Y, A (0xEB)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xCC
				c.Y = 0x00
			},
			Code: []byte{0xFD}, // MOV Y, A
			Check: func(t *testing.T, c *SPC700) {
				if c.Y != 0xCC {
					t.Errorf("MOV Y, A failed. Got %02X", c.Y)
				}
			},
		},
		{
			Name: "MOV A, (X) (0xE6)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x30
				b.Mem[0x0030] = 0xDD // Data at [X] (Page 0)
			},
			Code: []byte{0xE6}, // MOV A, (X)
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xDD {
					t.Errorf("MOV A, (X) failed. Got %02X", c.A)
				}
			},
		},
		{
			Name: "MOV A, (dp+X) (0xE7)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x02
				c.Y = 0x05
				b.Mem[0x0012] = 0x00
				b.Mem[0x0013] = 0x30
				b.Mem[0x3000] = 0xEE
				b.Mem[0x3005] = 0x44
			},
			Code: []byte{0xE7, 0x10}, // MOV A, ($10+X)
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xEE {
					t.Errorf("MOV A, (dp+X) failed. Got %02X", c.A)
				}
			},
		},
		{
			Name: "MOV A, (dp)+Y (0xF7)",
			Init: func(c *SPC700, b *TestBus) {
				c.X = 0x02
				c.Y = 0x05
				b.Mem[0x0010] = 0x00
				b.Mem[0x0011] = 0x30
				b.Mem[0x3000] = 0x44
				b.Mem[0x3005] = 0xEE
			},
			Code: []byte{0xF7, 0x10}, // MOV A, ($10)+Y
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xEE {
					t.Errorf("MOV A, (dp)+Y failed. Got %02X", c.A)
				}
			},
		},
		{
			Name: "MOV (dp)+Y, A (0xD7)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x5A
				c.Y = 0x05
				b.Mem[0x0010] = 0x00
				b.Mem[0x0011] = 0x30
			},
			Code: []byte{0xD7, 0x10}, // MOV ($10)+Y, A
			Check: func(t *testing.T, c *SPC700) {
				if got := c.bus.Read(0x3005); got != 0x5A {
					t.Fatalf("MOV (dp)+Y, A stored %02X, want 5A", got)
				}
				if c.Cycles != 7 {
					t.Fatalf("MOV (dp)+Y, A cycles = %d, want 7", c.Cycles)
				}
			},
		},
		// IPL Support Checks
		{
			Name: "MOV dp, #imm (0x8F)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x40] = 0x00 },
			Code: []byte{0x8F, 0x99, 0x40}, // MOV $40, #0x99
			Check: func(t *testing.T, c *SPC700) {
				if b := c.bus.Read(0x40); b != 0x99 {
					t.Errorf("MOV dp, #imm failed. Got %02X", b)
				}
			},
		},
		{
			Name: "MOV dp, dp (0xFA)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x50] = 0xAA; b.Mem[0x51] = 0x00 },
			Code: []byte{0xFA, 0x50, 0x51}, // MOV $51, $50
			Check: func(t *testing.T, c *SPC700) {
				if b := c.bus.Read(0x51); b != 0xAA {
					t.Errorf("MOV dp, dp failed. Got %02X", b)
				}
			},
		},
		{
			Name: "CMP dp, #imm (0x78)",
			Init: func(c *SPC700, b *TestBus) { b.Mem[0x60] = 0x10; c.Z = true },
			Code: []byte{0x78, 0x10, 0x60}, // CMP $60, #0x10. (Equal)
			Check: func(t *testing.T, c *SPC700) {
				if !c.Z {
					t.Errorf("CMP Equal failed. Z should be set")
				}
				if !c.C {
					t.Errorf("CMP Equal failed. C should be set (No Borrow)")
				}
				// CMP: C is set if lhs >= rhs (No borrow).
				// 10 - 10 = 0. 10 >= 10. C=1.
				// Wait. 6502 CMP: C=1 if A >= M.
				// My logic in Opcode: c.C = val >= imm.
				// So expected C=true.
			},
		},
	}
	RunTests(t, tests)
}

func TestTCALLUsesVectorTable(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "TCALL 0 reads FFDE vector",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0xFFDE] = 0x34
				b.Mem[0xFFDF] = 0x12
			},
			Code: []byte{0x01},
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0x1234 {
					t.Fatalf("PC = %04X, want 1234", c.PC)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestBitBranchOpcodes(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "BBS dp.0 branches when bit set",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0010] = 0x01
			},
			Code: []byte{0x03, 0x10, 0x02},
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC5 {
					t.Fatalf("PC = %04X, want FFC5", c.PC)
				}
			},
		},
		{
			Name: "BBC dp.0 branches when bit clear",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x0010] = 0x00
			},
			Code: []byte{0x13, 0x10, 0x02},
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFFC5 {
					t.Fatalf("PC = %04X, want FFC5", c.PC)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestMOVYAbsUsesAbsoluteAddressing(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MOV Y, abs reads 16-bit address",
			Init: func(c *SPC700, b *TestBus) {
				b.Mem[0x1234] = 0x77
				b.Mem[0x0034] = 0x11
			},
			Code: []byte{0xEC, 0x34, 0x12},
			Check: func(t *testing.T, c *SPC700) {
				if c.Y != 0x77 {
					t.Fatalf("Y = %02X, want 77", c.Y)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestWordOps(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "ADDW dp",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x34
				c.Y = 0x12
				b.Mem[0x0010] = 0x02
				b.Mem[0x0011] = 0x01
			},
			Code: []byte{0x7A, 0x10},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x36 || c.Y != 0x13 {
					t.Fatalf("YA = %02X%02X, want 1336", c.Y, c.A)
				}
			},
		},
		{
			Name: "CMPW dp sets carry on no borrow",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x34
				c.Y = 0x12
				b.Mem[0x0010] = 0x02
				b.Mem[0x0011] = 0x01
			},
			Code: []byte{0x5A, 0x10},
			Check: func(t *testing.T, c *SPC700) {
				if !c.C {
					t.Fatal("carry clear, want set")
				}
				if c.Z || c.N {
					t.Fatalf("flags Z=%v N=%v, want clear", c.Z, c.N)
				}
			},
		},
	}
	RunTests(t, tests)
}

func TestMulDivAndControlOps(t *testing.T) {
	tests := []OpcodeTest{
		{
			Name: "MUL YA",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x05
				c.Y = 0x06
			},
			Code: []byte{0xCF},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x1E || c.Y != 0x00 {
					t.Fatalf("YA = %02X%02X, want 001E", c.Y, c.A)
				}
			},
		},
		{
			Name: "DIV YA, X",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x14
				c.Y = 0x00
				c.X = 0x05
			},
			Code: []byte{0x9E},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x04 || c.Y != 0x00 {
					t.Fatalf("A=%02X Y=%02X, want 04 00", c.A, c.Y)
				}
			},
		},
		{
			Name: "DAA adjusts accumulator",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0x9A
			},
			Code: []byte{0xDF},
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0x00 || !c.C || !c.Z {
					t.Fatalf("A=%02X C=%v Z=%v, want 00 true true", c.A, c.C, c.Z)
				}
			},
		},
		{
			Name: "PCALL jumps to FFxx",
			Code: []byte{0x4F, 0x80},
			Check: func(t *testing.T, c *SPC700) {
				if c.PC != 0xFF80 {
					t.Fatalf("PC=%04X, want FF80", c.PC)
				}
				if c.SP != 0xED {
					t.Fatalf("SP=%02X, want ED", c.SP)
				}
				if lo, hi := c.bus.Read(0x01EE), c.bus.Read(0x01EF); lo != 0xC2 || hi != 0xFF {
					t.Fatalf("return bytes=%02X/%02X, want C2/FF", lo, hi)
				}
			},
		},
		{
			Name: "NOTC flips carry",
			Init: func(c *SPC700, b *TestBus) {
				c.C = true
			},
			Code: []byte{0xED},
			Check: func(t *testing.T, c *SPC700) {
				if c.C {
					t.Fatal("carry still set")
				}
			},
		},
		{
			Name: "POP Y restores stack value",
			Init: func(c *SPC700, b *TestBus) {
				c.push(0x5A)
			},
			Code: []byte{0xEE},
			Check: func(t *testing.T, c *SPC700) {
				if c.Y != 0x5A {
					t.Fatalf("Y = %02X, want 5A", c.Y)
				}
			},
		},
		{
			Name: "SLEEP stops execution",
			Code: []byte{0xEF},
			Check: func(t *testing.T, c *SPC700) {
				if !c.Stopped {
					t.Fatal("SPC700 should be stopped")
				}
			},
		},
	}
	RunTests(t, tests)
}
