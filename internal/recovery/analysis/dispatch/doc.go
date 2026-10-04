// Package dispatch recovers indirect jump dispatch tables and correlates
// them with dynamic execution witnesses.
//
// It identifies candidate indexed indirect jump sites (such as JMP ($abs,X)),
// validates candidate pointer tables in ROM, and classifies table entries as
// statically plausible, dynamically witnessed, or unwitnessed exploration frontiers.
//
// Basic usage:
//
//	engine := dispatch.NewEngine(rom)
//	table, err := engine.RecoverTable(site)
//	if err != nil {
//		log.Fatal(err)
//	}
//	if err := engine.Correlate(table, traces); err != nil {
//		log.Fatal(err)
//	}
//	frontiers := table.Frontiers()
package dispatch
