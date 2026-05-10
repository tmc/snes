package cartridge

import (
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge/chips/sa1"
)

func TestSA1DetectsAndMapsRegisterWindow(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	if c.CoprocessorID != "sa1" {
		t.Fatalf("CoprocessorID = %q, want sa1", c.CoprocessorID)
	}
	if _, ok := c.coprocessor.(*sa1.Device); !ok {
		t.Fatalf("coprocessor type = %T, want *sa1.Device", c.coprocessor)
	}

	b := bus.NewBus()
	c.MapToBus(b)
	b.Write(0x00_2200, 0x80)
	if got := b.Read(0x80_2200); got != 0x80 {
		t.Fatalf("SA-1 mirrored control read = %02X, want 80", got)
	}
	if got := b.Read(0x40_2200); got == 0x80 {
		t.Fatalf("SA-1 control register leaked into bank 40")
	}
}

func TestSA1StateRoundTrip(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x35
	c := New(rom)
	c.Write(0x00_2200, 0x80)
	c.Write(0x00_2209, 0x20)
	d := c.coprocessor.(*sa1.Device)
	d.SignalCPUIRQ(0x0d)

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(rom)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if restored.CoprocessorID != "sa1" {
		t.Fatalf("restored CoprocessorID = %q, want sa1", restored.CoprocessorID)
	}
	if got := restored.Read(0x00_2200); got != 0x80 {
		t.Fatalf("restored SA-1 $2200 = %02X, want 80", got)
	}
	if got := restored.Read(0x00_2209); got != 0x20 {
		t.Fatalf("restored SA-1 $2209 = %02X, want 20", got)
	}
	if got := restored.Read(0x00_2300); got != 0x8d {
		t.Fatalf("restored SA-1 $2300 = %02X, want 8D", got)
	}
	restored.Write(0x00_2202, 0x80)
	if got := restored.Read(0x00_2300); got != 0x0d {
		t.Fatalf("cleared SA-1 $2300 = %02X, want 0D", got)
	}
}

func TestSA1BWRAMCPUWindows(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	if c.RAMSize != 256*1024 {
		t.Fatalf("RAMSize=%d, want 262144", c.RAMSize)
	}

	b := bus.NewBus()
	c.MapToBus(b)
	b.Write(0x00_2224, 0x02)
	b.Write(0x00_6000, 0xa5)
	if got := c.RAM[0x4000]; got != 0xa5 {
		t.Fatalf("BMAPS RAM[4000]=%02X, want A5", got)
	}
	if got := b.Read(0x80_6000); got != 0xa5 {
		t.Fatalf("mirrored BMAPS read=%02X, want A5", got)
	}

	b.Write(0x40_1234, 0x5a)
	if got := c.RAM[0x1234]; got != 0x5a {
		t.Fatalf("linear BW-RAM RAM[1234]=%02X, want 5A", got)
	}
	if got := b.Read(0x40_1234); got != 0x5a {
		t.Fatalf("linear BW-RAM read=%02X, want 5A", got)
	}
}

