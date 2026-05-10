package obc1

import "testing"

// newDevice attaches an 8 KiB SRAM with the supplied $1FF5/$1FF6
// seed bytes so power() picks up specific baseptr/address/shift.
func newDevice(seed1FF5, seed1FF6 uint8) (*Device, []byte) {
	ram := make([]byte, 0x2000)
	ram[0x1FF5] = seed1FF5
	ram[0x1FF6] = seed1FF6
	d := New()
	d.SetRAM(ram)
	return d, ram
}

func TestPowerSelectsBaseptr1800WhenBit0Set(t *testing.T) {
	d, _ := newDevice(0x01, 0x00)
	if d.baseptr != 0x1800 {
		t.Errorf("baseptr = %#x, want 0x1800", d.baseptr)
	}
}

func TestPowerSelectsBaseptr1C00WhenBit0Clear(t *testing.T) {
	d, _ := newDevice(0x00, 0x00)
	if d.baseptr != 0x1C00 {
		t.Errorf("baseptr = %#x, want 0x1C00", d.baseptr)
	}
}

func TestPowerLatchesAddressAndShiftFrom1FF6(t *testing.T) {
	d, _ := newDevice(0x00, 0x42)
	if d.address != 0x42 {
		t.Errorf("address = %#x, want 0x42", d.address)
	}
	if d.shift != (0x42&3)<<1 {
		t.Errorf("shift = %d, want %d", d.shift, (0x42&3)<<1)
	}
}

func TestRegisterWindowReadsFromBaseptrPlusAddressShifted(t *testing.T) {
	// baseptr=0x1C00 (1FF5=0), address=0x10 (1FF6=0x10) → reads
	// from 0x1C00 + 0x10*4 + i = 0x1C40+i for i=0..3.
	d, ram := newDevice(0x00, 0x10)
	ram[0x1C40] = 0xAA
	ram[0x1C41] = 0xBB
	ram[0x1C42] = 0xCC
	ram[0x1C43] = 0xDD
	if got := d.Read(0x1FF0); got != 0xAA {
		t.Errorf("$1FF0 = %#x, want 0xAA", got)
	}
	if got := d.Read(0x1FF1); got != 0xBB {
		t.Errorf("$1FF1 = %#x, want 0xBB", got)
	}
	if got := d.Read(0x1FF2); got != 0xCC {
		t.Errorf("$1FF2 = %#x, want 0xCC", got)
	}
	if got := d.Read(0x1FF3); got != 0xDD {
		t.Errorf("$1FF3 = %#x, want 0xDD", got)
	}
}

func TestRegister1FF4ReadsFromBaseptrPlusAddressShiftedDownPlus0x200(t *testing.T) {
	// baseptr=0x1C00, address=0x10 → reads from 0x1C00 +
	// (0x10>>2) + 0x200 = 0x1C00 + 4 + 0x200 = 0x1E04.
	d, ram := newDevice(0x00, 0x10)
	ram[0x1E04] = 0x77
	if got := d.Read(0x1FF4); got != 0x77 {
		t.Errorf("$1FF4 = %#x, want 0x77", got)
	}
}

func TestRegisterWindowWritesToBaseptrPlusAddressShifted(t *testing.T) {
	d, ram := newDevice(0x00, 0x10)
	d.Write(0x1FF0, 0x11)
	d.Write(0x1FF1, 0x22)
	d.Write(0x1FF2, 0x33)
	d.Write(0x1FF3, 0x44)
	if ram[0x1C40] != 0x11 || ram[0x1C41] != 0x22 || ram[0x1C42] != 0x33 || ram[0x1C43] != 0x44 {
		t.Errorf("packed write produced %02X %02X %02X %02X, want 11 22 33 44",
			ram[0x1C40], ram[0x1C41], ram[0x1C42], ram[0x1C43])
	}
}

func TestRegister1FF4PackedBitWriteRespectsShift(t *testing.T) {
	// baseptr=0x1C00, address=0x10, shift=(0x10&3)<<1=0 → byte at
	// 0x1E04, replacing 2 bits at position 0 with the low 2 of val.
	d, ram := newDevice(0x00, 0x10)
	ram[0x1E04] = 0xF0 // existing high nibble preserved
	d.Write(0x1FF4, 0x03)
	if ram[0x1E04] != 0xF3 {
		t.Errorf("packed write at shift=0: %#x, want 0xF3", ram[0x1E04])
	}
}

func TestRegister1FF4PackedBitWriteAtShift2(t *testing.T) {
	// 1FF6 = 0x12 → address=0x12, shift=(0x12&3)<<1=4. Pack into
	// bits 4..5 of byte at 0x1C00 + (0x12>>2) + 0x200 = 0x1E04.
	d, ram := newDevice(0x00, 0x12)
	ram[0x1E04] = 0x0F // existing low nibble preserved
	d.Write(0x1FF4, 0x03)
	// Replace 2 bits at position 4: 0x0F = 0000_1111; clear bits 4..5
	// (0011_0000 → 0000_1111 stays); set 11 at 4..5: 0x3F.
	if ram[0x1E04] != 0x3F {
		t.Errorf("packed write at shift=4: %#x, want 0x3F", ram[0x1E04])
	}
}

func TestRegister1FF5UpdatesBaseptrLive(t *testing.T) {
	d, _ := newDevice(0x00, 0x00) // initial baseptr=0x1C00
	d.Write(0x1FF5, 0x01)
	if d.baseptr != 0x1800 {
		t.Errorf("after $1FF5=1: baseptr = %#x, want 0x1800", d.baseptr)
	}
	d.Write(0x1FF5, 0x00)
	if d.baseptr != 0x1C00 {
		t.Errorf("after $1FF5=0: baseptr = %#x, want 0x1C00", d.baseptr)
	}
}

func TestRegister1FF6UpdatesAddressAndShiftLive(t *testing.T) {
	d, _ := newDevice(0x00, 0x00)
	d.Write(0x1FF6, 0x55)
	if d.address != 0x55 {
		t.Errorf("address = %#x, want 0x55", d.address)
	}
	if d.shift != (0x55&3)<<1 {
		t.Errorf("shift = %d, want %d", d.shift, (0x55&3)<<1)
	}
}

func TestNonRegisterReadFallsThroughToRAM(t *testing.T) {
	d, ram := newDevice(0x00, 0x00)
	ram[0x0123] = 0xFE
	if got := d.Read(0x0123); got != 0xFE {
		t.Errorf("non-register read = %#x, want 0xFE", got)
	}
}

func TestNonRegisterWriteFallsThroughToRAM(t *testing.T) {
	d, ram := newDevice(0x00, 0x00)
	d.Write(0x0456, 0xCD)
	if ram[0x0456] != 0xCD {
		t.Errorf("non-register write = %#x, want 0xCD", ram[0x0456])
	}
}

func TestAddressMaskingIgnoresHighBits(t *testing.T) {
	// Read/Write should mask addr to 13 bits before dispatching.
	d, ram := newDevice(0x00, 0x00)
	ram[0x0100] = 0x99
	if got := d.Read(0xFF_E100); got != 0x99 {
		t.Errorf("masked read of high addr = %#x, want 0x99", got)
	}
	d.Write(0x80_C200, 0x88)
	if ram[0x0200] != 0x88 {
		t.Errorf("masked write to high addr stored at %#x = %#x, want at 0x200=0x88",
			0x80_C200&0x1FFF, ram[0x0200])
	}
}
