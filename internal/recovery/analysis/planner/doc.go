// Package planner prioritizes executable experiments to resolve code frontiers in SNES ROM recovery.
//
// Blind static analysis and dynamic trace imports leave unresolved frontiers
// in recovered 65C816 code. The planner classifies these frontiers into distinct
// categories (indirect jump/call targets, unknown register contexts, unobserved branch paths,
// callee call boundaries, and uninitialized memory reads) and ranks them by expected
// information gain against verification cost to emit actionable experiment plans.
//
// # Basic Usage
//
//	plan, err := planner.Plan(doc, planner.Options{MaxExperiments: 10})
//	if err != nil {
//		log.Fatal(err)
//	}
//	for _, exp := range plan.Experiments {
//		fmt.Printf("%s at $%06X: %s\n", exp.Classification, exp.Address, exp.RecommendedAction)
//	}
package planner
