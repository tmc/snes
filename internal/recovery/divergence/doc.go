// Package divergence analyzes and pinpoints the first meaningful divergence
// between two execution runs of a SNES program.
//
// Given two execution traces (baseline and altered), [Compare] identifies:
//   - The common execution prefix shared by both runs.
//   - The first divergent decision ([DivergenceDecision]), such as branch
//     outcomes, indirect jump targets, return targets, or register state changes.
//   - The first differing external observable effect ([DivergenceEffect]),
//     such as differing memory or hardware MMIO writes.
//
// # Basic Usage
//
// Compare two sequences of [Step] records:
//
//	report, err := divergence.Compare(baseline, altered, divergence.Options{})
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(report.Summary)
//
// # Filtering Effects
//
// To monitor specific memory addresses for the first observable consequence
// of a divergent decision, pass [Options.SelectedAddresses]:
//
//	report, err := divergence.Compare(baseline, altered, divergence.Options{
//		SelectedAddresses: []uint32{0x7E1F05},
//	})
package divergence
