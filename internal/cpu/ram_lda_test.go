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

	// 1. Setup Pointer at 0x0010 (Direct Page)
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

	// 2. Write Pattern at Target 00:1010 + Y
	// If Y=2. Target 1012.
	bus.Write(0x001012, 0x42)

	// 3. Setup CPU
	c.P = 0x30   // M=1, X=1
	c.D = 0x0000 // DP = 0000
	c.Y = 0x0002 // Y = 2
	c.PB = 0x00

	// 4. Inject instruction into low WRAM mirror.
	// $00:0000-$1FFF is mapped internal WRAM; $00:2000 is open bus.
	c.PC = 0x0200
	bus.Write(0x000200, 0xB7)
	bus.Write(0x000201, 0x10)

	// 5. Execute Step
	c.Step()

	// 6. Verify A = 0x42
	if c.A&0xFF != 0x42 {
		t.Errorf("LDA [10],Y failed. A=%02X, expected 42. PC=%04X", c.A&0xFF, c.PC)
	}
}
