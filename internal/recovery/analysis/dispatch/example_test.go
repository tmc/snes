package dispatch_test

import (
	"encoding/binary"
	"fmt"
	"log"

	"github.com/tmc/snes/internal/recovery/analysis/dispatch"
)

func Example() {
	// Build a synthetic ROM with a jump table at $8050 with two handlers.
	rom := make([]byte, 32*1024)
	binary.LittleEndian.PutUint16(rom[0x50:], 0x8060)
	binary.LittleEndian.PutUint16(rom[0x52:], 0x8070)
	copy(rom[0x60:], []byte{0xA9, 0x10, 0x60}) // Routine 0: LDA #$10, RTS
	copy(rom[0x70:], []byte{0xA9, 0x20, 0x60}) // Routine 1: LDA #$20, RTS

	site := dispatch.JumpSite{
		TableAddress: 0x008050,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  1,
	}

	engine := dispatch.NewEngine(rom)
	table, err := engine.RecoverTable(site)
	if err != nil {
		log.Fatal(err)
	}

	// Correlate a dynamic execution witness for selector 1.
	witness := dispatch.TraceEvent{
		EventID:       1001,
		Selector:      1,
		TargetAddress: 0x008070,
	}
	if err := engine.Correlate(table, []dispatch.TraceEvent{witness}); err != nil {
		log.Fatal(err)
	}

	for _, entry := range table.Entries {
		fmt.Printf("Selector %d: target=$%06X kind=%s\n", entry.Selector, entry.TargetAddress, entry.EvidenceKind)
	}

	// Output:
	// Selector 0: target=$008060 kind=unwitnessed_frontier
	// Selector 1: target=$008070 kind=witnessed
}

func ExampleDispatchTable_Frontiers() {
	rom := make([]byte, 32*1024)
	binary.LittleEndian.PutUint16(rom[0x50:], 0x8060)
	binary.LittleEndian.PutUint16(rom[0x52:], 0x8070)
	copy(rom[0x60:], []byte{0xA9, 0x10, 0x60})
	copy(rom[0x70:], []byte{0xA9, 0x20, 0x60})

	site := dispatch.JumpSite{
		TableAddress: 0x008050,
		EntryWidth:   2,
		MinSelector:  0,
		MaxSelector:  1,
	}

	engine := dispatch.NewEngine(rom)
	table, err := engine.RecoverTable(site)
	if err != nil {
		log.Fatal(err)
	}

	// Dynamic witness observed only selector 0.
	_ = engine.Correlate(table, []dispatch.TraceEvent{
		{EventID: 42, Selector: 0, TargetAddress: 0x008060},
	})

	frontiers := table.Frontiers()
	for _, f := range frontiers {
		fmt.Printf("Frontier selector %d: $%06X\n", f.Selector, f.TargetAddress)
	}

	// Output:
	// Frontier selector 1: $008070
}
