package sa1

import "testing"

func TestRegisterWindowMirrorsBanks(t *testing.T) {
	d := New()
	if !d.Write(0x00_2200, 0x80) {
		t.Fatalf("write to SA-1 control register rejected")
	}
	if got, ok := d.Read(0x80_2200); !ok || got != 0x80 {
		t.Fatalf("mirror read = %02X,%v want 80,true", got, ok)
	}
	if _, ok := d.Read(0x40_2200); ok {
		t.Fatalf("bank 40 unexpectedly mapped to SA-1 registers")
	}
	if d.Write(0x00_2400, 0x01) {
		t.Fatalf("outside SA-1 register window accepted write")
	}
}

func TestStateRoundTrip(t *testing.T) {
	d := New()
	d.Write(0x00_2200, 0x80)
	d.Write(0x00_2209, 0x20)
	d.SignalCPUIRQ(0x0b)
	d.SignalCharacterDMAIRQ()

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	for _, addr := range []uint32{0x00_2200, 0x00_2209} {
		want, _ := d.Read(addr)
		got, ok := restored.Read(addr)
		if !ok || got != want {
			t.Fatalf("restored read %06X = %02X,%v want %02X,true", addr, got, ok, want)
		}
	}
	if got, _ := restored.Read(0x00_2300); got != 0xab {
		t.Fatalf("restored SFR=%02X, want AB", got)
	}
}

func TestCPUStatusAndClear(t *testing.T) {
	d := New()
	d.Write(0x00_2201, 0xa0)
	d.SignalCPUIRQ(0x05)
	d.SignalCharacterDMAIRQ()

	if got, _ := d.Read(0x00_2300); got != 0xa5 {
		t.Fatalf("SFR before clear=%02X, want A5", got)
	}
	d.Write(0x00_2202, 0x80)
	if got, _ := d.Read(0x00_2300); got != 0x25 {
		t.Fatalf("SFR after CPU IRQ clear=%02X, want 25", got)
	}
	d.Write(0x00_2202, 0x20)
	if got, _ := d.Read(0x00_2300); got != 0x05 {
		t.Fatalf("SFR after CHDMA IRQ clear=%02X, want 05", got)
	}
}

func TestCPUBWRAMPageMasksToFiveBits(t *testing.T) {
	d := New()
	d.Write(0x00_2224, 0xff)
	if got := d.CPUBWRAMPage(); got != 0x1f {
		t.Fatalf("BMAPS page=%02X, want 1F", got)
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := restored.CPUBWRAMPage(); got != 0x1f {
		t.Fatalf("restored BMAPS page=%02X, want 1F", got)
	}
}

func TestBWRAMWriteProtectionState(t *testing.T) {
	d := New()
	d.Write(0x00_2228, 0x02)
	if d.AllowCPUBWRAMWrite(0x0003ff) {
		t.Fatalf("protected BW-RAM address reported writable")
	}
	if !d.AllowCPUBWRAMWrite(0x000400) {
		t.Fatalf("unprotected BW-RAM address reported read-only")
	}
	d.Write(0x00_2226, 0x80)
	if !d.AllowCPUBWRAMWrite(0x000000) {
		t.Fatalf("SWEN did not enable protected BW-RAM write")
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if !restored.AllowCPUBWRAMWrite(0x000000) {
		t.Fatalf("restored SWEN did not preserve BW-RAM write enable")
	}
}

func TestCPUROMBankMapping(t *testing.T) {
	d := New()
	for _, tt := range []struct {
		addr uint32
		want uint32
	}{
		{0xc0_1234, 0x00_1234},
		{0xd0_1234, 0x10_1234},
		{0xe0_1234, 0x20_1234},
		{0xf0_1234, 0x30_1234},
	} {
		got, ok := d.CPUROMAddress(tt.addr)
		if !ok || got != tt.want {
			t.Fatalf("CPUROMAddress(%06X)=%06X,%v want %06X,true", tt.addr, got, ok, tt.want)
		}
	}

	d.Write(0x00_2220, 0x82)
	got, ok := d.CPUROMAddress(0xc0_1234)
	if !ok || got != 0x20_1234 {
		t.Fatalf("remapped C bank=%06X,%v want 201234,true", got, ok)
	}

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	got, ok = restored.CPUROMAddress(0xc0_1234)
	if !ok || got != 0x20_1234 {
		t.Fatalf("restored remapped C bank=%06X,%v want 201234,true", got, ok)
	}
}

func TestSA1BWRAMLinearView(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x03)
	ram := make([]byte, 256*1024)

	addr, ok := d.SA1BWRAMAddress(0x40_1234)
	if !ok || addr != 0x7234 {
		t.Fatalf("SA1BWRAMAddress=%05X,%v want 07234,true", addr, ok)
	}
	d.WriteSA1BWRAM(ram, 0x40_1234, 0x5a)
	if got := ram[0x7234]; got != 0x5a {
		t.Fatalf("linear BW-RAM byte=%02X, want 5A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x40_1234); got != 0x5a {
		t.Fatalf("linear BW-RAM read=%02X, want 5A", got)
	}
}

func TestSA1BWRAMBitmapView4BPP(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x80|0x02)
	d.Write(0x00_2227, 0x80)
	ram := make([]byte, 256*1024)

	d.WriteSA1BWRAM(ram, 0x60_0000, 0x0a)
	d.WriteSA1BWRAM(ram, 0x60_0001, 0x05)
	if got := ram[0x2000]; got != 0x5a {
		t.Fatalf("4bpp packed byte=%02X, want 5A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x60_0000); got != 0x0a {
		t.Fatalf("4bpp low pixel=%02X, want 0A", got)
	}
	if got := d.ReadSA1BWRAM(ram, 0x60_0001); got != 0x05 {
		t.Fatalf("4bpp high pixel=%02X, want 05", got)
	}
}

func TestSA1BWRAMBitmapView2BPPSerializes(t *testing.T) {
	d := New()
	d.Write(0x00_2225, 0x80|0x02)
	d.Write(0x00_2227, 0x80)
	d.Write(0x00_223f, 0x80)
	ram := make([]byte, 256*1024)

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New()
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}

	for i, v := range []uint8{1, 2, 3, 0} {
		restored.WriteSA1BWRAM(ram, 0x60_0000+uint32(i), v)
	}
	if got := ram[0x1000]; got != 0x39 {
		t.Fatalf("2bpp packed byte=%02X, want 39", got)
	}
	for i, want := range []uint8{1, 2, 3, 0} {
		if got := restored.ReadSA1BWRAM(ram, 0x60_0000+uint32(i)); got != want {
			t.Fatalf("2bpp pixel %d=%02X, want %02X", i, got, want)
		}
	}
}