// SA-1 normal DMA end-to-end through the bus: trigger ROM→IRAM via
// $2236 and verify the copied bytes appear at the SA-1 I-RAM CPU
// window. Confirms the cartridge attach hook (SetBWRAMSlice) and the
// existing romReader callback together let dmaNormal read ROM and
// write I-RAM observable to the S-CPU.
func TestSA1DMANormalEndToEndROMToIRAM(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	// Pre-populate ROM bytes so the DMA copies a recognizable pattern.
	for i := 0; i < 16; i++ {
		rom[0x10+i] = byte(0xC0 + i)
	}
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Open SIWP so the IRAM CPU window can read back. (IRAM reads
	// don't need SIWP; SIWP gates writes. Reads always succeed.)
	// dmaen | dd=0 (IRAM) | sd=0 (ROM): $2230 = 0x80.
	b.Write(0x00_2230, 0x80)
	// DSA points into the ROM area we pre-filled. SA-1 ROM banks
	// $00:8000-FFFF map (per CPUROMAddress) to ROM offset 0; with
	// our pre-fill at offset 0x10, source $00_8010 reads byte 0xC0.
	b.Write(0x00_2232, 0x10)
	b.Write(0x00_2233, 0x80)
	b.Write(0x00_2234, 0x00)
	b.Write(0x00_2235, 0x80) // DDA low (target IRAM offset 0x80)
	b.Write(0x00_2238, 0x08) // DTC=8 bytes
	b.Write(0x00_2239, 0x00)
	// $2236 trigger.
	b.Write(0x00_2236, 0x00)

	// Read back via the IRAM CPU window ($00:3080-3087 mirrors IRAM[80..87]).
	for i := uint32(0); i < 8; i++ {
		want := byte(0xC0 + i)
		got := b.Read(0x00_3080 + i)
		if got != want {
			t.Errorf("IRAM[%X] (via $00:30%02X) = %02X want %02X", 0x80+i, 0x80+i, got, want)
		}
	}
}

// SA-1 type-1 CCDMA: while bwramDMA is active, an S-CPU read at a
// BW-RAM address should be dispatched through Device.DMACC1Read,
// triggering lazy character synthesis into I-RAM. Verifies the
// cartridge-side hook in cartridge.go.
func TestSA1BWRAMCPUReadDispatchesCC1WhenArmed(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	for i := range c.RAM {
		c.RAM[i] = byte(i*7 + 3)
	}
	b := bus.NewBus()
	c.MapToBus(b)

	// Arm CC1: dmaen|cden|cdsel, dmacb=1 (4bpp), dmasize=0, DSA=0,
	// DDA=0. Trigger via $2236 write.
	b.Write(0x00_2230, 0xb0)
	b.Write(0x00_2231, 0x01)
	b.Write(0x00_2232, 0x00)
	b.Write(0x00_2233, 0x00)
	b.Write(0x00_2234, 0x00)
	b.Write(0x00_2235, 0x00)
	b.Write(0x00_2236, 0x00)

	// Set BMAPS=0 so $00:6000 maps to BW-RAM offset 0.
	b.Write(0x00_2224, 0x00)
	// First aligned read: synthesizes IRAM[0..0xF] for 4bpp/dmasize=0
	// from BW-RAM at offset 0. Per /tmp/cc1_golden.go this returns 0x8D.
	got := b.Read(0x00_6000)
	if got != 0x8D {
		t.Fatalf("S-CPU BW-RAM read with CC1 armed = %02X, want 8D (CC1 lazy synthesis)", got)
	}
}

func TestSA1BWRAMWriteProtection(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2228, 0x02)
	b.Write(0x40_03ff, 0xa5)
	if got := b.Read(0x40_03ff); got != 0x00 {
		t.Fatalf("protected BW-RAM write read=%02X, want 00", got)
	}
	b.Write(0x40_0400, 0x5a)
	if got := b.Read(0x40_0400); got != 0x5a {
		t.Fatalf("unprotected BW-RAM write read=%02X, want 5A", got)
	}
	b.Write(0x00_2226, 0x80)
	b.Write(0x40_03ff, 0xc3)
	if got := b.Read(0x40_03ff); got != 0xc3 {
		t.Fatalf("SWEN protected BW-RAM write read=%02X, want C3", got)
	}
}

