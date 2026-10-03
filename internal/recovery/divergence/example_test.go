package divergence_test

import (
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery/divergence"
)

func ExampleCompare() {
	baseline := []divergence.Step{
		{
			Address:  0x008000,
			Opcode:   0xD0,
			Mnemonic: "BNE",
			Sequence: 0,
		},
		{
			Address:  0x008002,
			Opcode:   0xEA,
			Mnemonic: "NOP",
			Sequence: 1,
		},
	}
	altered := []divergence.Step{
		{
			Address:  0x008000,
			Opcode:   0xD0,
			Mnemonic: "BNE",
			Sequence: 0,
		},
		{
			Address:  0x008010,
			Opcode:   0xEA,
			Mnemonic: "NOP",
			Sequence: 1,
		},
	}

	report, err := divergence.Compare(baseline, altered, divergence.Options{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Prefix: %d\n", report.CommonPrefixLength)
	fmt.Printf("Category: %s\n", report.Decision.Category)
	fmt.Printf("PC: %s\n", divergence.FormatAddress(report.Decision.PC))
	// Output:
	// Prefix: 1
	// Category: branch_outcome
	// PC: $00:8000
}
