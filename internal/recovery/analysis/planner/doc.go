// Package planner prioritizes candidate experiments to resolve code frontiers in SNES ROM recovery.
//
// Blind static analysis and dynamic trace imports leave unresolved frontiers
// in recovered 65C816 code. The planner classifies these frontiers into distinct
// categories (indirect jump/call targets, unknown register contexts, unobserved branch paths,
// callee call boundaries, and uninitialized memory reads) and ranks them using heuristic
// category weights, proximity, and code density to emit prioritized experiment suggestions.
//
// InformationGain and VerificationCost are heuristic category/density rankings,
// not measured instruction yields or independently proven replay feasibility. Frame
// references are suggestive exploration starting points extracted from evidence annotations;
// they require admitted checkpoint and initial state verification before execution.
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