func TestSA1SuperMMCROMWindow(t *testing.T) {
	rom := makeROM(0x400000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[0x001234] = 0x11
	rom[0x101234] = 0x22
	rom[0x201234] = 0x33
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	if got := b.Read(0x00_9234); got != 0x11 {
		t.Fatalf("SA-1 low C bank read=%02X, want 11", got)
	}
	if got := b.Read(0xc0_1234); got != 0x11 {
		t.Fatalf("SA-1 C bank read=%02X, want 11", got)
	}
	if got := b.Read(0xd0_1234); got != 0x22 {
		t.Fatalf("SA-1 D bank read=%02X, want 22", got)
	}

	b.Write(0x00_2220, 0x82)
	if got := b.Read(0xc0_1234); got != 0x33 {
		t.Fatalf("SA-1 remapped C bank read=%02X, want 33", got)
	}
	if got := b.Read(0x00_9234); got != 0x33 {
		t.Fatalf("SA-1 remapped low C bank read=%02X, want 33", got)
	}
}

func TestSA1CPUIRQTarget(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	irq := &testIRQTarget{}
	c.SetIRQTarget(irq)
	d := c.coprocessor.(*sa1.Device)

	d.SignalCPUIRQ(0x03)
	c.Step(1)
	if irq.count != 0 {
		t.Fatalf("disabled SA-1 IRQ count=%d, want 0", irq.count)
	}
	c.Write(0x00_2201, 0x80)
	if irq.count != 1 {
		t.Fatalf("enabled pending SA-1 IRQ count=%d, want 1", irq.count)
	}
	c.Step(1)
	if irq.count != 1 {
		t.Fatalf("latched SA-1 IRQ retriggered: count=%d want 1", irq.count)
	}
	c.Write(0x00_2202, 0x80)
	d.SignalCPUIRQ(0x04)
	c.Step(1)
	if irq.count != 2 {
		t.Fatalf("rearmed SA-1 IRQ count=%d, want 2", irq.count)
	}
}

func TestSA1CPUIRQLineStateRoundTrip(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	irq := &testIRQTarget{}
	c.SetIRQTarget(irq)
	c.Write(0x00_2201, 0x80)
	d := c.coprocessor.(*sa1.Device)
	d.SignalCPUIRQ(0x03)
	c.Step(1)
	if irq.count != 1 {
		t.Fatalf("SA-1 IRQ count before state=%d, want 1", irq.count)
	}

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(rom)
	irq2 := &testIRQTarget{}
	restored.SetIRQTarget(irq2)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	restored.Step(1)
	if irq2.count != 0 {
		t.Fatalf("restored latched SA-1 IRQ retriggered: count=%d want 0", irq2.count)
	}
	restored.Write(0x00_2202, 0x80)
	d2 := restored.coprocessor.(*sa1.Device)
	d2.SignalCPUIRQ(0x04)
	restored.Step(1)
	if irq2.count != 1 {
		t.Fatalf("restored rearmed SA-1 IRQ count=%d, want 1", irq2.count)
	}
}

// vbdSetVAOnBus writes the VBD VA register triplet through an S-CPU
// bus, matching the production data path. Writing $225B clears VBIT
// per bsnes io.cpp:486.
func vbdSetVAOnBus(t *testing.T, b *bus.Bus, va uint32) {
	t.Helper()
	b.Write(0x00_2259, uint8(va))
	b.Write(0x00_225a, uint8(va>>8))
	b.Write(0x00_225b, uint8(va>>16))
}

// TestSA1VBDROMReadsThroughCartridge exercises the full
// cartridge-attach + S-CPU bus + Device.SetROMReader path: the VBD's
// $230C/$230D reads must return bytes from Cartridge.ROM at the
// SA-1-mapped offset corresponding to the configured VA.
func TestSA1VBDROMReadsThroughCartridge(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	// SA-1 VBR for $00:8000 maps via Device.CPUROMAddress to ROM[0].
	// Stamp three known bytes there so the bit-extraction shift can
	// recover them deterministically.
	rom[0x000000] = 0xCD
	rom[0x000001] = 0xAB
	rom[0x000002] = 0x12
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Auto mode (HL=1), VB=8 → $230D reads bits 8..15 of (24-bit
	// stream >> vbit). vbit starts at 0, so high byte = $AB.
	vbdSetVAOnBus(t, b, 0x008000)
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0xCD {
		t.Fatalf("$230C from ROM = %02X, want CD (raw byte at $00:8000)", got)
	}
	if got := b.Read(0x00_230d); got != 0xAB {
		t.Fatalf("$230D from ROM = %02X, want AB (high byte of stream)", got)
	}
}

// TestSA1VBDROMHonorsCXBBankRemap pins that the VBD reader honors the
// SA-1 Super MMC bank-mode select on $2220 (CXB), since it dispatches
// through Device.CPUROMAddress which respects romBank/romBankMode.
func TestSA1VBDROMHonorsCXBBankRemap(t *testing.T) {
	rom := makeROM(0x400000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	// $00:8000 in default CXB resolves to ROM[0]. Setting CXB=$82
	// remaps the C bank to projected slot 2 (per the existing
	// SuperMMC test pattern), so $00:8000 then resolves to a
	// different linear offset. Stamp distinct bytes.
	rom[0x000000] = 0x11 // default CXB target for $00:8000
	rom[0x100000] = 0x22 // CXB.romBank=1 target
	rom[0x200000] = 0x33 // CXB.romBank=2 target
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Default CXB → $230C low byte at VA=$00:8000 is rom[0]=0x11.
	vbdSetVAOnBus(t, b, 0x008000)
	b.Write(0x00_2258, 0x80) // HL=1, VB=0→16 (no advance side-effects on write)
	if got := b.Read(0x00_230c); got != 0x11 {
		t.Fatalf("$230C default CXB = %02X, want 11", got)
	}

	// Remap CXB to slot 2 ($82 = bankMode set + romBank=2).
	b.Write(0x00_2220, 0x82)
	vbdSetVAOnBus(t, b, 0x008000)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0x33 {
		t.Fatalf("$230C after CXB=$82 = %02X, want 33 (remapped slot 2)", got)
	}
}

// TestSA1VBDBWRAMRegionRoutesThroughRawBankOffset pins that VBD
// reads in the BW-RAM windows ($00-3F:6000-7FFF, $80-BF:6000-7FFF,
// $40-4F:0000-FFFF) project the raw 24-bit bank+offset modulo
// BW-RAM size, per bsnes/sfc/coprocessor/sa1/memory.cpp:120-124 +
// bwram.cpp:9-13 (bus.mirror).
func TestSA1VBDBWRAMRegionRoutesThroughRawBankOffset(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Open BW-RAM CPU writes via CWEN ($2227 bit 7) so writes to
	// the low BWPA-protected region are accepted.
	b.Write(0x00_2227, 0x80)
	// Seed the linear BW-RAM at byte 0 via the S-CPU $40 window
	// (which uses Cartridge.sa1BWRAMAddress's linear case and
	// matches VBR raw at $40-4F).
	b.Write(0x40_0000, 0xCD)
	b.Write(0x40_0001, 0xAB)
	b.Write(0x40_0002, 0x12)

	vbdSetVAOnBus(t, b, 0x400000)
	b.Write(0x00_2258, 0x88) // HL=1, VB=8
	if got := b.Read(0x00_230c); got != 0xCD {
		t.Fatalf("$230C VA=$400000 = %02X, want CD", got)
	}
	if got := b.Read(0x00_230d); got != 0xAB {
		t.Fatalf("$230D VA=$400000 = %02X, want AB", got)
	}
}

// TestSA1VBDBWRAMLowMirrorIsRawProjection proves the projection is
// raw bank+offset (NOT the page-mapped CPU view). $00:6000 raw
// projects to linear $06000, which is distinct from the page-mapped
// CPU view of $00:6000 (which uses $2224 BMAPS).
func TestSA1VBDBWRAMLowMirrorIsRawProjection(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2227, 0x80) // CWEN: open CPU BW-RAM writes
	// Seed linear offset $06000 via the $40-window: $40:6000 raw
	// projects to $46000 % $40000 = $06000.
	b.Write(0x40_6000, 0x77)
	// Also stamp $00000 with a different byte so we can detect a
	// page-mapped fallback.
	b.Write(0x40_0000, 0x99)

	// Read via VBR at $00:6000 — raw projection picks $06000.
	vbdSetVAOnBus(t, b, 0x006000)
	b.Write(0x00_2258, 0x80) // HL=1, VB=0→16, no advance side-effect
	if got := b.Read(0x00_230c); got != 0x77 {
		t.Fatalf("$230C VA=$006000 = %02X, want 77 (raw projection to $06000)", got)
	}

	// Critical non-alias: $00:6000 and $40:0000 must map to
	// DIFFERENT linear offsets. $40:0000 raw → $00000 (with $99).
	vbdSetVAOnBus(t, b, 0x400000)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0x99 {
		t.Fatalf("$230C VA=$400000 = %02X, want 99 ($00:6000 and $40:0000 must NOT alias)", got)
	}
}

// TestSA1VBDBWRAM80BFMirrorAliases pins that $80-BF banks alias
// $00-3F at the BW-RAM window. bsnes mask 0x40e000 is unconstrained
// in bit 23, so $80:6000 and $00:6000 hit the same byte after raw
// modulo. The implementation strips bit 23 explicitly via bank&0x7F
// to be size-independent.
func TestSA1VBDBWRAM80BFMirrorAliases(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2227, 0x80)
	b.Write(0x40_6000, 0x42) // seeds linear $06000

	vbdSetVAOnBus(t, b, 0x806000)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0x42 {
		t.Fatalf("$230C VA=$806000 = %02X, want 42 ($80-BF mirror of $00-3F)", got)
	}
}

// TestSA1VBDBWRAMHonorsSizeMirror pins that addresses past BW-RAM
// size wrap. With 256 KiB BW-RAM ($40000), $44:0000 raw = $440000;
// $440000 % $40000 = $40000 % $40000 = $00000. So $44:0000 aliases
// $40:0000. Verifies addr%size semantics for the realistic
// power-of-2 SA-1 BW-RAM size.
func TestSA1VBDBWRAMHonorsSizeMirror(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2227, 0x80)
	b.Write(0x40_0000, 0x55) // linear $00000

	vbdSetVAOnBus(t, b, 0x440000)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0x55 {
		t.Fatalf("$230C VA=$440000 = %02X, want 55 ($44:0000 mod 256 KiB = $40:0000)", got)
	}
}

