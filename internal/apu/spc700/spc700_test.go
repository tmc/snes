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
			Name: "MOV Y, A (0xEB)",
			Init: func(c *SPC700, b *TestBus) {
				c.A = 0xCC
				c.Y = 0x00
			},
			Code: []byte{0xEB}, // MOV Y, A
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
			Name: "MOV A, (dp)+Y (0xE7)",
			Init: func(c *SPC700, b *TestBus) {
				c.Y = 0x05
				// Pointer at dp = 0x0010
				b.Mem[0x0010] = 0x00
				b.Mem[0x0011] = 0x30 // Ptr -> 0x3000
				// Target = 0x3000 + Y(5) = 0x3005
				b.Mem[0x3005] = 0xEE
			},
			Code: []byte{0xE7, 0x10}, // MOV A, ($10)+Y
			Check: func(t *testing.T, c *SPC700) {
				if c.A != 0xEE {
					t.Errorf("MOV A, (dp)+Y failed. Got %02X", c.A)
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
