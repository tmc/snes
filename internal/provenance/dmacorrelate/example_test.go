package dmacorrelate_test

import (
	"fmt"

	"github.com/tmc/snes/internal/provenance/dmacorrelate"
)

func ExampleCorrelationResult_FormatMarkdown() {
	res := &dmacorrelate.CorrelationResult{
		CaseID:      "example_case",
		Frame:       82,
		EntryPC:     0x0085FC,
		EntrySeq:    100,
		ReturnSeq:   200,
		TotalWrites: 1,
		HighTableWrites: []dmacorrelate.MemoryWrite{
			{Address: 0x7E0A00, Value: 0xAA},
		},
		HazardAssessment: "UNVERIFIED",
	}

	md := res.FormatMarkdown()
	fmt.Println(len(md) > 0)
	// Output:
	// true
}