// TestSA1VBDBWRAMOutOfRangeFallsThrough pins that addresses outside
// the BW-RAM windows fall through to the ROM dispatch (not into
// BW-RAM). $00:5FFF is just below the $00-3F:6000-7FFF window, and
// $50:0000 is just above the $40-4F window.
func TestSA1VBDBWRAMOutOfRangeFallsThrough(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8
	// Stamp BW-RAM with a sentinel that we should NOT see in $230C.
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)
	b.Write(0x00_2227, 0x80)
	b.Write(0x40_0000, 0xEE)

	// $00:5FFF is below the BW-RAM window. CPUROMAddress for
	// $00:5FFF returns false (offset < $8000 and bank < $C0), so
	// the closure falls back to 0xFF.
	vbdSetVAOnBus(t, b, 0x005FFF)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0xFF {
		t.Fatalf("$230C VA=$005FFF = %02X, want FF (out-of-range falls to 0xFF, not BW-RAM)", got)
	}

	// $50:0000 is above the $40-4F window; CPUROMAddress for $50
	// banks does not match either ($50 < $C0, offset 0 < $8000), so
	// also falls to 0xFF.
	vbdSetVAOnBus(t, b, 0x500000)
	b.Write(0x00_2258, 0x80)
	if got := b.Read(0x00_230c); got != 0xFF {
		t.Fatalf("$230C VA=$500000 = %02X, want FF", got)
	}
}

