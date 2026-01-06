package cpu_test

import (
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cpu"
)

func TestRAMInitAndLDA(t *testing.T) {
	sys := snes.NewSystem(nil)
	c := sys.CPU
	bus := sys.Bus

	// 1. Verify RAM at 0x0000 is 0
	val := bus.Read(0x000000)
	if val != 0 {
		t.Fatalf("RAM[0] not 0! Got %02X", val)
	}

	// 2. Setup Pointer at 0x0010 (Direct Page)
	// Write Pointer 1010 at 0010
	bus.Write(0x000010, 0x10)
	bus.Write(0x000011, 0x10)
	bus.Write(0x000012, 0x00) // Bank 0

	// Verify Pointer
	p0 := bus.Read(0x000010)
	p1 := bus.Read(0x000011)
	p2 := bus.Read(0x000012)
	t.Logf("Pointer: %02X %02X %02X", p0, p1, p2)

	// Check Opcode
	op := cpu.Opcodes[0xB7]
	t.Logf("Opcode B7: Name=%s Mode=%d Size=%d", op.Name, op.Mode, op.Size)

	// Verify Target
	tgt := bus.Read(0x001012)
	t.Logf("Target: %02X", tgt)

	// 3. Write Pattern at Target 00:1010 + Y
	// If Y=2. Target 1012.
	bus.Write(0x001012, 0x42)

	// 4. Setup CPU
	c.P = 0x30   // M=1, X=1
	c.D = 0x0000 // DP = 0000
	c.Y = 0x0002 // Y = 2
	c.PB = 0x00

	// 5. Inject Instruction: LDA [10], Y (B7 10) at 00:2000 (RAM)
	c.PC = 0x2000
	bus.Write(0x002000, 0xB7)
	bus.Write(0x002001, 0x10)

	// 6. Execute Step
	c.Step()

	// 7. Verify A = 0x42
	if c.A&0xFF != 0x42 {
		t.Errorf("LDA [10],Y failed. A=%02X, expected 42. PC=%04X", c.A&0xFF, c.PC)
	}
}
