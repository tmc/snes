package planner_test

import (
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/analysis/planner"
)

func ExamplePlan() {
	doc := recovery.NewDocument(recovery.ROMIdentity{})
	doc.Instructions = []recovery.Instruction{
		{
			ID:       "inst-008000",
			Address:  0x008000,
			Offset:   0x000000,
			Bytes:    "7C0090",
			Opcode:   0x7C, // JMP ($9000,X)
			Mnemonic: "JMP ($9000,X)",
			Context:  recovery.Context{E: "set", M: "set", X: "set"},
		},
	}

	plan, err := planner.Plan(doc, planner.Options{MaxExperiments: 5})
	if err != nil {
		log.Fatal(err)
	}

	for _, exp := range plan.Experiments {
		fmt.Printf("%s at $%06X: %s\n", exp.Classification, exp.Address, exp.RecommendedAction)
	}
	// Output:
	// FrontierIndirectTarget at $008000: analyze dispatch table at $9000
}