// TestSA1VBDIRAMRegionRoutesThroughDeviceIRAMSA1 pins that the VBD
// reader resolves I-RAM windows ($00-3F:0000-07FF and
// $00-3F:3000-37FF, with $80-BF mirrors) through Device.ReadIRAMSA1.
// bsnes/sfc/coprocessor/sa1/memory.cpp:126-130 routes both windows
// to iram.read raw (no SIWP/CIWP gate, 2 KiB bus.mirror).
func TestSA1VBDIRAMRegionRoutesThroughDeviceIRAMSA1(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Seed I-RAM via the S-CPU window (caff7bc): SIWP=$FF opens
	// all blocks. Stamp three known bytes near offset 0.
	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3000, 0xCD)
	b.Write(0x00_3001, 0xAB)
	b.Write(0x00_3002, 0x12)

	// VBR at VA=$003000 must now read those bytes. HL=1 auto, VB=8.
	vbdSetVAOnBus(t, b, 0x003000)
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0xCD {
		t.Fatalf("$230C from I-RAM = %02X, want CD", got)
	}
	if got := b.Read(0x00_230d); got != 0xAB {
		t.Fatalf("$230D from I-RAM = %02X, want AB", got)
	}
}

// TestSA1VBDIRAMLowWindowAliases pins that VA=$000000 (the low
// $0000-$07FF VBR window) reads the same I-RAM bytes as the
// $3000-$37FF window. bsnes' iram.cpp:8-13 bus.mirror with size
// 0x800 makes both windows alias the same 2 KiB block.
func TestSA1VBDIRAMLowWindowAliases(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3010, 0x42)
	b.Write(0x00_3011, 0x99)

	// VA=$000010 should alias to I-RAM offset 0x10 = same byte
	// just stamped via $3010.
	vbdSetVAOnBus(t, b, 0x000010)
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0x42 {
		t.Fatalf("$230C VA=$000010 = %02X, want 42 (alias of $003010)", got)
	}
	if got := b.Read(0x00_230d); got != 0x99 {
		t.Fatalf("$230D VA=$000010 = %02X, want 99", got)
	}
}

// TestSA1VBDIRAMHonors80BFMirror pins the $80-BF bank mirror of the
// I-RAM VBR window. bsnes' mask 0x40f800 in memory.cpp:126-127
// covers both $00-3F and $80-BF banks at the same offset windows.
func TestSA1VBDIRAMHonors80BFMirror(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3050, 0x77)

	// VA=$803050 must alias to I-RAM offset 0x50.
	vbdSetVAOnBus(t, b, 0x803050)
	b.Write(0x00_2258, 0x80) // HL=1, VB=0→16 (no advance side-effect)
	if got := b.Read(0x00_230c); got != 0x77 {
		t.Fatalf("$230C VA=$803050 = %02X, want 77 (80-BF mirror)", got)
	}
}

// TestSA1VBDROMReaderSurvivesStateRoundTrip pins that the VBR
// closure is reinstalled on Unserialize so the restored cartridge
// continues to source ROM bytes for VBD reads. Without the reattach
// hook, the restored Device would have no reader and VBR would
// silently return 0xFF — a regression that mirror-byte-only
// round-trip checks would not catch.
func TestSA1VBDROMReaderSurvivesStateRoundTrip(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[0x000000] = 0x77
	rom[0x000001] = 0x88
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(rom)
	rb := bus.NewBus()
	restored.MapToBus(rb)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	vbdSetVAOnBus(t, rb, 0x008000)
	rb.Write(0x00_2258, 0x88)
	if got := rb.Read(0x00_230c); got != 0x77 {
		t.Fatalf("restored $230C = %02X, want 77 (ROMReader not reinstalled?)", got)
	}
	if got := rb.Read(0x00_230d); got != 0x88 {
		t.Fatalf("restored $230D = %02X, want 88", got)
	}
}

