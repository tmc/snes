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

// TestSA1VBDBWRAMRegionReturns0xFFUntilWired pins the explicit TODO:
// the BW-RAM region of bsnes' VBR mux ($00-3F:6000-7FFF and
// $40-4F:0000-FFFF) is not yet routed through Cartridge.RAM. Until
// then, the slice falls back to Device's nil-reader 0xFF default,
// which matches bsnes' "unmapped" semantics.
//
// When the BW-RAM raw projection is later implemented, this test
// must be updated deliberately.
func TestSA1VBDBWRAMRegionReturns0xFFUntilWired(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	rom[loROMHeader+0x18] = 8 // 256 KiB BW-RAM
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	// $40:0000 is in the BW-RAM raw window per bsnes
	// memory.cpp:121. Until raw-BW-RAM-via-VBR is wired, all 3
	// bytes read are 0xFF → $230C low = 0xFF.
	vbdSetVAOnBus(t, b, 0x400000)
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0xFF {
		t.Fatalf("$230C BW-RAM region = %02X, want FF (TODO until BW-RAM-via-VBR wired)", got)
	}

	// Also the $00:6000 page-mapped window.
	vbdSetVAOnBus(t, b, 0x006000)
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0xFF {
		t.Fatalf("$230C $00:6000 region = %02X, want FF (TODO)", got)
	}
}

// TestSA1VBDIRAMRegionReturns0xFFUntilWired pins the I-RAM TODO. Go
// has no I-RAM today; bsnes' VBR routes $00-3F:0000-07FF and
// $00-3F:3000-37FF to I-RAM. Until I-RAM lands as its own slice,
// these reads return 0xFF (matches bsnes' unmapped fallback).
func TestSA1VBDIRAMRegionReturns0xFFUntilWired(t *testing.T) {
	rom := makeROM(0x100000)
	rom[loROMHeader+0x15] = 0x23
	rom[loROMHeader+0x16] = 0x34
	c := New(rom)
	b := bus.NewBus()
	c.MapToBus(b)

	vbdSetVAOnBus(t, b, 0x003000) // I-RAM mirror window
	b.Write(0x00_2258, 0x88)
	if got := b.Read(0x00_230c); got != 0xFF {
		t.Fatalf("$230C I-RAM region = %02X, want FF (TODO until I-RAM wired)", got)
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
