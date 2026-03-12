package cpu

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
)

// SimpleRAM needed for block copy tests
type BlockTestRAM struct {
	data map[uint32]uint8
}

func NewBlockTestRAM() *BlockTestRAM                                { return &BlockTestRAM{data: make(map[uint32]uint8)} }
func (m *BlockTestRAM) Read(address uint32) uint8                   { return m.data[address] }
func (m *BlockTestRAM) Write(address uint32, value uint8)           { m.data[address] = value }
func (m *BlockTestRAM) BlockRead(address uint32, length int) []byte { return nil }

func TestOpMVN(t *testing.T) {
	// MVN
	b := bus.NewBus()
	ram := NewBlockTestRAM()
	b.Map(0x000000, 0xFFFFFF, ram)
	c := NewCPU(b)

	// Setup
	// Copy 3 bytes from $10:2000 to $20:3000
	// Data: A, B, C
	b.Write(0x102000, 0xAA)
	b.Write(0x102001, 0xBB)
	b.Write(0x102002, 0xCC)

	c.X = 0x2000
	c.Y = 0x3000
	c.A = 2 // Count is A = N-1? No, A decrements until Underflow.
	// So if A=2, loops: 2, 1, 0. (3 bytes). Then FFFF -> Stop.

	// Instruction: MVN Dest(20) Src(10)
	// Opcode 54 at 0x100
	c.PC = 0x100
	c.PB = 0x00
	b.Write(0x000100, 0x54)
	b.Write(0x000101, 0x20) // Dest
	b.Write(0x000102, 0x10) // Src

	// Step 1
	c.Step()
	if c.DB != 0x20 {
		t.Errorf("DB not updated to 0x20 on first step")
	}
	if b.Read(0x203000) != 0xAA {
		t.Errorf("Byte 1 not accessed/written")
	}
	if c.PC != 0x100 {
		t.Errorf("PC should wraparound (0x100). Got %X", c.PC)
	}
	if c.A != 1 {
		t.Errorf("A should decrement to 1. Got %d", c.A)
	}

	// Step 2
	c.Step()
	if b.Read(0x203001) != 0xBB {
		t.Errorf("Byte 2 not written")
	}
	if c.A != 0 {
		t.Errorf("A should decrement to 0. Got %d", c.A)
	}

	// Step 3
	c.Step()
	if b.Read(0x203002) != 0xCC {
		t.Errorf("Byte 3 not written")
	}
	// A becomes FFFF
	if c.A != 0xFFFF {
		t.Errorf("A should Underflow to FFFF. Got %X", c.A)
	}
	// PC should advance now (moved past 54 20 10 -> 103)
	// Wait logic:
	// A!=FFFF -> PC-=3.
	// A==FFFF -> No decrement. PC has advanced +1 in fetchOp, +2 in fetchBytes. Total +3.
	// So PC is at 103.
	if c.PC != 0x103 {
		t.Errorf("PC should finish at 0x103. Got %X", c.PC)
	}
}

func TestOpMVP(t *testing.T) {
	// MVP (Backwards)
	b := bus.NewBus()
	ram := NewBlockTestRAM()
	b.Map(0x000000, 0xFFFFFF, ram)
	c := NewCPU(b)

	// Copy 2 bytes backward
	// Src: $10:2001 (BB), $10:2000 (AA)
	// Dest: $20:3001, $20:3000

	b.Write(0x102001, 0xBB)
	b.Write(0x102000, 0xAA)

	c.X = 0x2001
	c.Y = 0x3001
	c.A = 1 // 2 bytes

	c.PC = 0x100
	b.Write(0x000100, 0x44)
	b.Write(0x000101, 0x20)
	b.Write(0x000102, 0x10)

	// Run loop until PC advances
	steps := 0
	for c.PC == 0x100 {
		c.Step()
		steps++
		if steps > 10 {
			t.Fatal("Loop stuck")
		}
	}

	if steps != 2 {
		t.Errorf("Expected 2 steps, got %d", steps)
	}

	if b.Read(0x203001) != 0xBB {
		t.Errorf("High byte copy failed")
	}
	if b.Read(0x203000) != 0xAA {
		t.Errorf("Low byte copy failed")
	}

	if c.X != 0x20FF {
		t.Errorf("X should decrement to 20FF. Got %X", c.X)
	}
	if c.Y != 0x30FF {
		t.Errorf("Y should decrement to 30FF. Got %X", c.Y)
	}
}

func TestOpMVN_Index8Wrap(t *testing.T) {
	b := bus.NewBus()
	ram := NewBlockTestRAM()
	b.Map(0x000000, 0xFFFFFF, ram)
	c := NewCPU(b)

	c.E = false
	c.P = 0x10
	c.X = 0x00FF
	c.Y = 0x00FF
	c.A = 0

	b.Write(0x0000FF, 0xAB)

	c.PC = 0x100
	b.Write(0x000100, 0x54)
	b.Write(0x000101, 0x20)
	b.Write(0x000102, 0x00)

	c.Step()

	if got := b.Read(0x2000FF); got != 0xAB {
		t.Fatalf("destination byte = %02X, want AB", got)
	}
	if c.X != 0x0000 {
		t.Fatalf("X = %04X, want 0000", c.X)
	}
	if c.Y != 0x0000 {
		t.Fatalf("Y = %04X, want 0000", c.Y)
	}
}

func TestOpMVP_Index8Wrap(t *testing.T) {
	b := bus.NewBus()
	ram := NewBlockTestRAM()
	b.Map(0x000000, 0xFFFFFF, ram)
	c := NewCPU(b)

	c.E = false
	c.P = 0x10
	c.X = 0x0000
	c.Y = 0x0000
	c.A = 0

	b.Write(0x100000, 0xCD)

	c.PC = 0x100
	b.Write(0x000100, 0x44)
	b.Write(0x000101, 0x20)
	b.Write(0x000102, 0x10)

	c.Step()

	if got := b.Read(0x200000); got != 0xCD {
		t.Fatalf("destination byte = %02X, want CD", got)
	}
	if c.X != 0x00FF {
		t.Fatalf("X = %04X, want 00FF", c.X)
	}
	if c.Y != 0x00FF {
		t.Fatalf("Y = %04X, want 00FF", c.Y)
	}
}