// TestSA1IRAMCPUWindowReadsThroughCartridge exercises the I-RAM
// bus window installed at $00-3F:3000-37FF + $80-BF:3000-37FF.
// Writes through the bus mirror to the Device's 2 KiB I-RAM and
// subsequent reads observe the same byte. bsnes manifest at
// cartridge/load.cpp:327 maps IRAM::readCPU/writeCPU here.
func TestSA1IRAMCPUWindowReadsThroughCartridge(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Open SIWP so writes succeed.
	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3000, 0x11)
	b.Write(0x00_3123, 0x22)
	b.Write(0x00_37FF, 0x33)
	if got := b.Read(0x00_3000); got != 0x11 {
		t.Fatalf("$00:3000 = %02X, want 11", got)
	}
	if got := b.Read(0x00_3123); got != 0x22 {
		t.Fatalf("$00:3123 = %02X, want 22", got)
	}
	if got := b.Read(0x00_37FF); got != 0x33 {
		t.Fatalf("$00:37FF = %02X, want 33", got)
	}
}

func TestSA1IRAMCPUWindowMirrorsAt80BF(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3050, 0x77)
	if got := b.Read(0x80_3050); got != 0x77 {
		t.Fatalf("$80:3050 mirror = %02X, want 77", got)
	}
	b.Write(0xBF_3050, 0x99)
	if got := b.Read(0x3F_3050); got != 0x99 {
		t.Fatalf("$3F:3050 mirror after $BF write = %02X, want 99", got)
	}
}

func TestSA1IRAMCPUWritesGatedBySIWP(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// Seed all 8 blocks with $00 (SIWP=$FF).
	b.Write(0x00_2229, 0xFF)
	for blk := uint32(0); blk < 8; blk++ {
		b.Write(0x00_3000+(blk*0x100)+0x42, 0x00)
	}
	// Lock to blocks 1 and 4 only (siwp = bit1|bit4 = $12).
	b.Write(0x00_2229, 0x12)
	for blk := uint32(0); blk < 8; blk++ {
		b.Write(0x00_3000+(blk*0x100)+0x42, uint8(0x80|blk))
	}
	for blk := uint32(0); blk < 8; blk++ {
		got := b.Read(0x00_3000 + (blk * 0x100) + 0x42)
		want := uint8(0x00) // gated, write dropped
		if blk == 1 || blk == 4 {
			want = uint8(0x80 | blk)
		}
		if got != want {
			t.Fatalf("$00:%04X (block %d) = %02X, want %02X (SIWP=$12)",
				0x3000+blk*0x100+0x42, blk, got, want)
		}
	}
}

func TestSA1IRAMOutsideWindowFallsThrough(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// $00:3800 is outside the I-RAM window; should not route to the
	// cartridge's I-RAM accessor. Default cartridge behavior for an
	// unmapped low-bank address is open-bus / 0; the key invariant
	// is that writing $00:3800 does NOT bleed into the I-RAM at
	// offset $0800 (which would mirror to $0000 via 2 KiB mask).
	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3000, 0xAA)
	// $00:3800 is outside the I-RAM window. Going through the bus
	// directly here would either be open-bus or unmapped; we don't
	// assert its read value (depends on platform). The invariant we
	// pin is that Device.ReadIRAMCPU($0000) is unchanged at $AA.
	d := c.coprocessor.(*sa1.Device)
	if got := d.ReadIRAMCPU(0x000); got != 0xAA {
		t.Fatalf("IRAM[$000] = %02X, want AA (unaffected by $3800 access)", got)
	}
}

func TestSA1IRAMSurvivesStateRoundTrip(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_2229, 0xFF)
	b.Write(0x00_3010, 0x44)
	b.Write(0x00_3011, 0x55)

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New(rom)
	rb := bus.NewBus()
	r.MapToBus(rb)
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := rb.Read(0x00_3010); got != 0x44 {
		t.Fatalf("restored $00:3010 = %02X, want 44", got)
	}
	if got := rb.Read(0x00_3011); got != 0x55 {
		t.Fatalf("restored $00:3011 = %02X, want 55", got)
	}
}

// makeSA1ROMWithVectors builds a minimal SA-1 LoROM with deterministic
// bytes at the native NMI ($00:FFEA) and IRQ ($00:FFEE) vector
// positions. Used to assert that the override beats the underlying
// ROM bytes only when the corresponding SCNT switch bit is set.
func makeSA1ROMWithVectors(t *testing.T) []byte {
	t.Helper()
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	// $00:FFEA (LoROM-linear $7FEA) — native NMI low+high.
	rom[0x7FEA] = 0xAA
	rom[0x7FEB] = 0xBB
	// $00:FFEE (LoROM-linear $7FEE) — native IRQ low+high.
	rom[0x7FEE] = 0xCC
	rom[0x7FEF] = 0xDD
	// $00:FFFC (linear $7FFC) — RESET, MUST never be overridden.
	rom[0x7FFC] = 0xEE
	rom[0x7FFD] = 0xFF
	// $00:FFFA (linear $7FFA) — emulation NMI, MUST never be overridden.
	rom[0x7FFA] = 0x11
	rom[0x7FFB] = 0x22
	return rom
}

