package computation_test

import (
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/divergence"
)

func ExampleExtract() {
	// A 3-step trace:
	// 0: LDY $1F05    (read $7E:1F05 = 115)
	// 1: LDA $098000,Y (read ROM table $09:8073 = 20)
	// 2: STA $1F06    (write $7E:1F06 = 20)
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xAC, // LDY abs
			Mnemonic:  "LDY",
			Registers: divergence.RegisterState{Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x7E1F05, Value: 115, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008003,
			Opcode:    0xB9, // LDA abs,Y
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x098073, Value: 20, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008006,
			Opcode:    0x8D, // STA abs
			Mnemonic:  "STA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Writes:    []divergence.MemoryAccess{{Address: 0x7E1F06, Value: 20, Width: 8}},
			Sequence:  2,
		},
	}

	comp, err := computation.Extract(steps, 2, computation.ExtractOptions{})
	if err != nil {
		log.Fatalf("extract failed: %v", err)
	}

	res, err := computation.Execute(comp, nil)
	if err != nil {
		log.Fatalf("execute failed: %v", err)
	}

	fmt.Println("Target:", comp.Target.Description)
	fmt.Printf("Instructions: %d\n", len(comp.Instructions))
	fmt.Printf("Inputs: %s = %d\n", comp.Inputs[0].Name, comp.Inputs[0].InitialValue)
	fmt.Printf("Executed Result: %d\n", res)

	// Output:
	// Target: store 0x14 to $7E:1F06
	// Instructions: 3
	// Inputs: in_1f05 = 115
	// Executed Result: 20
}
