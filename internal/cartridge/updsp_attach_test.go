package cartridge

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge/chips/updsp"
)

// TestAttachUPDSPLoROMWindow pins the Conductor wire-up: a LoROM
// cartridge with updsp attached claims $20:6000/6001 through the real
// bus path, and the SR/DR half-read handshake is observable end-to-end.
// This is the Phase 11 Verifiable Acceptance Criterion "register the
// coprocessor dispatcher at $6000-$7FFF" landing against the cartridge
// mapper.
func TestAttachUPDSPLoROMWindow(t *testing.T) {
	rom := makeROM(0x8000) // small LoROM
	cart := New(rom)
	cart.Mode = LoROM

	loader, err := updsp.Load(updsp.VariantDSP1, nil, nil)
	if err != nil {
		t.Fatalf("updsp.Load: %v", err)
	}
	mapper := cart.AttachUPDSP(loader)
	if mapper == nil {
		t.Fatalf("AttachUPDSP returned nil")
	}
	if cart.CoprocessorID != "updsp" {
		t.Fatalf("CoprocessorID = %q, want updsp", cart.CoprocessorID)
	}

	// Map into a bus and drive through it.
	b := bus.NewBus()
	cart.MapToBus(b)

	// Arrange: simulate DSP program writing 0xABCD to DR.
	loader.IO.SetDSPResult(0xABCD)

	// SR read at $20:6001 should expose RQM high (bit 7 of the high
	// byte).
	if got := b.Read(0x20_6001); got&0x80 == 0 {
		t.Fatalf("bus SR read = %02X, RQM not set", got)
	}

	// DR read pair: $20:6000 low then $20:6000 high.
	lo := b.Read(0x20_6000)
	hi := b.Read(0x20_6000)
	if lo != 0xCD || hi != 0xAB {
		t.Fatalf("bus DR read pair = %02X/%02X, want CD/AB", lo, hi)
	}

	// DR write pair round-trip back into Core.DR.
	b.Write(0x20_6000, 0x34)
	b.Write(0x20_6000, 0x12)
	if loader.Core.DR != 0x1234 {
		t.Fatalf("Core.DR after bus write pair = %04X, want 1234", loader.Core.DR)
	}
}

// TestAttachUPDSPHiROMWindow pins the HiROM variant uses the
// $00:6000-$00:6BFF window.
func TestAttachUPDSPHiROMWindow(t *testing.T) {
	rom := makeROM(0x10000)
	cart := New(rom)
	cart.Mode = HiROM

	loader, _ := updsp.Load(updsp.VariantDSP1, nil, nil)
	mapper := cart.AttachUPDSP(loader)
	if mapper.MapType != updsp.MapHiROM {
		t.Fatalf("MapType = %v, want MapHiROM", mapper.MapType)
	}

	b := bus.NewBus()
	cart.MapToBus(b)

	loader.IO.SetDSPResult(0xBEEF)
	if got := b.Read(0x00_6001); got&0x80 == 0 {
		t.Fatalf("HiROM SR read at $00:6001 = %02X, RQM not set", got)
	}
	lo := b.Read(0x00_6000)
	hi := b.Read(0x00_6000)
	if lo != 0xEF || hi != 0xBE {
		t.Fatalf("HiROM DR read pair = %02X/%02X, want EF/BE", lo, hi)
	}
}

func TestAttachUPDSPStateRoundTripWithSuppliedROMs(t *testing.T) {
	rom := makeROM(0x8000)
	cart := New(rom)
	cart.Mode = LoROM

	wantPrg, wantData := updsp.VariantDSP1.ROMSize()
	prog := make([]byte, wantPrg)
	data := make([]byte, wantData)
	prog[0], prog[1], prog[2] = 0x12, 0x34, 0x56
	data[0], data[1] = 0xAB, 0xCD

	loader, err := updsp.Load(updsp.VariantDSP1, prog, data)
	if err != nil {
		t.Fatalf("updsp.Load: %v", err)
	}
	cart.AttachUPDSP(loader)
	loader.Core.PC = 0x123
	loader.Core.DRAM[7] = 0xCAFE
	loader.IO.SetDSPResult(0xBEEF)
	if got := loader.IO.ReadDR(); got != 0xEF {
		t.Fatalf("priming DR low read = %02X, want EF", got)
	}

	state, err := cart.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	restored := New(rom)
	restored.Mode = LoROM
	restoredLoader, err := updsp.Load(updsp.VariantDSP1, prog, data)
	if err != nil {
		t.Fatalf("restored updsp.Load: %v", err)
	}
	restored.AttachUPDSP(restoredLoader)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	if restored.CoprocessorID != "updsp" {
		t.Fatalf("restored CoprocessorID = %q, want updsp", restored.CoprocessorID)
	}
	if restoredLoader.Core.PRG[0] != 0x123456 {
		t.Fatalf("restored PRG[0] = %06X, want 123456", restoredLoader.Core.PRG[0])
	}
	if restoredLoader.Core.DROM[0] != 0xABCD {
		t.Fatalf("restored DROM[0] = %04X, want ABCD", restoredLoader.Core.DROM[0])
	}
	if restoredLoader.Core.PC != 0x123 || restoredLoader.Core.DRAM[7] != 0xCAFE {
		t.Fatalf("restored core state PC=%04X DRAM[7]=%04X, want 0123/CAFE",
			restoredLoader.Core.PC, restoredLoader.Core.DRAM[7])
	}
	if got := restoredLoader.IO.ReadDR(); got != 0xBE {
		t.Fatalf("restored DR high read = %02X, want BE", got)
	}
}