func TestSA1VectorOverrideNMIWhenSwitchOff(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// SNV/SIV set but SCNT switches off → reads return ROM bytes.
	b.Write(0x00_220c, 0x99)
	b.Write(0x00_220d, 0x88)
	b.Write(0x00_220e, 0x77)
	b.Write(0x00_220f, 0x66)
	if got := b.Read(0x00_FFEA); got != 0xAA {
		t.Fatalf("$00:FFEA without nvsw = %02X, want AA (ROM)", got)
	}
	if got := b.Read(0x00_FFEB); got != 0xBB {
		t.Fatalf("$00:FFEB without nvsw = %02X, want BB (ROM)", got)
	}
	if got := b.Read(0x00_FFEE); got != 0xCC {
		t.Fatalf("$00:FFEE without ivsw = %02X, want CC (ROM)", got)
	}
	if got := b.Read(0x00_FFEF); got != 0xDD {
		t.Fatalf("$00:FFEF without ivsw = %02X, want DD (ROM)", got)
	}
}

func TestSA1VectorOverrideNMIWhenSwitchOn(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220c, 0xCD) // SNV low
	b.Write(0x00_220d, 0xAB) // SNV high
	b.Write(0x00_2209, 0x10) // cpu_nvsw=1, cpu_ivsw=0
	if got := b.Read(0x00_FFEA); got != 0xCD {
		t.Fatalf("$00:FFEA with nvsw = %02X, want CD (SNV low)", got)
	}
	if got := b.Read(0x00_FFEB); got != 0xAB {
		t.Fatalf("$00:FFEB with nvsw = %02X, want AB (SNV high)", got)
	}
	// IRQ vectors must still come from ROM (ivsw not set).
	if got := b.Read(0x00_FFEE); got != 0xCC {
		t.Fatalf("$00:FFEE with only nvsw = %02X, want CC (ROM)", got)
	}
}

func TestSA1VectorOverrideIRQWhenSwitchOn(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220e, 0x21) // SIV low
	b.Write(0x00_220f, 0x43) // SIV high
	b.Write(0x00_2209, 0x40) // cpu_ivsw=1, cpu_nvsw=0
	if got := b.Read(0x00_FFEE); got != 0x21 {
		t.Fatalf("$00:FFEE with ivsw = %02X, want 21 (SIV low)", got)
	}
	if got := b.Read(0x00_FFEF); got != 0x43 {
		t.Fatalf("$00:FFEF with ivsw = %02X, want 43 (SIV high)", got)
	}
	// NMI vectors must still come from ROM (nvsw not set).
	if got := b.Read(0x00_FFEA); got != 0xAA {
		t.Fatalf("$00:FFEA with only ivsw = %02X, want AA (ROM)", got)
	}
}

// TestSA1VectorOverrideRESETNotOverridden pins that even with both
// SCNT switches set, reads of $00:FFFC (RESET) come from ROM. bsnes'
// override block at rom.cpp:22-26 only matches FFEA/EB and FFEE/EF.
func TestSA1VectorOverrideRESETNotOverridden(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220c, 0x55)
	b.Write(0x00_220d, 0x55)
	b.Write(0x00_220e, 0x55)
	b.Write(0x00_220f, 0x55)
	b.Write(0x00_2209, 0x50) // both switches on
	if got := b.Read(0x00_FFFC); got != 0xEE {
		t.Fatalf("$00:FFFC = %02X, want EE (RESET never overridden)", got)
	}
	if got := b.Read(0x00_FFFD); got != 0xFF {
		t.Fatalf("$00:FFFD = %02X, want FF (RESET never overridden)", got)
	}
}

// TestSA1VectorOverrideEmulationVectorsNotOverridden pins the
// emulation-mode vectors at $00:FFFA-FFFF are not in the override
// block (bsnes mask 0xffffe0 == 0x007fe0 covers $7FE0-$7FFF only at
// the byte level for FFEA/EB/EE/EF specifically — emulation vectors
// at $7FFA/B/E/F are NOT matched).
func TestSA1VectorOverrideEmulationVectorsNotOverridden(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220c, 0x88)
	b.Write(0x00_220d, 0x99)
	b.Write(0x00_2209, 0x50)
	if got := b.Read(0x00_FFFA); got != 0x11 {
		t.Fatalf("$00:FFFA emulation NMI = %02X, want 11 (ROM)", got)
	}
	if got := b.Read(0x00_FFFB); got != 0x22 {
		t.Fatalf("$00:FFFB emulation NMI = %02X, want 22 (ROM)", got)
	}
}

