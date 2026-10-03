// Package counterfactual implements the counterfactual workbench for SNES program recovery.
//
// The workbench takes an extracted [computation.Computation], perturbs its inputs or
// initial state, re-executes the slice, and compares execution against the baseline
// run using [divergence.Compare]. It isolates input sensitivities, identifies first
// divergent decisions and external observable effects, and generates structured reports.
package counterfactual
