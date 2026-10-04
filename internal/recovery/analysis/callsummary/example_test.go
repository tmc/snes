package callsummary_test

import (
	"fmt"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis/callsummary"
	"github.com/tmc/snes/internal/recovery/structure"
)

func Example() {
	// Acallee subroutine that preserves DB via PHB ... PLB ... RTS
	insns := []recovery.Instruction{
		{Address: 0x008000, Opcode: 0x8B, Mnemonic: "PHB"},
		{Address: 0x008001, Opcode: 0xEA, Mnemonic: "NOP"},
		{Address: 0x008002, Opcode: 0xAB, Mnemonic: "PLB"},
		{Address: 0x008003, Opcode: 0x60, Mnemonic: "RTS"},
	}

	contract, err := callsummary.AnalyzeInstructions(insns)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Printf("DB preserved: %v\n", contract.Preserves(callsummary.RegDB))
	fmt.Printf("Balanced stack: %v (delta=%d)\n", contract.IsBalanced(), contract.StackDelta)
	// Output:
	// DB preserved: true
	// Balanced stack: true (delta=0)
}

func ExampleAnalyzeBlock() {
	block := &structure.BasicBlock{
		ID:           "sub_c435",
		StartAddress: 0x0CC435,
		Instructions: []recovery.Instruction{
			{Address: 0x0CC435, Opcode: 0x8B, Mnemonic: "PHB"},
			{Address: 0x0CC436, Opcode: 0x0B, Mnemonic: "PHD"},
			{Address: 0x0CC437, Opcode: 0x2B, Mnemonic: "PLD"},
			{Address: 0x0CC438, Opcode: 0xAB, Mnemonic: "PLB"},
			{Address: 0x0CC439, Opcode: 0x60, Mnemonic: "RTS"},
		},
	}

	contract, err := callsummary.AnalyzeBlock(block)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	fmt.Printf("Preserves DB: %v\n", contract.Preserves(callsummary.RegDB))
	fmt.Printf("Preserves DP: %v\n", contract.Preserves(callsummary.RegDP))
	fmt.Printf("Stack delta: %d\n", contract.StackDelta)
	// Output:
	// Preserves DB: true
	// Preserves DP: true
	// Stack delta: 0
}
