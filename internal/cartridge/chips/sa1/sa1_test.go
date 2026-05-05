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
