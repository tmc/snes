package updsp

import (
	"fmt"
	"testing"
)

func ExampleMapper_Power() {
	m := NewMapper(NewIO(NewCore()), MapLoROM)
	m.Power()
	fmt.Println(m.IO.Core.PC, m.IO.ReadSR())
	// Output: 0 0
}

func TestResetPreservesMemories(t *testing.T) {
	c := NewCore()
	c.PRG[0] = 3<<22 | 0x1234<<6 | regDR
	c.DROM[3], c.DRAM[4] = 0x5678, 0xabcd
	c.STK[0], c.SP = 17, 1
	c.Reset()
	if c.DROM[3] != 0x5678 || c.DRAM[4] != 0xabcd {
		t.Fatal("reset erased data memory")
	}
	if c.STK != [4]uint16{} || c.SP != 0 {
		t.Fatal("reset retained return stack")
	}
	c.Step(1)
	if c.DR != 0x1234 {
		t.Fatalf("firmware result after reset = %x", c.DR)
	}
}
