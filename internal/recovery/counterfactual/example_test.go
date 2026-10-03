package counterfactual_test

import (
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery/computation"
	"github.com/tmc/snes/internal/recovery/counterfactual"
	"github.com/tmc/snes/internal/recovery/divergence"
)

func ExampleWorkbench() {
	// A 3-step trace representing table lookup: 115 -> 20.
	steps := []divergence.Step{
		{
			Address:   0x008000,
			Opcode:    0xAC,
			Mnemonic:  "LDY",
			Registers: divergence.RegisterState{Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x7E1F05, Value: 115, Width: 8}},
			Sequence:  0,
		},
		{
			Address:   0x008003,
			Opcode:    0xB9,
			Mnemonic:  "LDA",
			Registers: divergence.RegisterState{A: 20, Y: 115},
			Reads:     []divergence.MemoryAccess{{Address: 0x098073, Value: 20, Width: 8}},
			Sequence:  1,
		},
		{
			Address:   0x008006,
			Opcode:    0x8D,
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
	// Supply neighboring table values in ROMData.
	comp.ROMData[0x098072] = 19
	comp.ROMData[0x098074] = 21

	wb, err := counterfactual.NewWorkbench(comp)
	if err != nil {
		log.Fatalf("new workbench: %v", err)
	}

	if err := wb.AddPerturbation("noop", nil, "no-op baseline check"); err != nil {
		log.Fatalf("add perturbation: %v", err)
	}
	if err := wb.AddStandardNeighborhood("in_1f05", -1, 1); err != nil {
		log.Fatalf("add neighborhood: %v", err)
	}

	report, err := wb.Run()
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Printf("Target: %s\n", report.Target.Description)
	fmt.Printf("Baseline output: %d\n", report.BaselineOutput)
	fmt.Printf("Cases: %d\n", len(report.Cases))
	for _, c := range report.Cases {
		fmt.Printf("%s: %s (out=%d)\n", c.Perturbation.Name, c.Status, c.OutputValue)
	}
	for _, s := range report.Sensitivities {
		fmt.Printf("Sensitivity %s: sensitive=%t\n", s.Variable, s.Sensitive)
	}

	// Output:
	// Target: store 0x14 to $7E:1F06
	// Baseline output: 20
	// Cases: 3
	// noop: baseline_match (out=20)
	// in_1f05-1: predicted_divergence (out=19)
	// in_1f05+1: predicted_divergence (out=21)
	// Sensitivity in_1f05: sensitive=true
}