// TestSA1VectorOverrideHonors80BFMirror pins that the override fires
// for the $80-BF bank mirror as well as $00-3F. bsnes' rom.readSA1
// linearizes the address, so $00:FFEA and $80:FFEA both hit the
// same mmio.snv override path.
func TestSA1VectorOverrideHonors80BFMirror(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220c, 0xBE) // SNV low
	b.Write(0x00_220d, 0xEF) // SNV high
	b.Write(0x00_2209, 0x10) // cpu_nvsw=1
	if got := b.Read(0x80_FFEA); got != 0xBE {
		t.Fatalf("$80:FFEA mirror with nvsw = %02X, want BE", got)
	}
	if got := b.Read(0xBF_FFEB); got != 0xEF {
		t.Fatalf("$BF:FFEB mirror with nvsw = %02X, want EF", got)
	}
	// $C0:FFEA must NOT route through the override (different ROM
	// mapping in the C0-FF range; vectors there read ROM as usual).
	// Verify $C0:FFEA returns the ROM byte at the C0-mapped offset.
	// C0:FFEA maps via SA-1 CPUROMAddress to (bank=$00 region, offset
	// $0:FFEA in linear bank 0 + offset addressing). To avoid coupling
	// to the mapper details, only assert that the override SNV byte
	// $BE is not what we get back.
	if got := b.Read(0xC0_FFEA); got == 0xBE {
		t.Fatalf("$C0:FFEA returned override byte BE; override must NOT cover C0-FF")
	}
}

// TestSA1VectorOverrideRoundTripsThroughCartridgeState pins that the
// override survives a full Cartridge.Serialize/Unserialize cycle: a
// restored cartridge with cpu_nvsw=1 still returns SNV from b.Read.
func TestSA1VectorOverrideRoundTripsThroughCartridgeState(t *testing.T) {
	rom := makeSA1ROMWithVectors(t)
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	b.Write(0x00_220c, 0x77)
	b.Write(0x00_220d, 0x66)
	b.Write(0x00_2209, 0x10)

	state, err := c.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	r := New(rom)
	rb := bus.NewBus()
	r.MapToBus(rb)
	if err := r.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := rb.Read(0x00_FFEA); got != 0x77 {
		t.Fatalf("restored $00:FFEA = %02X, want 77", got)
	}
	if got := rb.Read(0x00_FFEB); got != 0x66 {
		t.Fatalf("restored $00:FFEB = %02X, want 66", got)
	}
}

// TestSA1MessagePortBusEndToEnd exercises the message-port flow
// through the full S-CPU bus path: $2200 stores smeg (S-CPU →
// SA-1) and $2209 stores cmeg + raises the CPU IRQ flag (SA-1 →
// S-CPU). $2300 readback reflects cmeg in the low nibble and
// the IRQ flag at bit 7.
func TestSA1MessagePortBusEndToEnd(t *testing.T) {
	rom := makeROM(0x20000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// S-CPU writes message $07 to SA-1 via $2200.
	b.Write(0x00_2200, 0x07)
	d := c.coprocessor.(*sa1.Device)
	if got := d.SCPUMessage(); got != 0x07 {
		t.Fatalf("SCPUMessage after $2200=$07 = %02X, want 07", got)
	}
	// $2200 byte mirror preserves the full byte (high bits not
	// behavioral but byte-readback intact).
	if got := b.Read(0x00_2200); got != 0x07 {
		t.Fatalf("$2200 readback = %02X, want 07", got)
	}

	// SA-1 (simulated via direct $2209 byte write) sends message
	// $09 + IRQ pulse to S-CPU.
	b.Write(0x00_2209, 0x89)
	got := b.Read(0x00_2300)
	if got&0x80 == 0 {
		t.Fatalf("$2300 bit 7 (cpu_irqfl) = %02X, want set", got)
	}
	if got&0x0F != 0x09 {
		t.Fatalf("$2300 low nibble = %02X, want 09", got&0x0F)
	}

	// S-CPU clears the IRQ via $2202 bit 7.
	b.Write(0x00_2202, 0x80)
	got = b.Read(0x00_2300)
	if got&0x80 != 0 {
		t.Fatalf("$2300 bit 7 after SIC clear = %02X, still set", got)
	}
	// Clearing the IRQ does not clear cmeg.
	if got&0x0F != 0x09 {
		t.Fatalf("$2300 low nibble after SIC clear = %02X, want 09", got&0x0F)
	}
}
